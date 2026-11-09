// s-library: 配额缓存与本地预检（FR-6.1/6.3）。配额快照持久化到 ZlibrarySetting，
// 下载完成后强制刷新；下载前用本地计数预检避免浪费上游请求。
package zlibservice

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/nowen-reader/nowen-reader/internal/store"
	zlibrary "github.com/nowen-reader/nowen-reader/internal/zlibrary"
)

// QuotaSnapshot 配额快照（今日已用/总量）。
type QuotaSnapshot struct {
	Used      int       `json:"used"`
	Total     int       `json:"total"`
	ResetAt   string    `json:"resetAt,omitempty"`
	FetchedAt time.Time `json:"fetchedAt"`
}

// Remaining 剩余额度（total 未知时返回 -1 表示不限）。
func (q QuotaSnapshot) Remaining() int {
	if q.Total <= 0 {
		return -1
	}
	r := q.Total - q.Used
	if r < 0 {
		return 0
	}
	return r
}

// Quota 读取缓存快照，无缓存返回 (ok=false)。
func (s *Settings) Quota() (QuotaSnapshot, bool) {
	q := QuotaSnapshot{}
	raw, err := store.GetZlibrarySetting(store.ZlibSettingKeyQuotaCache)
	if err != nil || raw == "" {
		return q, false
	}
	if err := json.Unmarshal([]byte(raw), &q); err != nil {
		return q, false
	}
	return q, q.FetchedAt.After(time.Time{})
}

// SetQuota 写入快照缓存。
func (s *Settings) SetQuota(q QuotaSnapshot) error {
	if q.FetchedAt.IsZero() {
		q.FetchedAt = time.Now()
	}
	raw, err := json.Marshal(q)
	if err != nil {
		return err
	}
	return store.SetZlibrarySetting(store.ZlibSettingKeyQuotaCache, string(raw))
}

// RefreshQuota 从上游 profile 刷新配额缓存（下载完成后调用，FR-6.3）。
func (s *Settings) RefreshQuota(ctx context.Context) (*zlibrary.ZProfile, error) {
	client := s.Client()
	if client == nil || !client.Authenticated() {
		return nil, zlibrary.NewZError(zlibrary.CodeAuthRequired, "Z-Library 未连接", nil)
	}
	profile, err := client.Profile(ctx)
	if err != nil {
		return nil, err
	}
	if profile.DailyQuota > 0 {
		if err := s.SetQuota(QuotaSnapshot{
			Used:      profile.DailyUsed,
			Total:     profile.DailyQuota,
			FetchedAt: time.Now(),
		}); err != nil {
			log.Printf("[ZLIBRARY] save quota cache failed: %v", err)
		}
	}
	// s-library: 顺带缓存个人下载域名（下载 CF 防护下的唯一可行通道）
	if profile.PersonalDomain != "" {
		_ = s.SavePersonalDomain(profile.PersonalDomain)
	}
	return profile, nil
}

// QuotaPrecheck 本地预检：未连接 → Z_AUTH_REQUIRED；额度用尽 → Z_QUOTA_EXCEEDED。
// 无快照时不拦截（首次使用允许尝试，由上游报错兜底）。
func (s *Settings) QuotaPrecheck() error {
	if !s.Connected() {
		return zlibrary.NewZError(zlibrary.CodeAuthRequired, "Z-Library 未连接，请先在设置中登录", nil)
	}
	q, ok := s.Quota()
	if !ok || q.Total <= 0 {
		return nil
	}
	if q.Used >= q.Total {
		return zlibrary.NewZError(zlibrary.CodeQuotaExceeded,
			"今日下载额度已用完（"+itoa(q.Used)+"/"+itoa(q.Total)+"），将于次日重置后可重试", nil)
	}
	return nil
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := make([]byte, 0, 8)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	if neg {
		return "-" + string(digits)
	}
	return string(digits)
}
