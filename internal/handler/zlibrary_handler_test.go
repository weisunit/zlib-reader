// s-library: Z-Library 端点级回归测试。
// 覆盖 QA-REPORT.md 修复项：DEF-1（GET /settings 配置回显，防默认值静默覆盖）、
// DEF-4（非 failed 任务重试 → 409 + Z_INVALID_STATE，契约 §4(9)）。
package handler

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/nowen-reader/nowen-reader/internal/middleware"
	"github.com/nowen-reader/nowen-reader/internal/model"
	"github.com/nowen-reader/nowen-reader/internal/store"
	zlibrary "github.com/nowen-reader/nowen-reader/internal/zlibrary"
)

// zsettingsView GET /settings 响应视图（与 PUT /settings 请求体同构 + hasCredentials）。
type zsettingsView struct {
	Domains []struct {
		URL       string `json:"url"`
		Enabled   bool   `json:"enabled"`
		IsDefault bool   `json:"isDefault"`
	} `json:"domains"`
	Behavior struct {
		Concurrency      int    `json:"concurrency"`
		DefaultLibraryID string `json:"defaultLibraryId"`
		AllowScope       string `json:"allowScope"`
	} `json:"behavior"`
	HasCredentials bool `json:"hasCredentials"`
}

func TestZlibraryGetSettingsEchoesSavedConfig(t *testing.T) {
	r := setupTestRouter(t)
	cookie := registerAndLogin(t, r)

	// 初始：未配置凭据，behavior 为默认值（concurrency=1 / allowScope=admin，决策 D1）
	w := performAuthedRequest(r, "GET", "/api/zlibrary/settings", nil, cookie)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /settings = %d %s", w.Code, w.Body.String())
	}
	var initial zsettingsView
	if err := json.Unmarshal(w.Body.Bytes(), &initial); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if initial.HasCredentials {
		t.Error("hasCredentials should be false before any credential is saved")
	}
	if initial.Behavior.Concurrency != 1 || initial.Behavior.AllowScope != "admin" {
		t.Errorf("default behavior = %+v", initial.Behavior)
	}
	if len(initial.Domains) == 0 {
		t.Error("preset domains should be returned for first-time echo")
	}

	// 管理员保存非默认配置
	putBody := map[string]any{
		"domains": []map[string]any{
			{"url": "https://z-lib.sk", "enabled": true, "isDefault": true},
			{"url": "https://z-lib.fm", "enabled": false, "isDefault": false},
		},
		"behavior": map[string]any{
			"concurrency":      3,
			"defaultLibraryId": "",
			"allowScope":       "all",
		},
	}
	w = performAuthedRequest(r, "PUT", "/api/zlibrary/settings", putBody, cookie)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT /settings = %d %s", w.Code, w.Body.String())
	}

	// DEF-1 回归核心：GET 必须回显已保存值（此前 404 导致回显默认值、保存静默覆盖）
	w = performAuthedRequest(r, "GET", "/api/zlibrary/settings", nil, cookie)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /settings after PUT = %d %s", w.Code, w.Body.String())
	}
	var echoed zsettingsView
	if err := json.Unmarshal(w.Body.Bytes(), &echoed); err != nil {
		t.Fatalf("decode echo: %v", err)
	}
	if echoed.Behavior.Concurrency != 3 || echoed.Behavior.AllowScope != "all" {
		t.Errorf("behavior not echoed: %+v（DEF-1 回归：默认值静默覆盖）", echoed.Behavior)
	}
	if len(echoed.Domains) != 2 || echoed.Domains[0].URL != "https://z-lib.sk" ||
		!echoed.Domains[0].Enabled || !echoed.Domains[0].IsDefault ||
		echoed.Domains[1].URL != "https://z-lib.fm" || echoed.Domains[1].Enabled {
		t.Errorf("domains not echoed (顺序即优先级): %+v", echoed.Domains)
	}
	if echoed.HasCredentials {
		t.Error("hasCredentials should stay false（本端点不得回传/伪造凭据状态）")
	}
	// 秘密字段绝不回传：响应体不得出现密码或 userkey 字段
	body := w.Body.String()
	for _, secret := range []string{"password", "remixUserkey", "remixUserkeyEnc"} {
		if containsJSONKey(body, secret) {
			t.Errorf("secret field %q must never be echoed, body=%s", secret, body)
		}
	}
}

func TestZlibraryGetSettingsRequiresAdmin(t *testing.T) {
	r := setupTestRouter(t)
	_ = registerAndLogin(t, r)

	w := performRequest(r, "POST", "/api/auth/register", map[string]string{
		"username": "zlib-normal-user",
		"password": "password123",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("register normal user: %d %s", w.Code, w.Body.String())
	}
	var normalCookie string
	for _, c := range w.Result().Cookies() {
		if c.Name == middleware.SessionCookie {
			normalCookie = c.Value
		}
	}
	if normalCookie == "" {
		t.Fatal("no session cookie for normal user")
	}

	w = performAuthedRequest(r, "GET", "/api/zlibrary/settings", nil, normalCookie)
	if w.Code != http.StatusForbidden {
		t.Fatalf("normal user GET /settings = %d, want 403（配置端点恒 admin）", w.Code)
	}
}

func TestZlibraryRetryNonFailedReturns409(t *testing.T) {
	r := setupTestRouter(t)
	cookie := registerAndLogin(t, r)
	admin, err := store.GetUserByUsername("admin")
	if err != nil || admin == nil {
		t.Fatalf("load admin: %v %v", admin, err)
	}

	// DEF-4 回归：completed/queued/canceled 任务重试 → 409 + Z_INVALID_STATE
	//（此前返回 500 + Z_UPSTREAM，契约 §4(9) 约定 409）
	cases := []struct {
		name   string
		status string
	}{
		{"completed", model.ZlibTaskStatusCompleted},
		{"queued", model.ZlibTaskStatusQueued},
		{"canceled", model.ZlibTaskStatusCanceled},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			task := &model.ZlibraryDownloadTask{
				UserID: admin.ID, ZbookID: "42889905", ZbookHash: "hash-" + tc.name,
				Title: "三体", Author: "刘慈欣", Ext: "epub", LibraryID: "lib-x",
				Status: tc.status,
			}
			if err := store.CreateZlibraryDownloadTask(task); err != nil {
				t.Fatalf("create task: %v", err)
			}
			w := performAuthedRequest(r, "POST", "/api/zlibrary/downloads/"+itoa(task.ID)+"/retry", nil, cookie)
			if w.Code != http.StatusConflict {
				t.Fatalf("retry %s task = %d %s, want 409", tc.name, w.Code, w.Body.String())
			}
			var resp struct {
				Code string `json:"code"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if resp.Code != zlibrary.CodeInvalidState {
				t.Errorf("code = %q, want Z_INVALID_STATE", resp.Code)
			}
		})
	}

	// failed 任务未连接凭据 → 走前置校验返回 401 Z_AUTH_REQUIRED（而非 500/409）
	failed := &model.ZlibraryDownloadTask{
		UserID: admin.ID, ZbookID: "42889905", ZbookHash: "hash-failed",
		Title: "三体", Ext: "epub", LibraryID: "lib-x",
		Status: model.ZlibTaskStatusFailed, ErrorCode: "Z_QUOTA_EXCEEDED",
	}
	if err := store.CreateZlibraryDownloadTask(failed); err != nil {
		t.Fatalf("create failed task: %v", err)
	}
	w := performAuthedRequest(r, "POST", "/api/zlibrary/downloads/"+itoa(failed.ID)+"/retry", nil, cookie)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("retry failed task (disconnected) = %d %s, want 401", w.Code, w.Body.String())
	}
	var resp struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Code != zlibrary.CodeAuthRequired {
		t.Errorf("code = %q, want Z_AUTH_REQUIRED", resp.Code)
	}
}

// containsJSONKey 检查响应体 JSON 是否包含指定键（避免误伤值内容）。
func containsJSONKey(body, key string) bool {
	var m map[string]any
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		return false
	}
	_, found := m[key]
	return found
}

func itoa(n int64) string {
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



// s-library: assist-sync 端点测试——令牌鉴权、参数校验、同步生效。
func TestZlibraryAssistSync(t *testing.T) {
	r := setupTestRouter(t)
	cookie := registerAndLogin(t, r)

	// 取同步令牌（GET /settings，admin 会话）
	w := performAuthedRequest(r, "GET", "/api/zlibrary/settings", nil, cookie)
	var snap struct {
		AssistSyncToken string `json:"assistSyncToken"`
		BrowserAssist   struct {
			Configured bool `json:"configured"`
		} `json:"browserAssist"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &snap); err != nil {
		t.Fatalf("decode settings: %v", err)
	}
	if snap.AssistSyncToken == "" {
		t.Fatal("settings 应回显同步令牌")
	}

	// 错误令牌 → 403
	wr := performAuthedRequest(r, "GET", "/api/zlibrary/assist-sync?t=badtoken&ck=a=1", nil, "")
	if wr.Code != http.StatusForbidden {
		t.Fatalf("错误令牌应 403, got %d", wr.Code)
	}
	// 缺 ck → 400
	wr = performAuthedRequest(r, "GET", "/api/zlibrary/assist-sync?t="+snap.AssistSyncToken, nil, "")
	if wr.Code != http.StatusBadRequest {
		t.Fatalf("缺 ck 应 400, got %d", wr.Code)
	}
	// 正确令牌（无会话）→ 200 且生效
	wr = performAuthedRequest(r, "GET", "/api/zlibrary/assist-sync?t="+snap.AssistSyncToken+"&ck=bsrv=fresh&ua=UA-SYNC", nil, "")
	if wr.Code != http.StatusOK {
		t.Fatalf("同步应 200, got %d %s", wr.Code, wr.Body.String())
	}
	// 验证已生效（authed GET /settings）
	w = performAuthedRequest(r, "GET", "/api/zlibrary/settings", nil, cookie)
	if err := json.Unmarshal(w.Body.Bytes(), &snap); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !snap.BrowserAssist.Configured {
		t.Fatal("同步后 browserAssist 应为已配置")
	}
}
