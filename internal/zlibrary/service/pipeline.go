// s-library: 一键入库流水线（FR-4.2~4.6，架构 §5 时序）：
// fetch_info → downloading(临时文件+进度节流) → validating(魔数，失败重试1次) →
// 磁盘检查 → moving(规范化命名+move) → scanning(进程内 SyncLibraryByID 互斥退避) →
// PathToID 确定性匹配 → 写 ZlibraryImportedBook 映射 → completed。
// 任何失败：任务 failed + 结构化错误码 + 临时文件清理，不产生半截文件。
package zlibservice

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/nowen-reader/nowen-reader/internal/config"
	"github.com/nowen-reader/nowen-reader/internal/model"
	"github.com/nowen-reader/nowen-reader/internal/store"
	zlibrary "github.com/nowen-reader/nowen-reader/internal/zlibrary"
)

// Pipeline 单任务流水线。依赖全部可注入（queue_test.go / pipeline_test.go 使用 mock）。
type Pipeline struct {
	client ZClient
	quota  QuotaRefresher
	scanFn func(libraryID string) (added, removed int, err error)
	tmpDir string
	// settings 可为 nil（测试）；用于缓存个人下载域名
	settings *Settings
	// historyKeep 完成后保留的历史条数
	historyKeep int
	// scanBackoff 扫描互斥冲突退避（生产 2s/4s/8s，最多 3 次）
	scanBackoff []time.Duration
}

// ZClient 流水线对适配层的最小依赖接口（生产绑定 *zlibrary.Client，测试用 mock）。
type ZClient interface {
	Book(ctx context.Context, id, hash string) (*zlibrary.ZBook, error)
	Formats(ctx context.Context, id, hash string) ([]zlibrary.ZFormat, error)
	Download(ctx context.Context, downloadURL string, w io.Writer, progress zlibrary.ProgressFunc) error
	// s-library: 相对下载路径（z-lib.by 的 dl 字段形如 "/dl/xxx"）需补全当前域名
	CurrentDomain() string
}

// 编译期确认生产实现满足接口。
var _ ZClient = (*zlibrary.Client)(nil)

// downloadMaxAttempts 校验失败后的下载重试次数（共 2 次下载机会）。
const downloadMaxAttempts = 2

// progressThrottleInterval / progressThrottleDelta 进度写盘节流（架构 §6）。
const (
	progressThrottleInterval = 500 * time.Millisecond
	progressThrottleDelta    = 2
)

// Run 执行流水线。保证幂等退出：每次返回时任务必为终态或保持 canceled。
func (p *Pipeline) Run(ctx context.Context, task *model.ZlibraryDownloadTask) {
	// 乐观迁移 queued → downloading（防止与取消竞争：已被取消则直接退出）
	ok, err := store.StartZlibraryTask(task.ID, model.ZlibStageFetchInfo)
	if err != nil {
		log.Printf("[ZLIBRARY] task %d start failed: %v", task.ID, err)
		return
	}
	if !ok {
		return
	}
	task.Status = model.ZlibTaskStatusDownloading

	// ── 1. fetch_info：取最新详情与 dl 链接（未发起下载，不耗配额）──
	book, err := p.client.Book(ctx, task.ZbookID, task.ZbookHash)
	if err != nil {
		p.fail(task, err, false)
		return
	}
	// 快照更新：以最新详情修正标题/作者/大小
	if book.Title != "" {
		task.Title = book.Title
	}
	if book.Author != "" {
		task.Author = book.Author
	}
	if book.FileSize > 0 {
		task.FileSize = book.FileSize
	}
	if book.DownloadURL == "" {
		p.fail(task, zlibrary.NewZError(zlibrary.CodeNotFound,
			"该书在 Z-Library 已不可用（缺少下载链接）", nil), false)
		return
	}

	// 多格式未指定时按优先级选格式（FR-4.2）
	ext := normalizeExtForTask(task.Ext)
	if ext == "" {
		ext = normalizeExtForTask(book.Ext)
	}
	if ext == "" {
		formats, ferr := p.client.Formats(ctx, task.ZbookID, task.ZbookHash)
		if ferr != nil {
			p.fail(task, ferr, false)
			return
		}
		ext = zlibrary.PickExt(formats)
	}
	if ext == "" {
		p.fail(task, zlibrary.NewZError(zlibrary.CodeNotFound, "该书没有可下载的格式", nil), false)
		return
	}

	// FR-8.1：Z-Library 元数据随任务落库（M2 回填免重新下载），并回写快照字段
	if metaBytes, merr := json.Marshal(book); merr == nil {
		if serr := store.UpdateZlibraryTaskSnapshot(task.ID, task.Title, task.Author, ext, task.FileSize, string(metaBytes)); serr != nil {
			log.Printf("[ZLIBRARY] task %d: update snapshot failed: %v", task.ID, serr)
		}
	}
	// FR-2.4：个人下载域名缓存备用
	if p.settings != nil && book.DlDomain != "" {
		_ = p.settings.SavePersonalDomain(book.DlDomain)
	}

	// ── 2. 目标书库仍存在且启用？（PM §3.3-3：落盘前再次校验）──
	lib, err := store.GetLibraryByID(task.LibraryID)
	if err != nil {
		p.fail(task, zlibrary.NewZError(zlibrary.CodeUpstream, "读取目标书库失败", err), false)
		return
	}
	if lib == nil || !lib.Enabled {
		p.fail(task, zlibrary.NewZError(zlibrary.CodeUpstream,
			"目标书库不存在或已停用，请重新选择书库后重试", nil), false)
		return
	}

	// 扩展名与书库类型匹配（Z_EXT_MISMATCH 后端兜底）
	if !extAllowedInLibrary(lib, ext) {
		p.fail(task, zlibrary.NewZError(zlibrary.CodeExtMismatch,
			"."+ext+" 无法加入"+libraryTypeLabel(lib.Type), nil), false)
		return
	}

	// ── 3. 下载前磁盘预检（PM §3.3-6：不发起下载、不耗配额）──
	if err := checkDiskSpace(lib.RootPath, task.FileSize); err != nil {
		p.fail(task, err, false)
		return
	}

	// ── 4. downloading：流式写临时文件 + 进度节流 ──
	downloadURL := book.DownloadURL
	// s-library: z-lib.by 等镜像的 dl 字段是相对路径（如 "/dl/xxx"），补全当前域名；
	// 个人域名（绝对 dl 地址）不受影响。
	if strings.HasPrefix(downloadURL, "/") {
		// 个人域名是唯一能通过 Cloudflare 下载防护的通道（镜像域名的 /dl/ 有人机验证），
		// 其次才是当前镜像域名
		base := ""
		if p.settings != nil {
			base = p.settings.PersonalDomain()
		}
		if base == "" {
			base = p.client.CurrentDomain()
		}
		if base != "" {
			downloadURL = strings.TrimRight(base, "/") + downloadURL
		}
	}
	tmpPath := p.tmpPath(task.ID, ext)
	quotaConsumed := false // 一旦发起下载，配额视为已消耗（如实标注，PM §3.3-1）

	var validateErr error
	for attempt := 1; attempt <= downloadMaxAttempts; attempt++ {
		if ctx.Err() != nil {
			p.markCanceled(task, tmpPath)
			return
		}
		_ = store.UpdateZlibraryTaskStage(task.ID, model.ZlibStageDownloading)
		if attempt > 1 {
			quotaConsumed = true
		}
		dlErr := p.downloadToFile(ctx, task, downloadURL, tmpPath)
		if dlErr != nil {
			if ctx.Err() != nil {
				p.markCanceled(task, tmpPath)
				return
			}
			p.fail(task, dlErr, true)
			return
		}
		quotaConsumed = true

		// ── 5. validating：魔数+扩展名校验（失败重试下载 1 次）──
		_ = store.UpdateZlibraryTaskStage(task.ID, model.ZlibStageValidating)
		validateErr = validateFile(ext, tmpPath)
		if validateErr == nil {
			break
		}
		log.Printf("[ZLIBRARY] task %d validation failed (attempt %d): %v", task.ID, attempt, validateErr)
		_ = os.Remove(tmpPath)
	}
	if validateErr != nil {
		p.fail(task, zlibrary.NewZError(zlibrary.CodeUpstream,
			"文件校验失败："+validateErr.Error()+"（已重试下载）", validateErr), quotaConsumed)
		return
	}

	// ── 6. moving：磁盘复检 → 规范化命名 → move ──
	_ = store.UpdateZlibraryTaskStage(task.ID, model.ZlibStageMoving)
	info, serr := os.Stat(tmpPath)
	if serr != nil {
		p.fail(task, zlibrary.NewZError(zlibrary.CodeUpstream, "临时文件丢失", serr), quotaConsumed)
		return
	}
	if err := checkDiskSpace(lib.RootPath, info.Size()); err != nil {
		p.fail(task, err, quotaConsumed)
		return
	}
	finalName, err := uniqueTargetName(lib.RootPath, task.Title, task.Author, ext)
	if err != nil {
		p.fail(task, zlibrary.NewZError(zlibrary.CodeUpstream, "生成目标文件名失败", err), quotaConsumed)
		return
	}
	targetPath := filepath.Join(lib.RootPath, finalName)
	if err := os.Rename(tmpPath, targetPath); err != nil {
		// 跨卷 move 退化为 copy+delete
		if cerr := copyThenRemove(tmpPath, targetPath); cerr != nil {
			p.fail(task, zlibrary.NewZError(zlibrary.CodeDiskFull, "落盘失败："+cerr.Error(), cerr), quotaConsumed)
			return
		}
	}

	// ── 7. scanning：进程内调用 SyncLibraryByID（全局互斥 → 指数退避重试）──
	_ = store.UpdateZlibraryTaskStage(task.ID, model.ZlibStageScanning)
	if scanErr := p.scanWithBackoff(ctx, lib.ID); scanErr != nil {
		if ctx.Err() != nil {
			// 取消发生在扫描阶段：文件已落盘，保留文件（磁盘是事实来源），任务标 canceled
			p.markCanceled(task, "")
			return
		}
		p.failScan(task, tmpPath)
		return
	}

	// ── 8. 确定性 ID 匹配 + 映射回写（架构 §10-C5）──
	localID := store.PathToID(lib.ID, finalName)
	comic, cerr := store.GetComicByID(localID)
	if cerr != nil || comic == nil {
		log.Printf("[ZLIBRARY] task %d: scanned entry %s not found in DB (file kept: %s)",
			task.ID, localID, targetPath)
		p.failScan(task, "")
		return
	}

	mapping := &model.ZlibraryImportedBook{
		ZbookID:     task.ZbookID,
		ZbookHash:   task.ZbookHash,
		LocalBookID: localID,
		LibraryID:   lib.ID,
		ImportedBy:  task.UserID,
	}
	if err := store.UpsertZlibraryImportedBook(mapping); err != nil {
		log.Printf("[ZLIBRARY] task %d: upsert imported mapping failed: %v", task.ID, err)
	}

	// 配额刷新（尽力而为）+ 完成
	if p.quota != nil {
		qctx, qcancel := context.WithTimeout(context.Background(), 15*time.Second)
		if _, err := p.quota.RefreshQuota(qctx); err != nil {
			log.Printf("[ZLIBRARY] task %d: quota refresh failed: %v", task.ID, err)
		}
		qcancel()
	}
	if _, err := store.CompleteZlibraryTask(task.ID, localID); err != nil {
		log.Printf("[ZLIBRARY] task %d: mark completed failed: %v", task.ID, err)
		return
	}
	// 历史裁剪（FR-5.4）
	if _, err := store.TrimZlibraryTaskHistory(p.historyKeep); err != nil {
		log.Printf("[ZLIBRARY] trim task history failed: %v", err)
	}
	log.Printf("[ZLIBRARY] task %d completed: %s -> %s (%s)", task.ID, task.Title, localID, finalName)
}

// downloadToFile 流式下载到临时文件，进度节流写库。
func (p *Pipeline) downloadToFile(ctx context.Context, task *model.ZlibraryDownloadTask, url, tmpPath string) error {
	if err := os.MkdirAll(p.tmpDir, 0o755); err != nil {
		return zlibrary.NewZError(zlibrary.CodeUpstream, "创建临时目录失败", err)
	}
	f, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return zlibrary.NewZError(zlibrary.CodeUpstream, "创建临时文件失败", err)
	}
	defer f.Close()

	throttle := &progressThrottler{taskID: task.ID, last: time.Now().Add(-progressThrottleInterval)}
	err = p.client.Download(ctx, url, f, throttle.report)
	if err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return f.Sync()
}

// progressThrottler 进度节流（≥2% 或 ≥500ms 才写库）。
type progressThrottler struct {
	taskID    int64
	last      time.Time
	lastPct   int
	lastTotal int64
}

func (t *progressThrottler) report(done, total int64) {
	if total <= 0 {
		return
	}
	pct := int(done * 100 / total)
	if pct < 0 {
		pct = 0
	}
	if pct > 99 {
		pct = 99 // 100 留给 completed
	}
	now := time.Now()
	if pct-t.lastPct < progressThrottleDelta && now.Sub(t.last) < progressThrottleInterval && pct != 99 {
		return
	}
	t.last = now
	t.lastPct = pct
	if err := store.UpdateZlibraryTaskProgress(t.taskID, pct); err != nil {
		log.Printf("[ZLIBRARY] task %d progress write failed: %v", t.taskID, err)
	}
}

// scanWithBackoff 扫描互斥退避：最多 len(scanBackoff) 次，间隔 2s/4s/8s（任务要求）。
func (p *Pipeline) scanWithBackoff(ctx context.Context, libraryID string) error {
	backoffs := p.scanBackoff
	if len(backoffs) == 0 {
		backoffs = defaultScanBackoff()
	}
	var lastErr error
	for i := range backoffs {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if i > 0 {
			log.Printf("[ZLIBRARY] scan busy for library %s, retry %d/%d after %v",
				libraryID, i, len(backoffs), backoffs[i])
			select {
			case <-time.After(backoffs[i]):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		_, _, err := p.scanFn(libraryID)
		if err == nil {
			return nil
		}
		lastErr = err
		if !strings.Contains(err.Error(), "another library sync is already running") {
			// 非互斥冲突（书库被删等）：重试无意义，直接失败
			return err
		}
	}
	return lastErr
}

// failScan 扫描失败分支：文件保留（磁盘是事实来源，PM §3.3-2），任务标 Z_SCAN_FAILED。
func (p *Pipeline) failScan(task *model.ZlibraryDownloadTask, tmpPath string) {
	if tmpPath != "" {
		_ = os.Remove(tmpPath)
	}
	_, _ = store.FailZlibraryTask(task.ID, zlibrary.CodeScanFailed,
		"文件已下载但入库失败，请对目标书库手动扫描或查看日志（文件已保留）", true)
}

// fail 通用失败分支：清理临时文件 + 结构化错误码。
func (p *Pipeline) fail(task *model.ZlibraryDownloadTask, err error, quotaConsumed bool) {
	ze := zlibrary.AsZError(err)
	log.Printf("[ZLIBRARY] task %d failed: %s (%s)", task.ID, ze.Code, ze.Message)
	cleanupTaskTmp(p.tmpDir, task.ID)
	if _, ferr := store.FailZlibraryTask(task.ID, ze.Code, ze.Message, quotaConsumed); ferr != nil {
		log.Printf("[ZLIBRARY] task %d mark failed failed: %v", task.ID, ferr)
	}
}

// cleanupTaskTmp 删除某任务的全部临时文件（zlib-{id}.*）。
func cleanupTaskTmp(dir string, id int64) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	prefix := "zlib-" + itoa64(id) + "."
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), prefix) {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

// markCanceled 取消分支：清理临时文件 + 状态置 canceled（乐观）。
func (p *Pipeline) markCanceled(task *model.ZlibraryDownloadTask, tmpPath string) {
	if tmpPath != "" {
		_ = os.Remove(tmpPath)
	}
	cleanupTaskTmp(p.tmpDir, task.ID)
	if ok, err := store.CancelZlibraryTask(task.ID); err != nil || !ok {
		log.Printf("[ZLIBRARY] task %d cancel mark: ok=%v err=%v", task.ID, ok, err)
	}
}

// tmpPath 临时文件路径：DATA_DIR/tmp/zlib-{taskId}.{ext}。
func (p *Pipeline) tmpPath(taskID int64, ext string) string {
	name := "zlib-" + itoa64(taskID)
	if ext != "" {
		name += "." + ext
	}
	return filepath.Join(p.tmpDir, name)
}

// ============================================================
// 工具函数
// ============================================================

// checkDiskSpace 目标卷剩余空间 > fileSize × 1.1（FR-4.5）。fileSize<=0 时跳过。
func checkDiskSpace(dir string, fileSize int64) error {
	if fileSize <= 0 {
		return nil
	}
	free, err := diskFreeBytes(dir)
	if err != nil {
		// 无法确认剩余空间时选择放行（下游 move 失败会兜底报错）
		log.Printf("[ZLIBRARY] disk free check for %s failed: %v", dir, err)
		return nil
	}
	if free < fileSize+fileSize/10 {
		return zlibrary.NewZError(zlibrary.CodeDiskFull,
			"目标书库所在磁盘空间不足，请清理空间或更换书库", nil)
	}
	return nil
}

// extAllowedInLibrary 书库类型与扩展名匹配（novel=NovelExtensions，
// comic=SupportedExtensions，mixed=并集）。
func extAllowedInLibrary(lib *model.Library, ext string) bool {
	ext = normalizeExtForTask(ext)
	if ext == "" {
		return false
	}
	dot := "." + ext
	switch lib.Type {
	case "novel":
		return containsExt(config.NovelExtensions, dot)
	case "mixed":
		return containsExt(config.NovelExtensions, dot) || containsExt(config.SupportedExtensions, dot)
	default: // comic
		return containsExt(config.SupportedExtensions, dot)
	}
}

func containsExt(list []string, ext string) bool {
	for _, e := range list {
		if e == ext {
			return true
		}
	}
	return false
}

func libraryTypeLabel(t string) string {
	switch t {
	case "novel":
		return "小说书库"
	case "mixed":
		return "混合书库"
	default:
		return "漫画书库"
	}
}

func normalizeExtForTask(ext string) string {
	ext = strings.ToLower(strings.TrimSpace(ext))
	return strings.TrimPrefix(ext, ".")
}

var illegalNameChars = regexp.MustCompile(`[\\/:*?"<>|\x00-\x1f]`)
var spaceRun = regexp.MustCompile(`\s+`)

// sanitizeFilename 清洗文件名片段：非法字符→空格、压缩空白、去掉首尾点空格。
func sanitizeFilename(s string, maxBytes int) string {
	s = illegalNameChars.ReplaceAllString(s, " ")
	s = spaceRun.ReplaceAllString(s, " ")
	s = strings.Trim(s, " .")
	if s == "" {
		return ""
	}
	if maxBytes <= 0 {
		maxBytes = 100
	}
	return truncateUTF8(s, maxBytes)
}

// truncateUTF8 按字节截断但保证 UTF-8 边界完整。
func truncateUTF8(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	for maxBytes > 0 && !isUTF8Start(s[maxBytes]) {
		maxBytes--
	}
	return s[:maxBytes]
}

func isUTF8Start(b byte) bool { return b&0xC0 != 0x80 }

// uniqueTargetName 规范化命名「书名 - 作者.ext」，重名追加 (2)/(3)…（FR-4 步骤4b）。
func uniqueTargetName(root, title, author, ext string) (string, error) {
	namePart := sanitizeFilename(title, 100)
	if author = sanitizeFilename(author, 60); author != "" {
		namePart = strings.TrimSpace(namePart + " - " + author)
	}
	if namePart == "" {
		namePart = "zlib-book"
	}
	base := namePart + "." + ext
	candidate := filepath.Join(root, base)
	for i := 2; i < 1000; i++ {
		if _, err := os.Stat(candidate); os.IsNotExist(err) {
			return base, nil
		}
		base = fmt.Sprintf("%s (%d).%s", namePart, i, ext)
		candidate = filepath.Join(root, base)
	}
	return "", fmt.Errorf("cannot find unique filename for %s", namePart)
}

// copyThenRemove 跨卷落盘兜底：copy → 校验大小 → 删临时文件。
func copyThenRemove(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		_ = os.Remove(dst)
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(dst)
		return err
	}
	if si, err := os.Stat(src); err == nil {
		if di, err := os.Stat(dst); err != nil || di.Size() != si.Size() {
			_ = os.Remove(dst)
			return fmt.Errorf("size mismatch after copy")
		}
	}
	return os.Remove(src)
}

// validateFile 魔数+非空校验。
func validateFile(ext, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := zlibrary.ValidateMagic(ext, f); err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.Size() == 0 {
		return fmt.Errorf("下载内容为空")
	}
	return nil
}
