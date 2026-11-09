// s-library: 去重检查与批量标注（FR-7.2/7.3/7.4）。粒度 = zbookId+zbookHash；
// 本地条目被删后映射标记失效并允许再次入库。
package zlibservice

import (
	"log"

	"github.com/nowen-reader/nowen-reader/internal/store"
	zlibrary "github.com/nowen-reader/nowen-reader/internal/zlibrary"
)

// DedupStatus 去重三态。
const (
	DedupNone     = "none"
	DedupImported = "imported"
	DedupQueued   = "queued"
)

// DedupInfo 单本书的去重状态。
type DedupInfo struct {
	Status      string `json:"dedupStatus"`
	LocalBookID string `json:"localBookId,omitempty"`
}

// CheckDedup 判定一本书是否已入库/已在队列。
// 已入库要求映射的本地条目仍然存在（FR-7.4：删除后允许再入库）。
func CheckDedup(zbookID, zbookHash string) (DedupInfo, error) {
	info := DedupInfo{Status: DedupNone}
	m, err := store.GetZlibraryImportedBook(zbookID, zbookHash)
	if err != nil {
		return info, err
	}
	if m != nil {
		if m.LocalBookID != "" {
			comic, err := store.GetComicByID(m.LocalBookID)
			if err != nil {
				return info, err
			}
			if comic != nil {
				return DedupInfo{Status: DedupImported, LocalBookID: m.LocalBookID}, nil
			}
		}
		// 本地条目已不存在 → 标记失效（映射保留）
		if err := store.ClearZlibraryImportedBookLocal(zbookID, zbookHash); err != nil {
			log.Printf("[ZLIBRARY] clear stale imported mapping failed: %v", err)
		}
	}

	task, err := store.GetActiveZlibraryTaskForBook(zbookID, zbookHash)
	if err != nil {
		return info, err
	}
	if task != nil {
		return DedupInfo{Status: DedupQueued}, nil
	}
	return info, nil
}

// AnnotateBooks 为搜索/详情结果批量标注去重状态（按 id+hash 逐项查询，
// limit≤100 的本地 SQLite 查询开销可接受）。
func AnnotateBooks(books []zlibrary.ZBook) ([]DedupInfo, error) {
	out := make([]DedupInfo, len(books))
	for i := range books {
		info, err := CheckDedup(books[i].ID, books[i].Hash)
		if err != nil {
			return out, err
		}
		out[i] = info
	}
	return out, nil
}
