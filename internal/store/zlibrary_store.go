// s-library: Z-Library 三张新表（任务/去重映射/配置 KV）的 CRUD 与状态机更新，
// 见 docs/ARCHITECTURE.md §2.1/§3/§6。SQL 全部集中在 store 包（项目惯例）。
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/nowen-reader/nowen-reader/internal/model"
)

// ZlibrarySettingKV 配置表键名（FR-1.3）。
const (
	ZlibSettingKeyCredentials    = "credentials"
	ZlibSettingKeyDomains        = "domains"
	ZlibSettingKeyBehavior       = "behavior"
	ZlibSettingKeyPersonalDomain = "personalDomain"
	ZlibSettingKeyBrowserAssist  = "browserAssist" // s-library: 浏览器辅助通行（cf_clearance + UA）
	ZlibSettingKeyQuotaCache     = "quotaCache"
)

// ============================================================
// ZlibrarySetting（配置 KV）
// ============================================================

// GetZlibrarySetting 读取一个配置项，缺失时返回 ("", nil)。
func GetZlibrarySetting(key string) (string, error) {
	var value string
	err := db.QueryRow(`SELECT "value" FROM "ZlibrarySetting" WHERE "key" = ?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return value, nil
}

// SetZlibrarySetting 写入（UPSERT）一个配置项。
func SetZlibrarySetting(key, value string) error {
	_, err := db.Exec(`INSERT INTO "ZlibrarySetting" ("key", "value", "updatedAt")
		VALUES (?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT("key") DO UPDATE SET "value" = excluded."value", "updatedAt" = CURRENT_TIMESTAMP`,
		key, value)
	if err != nil {
		return fmt.Errorf("set zlibrary setting %s: %w", key, err)
	}
	return nil
}

// DeleteZlibrarySetting 删除一个配置项。
func DeleteZlibrarySetting(key string) error {
	_, err := db.Exec(`DELETE FROM "ZlibrarySetting" WHERE "key" = ?`, key)
	return err
}

// ============================================================
// ZlibraryDownloadTask（下载任务）
// ============================================================

// CreateZlibraryDownloadTask 新建任务（status=queued）。
func CreateZlibraryDownloadTask(t *model.ZlibraryDownloadTask) error {
	if t.Status == "" {
		t.Status = model.ZlibTaskStatusQueued
	}
	if t.Stage == "" {
		t.Stage = model.ZlibStageFetchInfo
	}
	res, err := db.Exec(`INSERT INTO "ZlibraryDownloadTask"
		("userId", "zbookId", "zbookHash", "title", "author", "ext", "fileSize",
		 "libraryId", "localBookId", "status", "progress", "stage",
		 "errorCode", "errorMsg", "quotaConsumed", "retryCount", "metaJson")
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.UserID, t.ZbookID, t.ZbookHash, t.Title, t.Author, t.Ext, t.FileSize,
		t.LibraryID, t.LocalBookID, t.Status, t.Progress, t.Stage,
		t.ErrorCode, t.ErrorMsg, t.QuotaConsumed, t.RetryCount, t.MetaJSON,
	)
	if err != nil {
		return err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	t.ID = id
	t.CreatedAt = time.Now()
	t.UpdatedAt = t.CreatedAt
	return nil
}

const zlibraryTaskColumns = `"id", "userId", "zbookId", "zbookHash", "title", "author", "ext", "fileSize",
	"libraryId", "localBookId", "status", "progress", "stage",
	"errorCode", "errorMsg", "quotaConsumed", "retryCount", "metaJson",
	"createdAt", "updatedAt", "finishedAt"`

func scanZlibraryTask(row interface{ Scan(...any) error }) (*model.ZlibraryDownloadTask, error) {
	t := &model.ZlibraryDownloadTask{}
	var finishedAt sql.NullTime
	err := row.Scan(&t.ID, &t.UserID, &t.ZbookID, &t.ZbookHash, &t.Title, &t.Author, &t.Ext, &t.FileSize,
		&t.LibraryID, &t.LocalBookID, &t.Status, &t.Progress, &t.Stage,
		&t.ErrorCode, &t.ErrorMsg, &t.QuotaConsumed, &t.RetryCount, &t.MetaJSON,
		&t.CreatedAt, &t.UpdatedAt, &finishedAt)
	if err != nil {
		return nil, err
	}
	if finishedAt.Valid {
		t.FinishedAt = &finishedAt.Time
	}
	return t, nil
}

// GetZlibraryDownloadTask 按 ID 读取任务，不存在返回 (nil, nil)。
func GetZlibraryDownloadTask(id int64) (*model.ZlibraryDownloadTask, error) {
	row := db.QueryRow(`SELECT `+zlibraryTaskColumns+` FROM "ZlibraryDownloadTask" WHERE "id" = ?`, id)
	t, err := scanZlibraryTask(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return t, nil
}

// ZlibraryTaskListOptions 任务列表过滤条件。UserID 为空表示不过滤（管理员全量视图）。
type ZlibraryTaskListOptions struct {
	UserID string
	Status string
	Page   int
	Limit  int
}

// ListZlibraryDownloadTasks 返回任务列表（createdAt DESC）与总数。
func ListZlibraryDownloadTasks(opts ZlibraryTaskListOptions) ([]*model.ZlibraryDownloadTask, int, error) {
	where := " WHERE 1=1"
	args := []any{}
	if opts.UserID != "" {
		where += ` AND "userId" = ?`
		args = append(args, opts.UserID)
	}
	if opts.Status != "" {
		where += ` AND "status" = ?`
		args = append(args, opts.Status)
	}

	var total int
	if err := db.QueryRow(`SELECT COUNT(*) FROM "ZlibraryDownloadTask"`+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	if opts.Limit <= 0 || opts.Limit > 100 {
		opts.Limit = 50
	}
	if opts.Page <= 0 {
		opts.Page = 1
	}
	query := `SELECT ` + zlibraryTaskColumns + ` FROM "ZlibraryDownloadTask"` + where +
		` ORDER BY "createdAt" DESC, "id" DESC LIMIT ? OFFSET ?`
	args = append(args, opts.Limit, (opts.Page-1)*opts.Limit)

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	tasks := []*model.ZlibraryDownloadTask{}
	for rows.Next() {
		t, err := scanZlibraryTask(rows)
		if err != nil {
			return nil, 0, err
		}
		tasks = append(tasks, t)
	}
	return tasks, total, rows.Err()
}

// GetNextQueuedZlibraryTask 返回最早的排队任务（不改变状态），无则返回 (nil, nil)。
func GetNextQueuedZlibraryTask() (*model.ZlibraryDownloadTask, error) {
	row := db.QueryRow(`SELECT ` + zlibraryTaskColumns + ` FROM "ZlibraryDownloadTask"
		WHERE "status" = ? ORDER BY "createdAt" ASC, "id" ASC LIMIT 1`, model.ZlibTaskStatusQueued)
	t, err := scanZlibraryTask(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return t, nil
}

// GetActiveZlibraryTaskForBook 查询某本书的活动任务（queued/downloading），
// 用于去重判定（Z_DUP_QUEUED）。
func GetActiveZlibraryTaskForBook(zbookID, zbookHash string) (*model.ZlibraryDownloadTask, error) {
	row := db.QueryRow(`SELECT `+zlibraryTaskColumns+` FROM "ZlibraryDownloadTask"
		WHERE "zbookId" = ? AND "zbookHash" = ? AND "status" IN (?, ?)
		ORDER BY "createdAt" ASC LIMIT 1`,
		zbookID, zbookHash, model.ZlibTaskStatusQueued, model.ZlibTaskStatusDownloading)
	t, err := scanZlibraryTask(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return t, nil
}

// StartZlibraryTask 状态机乐观迁移：queued → downloading（含阶段）。返回是否发生迁移。
func StartZlibraryTask(id int64, stage string) (bool, error) {
	res, err := db.Exec(`UPDATE "ZlibraryDownloadTask"
		SET "status" = ?, "stage" = ?, "updatedAt" = CURRENT_TIMESTAMP
		WHERE "id" = ? AND "status" = ?`,
		model.ZlibTaskStatusDownloading, stage, id, model.ZlibTaskStatusQueued)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// UpdateZlibraryTaskStage 更新阶段（仅 downloading 中的任务）。
func UpdateZlibraryTaskStage(id int64, stage string) error {
	_, err := db.Exec(`UPDATE "ZlibraryDownloadTask"
		SET "stage" = ?, "updatedAt" = CURRENT_TIMESTAMP
		WHERE "id" = ? AND "status" = ?`, stage, id, model.ZlibTaskStatusDownloading)
	return err
}

// UpdateZlibraryTaskProgress 节流写入进度（调用方负责节流，见 queue 设计 §6）。
func UpdateZlibraryTaskProgress(id int64, progress int) error {
	if progress < 0 {
		progress = 0
	}
	if progress > 100 {
		progress = 100
	}
	_, err := db.Exec(`UPDATE "ZlibraryDownloadTask"
		SET "progress" = ?, "updatedAt" = CURRENT_TIMESTAMP
		WHERE "id" = ? AND "status" = ?`, progress, id, model.ZlibTaskStatusDownloading)
	return err
}

// UpdateZlibraryTaskSnapshot fetch_info 后回写快照与元数据（FR-8.1：metaJson 供 M2 回填）。
func UpdateZlibraryTaskSnapshot(id int64, title, author, ext string, fileSize int64, metaJSON string) error {
	if metaJSON == "" {
		metaJSON = "{}"
	}
	_, err := db.Exec(`UPDATE "ZlibraryDownloadTask"
		SET "title" = ?, "author" = ?, "ext" = ?, "fileSize" = ?, "metaJson" = ?,
		    "updatedAt" = CURRENT_TIMESTAMP
		WHERE "id" = ?`, title, author, ext, fileSize, metaJSON, id)
	return err
}

// FailZlibraryTask 任务置 failed（乐观：仅 queued/downloading 可失败，防取消/完成竞争）。
func FailZlibraryTask(id int64, errorCode, errorMsg string, quotaConsumed bool) (bool, error) {
	res, err := db.Exec(`UPDATE "ZlibraryDownloadTask"
		SET "status" = ?, "errorCode" = ?, "errorMsg" = ?, "quotaConsumed" = ?,
		    "finishedAt" = CURRENT_TIMESTAMP, "updatedAt" = CURRENT_TIMESTAMP
		WHERE "id" = ? AND "status" IN (?, ?)`,
		model.ZlibTaskStatusFailed, errorCode, errorMsg, quotaConsumed, id,
		model.ZlibTaskStatusQueued, model.ZlibTaskStatusDownloading)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// CompleteZlibraryTask 任务置 completed 并回填 localBookId（乐观迁移）。
func CompleteZlibraryTask(id int64, localBookID string) (bool, error) {
	res, err := db.Exec(`UPDATE "ZlibraryDownloadTask"
		SET "status" = ?, "localBookId" = ?, "progress" = 100,
		    "finishedAt" = CURRENT_TIMESTAMP, "updatedAt" = CURRENT_TIMESTAMP
		WHERE "id" = ? AND "status" IN (?, ?)`,
		model.ZlibTaskStatusCompleted, localBookID, id,
		model.ZlibTaskStatusQueued, model.ZlibTaskStatusDownloading)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// CancelZlibraryTask 标记取消（仅 queued/downloading 可取消），返回是否成功取消。
func CancelZlibraryTask(id int64) (bool, error) {
	res, err := db.Exec(`UPDATE "ZlibraryDownloadTask"
		SET "status" = ?, "finishedAt" = CURRENT_TIMESTAMP, "updatedAt" = CURRENT_TIMESTAMP
		WHERE "id" = ? AND "status" IN (?, ?)`,
		model.ZlibTaskStatusCanceled, id,
		model.ZlibTaskStatusQueued, model.ZlibTaskStatusDownloading)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// RetryZlibraryTask failed → queued，retryCount+1，清空错误与进度（重试前重新前置校验）。
func RetryZlibraryTask(id int64) (bool, error) {
	res, err := db.Exec(`UPDATE "ZlibraryDownloadTask"
		SET "status" = ?, "stage" = ?, "progress" = 0, "errorCode" = '', "errorMsg" = '',
		    "quotaConsumed" = 0, "retryCount" = "retryCount" + 1,
		    "finishedAt" = NULL, "updatedAt" = CURRENT_TIMESTAMP
		WHERE "id" = ? AND "status" = ?`,
		model.ZlibTaskStatusQueued, model.ZlibStageFetchInfo, id, model.ZlibTaskStatusFailed)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// DeleteZlibraryTask 删除任务记录（queued 直删 / 终态清理共用）。
func DeleteZlibraryTask(id int64) error {
	_, err := db.Exec(`DELETE FROM "ZlibraryDownloadTask" WHERE "id" = ?`, id)
	return err
}

// MarkZlibraryTasksInterrupted 进程重启恢复：downloading → failed(Z_INTERRUPTED)。
// 返回受影响任务 ID 列表（供日志）。
func MarkZlibraryTasksInterrupted() ([]int64, error) {
	ids := []int64{}
	rows, err := db.Query(`SELECT "id" FROM "ZlibraryDownloadTask" WHERE "status" = ?`, model.ZlibTaskStatusDownloading)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return ids, err
	}
	for _, id := range ids {
		if _, err := db.Exec(`UPDATE "ZlibraryDownloadTask"
			SET "status" = ?, "errorCode" = 'Z_INTERRUPTED',
			    "errorMsg" = '进程重启导致下载中断，可重试',
			    "finishedAt" = CURRENT_TIMESTAMP, "updatedAt" = CURRENT_TIMESTAMP
			WHERE "id" = ? AND "status" = ?`,
			model.ZlibTaskStatusFailed, id, model.ZlibTaskStatusDownloading); err != nil {
			return ids, err
		}
	}
	return ids, nil
}

// TrimZlibraryTaskHistory 终态历史裁剪：仅保留最近 keep 条 completed/failed/canceled
// 记录（活动任务不受影响）。返回删除条数。
func TrimZlibraryTaskHistory(keep int) (int64, error) {
	res, err := db.Exec(`DELETE FROM "ZlibraryDownloadTask"
		WHERE "status" IN (?, ?, ?)
		  AND "id" NOT IN (
			SELECT "id" FROM "ZlibraryDownloadTask"
			WHERE "status" IN (?, ?, ?)
			ORDER BY "createdAt" DESC, "id" DESC LIMIT ?
		  )`,
		model.ZlibTaskStatusCompleted, model.ZlibTaskStatusFailed, model.ZlibTaskStatusCanceled,
		model.ZlibTaskStatusCompleted, model.ZlibTaskStatusFailed, model.ZlibTaskStatusCanceled,
		keep)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ============================================================
// ZlibraryImportedBook（去重映射，FR-7）
// ============================================================

// UpsertZlibraryImportedBook 写入/更新映射（唯一索引兜底）。
func UpsertZlibraryImportedBook(m *model.ZlibraryImportedBook) error {
	_, err := db.Exec(`INSERT INTO "ZlibraryImportedBook"
		("zbookId", "zbookHash", "localBookId", "libraryId", "importedBy", "importedAt")
		VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT("zbookId", "zbookHash") DO UPDATE SET
		  "localBookId" = excluded."localBookId",
		  "libraryId" = excluded."libraryId",
		  "importedBy" = excluded."importedBy",
		  "importedAt" = CURRENT_TIMESTAMP`,
		m.ZbookID, m.ZbookHash, m.LocalBookID, m.LibraryID, m.ImportedBy)
	if err != nil {
		return fmt.Errorf("upsert zlibrary imported book: %w", err)
	}
	return nil
}

// GetZlibraryImportedBook 按 (zbookId, zbookHash) 读取映射，不存在返回 (nil, nil)。
func GetZlibraryImportedBook(zbookID, zbookHash string) (*model.ZlibraryImportedBook, error) {
	m := &model.ZlibraryImportedBook{}
	err := db.QueryRow(`SELECT "zbookId", "zbookHash", "localBookId", "libraryId", "importedBy", "importedAt"
		FROM "ZlibraryImportedBook" WHERE "zbookId" = ? AND "zbookHash" = ?`,
		zbookID, zbookHash).Scan(&m.ZbookID, &m.ZbookHash, &m.LocalBookID, &m.LibraryID, &m.ImportedBy, &m.ImportedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return m, nil
}

// ClearZlibraryImportedBookLocal 本地条目被删除后标记失效（FR-7.4：映射保留、允许再入库）。
func ClearZlibraryImportedBookLocal(zbookID, zbookHash string) error {
	_, err := db.Exec(`UPDATE "ZlibraryImportedBook" SET "localBookId" = ''
		WHERE "zbookId" = ? AND "zbookHash" = ?`, zbookID, zbookHash)
	return err
}
