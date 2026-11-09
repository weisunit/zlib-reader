// s-library: eapi 登录/令牌校验/profile（配额字段映射），见 FR-1/FR-6.1。
package zlibrary

import (
	"context"
	"strings"
)

// LoginResult 登录结果：令牌 + 账号画像。
type LoginResult struct {
	UserID  string
	UserKey string
	Profile *ZProfile
}

// loginFailureMsg s-library: 统一构造带真实原因的登录失败文案。
// 上游拒绝登录有多种原因（密码错误/验证码/尝试次数超限/账号受限），
// 不能一律断言"密码错误"，否则用户会被误导。
func loginFailureMsg(detail string) string {
	detail = strings.ToLower(strings.TrimSpace(detail))
	switch {
	case strings.Contains(detail, "incorrect email or password"):
		return "登录失败：邮箱或密码错误"
	case strings.Contains(detail, "captcha"):
		return "登录失败：上游要求验证码，请在浏览器登录一次 z-lib.by，然后改用「remix 令牌」方式连接"
	case strings.Contains(detail, "wait") || strings.Contains(detail, "attempt") || strings.Contains(detail, "too many"):
		return "登录失败：尝试次数过多被临时限制，请稍后再试，或改用「remix 令牌」方式连接"
	case detail == "":
		return "登录失败：邮箱或密码错误"
	default:
		return "登录失败：" + detail
	}
}

// Login 邮箱+密码登录换取 remix 令牌。密码仅经内存传递，本包不做任何日志输出。
func (c *Client) Login(ctx context.Context, email, password string) (*LoginResult, error) {
	form := map[string][]string{
		"email":    {email},
		"password": {password},
	}
	m, err := c.postForm(ctx, "/eapi/user/login", form)
	if err != nil {
		// s-library: 透传上游真实拒绝原因，不再一律断言密码错误
		if ze := AsZError(err); ze != nil {
			switch ze.Code {
			case CodeAuthRequired:
				return nil, NewZError(CodeAuthFailed, loginFailureMsg(ze.Message), err)
			case CodeUpstream:
				// 400+JSON 已被嗅探为 CodeUpstream("上游返回异常: <上游文本>")，提取详情
				if i := strings.Index(ze.Message, ": "); i >= 0 {
					return nil, NewZError(CodeAuthFailed, loginFailureMsg(ze.Message[i+2:]), err)
				}
			}
		}
		return nil, err
	}
	if !isSuccess(m) {
		upErr, _ := m["error"].(string)
		return nil, NewZError(CodeAuthFailed, loginFailureMsg(upErr), nil)
	}
	user, _ := m["user"].(map[string]any)
	if user == nil {
		return nil, NewZError(CodeAuthFailed, "登录响应缺少用户信息", nil)
	}
	userID := objString(user, "id", "userId")
	userKey := objString(user, "remix_userkey", "remixUserkey", "userkey")
	if userID == "" || userKey == "" {
		return nil, NewZError(CodeAuthFailed, "登录响应缺少令牌字段", nil)
	}
	return &LoginResult{
		UserID:  userID,
		UserKey: userKey,
		Profile: parseProfile(user),
	}, nil
}

// Profile 用已配置令牌获取账号画像（校验登录态 + 配额）。
func (c *Client) Profile(ctx context.Context) (*ZProfile, error) {
	m, err := c.getJSON(ctx, "/eapi/user/profile")
	if err != nil {
		return nil, err
	}
	if !isSuccess(m) {
		return nil, NewZError(CodeAuthRequired, "Z-Library 登录态校验失败", nil)
	}
	user, _ := m["user"].(map[string]any)
	if user == nil {
		return nil, NewZError(CodeAuthRequired, "profile 响应缺少用户信息", nil)
	}
	profile := parseProfile(user)
	// s-library: 个人域名顶层兜底
	if profile.PersonalDomain == "" {
		profile.PersonalDomain = zprofilePersonalDomainFromTop(m)
	}
	return profile, nil
}

// parseProfile 兼容免费/捐赠账号的不同配额字段（FR-6.1）。
func parseProfile(user map[string]any) *ZProfile {
	p := &ZProfile{
		UserID: objString(user, "id", "userId"),
		Name: objString(user, "name", "username", "nickname"),
		Email: objString(user, "email", "mail"),
	}
	if p.Name == "" {
		first := objString(user, "firstname", "firstName")
		last := objString(user, "lastname", "lastName")
		if first != "" || last != "" {
			p.Name = strings.TrimSpace(first + " " + last)
		}
	}
	if p.Name == "" {
		p.Name = p.Email
	}
	// 账号类型：kind 常见值 "regular"/"donor"；兜底 accountType/type 字段与 donations 推断
	kind := strings.ToLower(objString(user, "kind", "accountType", "type"))
	switch {
	case strings.Contains(kind, "donor"), strings.Contains(kind, "premium"), strings.Contains(kind, "vip"):
		p.AccountType = "donor"
	case strings.Contains(kind, "regular"), strings.Contains(kind, "free"):
		p.AccountType = "free"
	case objFloat(user, "donations") > 0:
		p.AccountType = "donor"
	default:
		p.AccountType = "free"
	}
	// 每日配额：donor 账号用 dailyQuota，免费账号常见 dailyAmount；
	// s-library: 实测 z-lib.by 免费账号字段为 downloads_limit / downloads_today
	p.DailyQuota = objInt(user, "dailyQuota", "dailyAmount", "dailyLimit", "quota", "downloadsLimit", "downloads_limit")
	p.DailyUsed = objInt(user, "dailyUsed", "dailyDownloaded", "usedToday", "downloadsToday", "downloads_today")
	// s-library: 个人下载域名多候选解析（无契约，宽容处理）
	p.PersonalDomain = objString(user, "personalDomain", "myDomain", "personal_domain", "mirrorDomain")
	if p.PersonalDomain != "" && !strings.HasPrefix(p.PersonalDomain, "http") {
		p.PersonalDomain = "https://" + p.PersonalDomain
	}
	return p
}

// zprofilePersonalDomainFromTop s-library: 顶层 payload 的个人域名候选查找。
func zprofilePersonalDomainFromTop(m map[string]any) string {
	d := objString(m, "personalDomain", "myDomain", "personal_domain", "mirrorDomain")
	if d != "" && !strings.HasPrefix(d, "http") {
		d = "https://" + d
	}
	return d
}

// isSuccess 兼容 {"success":1} / {"success":true} / 无 success 字段但正常返回。
func isSuccess(m map[string]any) bool {
	v, ok := m["success"]
	if !ok {
		return true
	}
	switch t := v.(type) {
	case float64:
		return t != 0
	case bool:
		return t
	case string:
		return t == "1" || strings.EqualFold(t, "true")
	}
	return true
}
