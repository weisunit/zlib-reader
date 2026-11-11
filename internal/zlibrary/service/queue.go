// s-library: 下载任务队列（FR-4/FR-5，架构 §6）。
// DB 持久化 + dispatcher goroutine + 并发信号量（默认 1，可配 1~3）+
// 进程重启恢复 + 取消感知。进度节流在 pipeline 内实现。
package zlibservice

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/nowen-reader/nowen-reader/internal/config"
	"github.com/nowen-reader/nowen-reader/internal/model"
	"github.com/nowen-reader/nowen-reader/internal/service"
	"github.com/nowen-reader/nowen-reader/internal/store"
	zlibrary "github.com/nowen-reader/nowen-reader/internal/zlibrary"
)

// HistoryKeep 任务历史保留条数（FR-5.4）。
const HistoryKeep = 200

// Queue 任务队列。
type Queue struct {
	mu       sync.Mutex
	active   int
	cancels  map[int64]context.CancelFunc
	wake     chan struct{}
	stop     chan struct{}
	stopOnce sync.Once
	started  bool

	// 可注入依赖（测试用）
	scanFn      func(libraryID string) (added, removed int, err error)
	quotaFn     QuotaRefresher
	newPipeline func(q *Queue) *Pipeline
}

// QuotaRefresher 配额刷新接口（pipeline 完成后调用）。
type QuotaRefresher interface {
	RefreshQuota(ctx context.Context) (*zlibrary.ZProfile, error)
}

var (
	defaultQueue     *Queue
	defaultQueueOnce sync.Once
)

// DefaultQueue 返回进程级队列单例（未启动时自动启动）。
func DefaultQueue() *Queue {
	defaultQueueOnce.Do(func() { StartQueue() })
	return defaultQueue
}

// StartQueue 启动队列：进程重启恢复 + dispatcher 循环。
// 由 cmd/server/main.go 在迁移完成后显式调用（架构 §8-3）。
func StartQueue() *Queue {
	defaultQueueOnce.Do(func() {
		EnsureNovelLibraryForBootstrap() // s-library: 首启引导——保证存在小说书库（EPUB 入库前置）
		q := &Queue{
			cancels: map[int64]context.CancelFunc{},
			wake:    make(chan struct{}, 1),
			stop:    make(chan struct{}),
			scanFn:  syncLibraryByID,
			quotaFn: GetSettings(),
		}
		q.newPipeline = newDefaultPipeline
		defaultQueue = q
	})
	q := defaultQueue
	q.mu.Lock()
	if q.started {
		q.mu.Unlock()
		return q
	}
	q.started = true
	q.mu.Unlock()

	if err := q.Recover(); err != nil {
		log.Printf("[ZLIBRARY] queue recovery failed: %v", err)
	}
	go q.loop()
	log.Printf("[ZLIBRARY] download queue started (concurrency=%d)", q.Concurrency())
	return q
}

func newDefaultPipeline(q *Queue) *Pipeline {
	return &Pipeline{
		client:      GetSettings().Client(),
		quota:       q.quotaFn,
		scanFn:      q.scanFn,
		tmpDir:      TmpDir(),
		settings:    GetSettings(),
		historyKeep: HistoryKeep,
		scanBackoff: defaultScanBackoff(),
	}
}

func defaultScanBackoff() []time.Duration {
	return []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second}
}

// syncLibraryByID 进程内直接调用全局扫描器（架构 §10-C4：不发 HTTP 自调用，
// 与全局扫描共享 syncInProgress 互斥，冲突时由 pipeline 退避重试）。
func syncLibraryByID(libraryID string) (added, removed int, err error) {
	return service.SyncLibraryByID(libraryID)
}

// TmpDir 临时文件目录（DATA_DIR/tmp），重启恢复时全清 zlib-*。
func TmpDir() string {
	return filepath.Join(config.DataDir(), "tmp")
}

// Concurrency 当前配置的并发数。
func (q *Queue) Concurrency() int {
	return GetSettings().Behavior().Concurrency
}

// SetScanFn 测试注入扫描函数（必须在首次 StartQueue 前调用）。
func (q *Queue) SetScanFn(fn func(libraryID string) (int, int, error)) {
	q.mu.Lock()
	q.scanFn = fn
	q.mu.Unlock()
}

// Recover 进程重启恢复（PM §4.3：不留半截文件）：
// downloading → failed(Z_INTERRUPTED, 可重试)；清空临时文件目录；queued 保持排队。
func (q *Queue) Recover() error {
	ids, err := store.MarkZlibraryTasksInterrupted()
	if err != nil {
		return err
	}
	if len(ids) > 0 {
		log.Printf("[ZLIBRARY] recovered %d interrupted tasks as failed(Z_INTERRUPTED): %v", len(ids), ids)
	}
	return CleanTmpFiles()
}

// CleanTmpFiles 清理 zlib-* 临时文件。
func CleanTmpFiles() error {
	dir := TmpDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "zlib-") {
			continue
		}
		if err := os.Remove(filepath.Join(dir, e.Name())); err != nil {
			log.Printf("[ZLIBRARY] remove tmp file %s failed: %v", e.Name(), err)
		}
	}
	return nil
}

// Enqueue 创建任务并入队（立即返回，<1s 同步预检在 handler 层完成）。
func (q *Queue) Enqueue(task *model.ZlibraryDownloadTask) error {
	if err := store.CreateZlibraryDownloadTask(task); err != nil {
		return err
	}
	q.wakeUp()
	return nil
}

// wakeUp 非阻塞唤醒 dispatcher。
func (q *Queue) wakeUp() {
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

// Wake 外部（如重试端点）唤醒 dispatcher 立即尝试取任务。
func (q *Queue) Wake() {
	q.wakeUp()
}

// loop dispatcher 主循环。
func (q *Queue) loop() {
	for {
		select {
		case <-q.stop:
			return
		case <-q.wake:
			q.drain()
		}
	}
}

// drain 尽量取任务执行，直到无任务或达到并发上限。
func (q *Queue) drain() {
	for {
		select {
		case <-q.stop:
			return
		default:
		}
		q.mu.Lock()
		active := q.active
		q.mu.Unlock()
		if active >= q.Concurrency() {
			return
		}

		task, err := store.GetNextQueuedZlibraryTask()
		if err != nil {
			log.Printf("[ZLIBRARY] fetch next queued task failed: %v", err)
			return
		}
		if task == nil {
			return
		}
		q.mu.Lock()
		q.active++
		q.mu.Unlock()
		go q.runTask(task)
	}
}

// runTask 执行单个任务（独立 ctx 支持取消）。
func (q *Queue) runTask(task *model.ZlibraryDownloadTask) {
	defer func() {
		q.mu.Lock()
		q.active--
		q.mu.Unlock()
		q.wakeUp()
	}()

	ctx, cancel := context.WithCancel(context.Background())
	q.mu.Lock()
	q.cancels[task.ID] = cancel
	q.mu.Unlock()
	defer func() {
		cancel()
		q.mu.Lock()
		delete(q.cancels, task.ID)
		q.mu.Unlock()
	}()

	pipeline := q.newPipeline(q)
	pipeline.Run(ctx, task)
}

// Cancel 取消任务：downloading → cancel ctx（下载循环感知后清理）；queued → 直接置 canceled。
// 返回是否成功取消。
func (q *Queue) Cancel(id int64) bool {
	q.mu.Lock()
	cancel := q.cancels[id]
	q.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	ok, err := store.CancelZlibraryTask(id)
	if err != nil {
		log.Printf("[ZLIBRARY] cancel task %d failed: %v", id, err)
		return false
	}
	if ok {
		q.cleanupTmp(id)
	}
	return ok
}

// cleanupTmp 删除任务的临时文件（尽力而为）。
func (q *Queue) cleanupTmp(id int64) {
	entries, err := os.ReadDir(TmpDir())
	if err != nil {
		return
	}
	prefix := "zlib-" + itoa64(id) + "."
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), prefix) {
			_ = os.Remove(filepath.Join(TmpDir(), e.Name()))
		}
	}
}

// ActiveCount 当前执行中的任务数（测试/诊断用）。
func (q *Queue) ActiveCount() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.active
}

// Stop 停止 dispatcher（测试用；进程退出不需要显式调用）。
func (q *Queue) Stop() {
	q.stopOnce.Do(func() { close(q.stop) })
}

// SortTasks 按 createdAt 升序（dispatcher 选取顺序语义的测试辅助）。
func SortTasks(tasks []*model.ZlibraryDownloadTask) {
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].CreatedAt.Before(tasks[j].CreatedAt) })
}

func itoa64(n int64) string {
	if n == 0 {
		return "0"
	}
	digits := []byte{}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
