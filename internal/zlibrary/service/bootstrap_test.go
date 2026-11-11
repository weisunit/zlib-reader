// s-library: 首启引导判定逻辑单测（纯函数，不依赖 DB）。
package zlibservice

import (
	"testing"

	"github.com/nowen-reader/nowen-reader/internal/model"
)

func mkLib(name, ltype string, paths []string) model.Library {
	l := model.Library{Name: name, Type: ltype}
	if len(paths) > 0 {
		l.RootPath = paths[0]
		l.RootPaths = paths
	}
	return l
}

func TestNeedsNovelLibrary(t *testing.T) {
	const novels = "/app/novels"
	// 上游默认态：仅 comic 库 → 需要创建
	if !needsNovelLibrary([]model.Library{mkLib("Default", "comic", []string{"/app/comics"})}, novels) {
		t.Fatal("仅漫画库时应判定需要创建")
	}
	// 已有 novel 库覆盖 novels → 不需要
	if needsNovelLibrary([]model.Library{
		mkLib("Default", "comic", []string{"/app/comics"}),
		mkLib("小说书库", "novel", []string{novels}),
	}, novels) {
		t.Fatal("已有 novel 库时不应创建")
	}
	// mixed 库覆盖 novels → 不需要
	if needsNovelLibrary([]model.Library{mkLib("Mixed", "mixed", []string{"/app/comics", novels})}, novels) {
		t.Fatal("mixed 库已覆盖时不应创建")
	}
	// 仅 RootPath（无 RootPaths）也认作覆盖
	if needsNovelLibrary([]model.Library{mkLib("N", "novel", []string{novels})}, novels) {
		t.Fatal("RootPath 形式也应识别")
	}
}

func TestStripNovelsFromLibs(t *testing.T) {
	const novels = "/app/novels"
	libs := []model.Library{
		mkLib("Default", "comic", []string{"/app/comics", novels}),
	}
	out, changed := stripNovelsFromLibs(libs, novels)
	if !changed {
		t.Fatal("应产生变更")
	}
	if len(out[0].RootPaths) != 1 || out[0].RootPaths[0] != "/app/comics" {
		t.Fatalf("应剥离 novels 路径, got %+v", out[0].RootPaths)
	}
	// 单路径漫画库（剥离后无路径）→ 保留原状
	out2, changed2 := stripNovelsFromLibs([]model.Library{mkLib("Only", "comic", []string{novels})}, novels)
	if changed2 || out2[0].RootPaths[0] != novels {
		t.Fatal("剥离后无路径时应保留原状")
	}
	// novel 库自身不受影响
	out3, changed3 := stripNovelsFromLibs([]model.Library{mkLib("N", "novel", []string{novels})}, novels)
	if changed3 || out3[0].RootPaths[0] != novels {
		t.Fatal("novel 库不应被剥离")
	}
}

// s-library: 回归测试——browserAssist PUT 语义（空 cookie 保留现有 / clear 显式清除）。
func TestBrowserAssistKeepAndClear(t *testing.T) {
	setupServiceTestDB(t)
	s := GetSettings()
	t.Cleanup(func() { _ = s.SaveBrowserAssist(BrowserAssist{}) })

	// 1. 写入通行证
	if err := s.SaveBrowserAssist(BrowserAssist{CookieString: "bsrv=x; c_token=y", UserAgent: "UA-1"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	// 2. 只更新 UA（cookieString 空 → 保留）
	if err := s.SaveBrowserAssist(BrowserAssist{CookieString: "", UserAgent: "UA-2"}); err != nil {
		t.Fatalf("save ua only: %v", err)
	}
	if got := s.GetBrowserAssist(); got.CookieString != "bsrv=x; c_token=y" || got.UserAgent != "UA-2" {
		t.Fatalf("空 cookieString 应保留现有通行证, got %+v", got)
	}
	// 3. 显式清除
	if err := s.SaveBrowserAssist(BrowserAssist{Clear: true}); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if got := s.GetBrowserAssist(); got.CookieString != "" || got.UserAgent != "" {
		t.Fatalf("clear 应清空, got %+v", got)
	}
}
