// s-library: ZlibraryDownloadTask / ZlibraryImportedBook / ZlibrarySetting
// 三表 CRUD 与状态机单测（复用 store 包测试基建 setupTestDB）。
package store

import (
	"testing"

	"github.com/nowen-reader/nowen-reader/internal/model"
)

func zlSampleTask(userID string) *model.ZlibraryDownloadTask {
	return &model.ZlibraryDownloadTask{
		UserID:    userID,
		ZbookID:   "42889905",
		ZbookHash: "a1b2c3d4e5",
		Title:     "三体",
		Author:    "刘慈欣",
		Ext:       "epub",
		FileSize:  1048576,
		LibraryID: "lib-1",
	}
}

func TestZlibrarySettingKV(t *testing.T) {
	setupTestDB(t)

	v, err := GetZlibrarySetting("behavior")
	if err != nil || v != "" {
		t.Fatalf("missing key should return empty: %q %v", v, err)
	}
	if err := SetZlibrarySetting("behavior", `{"concurrency":1}`); err != nil {
		t.Fatalf("set: %v", err)
	}
	if err := SetZlibrarySetting("behavior", `{"concurrency":2}`); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	v, err = GetZlibrarySetting("behavior")
	if err != nil || v != `{"concurrency":2}` {
		t.Fatalf("after upsert: %q %v", v, err)
	}
	if err := DeleteZlibrarySetting("behavior"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if v, _ := GetZlibrarySetting("behavior"); v != "" {
		t.Fatalf("after delete: %q", v)
	}
}

func TestZlibraryTaskCRUDAndListFiltering(t *testing.T) {
	setupTestDB(t)

	task := zlSampleTask("user-a")
	if err := CreateZlibraryDownloadTask(task); err != nil {
		t.Fatalf("create: %v", err)
	}
	if task.ID == 0 || task.Status != model.ZlibTaskStatusQueued {
		t.Fatalf("task after create = %+v", task)
	}

	got, err := GetZlibraryDownloadTask(task.ID)
	if err != nil || got == nil {
		t.Fatalf("get: %v %v", got, err)
	}
	if got.Title != "三体" || got.Ext != "epub" || got.QuotaConsumed {
		t.Fatalf("got = %+v", got)
	}

	// 第二个任务（另一用户）
	other := zlSampleTask("user-b")
	other.ZbookHash = "different"
	if err := CreateZlibraryDownloadTask(other); err != nil {
		t.Fatalf("create other: %v", err)
	}

	// 全量列表
	tasks, total, err := ListZlibraryDownloadTasks(ZlibraryTaskListOptions{Page: 1, Limit: 10})
	if err != nil || total != 2 || len(tasks) != 2 {
		t.Fatalf("list all: total=%d len=%d err=%v", total, len(tasks), err)
	}
	// 按用户过滤
	tasks, total, err = ListZlibraryDownloadTasks(ZlibraryTaskListOptions{UserID: "user-a", Page: 1, Limit: 10})
	if err != nil || total != 1 || tasks[0].UserID != "user-a" {
		t.Fatalf("list by user: total=%d err=%v", total, err)
	}
	// 不存在的用户
	_, total, _ = ListZlibraryDownloadTasks(ZlibraryTaskListOptions{UserID: "nobody", Page: 1, Limit: 10})
	if total != 0 {
		t.Fatalf("list unknown user total = %d", total)
	}
}

func TestZlibraryTaskStateMachine(t *testing.T) {
	setupTestDB(t)
	task := zlSampleTask("user-a")
	if err := CreateZlibraryDownloadTask(task); err != nil {
		t.Fatalf("create: %v", err)
	}

	// queued → downloading（乐观）
	if ok, err := StartZlibraryTask(task.ID, model.ZlibStageFetchInfo); err != nil || !ok {
		t.Fatalf("start: ok=%v err=%v", ok, err)
	}
	// 再次 start 应失败（乐观迁移仅允许 queued）
	if ok, _ := StartZlibraryTask(task.ID, model.ZlibStageDownloading); ok {
		t.Fatal("second start should fail (not queued)")
	}

	if err := UpdateZlibraryTaskProgress(task.ID, 42); err != nil {
		t.Fatalf("progress: %v", err)
	}
	if err := UpdateZlibraryTaskStage(task.ID, model.ZlibStageDownloading); err != nil {
		t.Fatalf("stage: %v", err)
	}
	got, _ := GetZlibraryDownloadTask(task.ID)
	if got.Progress != 42 || got.Stage != model.ZlibStageDownloading || got.Status != model.ZlibTaskStatusDownloading {
		t.Fatalf("mid-state = %+v", got)
	}

	// complete（乐观：downloading 可完成）
	if ok, err := CompleteZlibraryTask(task.ID, "local-1"); err != nil || !ok {
		t.Fatalf("complete: ok=%v err=%v", ok, err)
	}
	got, _ = GetZlibraryDownloadTask(task.ID)
	if got.Status != model.ZlibTaskStatusCompleted || got.LocalBookID != "local-1" || got.Progress != 100 || got.FinishedAt == nil {
		t.Fatalf("completed = %+v", got)
	}
	// completed 后不可再 fail
	if ok, _ := FailZlibraryTask(task.ID, "Z_UPSTREAM", "late failure", true); ok {
		t.Fatal("fail after complete should not apply")
	}
	// completed 后不可重试
	if ok, _ := RetryZlibraryTask(task.ID); ok {
		t.Fatal("retry after complete should not apply")
	}
}

func TestZlibraryTaskFailCancelRetry(t *testing.T) {
	setupTestDB(t)

	// failed → retry
	task := zlSampleTask("user-a")
	_ = CreateZlibraryDownloadTask(task)
	if ok, err := FailZlibraryTask(task.ID, "Z_QUOTA_EXCEEDED", "额度用尽", true); err != nil || !ok {
		t.Fatalf("fail from queued: ok=%v err=%v", ok, err)
	}
	got, _ := GetZlibraryDownloadTask(task.ID)
	if got.Status != model.ZlibTaskStatusFailed || got.ErrorCode != "Z_QUOTA_EXCEEDED" || !got.QuotaConsumed {
		t.Fatalf("failed = %+v", got)
	}
	if ok, err := RetryZlibraryTask(task.ID); err != nil || !ok {
		t.Fatalf("retry: ok=%v err=%v", ok, err)
	}
	got, _ = GetZlibraryDownloadTask(task.ID)
	if got.Status != model.ZlibTaskStatusQueued || got.RetryCount != 1 ||
		got.ErrorCode != "" || got.QuotaConsumed || got.Progress != 0 || got.FinishedAt != nil {
		t.Fatalf("retried = %+v", got)
	}

	// queued → canceled
	if ok, err := CancelZlibraryTask(task.ID); err != nil || !ok {
		t.Fatalf("cancel queued: ok=%v err=%v", ok, err)
	}
	// canceled 不可再取消/完成/失败
	if ok, _ := CancelZlibraryTask(task.ID); ok {
		t.Fatal("double cancel should not apply")
	}
	if ok, _ := CompleteZlibraryTask(task.ID, "x"); ok {
		t.Fatal("complete after cancel should not apply")
	}

	// active task lookup（去重用）
	active := zlSampleTask("user-a")
	active.ZbookHash = "hash-active"
	_ = CreateZlibraryDownloadTask(active)
	found, err := GetActiveZlibraryTaskForBook("42889905", "hash-active")
	if err != nil || found == nil || found.ID != active.ID {
		t.Fatalf("active lookup: %v %v", found, err)
	}
	// 终态任务不视为 active
	if ok, _ := CancelZlibraryTask(active.ID); !ok {
		t.Fatal("cancel active")
	}
	found, _ = GetActiveZlibraryTaskForBook("42889905", "hash-active")
	if found != nil {
		t.Fatalf("canceled task should not be active: %+v", found)
	}
}

func TestZlibraryTaskInterruptedAndTrim(t *testing.T) {
	setupTestDB(t)

	// 两个 downloading（应被恢复为 failed）+ 一个 queued（保持）+ 一个 completed（裁剪候选）
	dl1, dl2 := zlSampleTask("u1"), zlSampleTask("u1")
	dl2.ZbookHash = "h2"
	_ = CreateZlibraryDownloadTask(dl1)
	_ = CreateZlibraryDownloadTask(dl2)
	queued := zlSampleTask("u1")
	queued.ZbookHash = "h3"
	_ = CreateZlibraryDownloadTask(queued)
	done := zlSampleTask("u1")
	done.ZbookHash = "h4"
	_ = CreateZlibraryDownloadTask(done)
	_, _ = CompleteZlibraryTask(done.ID, "local-x")

	_, _ = StartZlibraryTask(dl1.ID, model.ZlibStageDownloading)
	_, _ = StartZlibraryTask(dl2.ID, model.ZlibStageDownloading)

	ids, err := MarkZlibraryTasksInterrupted()
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	if len(ids) != 2 {
		t.Fatalf("recovered ids = %v, want 2", ids)
	}
	for _, id := range ids {
		got, _ := GetZlibraryDownloadTask(id)
		if got.Status != model.ZlibTaskStatusFailed || got.ErrorCode != "Z_INTERRUPTED" {
			t.Fatalf("recovered task = %+v", got)
		}
	}
	q, _ := GetZlibraryDownloadTask(queued.ID)
	if q.Status != model.ZlibTaskStatusQueued {
		t.Fatalf("queued after recover = %+v", q)
	}

	// 裁剪：keep=1 → 只留最新一条终态（completed），不删除 queued/failed-of-download? failed 也是终态
	removed, err := TrimZlibraryTaskHistory(1)
	if err != nil {
		t.Fatalf("trim: %v", err)
	}
	// 终态 = 2 failed + 1 completed = 3 条，keep=1 → 删 2
	if removed != 2 {
		t.Fatalf("removed = %d, want 2", removed)
	}
	// 活动任务（queued）不受影响
	q, _ = GetZlibraryDownloadTask(queued.ID)
	if q == nil {
		t.Fatal("queued task should survive trim")
	}
}

func TestZlibraryImportedBookUpsertAndClear(t *testing.T) {
	setupTestDB(t)

	m := &model.ZlibraryImportedBook{
		ZbookID: "42", ZbookHash: "hash-x", LocalBookID: "abc123",
		LibraryID: "lib-1", ImportedBy: "user-a",
	}
	if err := UpsertZlibraryImportedBook(m); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got, err := GetZlibraryImportedBook("42", "hash-x")
	if err != nil || got == nil || got.LocalBookID != "abc123" {
		t.Fatalf("get: %v %v", got, err)
	}
	if got.ImportedAt.IsZero() {
		t.Fatalf("importedAt should be set: %+v", got)
	}
	// 二次入库（换书库）应覆盖
	m2 := &model.ZlibraryImportedBook{
		ZbookID: "42", ZbookHash: "hash-x", LocalBookID: "def456",
		LibraryID: "lib-2", ImportedBy: "user-b",
	}
	if err := UpsertZlibraryImportedBook(m2); err != nil {
		t.Fatalf("upsert again: %v", err)
	}
	got, _ = GetZlibraryImportedBook("42", "hash-x")
	if got.LocalBookID != "def456" || got.LibraryID != "lib-2" {
		t.Fatalf("after re-import: %+v", got)
	}
	// 不同 hash 是不同条目（FR-7.2 同书不同格式允许分别入库）
	if err := UpsertZlibraryImportedBook(&model.ZlibraryImportedBook{
		ZbookID: "42", ZbookHash: "hash-y", LocalBookID: "ghi789", ImportedBy: "u",
	}); err != nil {
		t.Fatalf("upsert other hash: %v", err)
	}
	if got, _ := GetZlibraryImportedBook("42", "hash-y"); got == nil || got.LocalBookID != "ghi789" {
		t.Fatal("second hash entry missing")
	}
	// 标记本地失效（FR-7.4）
	if err := ClearZlibraryImportedBookLocal("42", "hash-x"); err != nil {
		t.Fatalf("clear: %v", err)
	}
	got, _ = GetZlibraryImportedBook("42", "hash-x")
	if got == nil || got.LocalBookID != "" {
		t.Fatalf("after clear: %+v", got)
	}
}
