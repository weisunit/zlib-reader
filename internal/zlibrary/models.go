// s-library: Z-Library eapi 适配层 —— 统一模型与防御性解析（未知字段宽容），
// 见 docs/ARCHITECTURE.md §2.1 与 REQUIREMENTS.md §3.3/§7。
// 本包为纯客户端：禁止 import store/service/config（架构 §2.2 依赖约束）。
package zlibrary

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"strings"
)

// 错误码（REQUIREMENTS.md §7 错误矩阵）。适配层负责把上游网络/HTTP/业务失败
// 归类为这些稳定码，handler 层据此映射 HTTP 状态。
const (
	CodeAuthRequired      = "Z_AUTH_REQUIRED"      // 未配置凭据/令牌失效
	CodeAuthFailed        = "Z_AUTH_FAILED"        // 账号密码错误
	CodeQuotaExceeded     = "Z_QUOTA_EXCEEDED"     // 今日额度用尽
	CodeSizeLimit         = "Z_SIZE_LIMIT"         // 超过账号单文件上限
	CodeDomainUnreachable = "Z_DOMAIN_UNREACHABLE" // 域名全部不可达
	CodeBlocked           = "Z_BLOCKED"            // CF 挑战/风控
	CodeNotFound          = "Z_NOT_FOUND"          // 书籍下架/链接失效
	// 以下为业务/流水线侧扩展码（§7 矩阵同源，非上游 HTTP 分类）
	CodeExtMismatch  = "Z_EXT_MISMATCH"  // 目标书库类型不允许该格式
	CodeDiskFull     = "Z_DISK_FULL"     // 磁盘空间不足/落盘失败
	CodeScanFailed   = "Z_SCAN_FAILED"   // 扫描未匹配到新条目
	CodeDupImported  = "Z_DUP_IMPORTED"  // 已入库
	CodeDupQueued    = "Z_DUP_QUEUED"    // 已在队列
	CodeForbidden    = "Z_FORBIDDEN"     // 无权使用（权限档）
	CodeInterrupted  = "Z_INTERRUPTED"   // 进程重启中断（可重试）
	CodeInvalidState = "Z_INVALID_STATE" // 任务状态不允许该操作（DEF-4：retry 非 failed → 409）
	CodeLibNotFound  = "Z_LIB_NOT_FOUND" // 目标书库不存在
	CodeRateLimited  = "Z_RATE_LIMITED"  // 上游限流（429），内部扩展码
	CodeUpstream     = "Z_UPSTREAM"      // 其他上游失败（内部扩展码）
)

// ZError 结构化错误：Code 稳定、Message 人话、Err 保留根因。
type ZError struct {
	Code    string
	Message string
	Err     error
}

func (e *ZError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %v", e.Code, e.Err)
	}
	return e.Code + ": " + e.Message
}

func (e *ZError) Unwrap() error { return e.Err }

// NewZError 构造结构化错误。
func NewZError(code, message string, err error) *ZError {
	return &ZError{Code: code, Message: message, Err: err}
}

// AsZError 提取错误链中的 *ZError；非 ZError 归类为 Z_UPSTREAM。
func AsZError(err error) *ZError {
	if err == nil {
		return nil
	}
	var ze *ZError
	if errors.As(err, &ze) {
		return ze
	}
	return &ZError{Code: CodeUpstream, Message: err.Error(), Err: err}
}

// ZBook 统一书籍模型（搜索/详情/格式解析结果）。ext 统一小写且不带点（如 "epub"）。
type ZBook struct {
	ID          string   `json:"zbookId"`
	Hash        string   `json:"hash"`
	Title       string   `json:"title"`
	Subtitle    string   `json:"subtitle,omitempty"`
	Author      string   `json:"author,omitempty"`
	Publisher   string   `json:"publisher,omitempty"`
	Year        *int     `json:"year"`
	Language    string   `json:"language,omitempty"`
	Ext         string   `json:"ext,omitempty"`
	FileSize    int64    `json:"fileSize"`
	Rating      *float64 `json:"rating"`
	CoverURL    string   `json:"coverUrl,omitempty"`
	Description string   `json:"description,omitempty"`
	Formats     []ZFormat `json:"formats,omitempty"`
	DlDomain    string   `json:"dlDomain,omitempty"` // 个人下载域名（host）
	DownloadURL string   `json:"downloadUrl,omitempty"` // 详情 dl 字段的完整下载地址
}

// ZFormat 单一可用格式。
type ZFormat struct {
	Ext      string `json:"ext"`
	FileSize int64  `json:"fileSize"`
}

// ZProfile 统一账号/配额模型（FR-6.1：免费/捐赠字段映射）。
type ZProfile struct {
	UserID      string
	Name        string
	Email       string
	AccountType string // "free" | "donor"（尽力推断）
	DailyQuota  int
	DailyUsed   int
	// s-library: 个人下载域名（CF 下载防护下唯一可行的下载通道），
	// eapi 无契约，多候选防御性解析
	PersonalDomain string
}

// ZSearchResult 统一搜索结果。
type ZSearchResult struct {
	Total int
	Page  int
	Limit int
	Books []ZBook
}

// ============================================================
// 防御性解析助手（eapi 字段无契约：string/number 混用，缺字段常见）
// ============================================================

func parseJSONObject(data []byte) (map[string]any, error) {
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return m, nil
}

// objString 依次尝试多个键，返回第一个非空字符串值。
func objString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok && v != nil {
			switch t := v.(type) {
			case string:
				if strings.TrimSpace(t) != "" {
					return strings.TrimSpace(t)
				}
			case float64:
				// eapi 偶尔把字符串字段回传为数字。
				// s-library: 大整数（如用户 ID 98765432）经 %v 会渲染成科学计数法
				// "1.5155537e+07"，导致后续认证全部 Invalid credentials——
				// 整数值必须用 %.0f 输出完整数字。
				if t == math.Trunc(t) && math.Abs(t) < 1e15 {
					return fmt.Sprintf("%.0f", t)
				}
				return fmt.Sprintf("%v", t)
			}
		}
	}
	return ""
}

// objInt 依次尝试多个键，兼容数字与数字字符串。
func objInt(m map[string]any, keys ...string) int {
	if v := objFloat(m, keys...); v != 0 {
		return int(v)
	}
	return 0
}

func objFloat(m map[string]any, keys ...string) float64 {
	for _, k := range keys {
		v, ok := m[k]
		if !ok || v == nil {
			continue
		}
		switch t := v.(type) {
		case float64:
			return t
		case string:
			t = strings.TrimSpace(t)
			if t == "" {
				continue
			}
			var f float64
			if err := json.Unmarshal([]byte(t), &f); err == nil {
				return f
			}
		}
	}
	return 0
}

// objIntPtr 年份等可空整数字段。
func objIntPtr(m map[string]any, keys ...string) *int {
	if v := objInt(m, keys...); v != 0 {
		return &v
	}
	return nil
}

// objFloatPtr 评分等可空浮点字段。
func objFloatPtr(m map[string]any, keys ...string) *float64 {
	if v := objFloat(m, keys...); v != 0 {
		return &v
	}
	return nil
}

// normalizeExt 规范化扩展名："EPUB" / ".epub" / "epub" → "epub"。
func normalizeExt(ext string) string {
	ext = strings.ToLower(strings.TrimSpace(ext))
	return strings.TrimPrefix(ext, ".")
}

// parseBook 从搜索/详情响应的 book 对象做防御性解析。
func parseBook(m map[string]any) ZBook {
	b := ZBook{
		ID:          objString(m, "id", "zbookId", "bookId"),
		Hash:        objString(m, "hash", "zbookHash", "md5"),
		Title:       objString(m, "title", "name"),
		Subtitle:    objString(m, "subtitle"),
		Author:      objString(m, "author", "authors"),
		Publisher:   objString(m, "publisher"),
		Year:        objIntPtr(m, "year", "yearString"),
		Language:    objString(m, "language", "lang"),
		Ext:         normalizeExt(objString(m, "extension", "ext", "format")),
		FileSize:    int64(objFloat(m, "filesize", "fileSize", "size")),
		Rating:      objFloatPtr(m, "rating", "stars"),
		CoverURL:    normalizeCover(objString(m, "cover", "coverUrl", "cover_url")),
		Description: objString(m, "descr", "description", "descriptionShort"),
		DownloadURL: objString(m, "dl", "downloadUrl", "download_url"),
	}
	if b.DownloadURL != "" {
		if u, err := url.Parse(b.DownloadURL); err == nil && u.Host != "" {
			b.DlDomain = u.Scheme + "://" + u.Host
		}
	}
	return b
}

// normalizeCover s-library: 占位图（/img/cover-not-exists.png）视为无封面，
// 由前端展示本地占位样式，避免加载上游占位资源。
func normalizeCover(cover string) string {
	if strings.Contains(cover, "cover-not-exists") {
		return ""
	}
	return cover
}

// dedupeFormats 按 ext 去重，保持顺序。
func dedupeFormats(formats []ZFormat) []ZFormat {
	seen := make(map[string]bool, len(formats))
	out := formats[:0:0]
	for _, f := range formats {
		if f.Ext == "" || seen[f.Ext] {
			continue
		}
		seen[f.Ext] = true
		out = append(out, f)
	}
	return out
}
