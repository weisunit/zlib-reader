// s-library: Z-Library 设置管理（行为/域名/凭据/权限三档），FR-1/FR-2/FR-9/FR-10。
// 凭据仅保存 remix 令牌且 userkey 经 AES-GCM 加密落库，密码用后即弃（FR-1.3）。
package zlibservice

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/nowen-reader/nowen-reader/internal/config"
	"github.com/nowen-reader/nowen-reader/internal/model"
	"github.com/nowen-reader/nowen-reader/internal/store"
	zlibrary "github.com/nowen-reader/nowen-reader/internal/zlibrary"
)

// 权限三档（FR-10.2，默认仅管理员，决策 D1）。
const (
	ScopeAdmin     = "admin"
	ScopeCanManage = "canManage"
	ScopeAll       = "all"
)

// Behavior 下载行为配置（FR-4.1/FR-9.3）。
type Behavior struct {
	Concurrency      int               `json:"concurrency"`
	DefaultLibraryID string            `json:"defaultLibraryId"`
	AllowScope       string            `json:"allowScope"`
	LastLibraryByExt map[string]string `json:"lastLibraryByExt,omitempty"`
}

// Normalized 填充默认值并纠正越界字段。
func (b Behavior) Normalized() Behavior {
	if b.Concurrency < 1 || b.Concurrency > 3 {
		b.Concurrency = 1
	}
	switch b.AllowScope {
	case ScopeAdmin, ScopeCanManage, ScopeAll:
	default:
		b.AllowScope = ScopeAdmin
	}
	if b.LastLibraryByExt == nil {
		b.LastLibraryByExt = map[string]string{}
	}
	return b
}

// Credentials 凭据存档。密码永不落库；userkey 仅存密文。
type Credentials struct {
	RemixUserid     string `json:"remixUserid"`
	RemixUserkeyEnc string `json:"remixUserkeyEnc"`
	Connected       bool   `json:"connected"`
	FailedAt        int64  `json:"failedAt,omitempty"`
}

// HasToken 是否已保存令牌。
func (c Credentials) HasToken() bool { return c.RemixUserid != "" && c.RemixUserkeyEnc != "" }

// DomainEntry 域名配置（顺序即优先级）。
type DomainEntry struct {
	URL       string `json:"url"`
	Enabled   bool   `json:"enabled"`
	IsDefault bool   `json:"isDefault"`
}

// Settings Z-Library 设置聚合（含共享 eapi 客户端）。并发安全。
type Settings struct {
	mu        sync.RWMutex
	secretKey []byte
	client    *zlibrary.Client
	behavior  Behavior
	domains   []DomainEntry
	// s-library: 浏览器辅助通行（cf_clearance + UA，下载通道用）
	browserAssist BrowserAssist
	// s-library: 通行证自动同步令牌
	assistSyncToken string
}

var (
	defaultSettings     *Settings
	defaultSettingsOnce sync.Once
)

// GetSettings 返回进程级单例（懒加载，需在 store.InitDB 之后使用）。
func GetSettings() *Settings {
	defaultSettingsOnce.Do(func() {
		s, err := NewSettings()
		if err != nil {
			// 初始化失败不阻断进程：降级为默认设置（无凭据/默认域名）
			log.Printf("[ZLIBRARY] settings init failed, fallback to defaults: %v", err)
			s = &Settings{behavior: Behavior{}.Normalized(), domains: defaultDomainEntries()}
		}
		defaultSettings = s
	})
	return defaultSettings
}

// NewSettings 构造设置管理器并从 DB 加载。
func NewSettings() (*Settings, error) {
	s := &Settings{behavior: Behavior{}.Normalized(), domains: defaultDomainEntries()}
	secret, err := zlibrary.LoadOrCreateSecret(config.DataDir())
	if err != nil {
		return nil, fmt.Errorf("load zlib secret: %w", err)
	}
	s.secretKey = secret
	if err := s.load(); err != nil {
		return nil, err
	}
	s.client = zlibrary.NewClient(s.enabledDomainURLs())
	if creds := s.credentialsLocked(); creds.HasToken() && creds.Connected {
		s.client.SetAuth(creds.RemixUserid, s.decryptUserKey(creds))
	}
	// s-library: 浏览器辅助通行注入客户端
	if ba := s.browserAssist; ba.CookieString != "" {
		s.client.SetBrowserAssist(ba.CookieString, ba.UserAgent)
	}
	return s, nil
}

func defaultDomainEntries() []DomainEntry {
	out := make([]DomainEntry, 0, len(zlibrary.DefaultDomains))
	for i, u := range zlibrary.DefaultDomains {
		out = append(out, DomainEntry{URL: u, Enabled: true, IsDefault: i == 0})
	}
	return out
}

func (s *Settings) load() error {
	if raw, err := store.GetZlibrarySetting(store.ZlibSettingKeyBehavior); err != nil {
		return err
	} else if raw != "" {
		var b Behavior
		if err := json.Unmarshal([]byte(raw), &b); err == nil {
			s.behavior = b.Normalized()
		}
	}
	if raw, err := store.GetZlibrarySetting(store.ZlibSettingKeyDomains); err != nil {
		return err
	} else if raw != "" {
		var domains []DomainEntry
		if err := json.Unmarshal([]byte(raw), &domains); err == nil && len(domains) > 0 {
			s.domains = domains
		}
	}
	// s-library: 浏览器辅助通行配置
	if raw, err := store.GetZlibrarySetting(store.ZlibSettingKeyBrowserAssist); err == nil && raw != "" {
		var ba BrowserAssist
		if err := json.Unmarshal([]byte(raw), &ba); err == nil {
			s.mu.Lock()
			s.browserAssist = ba
			s.mu.Unlock()
		}
	}
	return nil
}

func (s *Settings) enabledDomainURLs() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	urls := []string{}
	for _, d := range s.domains {
		if d.Enabled {
			urls = append(urls, d.URL)
		}
	}
	return urls
}

// Client 共享 eapi 客户端。
func (s *Settings) Client() *zlibrary.Client {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.client
}

// SecretKey AES-GCM 密钥（导出供测试/审计，勿落日志）。
func (s *Settings) SecretKey() []byte {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.secretKey
}

// ============================================================
// 行为配置
// ============================================================

func (s *Settings) Behavior() Behavior {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.behavior
}

// SaveBehavior 保存下载行为（并发 1~3、默认书库、权限档）。
func (s *Settings) SaveBehavior(b Behavior) error {
	b = b.Normalized()
	raw, err := json.Marshal(b)
	if err != nil {
		return err
	}
	if err := store.SetZlibrarySetting(store.ZlibSettingKeyBehavior, string(raw)); err != nil {
		return err
	}
	s.mu.Lock()
	s.behavior = b
	s.mu.Unlock()
	return nil
}

// RememberLibrary 记忆扩展名对应的目标书库（FR-4.1）。
func (s *Settings) RememberLibrary(ext, libraryID string) {
	b := s.Behavior()
	if b.LastLibraryByExt == nil {
		b.LastLibraryByExt = map[string]string{}
	}
	if b.LastLibraryByExt[ext] == libraryID {
		return
	}
	b.LastLibraryByExt[ext] = libraryID
	_ = s.SaveBehavior(b) // 记忆失败不影响主流程
}

// ============================================================
// 域名配置
// ============================================================

func (s *Settings) Domains() []DomainEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]DomainEntry, len(s.domains))
	copy(out, s.domains)
	return out
}

// SaveDomains 保存域名列表（顺序即优先级）。要求至少一个启用域名。
func (s *Settings) SaveDomains(entries []DomainEntry) error {
	cleaned := make([]DomainEntry, 0, len(entries))
	seen := map[string]bool{}
	hasEnabled := false
	for _, e := range entries {
		u := strings.TrimRight(strings.TrimSpace(e.URL), "/")
		if u == "" || seen[u] {
			continue
		}
		if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
			u = "https://" + u
		}
		seen[u] = true
		if e.Enabled {
			hasEnabled = true
		}
		cleaned = append(cleaned, DomainEntry{URL: u, Enabled: e.Enabled, IsDefault: e.IsDefault})
	}
	if len(cleaned) == 0 {
		return fmt.Errorf("域名列表不能为空")
	}
	if !hasEnabled {
		return fmt.Errorf("至少需要启用一个域名")
	}
	raw, err := json.Marshal(cleaned)
	if err != nil {
		return err
	}
	if err := store.SetZlibrarySetting(store.ZlibSettingKeyDomains, string(raw)); err != nil {
		return err
	}
	s.mu.Lock()
	s.domains = cleaned
	if s.client != nil {
		s.client.SetDomains(s.enabledDomainURLsLocked())
	}
	s.mu.Unlock()
	return nil
}

func (s *Settings) enabledDomainURLsLocked() []string {
	urls := []string{}
	for _, d := range s.domains {
		if d.Enabled {
			urls = append(urls, d.URL)
		}
	}
	return urls
}

// ============================================================
// 凭据（FR-1）
// ============================================================

func (s *Settings) Credentials() Credentials {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.credentialsLocked()
}

func (s *Settings) credentialsLocked() Credentials {
	raw, err := store.GetZlibrarySetting(store.ZlibSettingKeyCredentials)
	if err != nil || raw == "" {
		return Credentials{}
	}
	var c Credentials
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		return Credentials{}
	}
	return c
}

// decryptUserKey 解密 userkey（仅内存使用，禁止日志输出）。
func (s *Settings) decryptUserKey(c Credentials) string {
	if !c.HasToken() || len(s.secretKey) != 32 {
		return ""
	}
	key, err := zlibrary.DecryptUserKey(c.RemixUserkeyEnc, s.secretKey)
	if err != nil {
		log.Printf("[ZLIBRARY] decrypt userkey failed: %v", err)
		return ""
	}
	return key
}

// SaveCredentialsToken 保存登录换取的 remix 令牌（userkey AES-GCM 加密）。
// 密码不会传入本函数——调用方在登录成功后只传令牌。
func (s *Settings) SaveCredentialsToken(userID, userKey string) error {
	if userID == "" || userKey == "" {
		return fmt.Errorf("令牌不完整")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	enc, err := zlibrary.EncryptUserKey(userKey, s.secretKey)
	if err != nil {
		return fmt.Errorf("encrypt userkey: %w", err)
	}
	creds := Credentials{RemixUserid: userID, RemixUserkeyEnc: enc, Connected: true}
	raw, err := json.Marshal(creds)
	if err != nil {
		return err
	}
	if err := store.SetZlibrarySetting(store.ZlibSettingKeyCredentials, string(raw)); err != nil {
		return err
	}
	if s.client != nil {
		s.client.SetAuth(userID, userKey)
	}
	return nil
}

// MarkAuthFailed 令牌失效时置「已断开」（FR-1.4）。
func (s *Settings) MarkAuthFailed() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	creds := s.credentialsLocked()
	if !creds.HasToken() {
		return nil
	}
	creds.Connected = false
	creds.FailedAt = time.Now().Unix()
	raw, err := json.Marshal(creds)
	if err != nil {
		return err
	}
	return store.SetZlibrarySetting(store.ZlibSettingKeyCredentials, string(raw))
}

// ClearCredentials 解绑（FR-1.5）。任务历史不清理。
func (s *Settings) ClearCredentials() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := store.DeleteZlibrarySetting(store.ZlibSettingKeyCredentials); err != nil {
		return err
	}
	_ = store.DeleteZlibrarySetting(store.ZlibSettingKeyPersonalDomain)
	if s.client != nil {
		s.client.SetAuth("", "")
	}
	return nil
}

// Connected 登录态是否有效（已保存令牌且未标记断开）。
func (s *Settings) Connected() bool {
	c := s.Credentials()
	return c.HasToken() && c.Connected
}

// ============================================================
// 个人下载域名（FR-2.4：从书籍详情 dl 自动解析并缓存备用）
// ============================================================

// BrowserAssist s-library: 浏览器辅助通行配置。
// 下载路径（/dl/*）受 DiamWall/Cloudflare 反爬保护，token 绑定浏览器 UA，
// 纯客户端无法通过；用户浏览器与服务器走同一代理出口 IP 时，
// 可借用浏览器的完整 Cookie 串 + 真实 UA，由服务端代为出示。
type BrowserAssist struct {
	CookieString string `json:"cookieString"` // 空 = 保留已存通行证
	UserAgent    string `json:"userAgent"`
	Clear        bool   `json:"clear,omitempty"` // true = 显式清除
}

// GetBrowserAssist 读取浏览器辅助通行配置。
func (s *Settings) GetBrowserAssist() BrowserAssist {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.browserAssist
}

// SaveBrowserAssist 保存浏览器辅助通行配置。
func (s *Settings) SaveBrowserAssist(b BrowserAssist) error {
	// s-library: 语义单点——Clear=true 显式清除；cookieString 为空 = 保留已存通行证（支持只更新 UA）
	if b.Clear {
		b = BrowserAssist{}
	} else if b.CookieString == "" {
		if existing := s.GetBrowserAssist().CookieString; existing != "" {
			b.CookieString = existing
		}
	}
	raw, err := json.Marshal(BrowserAssist{CookieString: b.CookieString, UserAgent: b.UserAgent})
	if err != nil {
		return err
	}
	if err := store.SetZlibrarySetting(store.ZlibSettingKeyBrowserAssist, string(raw)); err != nil {
		return err
	}
	s.mu.Lock()
	s.browserAssist = b
	s.mu.Unlock()
	// s-library: 热更新注入客户端
	if s.client != nil {
		s.client.SetBrowserAssist(b.CookieString, b.UserAgent)
	}
	return nil
}

// GetAssistSyncToken s-library: 通行证自动同步令牌（懒生成持久化）。
// 浏览器侧（书签脚本/油猴）凭此令牌调用 assist-sync 端点上传通行证。
func (s *Settings) GetAssistSyncToken() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.assistSyncToken != "" {
		return s.assistSyncToken
	}
	tok, err := store.GetZlibrarySetting(store.ZlibSettingKeyAssistSyncTok)
	if err == nil && tok != "" {
		s.assistSyncToken = tok
		return tok
	}
	// 生成 32 位 hex
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		log.Printf("[ZLIBRARY] 生成同步令牌失败: %v", err)
		return ""
	}
	tok = hex.EncodeToString(raw)
	if err := store.SetZlibrarySetting(store.ZlibSettingKeyAssistSyncTok, tok); err != nil {
		log.Printf("[ZLIBRARY] 持久化同步令牌失败: %v", err)
		return ""
	}
	s.assistSyncToken = tok
	log.Printf("[ZLIBRARY] 已生成通行证同步令牌")
	return tok
}

// ResetAssistSyncToken 重置同步令牌（旧令牌立即失效）。
func (s *Settings) ResetAssistSyncToken() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	tok := hex.EncodeToString(raw)
	if err := store.SetZlibrarySetting(store.ZlibSettingKeyAssistSyncTok, tok); err != nil {
		return "", err
	}
	s.mu.Lock()
	s.assistSyncToken = tok
	s.mu.Unlock()
	return tok, nil
}

// PersonalDomain 最近一次看到的个人下载域名。
func (s *Settings) PersonalDomain() string {
	raw, err := store.GetZlibrarySetting(store.ZlibSettingKeyPersonalDomain)
	if err != nil {
		return ""
	}
	return raw
}

// SavePersonalDomain 缓存个人域名。
func (s *Settings) SavePersonalDomain(domain string) error {
	domain = strings.TrimRight(strings.TrimSpace(domain), "/")
	if domain == "" {
		return nil
	}
	return store.SetZlibrarySetting(store.ZlibSettingKeyPersonalDomain, domain)
}

// ============================================================
// 权限三档判定（FR-10.2）
// ============================================================

// CanUse 判定用户是否可使用 Z-Library 功能（admin 恒通过）。
func (s *Settings) CanUse(user *model.AuthUser) bool {
	if user == nil {
		return false
	}
	if user.Role == "admin" {
		return true
	}
	switch s.Behavior().AllowScope {
	case ScopeAll:
		return true
	case ScopeCanManage:
		return userHasAnyManageableLibrary(user.ID)
	default: // ScopeAdmin
		return false
	}
}

// userHasAnyManageableLibrary b 档判定：对任意书库拥有 canManage。
func userHasAnyManageableLibrary(userID string) bool {
	libs, err := store.GetAllLibraries()
	if err != nil {
		return false
	}
	for _, lib := range libs {
		if ok, _ := store.UserCanManageLibrary(userID, lib.ID); ok {
			return true
		}
	}
	return false
}
