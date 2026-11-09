// s-library: Z-Library eapi HTTP 客户端 —— 域名轮换/请求级 failover/UA/超时/
// 请求间隔/冷却表/CF 拦截识别，见 docs/ARCHITECTURE.md §2.1。
// 所有 Z-Library 流量仅由服务端经本包发出；日志不输出凭据/userkey。
package zlibrary

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// DefaultDomains 内置镜像域名（FR-2.1 预置列表，顺序即优先级）。
// s-library: 2026-11-09 实测校准——z-lib.by 直连 eapi 稳定 200；
// z-lib.sk/z-lib.fm 对非浏览器 TLS 指纹存在自重定向反爬，保留作备用；
// zh.zlib.gd/zlib.fi 证书失效剔除；zlib.by 301 跳转新域名，易引发 POST 降级，剔除。
var DefaultDomains = []string{
	"https://z-lib.by",
	"https://z-lib.sk",
	"https://z-lib.fm",
}

const (
	DefaultUserAgent   = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"
	defaultTimeout     = 15 * time.Second
	defaultMinInterval = 1200 * time.Millisecond // 请求间隔（防风控 R2）
	cooldownDuration   = 5 * time.Minute         // 不健康域名冷却（FR-2.2）
)

// Client eapi 客户端。并发安全；domains 为空时使用 DefaultDomains。
type Client struct {
	http        *http.Client
	downloadHTTP *http.Client
	ua          string
	minInterval time.Duration

	// s-library: 浏览器辅助通行（cf_clearance + UA）——下载通道过 Cloudflare 用
	assistMu     sync.Mutex
	cookieString string
	assistUA     string

	mu           sync.Mutex
	domains      []string
	current      int
	cooldown     map[string]time.Time
	lastHealth   map[string]HealthState
	lastRequest  time.Time
	authUserID   string
	authUserKey  string
}

// HealthState 域名最近一次被动/主动探测结果。
type HealthState struct {
	Healthy       bool
	LastCheckedAt time.Time
	LatencyMs     int64
	Error         string
}

// Option 客户端可选项（便于测试注入）。
type Option func(*Client)

// WithHTTPClient 注入 *http.Client（单测用 httptest）。
func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) { c.http = hc }
}

// WithUserAgent 覆盖 UA。
func WithUserAgent(ua string) Option {
	return func(c *Client) { c.ua = ua }
}

// WithMinInterval 覆盖请求最小间隔。
func WithMinInterval(d time.Duration) Option {
	return func(c *Client) { c.minInterval = d }
}

// NewClient 构造客户端。domains 为空使用内置列表。
func NewClient(domains []string, opts ...Option) *Client {
	c := &Client{
		ua:          DefaultUserAgent,
		minInterval: defaultMinInterval,
		cooldown:    map[string]time.Time{},
		lastHealth:  map[string]HealthState{},
	}
	for _, opt := range opts {
		opt(c)
	}
	if c.http == nil {
		c.http = &http.Client{Timeout: defaultTimeout}
	}
	// s-library: 一律不跟随重定向。健康域名的 eapi 端点应直接返回 2xx；
	// 重定向 = 域名迁移或反爬自循环（z-lib.sk 曾对非浏览器客户端无限 307 自指向），
	// 交给 classifyResponse 判定 failover，避免 Go 默认跟随导致 POST 降级/循环烧时。
	c.http.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}
	// 下载走个人域名且文件可能很大，不设整体超时（由 ctx 控制生命周期）。
	// s-library: 下载允许跟随重定向（上限 5）——/dl/* 会 302 到带签名的 CDN 文件地址
	// （跨域后 Go 自动剥离 Cookie，CDN 地址签名自校验无需凭据）；
	// 上限防止 DiamWall 式自循环。
	c.downloadHTTP = &http.Client{Transport: c.http.Transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("stopped after 5 redirects")
			}
			return nil
		}}
	c.SetDomains(domains)
	return c
}

// SetDomains 更新域名列表（顺序即优先级），保留已知健康信息。
func (c *Client) SetDomains(domains []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	normalized := make([]string, 0, len(domains))
	for _, d := range domains {
		d = strings.TrimRight(strings.TrimSpace(d), "/")
		if d == "" {
			continue
		}
		if !strings.Contains(d, "://") {
			d = "https://" + d
		}
		normalized = append(normalized, d)
	}
	if len(normalized) == 0 {
		normalized = append(normalized, DefaultDomains...)
	}
	c.domains = normalized
	if c.current >= len(c.domains) {
		c.current = 0
	}
}

// SetAuth 配置 remix 令牌（后续请求带 remix-userid/remix-userkey 头）。
func (c *Client) SetAuth(userID, userKey string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.authUserID = userID
	c.authUserKey = userKey
}

// Authenticated 是否已配置令牌。
func (c *Client) Authenticated() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.authUserID != "" && c.authUserKey != ""
}

// CurrentDomain 当前生效域名。
func (c *Client) CurrentDomain() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.domains) == 0 {
		return ""
	}
	return c.domains[c.current%len(c.domains)]
}

// DomainStates 返回各域名健康快照（只读副本）。
func (c *Client) DomainStates() map[string]HealthState {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string]HealthState, len(c.lastHealth))
	for k, v := range c.lastHealth {
		out[k] = v
	}
	return out
}

// MarkHealth 记录一次域名探测结果；不健康自动进入冷却期（FR-2.2）。
func (c *Client) MarkHealth(domain string, healthy bool, latency time.Duration, errMsg string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lastHealth[domain] = HealthState{
		Healthy:       healthy,
		LastCheckedAt: time.Now(),
		LatencyMs:     latency.Milliseconds(),
		Error:         errMsg,
	}
	if healthy {
		delete(c.cooldown, domain)
	} else {
		c.cooldown[domain] = time.Now().Add(cooldownDuration)
	}
}

// availableDomains 返回启用且不在冷却期的域名（从 current 开始）。
func (c *Client) availableDomains() []string {
	now := time.Now()
	out := make([]string, 0, len(c.domains))
	for i := 0; i < len(c.domains); i++ {
		d := c.domains[(c.current+i)%len(c.domains)]
		if until, cooling := c.cooldown[d]; cooling && now.Before(until) {
			continue
		}
		out = append(out, d)
	}
	return out
}

// rotate 把 current 指到下一个域名。
func (c *Client) rotate() {
	if len(c.domains) > 0 {
		c.current = (c.current + 1) % len(c.domains)
	}
}

// throttle 请求间隔控制。
func (c *Client) throttle(ctx context.Context) {
	c.mu.Lock()
	last := c.lastRequest
	interval := c.minInterval
	c.mu.Unlock()

	if interval <= 0 {
		return
	}
	if elapsed := time.Since(last); elapsed < interval {
		select {
		case <-time.After(interval - elapsed):
		case <-ctx.Done():
		}
	}
	c.mu.Lock()
	c.lastRequest = time.Now()
	c.mu.Unlock()
}

// DomainCheck 单域名手动探测结果（FR-2.6/端点 11）。
type DomainCheck struct {
	URL       string `json:"url"`
	Healthy   bool   `json:"healthy"`
	LatencyMs int64  `json:"latencyMs"`
	Error     string `json:"error,omitempty"`
}

// CheckDomains 主动探测全部启用域名（任何 HTTP 响应 = 可达）。
func (c *Client) CheckDomains(ctx context.Context, timeout time.Duration) []DomainCheck {
	c.mu.Lock()
	domains := append([]string(nil), c.domains...)
	authID, authKey := c.authUserID, c.authUserKey
	ua := c.ua
	c.mu.Unlock()

	results := make([]DomainCheck, 0, len(domains))
	for _, domain := range domains {
		res := DomainCheck{URL: domain}
		start := time.Now()
		hc := &http.Client{Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }} // s-library: 不跟随重定向，与 API 客户端行为一致
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, domain+"/eapi/user/profile", nil)
		if err == nil {
			req.Header.Set("User-Agent", ua)
			if authID != "" && authKey != "" {
				req.Header.Set("remix-userid", authID)
				req.Header.Set("remix-userkey", authKey)
				// s-library: 与 API 请求一致，Cookie 双发（z-lib.by 只认 Cookie）
				req.AddCookie(&http.Cookie{Name: "remix_userid", Value: authID})
				req.AddCookie(&http.Cookie{Name: "remix_userkey", Value: authKey})
			}
			resp, err := hc.Do(req)
			if err == nil {
				io.Copy(io.Discard, io.LimitReader(resp.Body, 512))
				resp.Body.Close()
				res.Healthy = true // 收到任何 HTTP 响应即视为域名可达
			} else {
				res.Error = err.Error()
			}
		} else {
			res.Error = err.Error()
		}
		res.LatencyMs = time.Since(start).Milliseconds()
		c.MarkHealth(domain, res.Healthy, time.Duration(res.LatencyMs)*time.Millisecond, res.Error)
		results = append(results, res)
	}
	return results
}
// requestBuilder 构造指向指定域名的请求。
type requestBuilder func(baseURL string) (*http.Request, error)

// classify 判定一次响应是否需要 failover，及错误归类。
type verdict struct {
	failover bool
	blocked  bool // CF 拦截特征
	zerr     *ZError
}

// do 遍历启用域名执行请求（网络错误/5xx/CF → 下一域名，单轮）。
// 4xx（401/404/429 等）不 failover——它们是域名无关的业务错误。
func (c *Client) do(ctx context.Context, build requestBuilder) (*http.Response, error) {
	c.mu.Lock()
	domains := c.availableDomains()
	c.mu.Unlock()
	if len(domains) == 0 {
		return nil, NewZError(CodeDomainUnreachable, "所有 Z-Library 域名均处于冷却或不可用状态", nil)
	}

	var lastZerr *ZError
	for _, domain := range domains {
		c.throttle(ctx)
		if err := ctx.Err(); err != nil {
			return nil, NewZError(CodeUpstream, "请求已取消", err)
		}

		req, err := build(domain)
		if err != nil {
			return nil, NewZError(CodeUpstream, "构造请求失败", err)
		}
		c.mu.Lock()
		c.setAuthOnRequest(req)
		hc := c.http
		c.mu.Unlock()

		start := time.Now()
		resp, err := hc.Do(req)
		latency := time.Since(start)
		if err != nil {
			// 网络错误 → 标记不健康并换下一个域名
			log.Printf("[ZLIBRARY] %s %s 网络错误: %v", domain, req.URL.Path, err) // s-library: 域名级诊断日志
			c.MarkHealth(domain, false, latency, err.Error())
			c.rotate()
			lastZerr = NewZError(CodeDomainUnreachable, "域名不可达: "+domain, err)
			continue
		}

		v := classifyResponse(resp)
		switch {
		case v.failover:
			log.Printf("[ZLIBRARY] %s %s HTTP %d 触发 failover: %s", domain, req.URL.Path, resp.StatusCode, v.zerr.Message) // s-library: 域名级诊断日志
			io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
			resp.Body.Close()
			c.MarkHealth(domain, false, latency, v.zerr.Message)
			c.rotate()
			lastZerr = v.zerr
			continue
		case v.zerr != nil:
			// 业务错误（401/404/429...）：域名本身可达
			log.Printf("[ZLIBRARY] %s %s 业务错误 HTTP %d: %s", domain, req.URL.Path, resp.StatusCode, v.zerr.Message) // s-library: 域名级诊断日志
			c.MarkHealth(domain, true, latency, "")
			io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
			resp.Body.Close()
			return nil, v.zerr
		default:
			c.MarkHealth(domain, true, latency, "")
			return resp, nil
		}
	}

	if lastZerr == nil {
		lastZerr = NewZError(CodeDomainUnreachable, "所有 Z-Library 域名均无法访问", nil)
	}
	return nil, lastZerr
}

// setAuthOnRequest s-library: remix 凭据同时以 header 与 Cookie 双发。
// 实测部分镜像（z-lib.by）只读 Cookie、忽略 header（无 cookie 时返回
// 400 {"success":0,"error":"Please login"}）；双发对所有镜像兼容。
func (c *Client) setAuthOnRequest(req *http.Request) {
	req.Header.Set("User-Agent", c.ua)
	if c.authUserID != "" && c.authUserKey != "" {
		req.Header.Set("remix-userid", c.authUserID)
		req.Header.Set("remix-userkey", c.authUserKey)
		req.AddCookie(&http.Cookie{Name: "remix_userid", Value: c.authUserID})
		req.AddCookie(&http.Cookie{Name: "remix_userkey", Value: c.authUserKey})
	}
	c.applyAssistOnRequest(req) // s-library: 叠加浏览器辅助（cf_clearance + UA 覆盖）
}

// SetBrowserAssist s-library: 注入浏览器辅助通行（完整 Cookie 串 + 真实浏览器 UA）。
// 反爬 token（c_token/bsrv 等）绑定 UA 与出口 IP——用户浏览器与服务器
// 同代理出口时可整串复用；remix_* 以服务端存储的凭据为准，串中同名项忽略。
func (c *Client) SetBrowserAssist(cookieString, userAgent string) {
	c.assistMu.Lock()
	defer c.assistMu.Unlock()
	c.cookieString = cookieString
	c.assistUA = userAgent
}

// applyAssistOnRequest s-library: 请求上叠加浏览器辅助（浏览器 Cookie 串 + UA 覆盖）。
func (c *Client) applyAssistOnRequest(req *http.Request) {
	c.assistMu.Lock()
	cs, aua := c.cookieString, c.assistUA
	c.assistMu.Unlock()
	if aua != "" {
		req.Header.Set("User-Agent", aua)
	}
	for _, part := range strings.Split(cs, ";") {
		part = strings.TrimSpace(part)
		if part == "" || !strings.Contains(part, "=") {
			continue
		}
		kv := strings.SplitN(part, "=", 2)
		name := strings.TrimSpace(kv[0])
		if name == "" || strings.HasPrefix(strings.ToLower(name), "remix_") {
			continue // remix_* 以服务端加密存储的凭据为准
		}
		req.AddCookie(&http.Cookie{Name: name, Value: strings.TrimSpace(kv[1])})
	}
}

// zapiErrorFromBody s-library: 识别 eapi 的 400+JSON 业务错误形态
// （{"success":0,"error":"..."}），返回 error 文本。
func zapiErrorFromBody(r io.Reader) (msg string, ok bool) {
	b, err := io.ReadAll(io.LimitReader(r, 4096))
	if err != nil {
		return "", false
	}
	var m struct {
		Success any    `json:"success"`
		Error   string `json:"error"`
	}
	if json.Unmarshal(b, &m) != nil {
		return "", false
	}
	switch v := m.Success.(type) {
	case float64:
		if v != 0 {
			return "", false
		}
	case string:
		if v != "0" {
			return "", false
		}
	default:
		return "", false
	}
	return m.Error, m.Error != ""
}

// classifyResponse 依据状态码与 CF 特征分类。
func classifyResponse(resp *http.Response) verdict {
	status := resp.StatusCode
	if status >= 200 && status < 300 {
		return verdict{}
	}
	// s-library: 3xx 不再跟随（CheckRedirect 已禁）——健康域名不应重定向，
	// 视为域名迁移/反爬自循环，触发 failover 换下一个域名
	if status >= 300 && status < 400 {
		return verdict{failover: true,
			zerr: NewZError(CodeDomainUnreachable, "域名重定向（疑似迁移或反爬）: "+resp.Header.Get("Location"), nil)}
	}
	if isCloudflareChallenge(resp) {
		return verdict{failover: true, blocked: true,
			zerr: NewZError(CodeBlocked, "访问被拦截（Cloudflare 验证），已尝试切换域名", nil)}
	}
	switch {
	case status == http.StatusUnauthorized:
		return verdict{zerr: NewZError(CodeAuthRequired, "Z-Library 登录态已失效，请重新连接", nil)}
	case status == http.StatusForbidden:
		return verdict{zerr: NewZError(CodeBlocked, "访问被上游拒绝", nil)}
	case status == http.StatusNotFound:
		return verdict{zerr: NewZError(CodeNotFound, "内容在 Z-Library 已不可用", nil)}
	case status == http.StatusTooManyRequests:
		return verdict{zerr: NewZError(CodeRateLimited, "请求过于频繁，被上游限流", nil)}
	case status >= 500:
		return verdict{failover: true,
			zerr: NewZError(CodeDomainUnreachable, "上游服务错误", nil)}
	default:
		// s-library: 部分 eapi 镜像以 400 + JSON body 表达业务错误（如令牌失效）
		if msg, ok := zapiErrorFromBody(resp.Body); ok {
			lower := strings.ToLower(msg)
			switch {
			case strings.Contains(lower, "please login"):
				return verdict{zerr: NewZError(CodeAuthRequired, "Z-Library 登录态已失效，请重新连接", nil)}
			case strings.Contains(lower, "invalid credentials"):
				return verdict{zerr: NewZError(CodeAuthFailed, "凭据无效或已失效，请重新登录", nil)}
			default:
				return verdict{zerr: NewZError(CodeUpstream, "上游返回异常: "+msg, nil)}
			}
		}
		return verdict{zerr: NewZError(CodeUpstream, "上游返回异常状态", nil)}
	}
}

// isCloudflareChallenge 识别 Cloudflare 挑战页（403/503 + 特征头/特征串）。
func isCloudflareChallenge(resp *http.Response) bool {
	if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusServiceUnavailable {
		return false
	}
	if server := resp.Header.Get("Server"); strings.Contains(strings.ToLower(server), "cloudflare") {
		return true
	}
	if strings.Contains(strings.ToLower(resp.Header.Get("cf-mitigated")), "challenge") {
		return true
	}
	// 响应体特征（仅读前 4KB 判断）
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	lower := strings.ToLower(string(body))
	return strings.Contains(lower, "just a moment") ||
		strings.Contains(lower, "checking your browser") ||
		strings.Contains(lower, "cf-browser-verification") ||
		strings.Contains(lower, "cf_chl_opt") ||
		strings.Contains(lower, "attention required")
}

// postForm 发送 application/x-www-form-urlencoded 请求并返回 JSON 对象。
func (c *Client) postForm(ctx context.Context, path string, form url.Values) (map[string]any, error) {
	return c.doJSON(ctx, http.MethodPost, path, strings.NewReader(form.Encode()))
}

// getJSON 发送 GET 请求并返回 JSON 对象。
func (c *Client) getJSON(ctx context.Context, path string) (map[string]any, error) {
	return c.doJSON(ctx, http.MethodGet, path, nil)
}

func (c *Client) doJSON(ctx context.Context, method, path string, body io.Reader) (map[string]any, error) {
	// body 可能随多个域名重试被复用，先读成字节切片
	var payload []byte
	if body != nil {
		var err error
		if payload, err = io.ReadAll(body); err != nil {
			return nil, NewZError(CodeUpstream, "读取请求体失败", err)
		}
	}
	build := func(baseURL string) (*http.Request, error) {
		var reader io.Reader
		if payload != nil {
			reader = bytes.NewReader(payload)
		}
		req, err := http.NewRequestWithContext(ctx, method, baseURL+path, reader)
		if err != nil {
			return nil, err
		}
		if method == http.MethodPost {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		req.Header.Set("Accept", "application/json")
		return req, nil
	}
	resp, err := c.do(ctx, build)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return parseJSONBody(resp)
}

func parseJSONBody(resp *http.Response) (map[string]any, error) {
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, NewZError(CodeUpstream, "读取响应失败", err)
	}
	m, perr := parseJSONObject(data)
	if perr != nil {
		// eapi 偶发返回 HTML 错误页
		if bytes.Contains(bytes.ToLower(data[:minInt(len(data), 512)]), []byte("<!doctype html")) ||
			bytes.Contains(bytes.ToLower(data[:minInt(len(data), 512)]), []byte("<html")) {
			return nil, NewZError(CodeUpstream, "上游返回了非 JSON 响应", perr)
		}
		return nil, NewZError(CodeUpstream, "解析响应失败", perr)
	}
	return m, nil
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
