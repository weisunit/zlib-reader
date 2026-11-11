// s-library: 首启引导 —— 上游首启只建 comic 类型 Default Library（迁移 23），
// Z-Library 的一键入库以"漫画/电子书分流到对应类型书库"为核心路径，
// 需要保证存在 novel 类型书库，否则 EPUB/TXT 开箱即不可入库。
// 幂等：已存在 novel/mixed 书库覆盖 novels 目录时跳过；
// novels 目录被 comic 库占用且该库尚有其它路径时，先剥离再创建。
package zlibservice

import (
	"log"
	"path/filepath"
	"strings"

	"github.com/nowen-reader/nowen-reader/internal/config"
	"github.com/nowen-reader/nowen-reader/internal/model"
	"github.com/nowen-reader/nowen-reader/internal/store"
)

// effectivePaths 返回书库生效的根路径列表（RootPaths 优先，退回 RootPath）。
func effectivePaths(l model.Library) []string {
	if len(l.RootPaths) > 0 {
		return l.RootPaths
	}
	if l.RootPath != "" {
		return []string{l.RootPath}
	}
	return nil
}

// claimsNovelsDir 判断书库是否覆盖 novels 目录。
func claimsNovelsDir(l model.Library, novelsDir string) bool {
	for _, p := range effectivePaths(l) {
		if filepath.Clean(strings.TrimRight(p, "/")) == novelsDir {
			return true
		}
	}
	return false
}

// needsNovelLibrary 判定是否需要引导创建小说书库（纯函数，单测覆盖）。
func needsNovelLibrary(libs []model.Library, novelsDir string) bool {
	for _, l := range libs {
		if (l.Type == "novel" || l.Type == "mixed") && claimsNovelsDir(l, novelsDir) {
			return false
		}
	}
	return true
}

// stripNovelsFromLibs 返回剥离 novels 路径后的书库列表（仅处理非 novel/mixed 类型，
// 且剥离后仍保留至少一个路径的库）；second 返回值表示是否有需要落库的变更。
func stripNovelsFromLibs(libs []model.Library, novelsDir string) ([]model.Library, bool) {
	changed := false
	out := libs
	for i := range out {
		l := out[i]
		if l.Type == "novel" || l.Type == "mixed" || !claimsNovelsDir(l, novelsDir) {
			continue
		}
		if len(effectivePaths(l)) <= 1 {
			continue // 剥离后无路径，保留原状（由用户在 UI 决定去留）
		}
		newPaths := make([]string, 0, len(l.RootPaths))
		for _, p := range l.RootPaths {
			if filepath.Clean(strings.TrimRight(p, "/")) != novelsDir {
				newPaths = append(newPaths, p)
			}
		}
		if len(newPaths) == 0 && l.RootPath != "" &&
			filepath.Clean(strings.TrimRight(l.RootPath, "/")) == novelsDir {
			continue // 主路径即 novels，无法剥离
		}
		if len(newPaths) > 0 {
			out[i].RootPaths = newPaths
			out[i].RootPath = newPaths[0]
			changed = true
		}
	}
	return out, changed
}

// EnsureNovelLibraryForBootstrap 在启动时保证存在可接收电子书的小说书库（FR-4.1 路由前置）。
// 失败仅记录日志，不阻断启动（书库可由管理员手动创建）。
func EnsureNovelLibraryForBootstrap() {
	libs, err := store.GetAllLibraries()
	if err != nil {
		log.Printf("[ZLIBRARY] bootstrap: 读取书库失败，跳过小说书库引导: %v", err)
		return
	}
	novelsDir := filepath.Clean(strings.TrimRight(config.GetNovelsDir(), "/"))
	if novelsDir == "" || novelsDir == "." {
		return
	}
	if !needsNovelLibrary(libs, novelsDir) {
		return
	}
	libs, changed := stripNovelsFromLibs(libs, novelsDir)
	if changed {
		for _, l := range libs {
			if err := store.UpdateLibrary(&l); err != nil {
				log.Printf("[ZLIBRARY] bootstrap: 更新书库 %s 根路径失败: %v", l.Name, err)
			} else {
				log.Printf("[ZLIBRARY] bootstrap: 已从漫画书库「%s」剥离 novels 路径", l.Name)
			}
		}
	}
	lib := &model.Library{
		Name:          "小说书库",
		Type:          "novel",
		RootPath:      novelsDir,
		RootPaths:     []string{novelsDir},
		Enabled:       true,
		DefaultAccess: "private",
		ScanEnabled:   true,
	}
	if err := store.CreateLibrary(lib); err != nil {
		log.Printf("[ZLIBRARY] bootstrap: 创建小说书库失败（可能 novels 目录被占用）: %v", err)
		return
	}
	log.Printf("[ZLIBRARY] bootstrap: 已创建小说书库（%s）——Z-Library 电子书入库就绪", novelsDir)
}
