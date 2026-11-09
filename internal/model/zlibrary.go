// s-library: Z-Library 集成的数据模型（任务/去重映射），见 docs/ARCHITECTURE.md §3。
package model

import "time"

// Z-Library 下载任务状态机（FR-5.1）。
const (
	ZlibTaskStatusQueued      = "queued"
	ZlibTaskStatusDownloading = "downloading"
	ZlibTaskStatusCompleted   = "completed"
	ZlibTaskStatusFailed      = "failed"
	ZlibTaskStatusCanceled    = "canceled"
)

// Z-Library 任务阶段（FR-5.1）。
const (
	ZlibStageFetchInfo  = "fetch_info"
	ZlibStageDownloading = "downloading"
	ZlibStageValidating = "validating"
	ZlibStageMoving     = "moving"
	ZlibStageScanning   = "scanning"
)

// ZlibraryDownloadTask 对应表 "ZlibraryDownloadTask"（M1 不进入 metadata 阶段，
// 元数据快照随 metaJson 落库供 M2 回填，见决策 D2）。
type ZlibraryDownloadTask struct {
	ID            int64      `json:"id"`
	UserID        string     `json:"userId"`
	ZbookID       string     `json:"zbookId"`
	ZbookHash     string     `json:"zbookHash"`
	Title         string     `json:"title"`
	Author        string     `json:"author"`
	Ext           string     `json:"ext"`
	FileSize      int64      `json:"fileSize"`
	LibraryID     string     `json:"libraryId"`
	LocalBookID   string     `json:"localBookId"`
	Status        string     `json:"status"`
	Progress      int        `json:"progress"`
	Stage         string     `json:"stage"`
	ErrorCode     string     `json:"errorCode"`
	ErrorMsg      string     `json:"errorMsg"`
	QuotaConsumed bool       `json:"quotaConsumed"`
	RetryCount    int        `json:"retryCount"`
	MetaJSON      string     `json:"-"` // FR-8.1 原始元数据，仅供 M2 使用
	CreatedAt     time.Time  `json:"createdAt"`
	UpdatedAt     time.Time  `json:"updatedAt"`
	FinishedAt    *time.Time `json:"finishedAt"`

	// Username 仅在管理员列表视图中填充（handler 层负责）。
	Username string `json:"user,omitempty"`
}

// ZlibraryImportedBook 对应表 "ZlibraryImportedBook"（FR-7.1，唯一键 zbookId+zbookHash）。
type ZlibraryImportedBook struct {
	ZbookID     string    `json:"zbookId"`
	ZbookHash   string    `json:"zbookHash"`
	LocalBookID string    `json:"localBookId"`
	LibraryID   string    `json:"libraryId"`
	ImportedBy  string    `json:"importedBy"`
	ImportedAt  time.Time `json:"importedAt"`
}
