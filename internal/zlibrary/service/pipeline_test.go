// s-library: 流水线与队列单测 —— mock ZClient + 内存 SQLite + t.TempDir() 书库根目录。
// 覆盖：成功链路（映射回写+PathToID 匹配）、扫描互斥退避、扫描失败文件保留、
// 校验失败重试后放弃、扩展名不匹配、下载中取消、队列并发/恢复/去重查询。
package zlibservice

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nowen-reader/nowen-reader/internal/model"
	"github.com/nowen-reader/nowen-reader/internal/store"
	zlibrary "github.com/nowen-reader/nowen-reader/internal/zlibrary"
)

// ============================================================
// 测试基建
// ============================================================

func setupServiceTestDB(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	if err := store.InitDB(filepath.Join(dir, "test.db")); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	if err := store.RunMigrations(); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}
	t.Cleanup(store.CloseDB)
}

func createTestLibrary(t *testing.T, root string, libType string) *model.Library {
	t.Helper()
	lib := &model.Library{
		ID:        fmt.Sprintf("lib-%d", time.Now().UnixNano()),
		Name:      "zlib-test-lib",
		Type:      libType,
		RootPath:  root,
		RootPaths: []string{root},
		Enabled:   true,
	}
	if err := store.CreateLibrary(lib); err != nil {
		t.Fatalf("CreateLibrary: %v", err)
	}
	return lib
}

// mockClient 固定剧本的 ZClient。
type mockClient struct {
	book    *zlibrary.ZBook
	bookErr error
	formats []zlibrary.ZFormat
	domain  string // s-library: CurrentDomain 返回值（相对下载路径补全测试用）

	downloadFn func(ctx context.Context, url string, w io.Writer, progress zlibrary.ProgressFunc) error

	downloadCalls int32
	mu            sync.Mutex
}

// CurrentDomain s-library: 返回预设域名（相对下载路径补全用）。
func (m *mockClient) CurrentDomain() string {
	if m.domain != "" {
		return m.domain
	}
	return "https://z-lib.by"
}

func (m *mockClient) Book(ctx context.Context, id, hash string) (*zlibrary.ZBook, error) {
	if m.bookErr != nil {
		return nil, m.bookErr
	}
	b := *m.book
	return &b, nil
}

func (m *mockClient) Formats(ctx context.Context, id, hash string) ([]zlibrary.ZFormat, error) {
	return m.formats, nil
}

func (m *mockClient) Download(ctx context.Context, url string, w io.Writer, progress zlibrary.ProgressFunc) error {
	atomic.AddInt32(&m.downloadCalls, 1)
	if m.downloadFn != nil {
		return m.downloadFn(ctx, url, w, progress)
	}
	content := []byte("PK\x03\x04 fake epub content for testing")
	if _, err := w.Write(content); err != nil {
		return err
	}
	if progress != nil {
		progress(int64(len(content)), int64(len(content)))
	}
	return nil
}

func newTestTask(t *testing.T, libID string) *model.ZlibraryDownloadTask {
	t.Helper()
	task := &model.ZlibraryDownloadTask{
		UserID: "user-a", ZbookID: "42889905", ZbookHash: "a1b2c3d4e5",
		Title: "三体", Author: "刘慈欣", Ext: "epub", FileSize: 36,
		LibraryID: libID,
	}
	if err := store.CreateZlibraryDownloadTask(task); err != nil {
		t.Fatalf("CreateZlibraryDownloadTask: %v", err)
	}
	return task
}

// insertComicRow 模拟扫描器入库的最小行为（生产中由 SyncLibraryByID 完成）。
// 已存在时静默忽略（等价于扫描器的 INSERT OR IGNORE 语义）。
func insertComicRow(t *testing.T, libID, relPath string) string {
	t.Helper()
	id := store.PathToID(libID, relPath)
	_, err := store.DB().Exec(`INSERT OR IGNORE INTO "Comic" ("id", "filename", "title", "type", "libraryId", "relativePath", "addedAt")
		VALUES (?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)`,
		id, relPath, "三体", "novel", libID, relPath)
	if err != nil {
		t.Logf("insert comic row %s: %v", relPath, err)
	}
	return id
}

// dirScanFn 返回一个模拟扫描器：扫描书库根目录，为每个文件建立 Comic 行。
func dirScanFn(t *testing.T, root string) func(string) (int, int, error) {
	return func(libraryID string) (int, int, error) {
		entries, err := os.ReadDir(root)
		if err != nil {
			return 0, 0, err
		}
		n := 0
		for _, e := range entries {
			if !e.IsDir() {
				insertComicRow(t, libraryID, e.Name())
				n++
			}
		}
		return n, 0, nil
	}
}

func newTestPipeline(client ZClient, scanFn func(string) (int, int, error), tmpDir string) *Pipeline {
	return &Pipeline{
		client:      client,
		scanFn:      scanFn,
		tmpDir:      tmpDir,
		historyKeep: HistoryKeep,
		scanBackoff: []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond},
	}
}

func waitTaskStatus(t *testing.T, id int64, statuses ...string) *model.ZlibraryDownloadTask {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		task, err := store.GetZlibraryDownloadTask(id)
		if err != nil {
			t.Fatalf("get task: %v", err)
		}
		for _, s := range statuses {
			if task.Status == s {
				return task
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	task, _ := store.GetZlibraryDownloadTask(id)
	t.Fatalf("task %d did not reach %v, current=%+v", id, statuses, task)
	return nil
}

// ============================================================
// Pipeline 用例
// ============================================================

func TestPipelineSuccessWritesMapping(t *testing.T) {
	setupServiceTestDB(t)
	root := t.TempDir()
	lib := createTestLibrary(t, root, "novel")
	task := newTestTask(t, lib.ID)

	var scannedLib string
	scanFn := func(libraryID string) (int, int, error) {
		scannedLib = libraryID
		relPath := "三体 - 刘慈欣.epub"
		insertComicRow(t, libraryID, relPath)
		return 1, 0, nil
	}
	client := &mockClient{book: &zlibrary.ZBook{
		ID: task.ZbookID, Hash: task.ZbookHash, Title: "三体", Author: "刘慈欣",
		Ext: "epub", FileSize: 36, DownloadURL: "https://personal.example.org/dl/1",
	}}
	p := newTestPipeline(client, scanFn, t.TempDir())
	p.Run(context.Background(), task)

	final := waitTaskStatus(t, task.ID, model.ZlibTaskStatusCompleted)
	if final.LocalBookID == "" {
		t.Fatal("localBookId should be filled")
	}
	// 确定性 ID 匹配
	expected := store.PathToID(lib.ID, "三体 - 刘慈欣.epub")
	if final.LocalBookID != expected {
		t.Fatalf("localBookId = %q, want %q", final.LocalBookID, expected)
	}
	if scannedLib != lib.ID {
		t.Fatalf("scan called with %q, want %q", scannedLib, lib.ID)
	}
	// 文件落盘且命名规范（FR-8.3）
	data, err := os.ReadFile(filepath.Join(root, "三体 - 刘慈欣.epub"))
	if err != nil {
		t.Fatalf("target file missing: %v", err)
	}
	if !strings.HasPrefix(string(data), "PK") {
		t.Fatal("file content mismatch")
	}
	// 映射写入
	m, _ := store.GetZlibraryImportedBook(task.ZbookID, task.ZbookHash)
	if m == nil || m.LocalBookID != expected || m.ImportedBy != "user-a" {
		t.Fatalf("mapping = %+v", m)
	}
	// 临时文件已清理
	entries, _ := os.ReadDir(p.tmpDir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "zlib-") {
			t.Fatalf("tmp file leaked: %s", e.Name())
		}
	}
}

func TestPipelineScanBusyRetriesThenSucceeds(t *testing.T) {
	setupServiceTestDB(t)
	root := t.TempDir()
	lib := createTestLibrary(t, root, "novel")
	task := newTestTask(t, lib.ID)

	var attempts int32
	scanFn := func(libraryID string) (int, int, error) {
		if atomic.AddInt32(&attempts, 1) <= 2 {
			return 0, 0, errors.New("another library sync is already running")
		}
		insertComicRow(t, libraryID, "三体 - 刘慈欣.epub")
		return 1, 0, nil
	}
	client := &mockClient{book: &zlibrary.ZBook{
		ID: task.ZbookID, Hash: task.ZbookHash, Title: "三体", Author: "刘慈欣",
		Ext: "epub", DownloadURL: "https://personal.example.org/dl/1",
	}}
	p := newTestPipeline(client, scanFn, t.TempDir())
	p.Run(context.Background(), task)

	final := waitTaskStatus(t, task.ID, model.ZlibTaskStatusCompleted)
	if final.LocalBookID == "" {
		t.Fatal("should complete after backoff")
	}
	if got := atomic.LoadInt32(&attempts); got != 3 {
		t.Fatalf("scan attempts = %d, want 3", got)
	}
}

func TestPipelineScanFailsKeepsFile(t *testing.T) {
	setupServiceTestDB(t)
	root := t.TempDir()
	lib := createTestLibrary(t, root, "novel")
	task := newTestTask(t, lib.ID)

	scanFn := func(libraryID string) (int, int, error) {
		return 0, 0, errors.New("another library sync is already running")
	}
	client := &mockClient{book: &zlibrary.ZBook{
		ID: task.ZbookID, Hash: task.ZbookHash, Title: "三体", Author: "刘慈欣",
		Ext: "epub", DownloadURL: "https://personal.example.org/dl/1",
	}}
	p := newTestPipeline(client, scanFn, t.TempDir())
	p.Run(context.Background(), task)

	final := waitTaskStatus(t, task.ID, model.ZlibTaskStatusFailed)
	if final.ErrorCode != "Z_SCAN_FAILED" {
		t.Fatalf("errorCode = %q, want Z_SCAN_FAILED", final.ErrorCode)
	}
	if !final.QuotaConsumed {
		t.Fatal("quota should be marked consumed after download")
	}
	// 文件保留（PM §3.3-2：磁盘是事实来源）
	if _, err := os.Stat(filepath.Join(root, "三体 - 刘慈欣.epub")); err != nil {
		t.Fatalf("downloaded file should be kept: %v", err)
	}
}

func TestPipelineValidationFailureCleansUp(t *testing.T) {
	setupServiceTestDB(t)
	root := t.TempDir()
	lib := createTestLibrary(t, root, "novel")
	task := newTestTask(t, lib.ID)

	// 内容不是合法 EPUB（魔数不符）→ 两次下载校验都失败
	client := &mockClient{
		book: &zlibrary.ZBook{ID: task.ZbookID, Hash: task.ZbookHash, Title: "Bad",
			Author: "x", Ext: "epub", DownloadURL: "https://p.example/dl"},
		downloadFn: func(ctx context.Context, url string, w io.Writer, progress zlibrary.ProgressFunc) error {
			_, err := w.Write([]byte("this is definitely not a zip file"))
			return err
		},
	}
	p := newTestPipeline(client, func(string) (int, int, error) { return 0, 0, nil }, t.TempDir())
	p.Run(context.Background(), task)

	final := waitTaskStatus(t, task.ID, model.ZlibTaskStatusFailed)
	if atomic.LoadInt32(&client.downloadCalls) != 2 {
		t.Fatalf("download attempts = %d, want 2（校验失败重试 1 次）", client.downloadCalls)
	}
	if final.QuotaConsumed != true {
		t.Fatal("quota consumed should be marked after download")
	}
	// 不产生半截文件（PM §4.3）
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("library root should be empty, got %v", entries)
	}
	// 临时文件清理
	entries, _ = os.ReadDir(p.tmpDir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "zlib-") {
			t.Fatalf("tmp file leaked: %s", e.Name())
		}
	}
}

func TestPipelineExtMismatch(t *testing.T) {
	setupServiceTestDB(t)
	root := t.TempDir()
	lib := createTestLibrary(t, root, "comic") // 漫画书库
	task := newTestTask(t, lib.ID)
	task.Ext = "epub" // epub 无法加入漫画书库
	if err := store.CreateZlibraryDownloadTask(task); err != nil {
		t.Fatal(err)
	}

	client := &mockClient{book: &zlibrary.ZBook{
		ID: task.ZbookID, Hash: task.ZbookHash, Title: "x", Ext: "epub",
		DownloadURL: "https://p.example/dl",
	}}
	p := newTestPipeline(client, func(string) (int, int, error) { return 0, 0, nil }, t.TempDir())
	p.Run(context.Background(), task)

	final := waitTaskStatus(t, task.ID, model.ZlibTaskStatusFailed)
	if final.ErrorCode != "Z_EXT_MISMATCH" {
		t.Fatalf("errorCode = %q, want Z_EXT_MISMATCH", final.ErrorCode)
	}
	if final.QuotaConsumed {
		t.Fatal("no download started → quota should NOT be consumed")
	}
	if got := atomic.LoadInt32(&client.downloadCalls); got != 0 {
		t.Fatalf("download calls = %d, want 0", got)
	}
}

func TestPipelineCancelDuringDownload(t *testing.T) {
	setupServiceTestDB(t)
	root := t.TempDir()
	lib := createTestLibrary(t, root, "novel")
	task := newTestTask(t, lib.ID)

	ctx, cancel := context.WithCancel(context.Background())
	client := &mockClient{
		book: &zlibrary.ZBook{ID: task.ZbookID, Hash: task.ZbookHash, Title: "三体",
			Author: "刘慈欣", Ext: "epub", DownloadURL: "https://p.example/dl"},
		downloadFn: func(dctx context.Context, url string, w io.Writer, progress zlibrary.ProgressFunc) error {
			// 模拟慢速下载：等 ctx 取消
			<-dctx.Done()
			return dctx.Err()
		},
	}
	p := newTestPipeline(client, func(string) (int, int, error) { return 0, 0, nil }, t.TempDir())
	done := make(chan struct{})
	go func() {
		defer close(done)
		p.Run(ctx, task)
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done

	final, err := store.GetZlibraryDownloadTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if final.Status != model.ZlibTaskStatusCanceled {
		t.Fatalf("status = %q, want canceled", final.Status)
	}
	entries, _ := os.ReadDir(p.tmpDir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "zlib-") {
			t.Fatalf("tmp file leaked after cancel: %s", e.Name())
		}
	}
}

func TestPipelineUpstreamNotFound(t *testing.T) {
	setupServiceTestDB(t)
	root := t.TempDir()
	lib := createTestLibrary(t, root, "novel")
	task := newTestTask(t, lib.ID)

	client := &mockClient{bookErr: zlibrary.NewZError(zlibrary.CodeNotFound, "该书在 Z-Library 已不可用", nil)}
	p := newTestPipeline(client, func(string) (int, int, error) { return 0, 0, nil }, t.TempDir())
	p.Run(context.Background(), task)

	final := waitTaskStatus(t, task.ID, model.ZlibTaskStatusFailed)
	if final.ErrorCode != "Z_NOT_FOUND" {
		t.Fatalf("errorCode = %q", final.ErrorCode)
	}
	if final.QuotaConsumed {
		t.Fatal("fetch_info 失败未发起下载 → 不耗配额")
	}
}

func TestPipelineDedupCheckStaleMapping(t *testing.T) {
	setupServiceTestDB(t)

	// 已入库且本地条目存在 → imported
	lib := createTestLibrary(t, t.TempDir(), "novel")
	localID := insertComicRow(t, lib.ID, "三体 - 刘慈欣.epub")
	_ = store.UpsertZlibraryImportedBook(&model.ZlibraryImportedBook{
		ZbookID: "42", ZbookHash: "h1", LocalBookID: localID, LibraryID: lib.ID, ImportedBy: "u",
	})
	info, err := CheckDedup("42", "h1")
	if err != nil || info.Status != DedupImported || info.LocalBookID != localID {
		t.Fatalf("dedup imported: %+v %v", info, err)
	}

	// 本地条目被删除 → 标记失效 + 允许再入库（FR-7.4）
	store.DB().Exec(`DELETE FROM "Comic" WHERE "id" = ?`, localID)
	info, err = CheckDedup("42", "h1")
	if err != nil || info.Status != DedupNone {
		t.Fatalf("stale mapping should become none: %+v %v", info, err)
	}
	m, _ := store.GetZlibraryImportedBook("42", "h1")
	if m == nil || m.LocalBookID != "" {
		t.Fatalf("mapping should be kept with cleared local id: %+v", m)
	}

	// 队列中 → queued
	task := &model.ZlibraryDownloadTask{UserID: "u", ZbookID: "42", ZbookHash: "h2", LibraryID: lib.ID}
	_ = store.CreateZlibraryDownloadTask(task)
	info, err = CheckDedup("42", "h2")
	if err != nil || info.Status != DedupQueued {
		t.Fatalf("dedup queued: %+v %v", info, err)
	}
}

// ============================================================
// Queue 用例
// ============================================================

func TestQueueEnqueueRunsPipelineAndCompletes(t *testing.T) {
	setupServiceTestDB(t)
	root := t.TempDir()
	lib := createTestLibrary(t, root, "novel")

	scanFn := func(libraryID string) (int, int, error) {
		insertComicRow(t, libraryID, "三体 - 刘慈欣.epub")
		return 1, 0, nil
	}
	client := &mockClient{book: &zlibrary.ZBook{
		ID: "42889905", Hash: "a1b2c3d4e5", Title: "三体", Author: "刘慈欣",
		Ext: "epub", DownloadURL: "https://p.example/dl",
	}}

	q := &Queue{
		cancels: map[int64]context.CancelFunc{},
		wake:    make(chan struct{}, 1),
		stop:    make(chan struct{}),
		scanFn:  scanFn,
	}
	q.newPipeline = func(*Queue) *Pipeline { return newTestPipeline(client, scanFn, t.TempDir()) }
	go q.loop()
	defer q.Stop()

	task := &model.ZlibraryDownloadTask{
		UserID: "user-a", ZbookID: "42889905", ZbookHash: "a1b2c3d4e5",
		Title: "三体", Author: "刘慈欣", Ext: "epub", LibraryID: lib.ID,
	}
	if err := q.Enqueue(task); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	final := waitTaskStatus(t, task.ID, model.ZlibTaskStatusCompleted, model.ZlibTaskStatusFailed)
	if final.Status != model.ZlibTaskStatusCompleted {
		t.Fatalf("task = %+v", final)
	}
}

func TestQueueConcurrencyOne(t *testing.T) {
	setupServiceTestDB(t)
	root := t.TempDir()
	lib := createTestLibrary(t, root, "novel")

	var concurrent, peak int32
	var mu sync.Mutex
	// 让下载变慢以观察并发
	client := &mockClient{
		book: &zlibrary.ZBook{ID: "x", Hash: "y", Title: "三体", Author: "刘慈欣", Ext: "epub",
			DownloadURL: "https://p.example/dl"},
		downloadFn: func(ctx context.Context, url string, w io.Writer, progress zlibrary.ProgressFunc) error {
			cur := atomic.AddInt32(&concurrent, 1)
			mu.Lock()
			if cur > peak {
				peak = cur
			}
			mu.Unlock()
			time.Sleep(100 * time.Millisecond)
			_, werr := w.Write([]byte("PK\x03\x04payload"))
			atomic.AddInt32(&concurrent, -1)
			return werr
		},
	}
	// 扫描成功：模拟真实扫描器——为书库根目录下所有文件建行
	scanFn := dirScanFn(t, root)

	q := &Queue{cancels: map[int64]context.CancelFunc{}, wake: make(chan struct{}, 1), stop: make(chan struct{}), scanFn: scanFn}
	q.newPipeline = func(*Queue) *Pipeline { return newTestPipeline(client, scanFn, t.TempDir()) }
	go q.loop()
	defer q.Stop()

	// 三个任务
	for i := 0; i < 3; i++ {
		task := &model.ZlibraryDownloadTask{
			UserID: "user-a", ZbookID: "42889905", ZbookHash: fmt.Sprintf("hash-%d", i),
			Title: "三体", Author: "刘慈欣", Ext: "epub", LibraryID: lib.ID,
		}
		if err := q.Enqueue(task); err != nil {
			t.Fatal(err)
		}
	}
	// 全部终态
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		_, total, _ := store.ListZlibraryDownloadTasks(store.ZlibraryTaskListOptions{Status: model.ZlibTaskStatusCompleted, Page: 1, Limit: 10})
		if total >= 3 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	_, completed, _ := store.ListZlibraryDownloadTasks(store.ZlibraryTaskListOptions{Status: model.ZlibTaskStatusCompleted, Page: 1, Limit: 10})
	if completed != 3 {
		t.Fatalf("completed = %d, want 3", completed)
	}
	// 默认并发=1（settings 未配置时 Normalized 默认 1）
	mu.Lock()
	p := peak
	mu.Unlock()
	if p != 1 {
		t.Fatalf("peak concurrency = %d, want 1（默认串行）", p)
	}
}

func TestQueueCancelQueuedTask(t *testing.T) {
	setupServiceTestDB(t)
	lib := createTestLibrary(t, t.TempDir(), "novel")

	// 拦住 pipeline：下载阻塞直至取消
	client := &mockClient{
		book: &zlibrary.ZBook{ID: "x", Hash: "y", Title: "t", Author: "a", Ext: "epub", DownloadURL: "u"},
		downloadFn: func(ctx context.Context, url string, w io.Writer, progress zlibrary.ProgressFunc) error {
			<-ctx.Done()
			return ctx.Err()
		},
	}
	scanFn := func(string) (int, int, error) { return 0, 0, nil }
	q := &Queue{cancels: map[int64]context.CancelFunc{}, wake: make(chan struct{}, 1), stop: make(chan struct{}), scanFn: scanFn}
	q.newPipeline = func(*Queue) *Pipeline { return newTestPipeline(client, scanFn, t.TempDir()) }
	go q.loop()
	defer q.Stop()

	task := &model.ZlibraryDownloadTask{
		UserID: "user-a", ZbookID: "x", ZbookHash: "y", Title: "t", Ext: "epub", LibraryID: lib.ID,
	}
	if err := q.Enqueue(task); err != nil {
		t.Fatal(err)
	}
	// 等 pipeline 起来（downloading），再取消
	waitTaskStatus(t, task.ID, model.ZlibTaskStatusDownloading)
	if !q.Cancel(task.ID) {
		t.Fatal("cancel should succeed")
	}
	final := waitTaskStatus(t, task.ID, model.ZlibTaskStatusCanceled)
	if final.Status != model.ZlibTaskStatusCanceled {
		t.Fatalf("status = %q", final.Status)
	}
}
