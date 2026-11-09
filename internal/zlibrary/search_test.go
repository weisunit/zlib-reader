// s-library: 适配层单测 —— httptest 固定样本覆盖：搜索解析、详情/格式解析、
// 登录解析、错误分类、域名 failover（docs/ARCHITECTURE.md §9）。
package zlibrary

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// newTestClient 构造指向 httptest 服务器、关闭限速的客户端。
func newTestClient(t *testing.T, ts *httptest.Server) *Client {
	t.Helper()
	hc := ts.Client()
	hc.Timeout = 5 * time.Second
	return NewClient([]string{ts.URL}, WithHTTPClient(hc), WithMinInterval(0))
}

// searchSample 固定样本：包含未知字段、字符串年份、字符串评分等非契约形态。
const searchSample = `{
  "success": 1,
  "total": "1234",
  "page": 2,
  "books": [
    {
      "id": "42889905",
      "hash": "a1b2c3d4e5",
      "title": "三体",
      "subtitle": "地球往事三部曲之一",
      "author": "刘慈欣",
      "publisher": "重庆出版社",
      "year": "2008",
      "language": "chinese",
      "extension": "EPUB",
      "filesize": 1048576,
      "cover": "https://example.org/cover1.jpg",
      "rating": "4.5",
      "unknownFutureField": {"nested": true}
    },
    {
      "id": 999,
      "hash": "fff000111",
      "title": "Sample PDF",
      "year": 2020,
      "extension": "PDF",
      "filesize": "2048000",
      "rating": 4.0,
      "author": ""
    },
    {"id": "no-hash-only"},
    "not-an-object"
  ]
}`

func TestSearchParsesFixedSampleDefensively(t *testing.T) {
	var gotPath string
	var gotForm url.Values
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if err := r.ParseForm(); err != nil {
			t.Errorf("ParseForm: %v", err)
		}
		gotForm = r.Form
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(searchSample))
	}))
	defer ts.Close()

	c := newTestClient(t, ts)
	yearFrom := 2000
	res, err := c.Search(context.Background(), SearchQuery{
		Message:    "三体",
		Languages:  []string{"chinese", "english"},
		Extensions: []string{"EPUB", ".pdf"},
		YearFrom:   &yearFrom,
		Order:      OrderNewest,
		Page:       2,
		Limit:      20,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	if gotPath != "/eapi/book/search" {
		t.Errorf("path = %s", gotPath)
	}
	if gotForm.Get("message") != "三体" || gotForm.Get("order") != "newest" ||
		gotForm.Get("page") != "2" || gotForm.Get("limit") != "20" ||
		gotForm.Get("yearFrom") != "2000" {
		t.Errorf("form = %v", gotForm)
	}
	if langs := gotForm["languages[]"]; len(langs) != 2 || langs[0] != "chinese" {
		t.Errorf("languages[] = %v", langs)
	}
	if exts := gotForm["extensions[]"]; len(exts) != 2 || exts[0] != "epub" || exts[1] != "pdf" {
		t.Errorf("extensions[] = %v (期望小写无点规范化)", exts)
	}

	if res.Total != 1234 || res.Page != 2 || res.Limit != 20 {
		t.Errorf("total/page/limit = %d/%d/%d", res.Total, res.Page, res.Limit)
	}
	if len(res.Books) != 2 {
		t.Fatalf("books = %d, want 2（缺 hash 的条目应跳过）", len(res.Books))
	}

	b0 := res.Books[0]
	if b0.ID != "42889905" || b0.Hash != "a1b2c3d4e5" || b0.Title != "三体" ||
		b0.Author != "刘慈欣" || b0.Subtitle != "地球往事三部曲之一" ||
		b0.Publisher != "重庆出版社" || b0.Language != "chinese" {
		t.Errorf("book0 = %+v", b0)
	}
	if b0.Ext != "epub" {
		t.Errorf("ext = %q, want epub（大写加点均规范化）", b0.Ext)
	}
	if b0.Year == nil || *b0.Year != 2008 {
		t.Errorf("year = %v, want 2008（字符串年份）", b0.Year)
	}
	if b0.Rating == nil || *b0.Rating != 4.5 {
		t.Errorf("rating = %v, want 4.5（字符串评分）", b0.Rating)
	}
	if b0.FileSize != 1048576 {
		t.Errorf("fileSize = %d", b0.FileSize)
	}
	if b0.CoverURL != "https://example.org/cover1.jpg" {
		t.Errorf("coverUrl = %q", b0.CoverURL)
	}

	b1 := res.Books[1]
	if b1.ID != "999" {
		t.Errorf("book1.ID = %q（数字 id 应转字符串）", b1.ID)
	}
	if b1.Year == nil || *b1.Year != 2020 {
		t.Errorf("book1.year = %v（数字年份）", b1.Year)
	}
	if b1.FileSize != 2048000 {
		t.Errorf("book1.fileSize = %d（字符串大小）", b1.FileSize)
	}
	if b1.Rating == nil || *b1.Rating != 4.0 {
		t.Errorf("book1.rating = %v", b1.Rating)
	}
}

func TestSearchDefaultOrderAndPagination(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("order") != "bestmatch" || r.Form.Get("page") != "1" || r.Form.Get("limit") != "20" {
			t.Errorf("defaults: %v", r.Form)
		}
		_, _ = w.Write([]byte(`{"success":1,"total":0,"books":[]}`))
	}))
	defer ts.Close()

	c := newTestClient(t, ts)
	res, err := c.Search(context.Background(), SearchQuery{Message: "x"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if res.Page != 1 || res.Limit != 20 || len(res.Books) != 0 {
		t.Errorf("res = %+v", res)
	}
}

// detailSample 固定样本：详情含 descr 与 dl 个人域名地址。
const detailSample = `{
  "success": 1,
  "book": {
    "id": "42889905",
    "hash": "a1b2c3d4e5",
    "title": "三体",
    "author": "刘慈欣",
    "extension": "EPUB",
    "filesize": 1048576,
    "descr": "<p>文化大革命前夕……</p>",
    "dl": "https://gw-personal.example.org/dl/42889905/a1b2c3d4e5",
    "year": "2008"
  }
}`

const formatsSample = `{"success":1,"formats":[{"ext":"EPUB","filesize":1048576},{"ext":".pdf","filesize":9},{"ext":"EPUB"}]}`

func TestBookAndFormatsParsing(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/formats"):
			_, _ = w.Write([]byte(formatsSample))
		default:
			_, _ = w.Write([]byte(detailSample))
		}
	}))
	defer ts.Close()

	c := newTestClient(t, ts)
	book, err := c.Book(context.Background(), "42889905", "a1b2c3d4e5")
	if err != nil {
		t.Fatalf("Book: %v", err)
	}
	if book.Description == "" || book.DownloadURL != "https://gw-personal.example.org/dl/42889905/a1b2c3d4e5" {
		t.Errorf("book = %+v", book)
	}
	if book.DlDomain != "https://gw-personal.example.org" {
		t.Errorf("dlDomain = %q", book.DlDomain)
	}

	formats, err := c.Formats(context.Background(), "42889905", "a1b2c3d4e5")
	if err != nil {
		t.Fatalf("Formats: %v", err)
	}
	if len(formats) != 2 {
		t.Fatalf("formats = %+v, want 2（重复 EPUB 去重）", formats)
	}
	if formats[0].Ext != "epub" || formats[0].FileSize != 1048576 || formats[1].Ext != "pdf" {
		t.Errorf("formats = %+v", formats)
	}
	if got := PickExt(formats); got != "epub" {
		t.Errorf("PickExt = %q, want epub", got)
	}
	if got := PickExt([]ZFormat{{Ext: "mobi"}, {Ext: "azw3"}}); got != "mobi" {
		t.Errorf("PickExt(mobi/azw3) = %q, want mobi", got)
	}
}

// loginSample 固定样本：免费账号字段（dailyAmount）。
const loginSample = `{
  "success": 1,
  "user": {
    "id": 123456,
    "email": "reader@example.org",
    "firstname": "San",
    "lastname": "Wang",
    "remix_userkey": "sk-0000-ffff",
    "kind": "regular",
    "dailyAmount": 10,
    "dailyUsed": 3,
    "someUnknownField": [1, 2, 3]
  }
}`

func TestLoginParsesTokensAndProfile(t *testing.T) {
	var gotEmail, gotPassword string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotEmail, gotPassword = r.Form.Get("email"), r.Form.Get("password")
		_, _ = w.Write([]byte(loginSample))
	}))
	defer ts.Close()

	c := newTestClient(t, ts)
	res, err := c.Login(context.Background(), "reader@example.org", "pw-secret")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if gotEmail != "reader@example.org" || gotPassword != "pw-secret" {
		t.Errorf("credentials not forwarded: %q/%q", gotEmail, gotPassword)
	}
	if res.UserID != "123456" || res.UserKey != "sk-0000-ffff" {
		t.Errorf("tokens = %q/%q（数字 id 转字符串）", res.UserID, res.UserKey)
	}
	p := res.Profile
	if p.Name != "San Wang" || p.Email != "reader@example.org" {
		t.Errorf("profile name/email = %q/%q", p.Name, p.Email)
	}
	if p.AccountType != "free" || p.DailyQuota != 10 || p.DailyUsed != 3 {
		t.Errorf("profile = %+v（免费账号 dailyAmount 映射）", p)
	}
}

func TestProfileDonorQuotaFieldsAndAuthHeaders(t *testing.T) {
	var gotUserID, gotUserKey string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUserID = r.Header.Get("remix-userid")
		gotUserKey = r.Header.Get("remix-userkey")
		_, _ = w.Write([]byte(`{"success":1,"user":{"id":"42","name":"donor-x","email":"d@x.org","kind":"donor","dailyQuota":999,"dailyUsed":1}}`))
	}))
	defer ts.Close()

	c := newTestClient(t, ts)
	c.SetAuth("42", "key-xyz")
	if !c.Authenticated() {
		t.Fatal("Authenticated() = false")
	}
	p, err := c.Profile(context.Background())
	if err != nil {
		t.Fatalf("Profile: %v", err)
	}
	if gotUserID != "42" || gotUserKey != "key-xyz" {
		t.Errorf("remix headers missing: %q/%q", gotUserID, gotUserKey)
	}
	if p.AccountType != "donor" || p.DailyQuota != 999 || p.DailyUsed != 1 {
		t.Errorf("donor profile = %+v", p)
	}
}

func TestLoginWrongPasswordMapsToAuthFailed(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"Invalid email or password"}`))
	}))
	defer ts.Close()

	c := newTestClient(t, ts)
	_, err := c.Login(context.Background(), "a@b.c", "wrong")
	if ze := AsZError(err); ze == nil || ze.Code != CodeAuthFailed {
		t.Fatalf("err = %v, want Z_AUTH_FAILED", err)
	}
}

// ============================================================
// 错误分类
// ============================================================

func TestErrorClassification(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		header map[string]string
		want   string
	}{
		{"401 → auth required", http.StatusUnauthorized, `{}`, nil, CodeAuthRequired},
		{"404 → not found", http.StatusNotFound, `{}`, nil, CodeNotFound},
		{"429 → rate limited", http.StatusTooManyRequests, `{}`, nil, CodeRateLimited},
		{"plain 403 → blocked", http.StatusForbidden, `{}`, nil, CodeBlocked},
		{"CF challenge → blocked", http.StatusForbidden, "<html>Just a moment...</html>",
			map[string]string{"Server": "cloudflare"}, CodeBlocked},
		{"503 CF → blocked", http.StatusServiceUnavailable,
			"<html>Checking your browser before accessing</html>", nil, CodeBlocked},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				for k, v := range tc.header {
					w.Header().Set(k, v)
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer ts.Close()

			c := newTestClient(t, ts)
			_, err := c.Search(context.Background(), SearchQuery{Message: "x"})
			ze := AsZError(err)
			if ze == nil || ze.Code != tc.want {
				t.Fatalf("err = %v, want code %s", err, tc.want)
			}
		})
	}
}

func TestFailoverOnNetworkErrorThenSuccess(t *testing.T) {
	// 第一个域名不可达（关闭的端口），第二个域名正常返回
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	deadURL := dead.URL
	dead.Close()

	var hits int32
	live := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		_, _ = w.Write([]byte(`{"success":1,"total":1,"books":[]}`))
	}))
	defer live.Close()

	hc := live.Client()
	hc.Timeout = 2 * time.Second
	c := NewClient([]string{deadURL, live.URL}, WithHTTPClient(hc), WithMinInterval(0))

	res, err := c.Search(context.Background(), SearchQuery{Message: "x"})
	if err != nil {
		t.Fatalf("Search after failover: %v", err)
	}
	if atomic.LoadInt32(&hits) != 1 {
		t.Errorf("live hits = %d, want 1", hits)
	}
	if res.Total != 1 {
		t.Errorf("total = %d", res.Total)
	}
	if c.CurrentDomain() != live.URL {
		t.Errorf("current = %q, want %q（failover 后切换当前域名）", c.CurrentDomain(), live.URL)
	}
}

func TestAllDomainsUnreachable(t *testing.T) {
	dead1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	d1 := dead1.URL
	dead1.Close()
	dead2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	d2 := dead2.URL
	dead2.Close()

	hc := &http.Client{Timeout: 2 * time.Second}
	c := NewClient([]string{d1, d2}, WithHTTPClient(hc), WithMinInterval(0))
	_, err := c.Search(context.Background(), SearchQuery{Message: "x"})
	ze := AsZError(err)
	if ze == nil || ze.Code != CodeDomainUnreachable {
		t.Fatalf("err = %v, want Z_DOMAIN_UNREACHABLE", err)
	}
}

func TestFailoverOn5xx(t *testing.T) {
	var mode int32
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.LoadInt32(&mode) == 0 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte(`{"success":1,"total":0,"books":[]}`))
	}))
	defer primary.Close()
	secondary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"success":1,"total":0,"books":[]}`))
	}))
	defer secondary.Close()

	hc := secondary.Client()
	hc.Timeout = 3 * time.Second
	c := NewClient([]string{primary.URL, secondary.URL}, WithHTTPClient(hc), WithMinInterval(0))
	if _, err := c.Search(context.Background(), SearchQuery{Message: "x"}); err != nil {
		t.Fatalf("Search: %v", err)
	}
}

func TestQuotaExceededStyleUpstreamPayloadDoesNotCrash(t *testing.T) {
	// eapi 可能以 200 + success=0 返回业务错误，不应 panic 且宽容解析
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"success":0,"error":"Daily limit exceeded"}`))
	}))
	defer ts.Close()
	c := newTestClient(t, ts)
	res, err := c.Search(context.Background(), SearchQuery{Message: "x"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if res == nil || len(res.Books) != 0 {
		t.Errorf("res = %+v", res)
	}
}

func TestHTMLResponseClassifiedAsUpstreamError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<!DOCTYPE html><html><body>maintenance</body></html>"))
	}))
	defer ts.Close()
	c := newTestClient(t, ts)
	_, err := c.Search(context.Background(), SearchQuery{Message: "x"})
	if ze := AsZError(err); ze == nil || ze.Code != CodeUpstream {
		t.Fatalf("err = %v, want Z_UPSTREAM", err)
	}
}

// 防止 json 契约漂移：ZBook 的 JSON tag 稳定性抽查（handler 直接序列化该结构）。
func TestZBookJSONTags(t *testing.T) {
	year := 2008
	rating := 4.5
	b := ZBook{ID: "1", Hash: "h", Title: "t", Year: &year, Rating: &rating, FileSize: 10, Ext: "epub"}
	data, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"zbookId"`, `"hash"`, `"title"`, `"year"`, `"rating"`, `"fileSize"`, `"ext"`} {
		if !strings.Contains(string(data), key) {
			t.Errorf("missing json key %s in %s", key, data)
		}
	}
}

// s-library: 回归测试——3xx 一律 failover，不再跟随重定向
// （z-lib.sk 对非浏览器 TLS 指纹 307 自循环；zlib.by 301 会导致 POST 降级）。
func TestClassifyResponseRedirectFailsOver(t *testing.T) {
	raw := "HTTP/1.1 307 Temporary Redirect\r\nLocation: https://z-lib.sk/eapi/book/search\r\n\r\n"
	resp, err := http.ReadResponse(bufio.NewReader(strings.NewReader(raw)), nil)
	if err != nil {
		t.Fatalf("ReadResponse: %v", err)
	}
	v := classifyResponse(resp)
	if !v.failover || v.zerr == nil || v.zerr.Code != CodeDomainUnreachable {
		t.Fatalf("307 应 failover + Z_DOMAIN_UNREACHABLE, got failover=%v zerr=%v", v.failover, v.zerr)
	}

	raw200 := "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\n\r\n{}"
	resp200, err := http.ReadResponse(bufio.NewReader(strings.NewReader(raw200)), nil)
	if err != nil {
		t.Fatalf("ReadResponse 200: %v", err)
	}
	v2 := classifyResponse(resp200)
	if v2.failover || v2.zerr != nil {
		t.Fatalf("200 应直接成功, got failover=%v zerr=%v", v2.failover, v2.zerr)
	}
}

// s-library: 回归测试——DefaultDomains 已按 2026-11-09 实测校准（z-lib.by 优先）。
func TestDefaultDomainsCalibrated(t *testing.T) {
	if len(DefaultDomains) == 0 || DefaultDomains[0] != "https://z-lib.by" {
		t.Fatalf("DefaultDomains[0] 应为 z-lib.by, got %v", DefaultDomains)
	}
	for _, d := range []string{"zh.zlib.gd", "zlib.fi", "https://zlib.by"} {
		for _, dd := range DefaultDomains {
			if strings.Contains(dd, d) {
				t.Fatalf("失效域名 %s 不应出现在预设列表: %v", d, DefaultDomains)
			}
		}
	}
}

// s-library: 回归测试——remix 凭据必须 header+Cookie 双发
// （z-lib.by 只认 Cookie，header 被忽略返回 Please login）。
func TestAuthSentAsHeaderAndCookie(t *testing.T) {
	var gotHeaderID, gotCookieID string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeaderID = r.Header.Get("remix-userid")
		if ck, err := r.Cookie("remix_userid"); err == nil {
			gotCookieID = ck.Value
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":1,"user":{"id":"77","name":"QA","dailyAmount":10,"dailyUsed":2}}`))
	}))
	defer ts.Close()

	c := newTestClient(t, ts)
	c.SetAuth("77", "key-abc")
	if _, err := c.Profile(context.Background()); err != nil {
		t.Fatalf("Profile: %v", err)
	}
	if gotHeaderID != "77" || gotCookieID != "77" {
		t.Fatalf("凭据应 header+Cookie 双发, header=%q cookie=%q", gotHeaderID, gotCookieID)
	}
}

// s-library: 回归测试——400+JSON 业务错误精确分类（不再误报"上游异常"）。
func TestClassify400EapiBusinessErrors(t *testing.T) {
	cases := []struct {
		body string
		want string
	}{
		{`{"success":0,"error":"Please login"}`, CodeAuthRequired},
		{`{"success":0,"error":"Invalid credentials"}`, CodeAuthFailed},
		{`{"success":0,"error":"Something else"}`, CodeUpstream},
	}
	for _, tc := range cases {
		raw := "HTTP/1.1 400 Bad Request\r\nContent-Type: application/json\r\n\r\n" + tc.body
		resp, err := http.ReadResponse(bufio.NewReader(strings.NewReader(raw)), nil)
		if err != nil {
			t.Fatalf("ReadResponse: %v", err)
		}
		v := classifyResponse(resp)
		if v.zerr == nil || v.zerr.Code != tc.want {
			t.Fatalf("body=%q 期望 %s, got %v", tc.body, tc.want, v.zerr)
		}
	}
}

// s-library: 回归测试——数字用户 ID 不得渲染为科学计数法
// （曾导致存档 userid="1.5155537e+07"，后续认证全部 Invalid credentials）。
func TestObjStringNumericIDNotScientific(t *testing.T) {
	m := map[string]any{"id": float64(98765432)}
	if got := objString(m, "id"); got != "98765432" {
		t.Fatalf("整数 ID 应输出完整数字, got %q", got)
	}
	if got := objString(map[string]any{"id": float64(42)}, "id"); got != "42" {
		t.Fatalf("got %q", got)
	}
	if got := objString(map[string]any{"ratio": float64(4.5)}, "ratio"); got != "4.5" {
		t.Fatalf("非整数浮点应保留小数, got %q", got)
	}
}
