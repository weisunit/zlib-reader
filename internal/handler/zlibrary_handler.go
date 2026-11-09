// s-library: Z-Library HTTP 端点（11 个，契约见 docs/ARCHITECTURE.md §4）。
// 薄层：绑定/校验/调 service；错误统一 {"error","code"} JSON（§7 矩阵）。
// 日志不输出凭据/userkey；所有 Z-Library 流量仅在 service 层服务端发起。
package handler

import (
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nowen-reader/nowen-reader/internal/config"
	"github.com/nowen-reader/nowen-reader/internal/middleware"
	"github.com/nowen-reader/nowen-reader/internal/model"
	"github.com/nowen-reader/nowen-reader/internal/store"
	zlibrary "github.com/nowen-reader/nowen-reader/internal/zlibrary"
	zlibservice "github.com/nowen-reader/nowen-reader/internal/zlibrary/service"
)

// zHTTPStatus ZError → HTTP 状态码映射（契约 §4：status 表达类别，code 结构化）。
func zHTTPStatus(code string) int {
	switch code {
	case zlibrary.CodeAuthRequired:
		return http.StatusUnauthorized
	case zlibrary.CodeAuthFailed:
		return http.StatusUnprocessableEntity // 422（契约 (3) 约定）
	case zlibrary.CodeForbidden:
		return http.StatusForbidden
	case zlibrary.CodeNotFound, zlibrary.CodeLibNotFound:
		return http.StatusNotFound
	case zlibrary.CodeQuotaExceeded, zlibrary.CodeDupImported, zlibrary.CodeDupQueued,
		zlibrary.CodeRateLimited, zlibrary.CodeInvalidState:
		return http.StatusConflict
	case zlibrary.CodeExtMismatch:
		return http.StatusBadRequest
	case zlibrary.CodeDiskFull:
		return http.StatusConflict
	case zlibrary.CodeDomainUnreachable, zlibrary.CodeBlocked:
		return http.StatusBadGateway
	case zlibrary.CodeSizeLimit:
		return http.StatusRequestEntityTooLarge
	default:
		return http.StatusInternalServerError
	}
}

// zlibraryError 统一错误响应：{"error": 人话, "code": "Z_XXX"}。可选附带字段。
func zlibraryError(c *gin.Context, err error, extra gin.H) {
	ze := zlibrary.AsZError(err)
	status := zHTTPStatus(ze.Code)
	body := gin.H{"error": ze.Message, "code": ze.Code}
	for k, v := range extra {
		body[k] = v
	}
	c.JSON(status, body)
}

// zmarkAuthFailed 令牌失效时全局置「已断开」（FR-1.4）。
func zmarkAuthFailed(err error) {
	if ze := zlibrary.AsZError(err); ze != nil && ze.Code == zlibrary.CodeAuthRequired {
		_ = zlibservice.GetSettings().MarkAuthFailed()
	}
}

// zcurrentUser 带登录校验取当前用户（未登录返回 false，已写响应）。
func zcurrentUser(c *gin.Context) (*model.AuthUser, bool) {
	user := middleware.GetCurrentUser(c)
	if user == nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return nil, false
	}
	return user, true
}

// ============================================================
// (1) GET /api/zlibrary/status
// ============================================================

// ZlibraryStatus 连接状态 + 配额 + 当前域名 + 各域名健康（FR-2.6/FR-6.2）。
func ZlibraryStatus(c *gin.Context) {
	user, ok := zcurrentUser(c)
	if !ok {
		return
	}
	s := zlibservice.GetSettings()
	if !s.CanUse(user) {
		zlibraryError(c, zlibrary.NewZError(zlibrary.CodeForbidden, "无权使用 Z-Library 功能", nil), nil)
		return
	}

	resp := gin.H{"connected": s.Connected()}
	resp["accountName"] = ""
	if s.Connected() {
		if profile, err := s.RefreshQuota(c.Request.Context()); err == nil && profile != nil {
			resp["accountName"] = profile.Name
			resp["accountType"] = profile.AccountType
		} else if err != nil {
			// 令牌失效 → 全局置断开（FR-1.4），状态响应仍返回缓存配额
			zmarkAuthFailed(err)
		}
	}
	if q, has := s.Quota(); has {
		resp["quota"] = gin.H{"used": q.Used, "total": q.Total, "resetAt": q.ResetAt}
	} else {
		resp["quota"] = gin.H{"used": 0, "total": 0, "resetAt": ""}
	}

	client := s.Client()
	resp["currentDomain"] = ""
	health := map[string]zlibrary.HealthState{}
	if client != nil {
		resp["currentDomain"] = client.CurrentDomain()
		health = client.DomainStates()
	}
	resp["personalDomain"] = s.PersonalDomain()

	domains := []gin.H{}
	for _, d := range s.Domains() {
		item := gin.H{
			"url":           d.URL,
			"enabled":       d.Enabled,
			"isDefault":     d.IsDefault,
			"healthy":       false,
			"lastCheckedAt": nil,
			"latencyMs":     nil,
		}
		if hs, has := health[d.URL]; has {
			item["healthy"] = hs.Healthy
			if !hs.LastCheckedAt.IsZero() {
				item["lastCheckedAt"] = hs.LastCheckedAt.UTC().Format(time.RFC3339)
			}
			if hs.Healthy && hs.LatencyMs > 0 {
				ms := hs.LatencyMs
				item["latencyMs"] = ms
			}
		}
		domains = append(domains, item)
	}
	resp["domains"] = domains
	resp["allowScope"] = s.Behavior().AllowScope
	resp["canUse"] = s.CanUse(user)
	c.JSON(http.StatusOK, resp)
}

// ============================================================
// (2) GET+PUT /api/zlibrary/settings
// ============================================================

// ZlibraryGetSettings 读取域名/行为/权限档配置回显（admin）。
// DEF-1（QA-REPORT.md）修复：响应与 PUT /settings 的请求体同构
// （domains[]{url,enabled,isDefault} + behavior{concurrency,defaultLibraryId,allowScope}，
// JSON 字段名完全一致），前端可直接把响应体作为下一次 PUT 的载荷回传。
// 凭据秘密字段（password/remixUserkey）一律不回传（本端点不含任何凭据字段，
// 凭据状态仅以 hasCredentials 布尔值表达）。
func ZlibraryGetSettings(c *gin.Context) {
	s := zlibservice.GetSettings()
	b := s.Behavior()
	c.JSON(http.StatusOK, gin.H{
		"domains": s.Domains(),
		"behavior": gin.H{
			"concurrency":      b.Concurrency,
			"defaultLibraryId": b.DefaultLibraryID,
			"allowScope":       b.AllowScope,
		},
		"hasCredentials": s.Credentials().HasToken(),
		// s-library: 浏览器辅助通行（cookie 串不回传，仅配置状态 + UA）
		"browserAssist": gin.H{
			"configured": s.GetBrowserAssist().CookieString != "",
			"userAgent":  s.GetBrowserAssist().UserAgent,
		},
	})
}

// ZlibrarySaveSettings 保存域名/行为/权限档配置（admin）。
func ZlibrarySaveSettings(c *gin.Context) {
	var req struct {
		Domains  *[]zlibservice.DomainEntry `json:"domains"`
		Behavior *struct {
			Concurrency      *int               `json:"concurrency"`
			DefaultLibraryID *string            `json:"defaultLibraryId"`
			AllowScope       *string            `json:"allowScope"`
		} `json:"behavior"`
		BrowserAssist *struct { // s-library: 浏览器辅助通行（完整 cookie 串 + 真实 UA）
			CookieString string `json:"cookieString"`
			UserAgent    string `json:"userAgent"`
		} `json:"browserAssist"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数非法"})
		return
	}
	s := zlibservice.GetSettings()

	if req.Domains != nil {
		if err := s.SaveDomains(*req.Domains); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
	}
	// s-library: 浏览器辅助通行（Cloudflare 下载通道）
	if req.BrowserAssist != nil {
		if err := s.SaveBrowserAssist(zlibservice.BrowserAssist{
			CookieString: strings.TrimSpace(req.BrowserAssist.CookieString),
			UserAgent:    strings.TrimSpace(req.BrowserAssist.UserAgent),
		}); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "保存浏览器辅助配置失败"})
			return
		}
	}
	if req.Behavior != nil {
		b := s.Behavior()
		if req.Behavior.Concurrency != nil {
			if *req.Behavior.Concurrency < 1 || *req.Behavior.Concurrency > 3 {
				c.JSON(http.StatusBadRequest, gin.H{"error": "并发数仅支持 1~3"})
				return
			}
			b.Concurrency = *req.Behavior.Concurrency
		}
		if req.Behavior.AllowScope != nil {
			switch *req.Behavior.AllowScope {
			case zlibservice.ScopeAdmin, zlibservice.ScopeCanManage, zlibservice.ScopeAll:
				b.AllowScope = *req.Behavior.AllowScope
			default:
				c.JSON(http.StatusBadRequest, gin.H{"error": "权限档非法"})
				return
			}
		}
		if req.Behavior.DefaultLibraryID != nil {
			libID := strings.TrimSpace(*req.Behavior.DefaultLibraryID)
			if libID != "" {
				lib, err := store.GetLibraryByID(libID)
				if err != nil || lib == nil {
					zlibraryError(c, zlibrary.NewZError(zlibrary.CodeLibNotFound, "默认书库不存在", nil), nil)
					return
				}
			}
			b.DefaultLibraryID = libID
		}
		if err := s.SaveBehavior(b); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "保存设置失败", "code": zlibrary.CodeUpstream}) // s-library: 保留 500 内部错误的兜底码
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// ============================================================
// (3) POST /api/zlibrary/auth/test
// ============================================================

// ZlibraryTestAuth 测试连接：{email,password} 登录换令牌 / {remixUserid,remixUserkey}
// 粘贴令牌（FR-1.1b）/ {} 用已存令牌校验。密码用后即弃。
func ZlibraryTestAuth(c *gin.Context) {
	var req struct {
		Email        string `json:"email"`
		Password     string `json:"password"`
		RemixUserid  string `json:"remixUserid"`
		RemixUserkey string `json:"remixUserkey"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		req = struct {
			Email        string `json:"email"`
			Password     string `json:"password"`
			RemixUserid  string `json:"remixUserid"`
			RemixUserkey string `json:"remixUserkey"`
		}{}
	}
	s := zlibservice.GetSettings()
	client := s.Client()
	if client == nil {
		zlibraryError(c, zlibrary.NewZError(zlibrary.CodeDomainUnreachable, "客户端未就绪", nil), nil)
		return
	}

	var profile *zlibrary.ZProfile
	switch {
	case req.Email != "" && req.Password != "":
		res, err := client.Login(c.Request.Context(), req.Email, req.Password)
		if err != nil {
			// 登录失败：密码错误 → 422 Z_AUTH_FAILED；网络类原样映射（不置全局断开）
			zlibraryError(c, err, nil)
			return
		}
		// 换取令牌后立即保存（密码到此已丢弃，不落库不落日志）
		if err := s.SaveCredentialsToken(res.UserID, res.UserKey); err != nil {
			zlibraryError(c, zlibrary.NewZError(zlibrary.CodeUpstream, "保存凭据失败", err), nil)
			return
		}
		profile = res.Profile
	case req.RemixUserid != "" && req.RemixUserkey != "":
		client.SetAuth(req.RemixUserid, req.RemixUserkey)
		p, err := client.Profile(c.Request.Context())
		if err != nil {
			zmarkAuthFailed(err)
			zlibraryError(c, zlibrary.NewZError(zlibrary.CodeAuthFailed, "令牌无效或已失效", err), nil)
			return
		}
		if err := s.SaveCredentialsToken(req.RemixUserid, req.RemixUserkey); err != nil {
			zlibraryError(c, zlibrary.NewZError(zlibrary.CodeUpstream, "保存凭据失败", err), nil)
			return
		}
		profile = p
	default:
		if !s.Connected() {
			zlibraryError(c, zlibrary.NewZError(zlibrary.CodeAuthRequired, "尚未配置 Z-Library 凭据", nil), nil)
			return
		}
		p, err := s.RefreshQuota(c.Request.Context())
		if err != nil {
			zmarkAuthFailed(err)
			zlibraryError(c, err, nil)
			return
		}
		profile = p
	}

	resp := gin.H{"connected": true, "accountName": profile.Name, "accountType": profile.AccountType}
	quota := gin.H{"used": profile.DailyUsed, "total": profile.DailyQuota}
	resp["quota"] = quota
	// 立即写入配额缓存，保证首次入库预检有数据（FR-6.3）
	if profile.DailyQuota > 0 {
		_ = s.SetQuota(zlibservice.QuotaSnapshot{Used: profile.DailyUsed, Total: profile.DailyQuota})
	}
	c.JSON(http.StatusOK, resp)
}

// ============================================================
// (4) DELETE /api/zlibrary/auth
// ============================================================

// ZlibraryDisconnectAuth 解绑（清除 credentials；任务不清理）。
func ZlibraryDisconnectAuth(c *gin.Context) {
	if err := zlibservice.GetSettings().ClearCredentials(); err != nil {
		zlibraryError(c, zlibrary.NewZError(zlibrary.CodeUpstream, "解绑失败", err), nil)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// ============================================================
// (5) POST /api/zlibrary/search
// ============================================================

// ZlibraryBookView 搜索/详情条目（ZBook + 去重标注）。
type ZlibraryBookView struct {
	zlibrary.ZBook
	DedupStatus string `json:"dedupStatus"`
	LocalBookID string `json:"localBookId,omitempty"`
}

// ZlibrarySearch 代理搜索 + 去重标注（FR-3.2~3.4 / FR-7.3）。
func ZlibrarySearch(c *gin.Context) {
	var req struct {
		Message    string   `json:"message"`
		Languages  []string `json:"languages"`
		Extensions []string `json:"extensions"`
		YearFrom   *int     `json:"yearFrom"`
		YearTo     *int     `json:"yearTo"`
		Order      string   `json:"order"`
		Page       *int     `json:"page"`
		Limit      *int     `json:"limit"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数非法"})
		return
	}
	message := strings.TrimSpace(req.Message)
	if message == "" || len([]rune(message)) > 256 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "搜索关键词必填且不超过 256 字"})
		return
	}
	switch req.Order {
	case "", zlibrary.OrderBestMatch, zlibrary.OrderNewest, zlibrary.OrderRating:
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "排序参数非法"})
		return
	}
	// s-library: 搜索前置校验——未连接时快速失败（与下载端点语义一致），避免空跑域名故障转移 30s
	s := zlibservice.GetSettings()
	if !s.Connected() {
		zlibraryError(c, zlibrary.NewZError(zlibrary.CodeAuthRequired, "Z-Library 未连接，请先在设置中登录", nil), nil)
		return
	}
	client := s.Client()
	res, err := client.Search(c.Request.Context(), zlibrary.SearchQuery{
		Message:    message,
		Languages:  req.Languages,
		Extensions: req.Extensions,
		YearFrom:   req.YearFrom,
		YearTo:     req.YearTo,
		Order:      req.Order,
		Page:       derefInt(req.Page, 1),
		Limit:      clampInt(derefInt(req.Limit, 20), 1, 100),
	})
	if err != nil {
		zmarkAuthFailed(err)
		zlibraryError(c, err, nil)
		return
	}

	views := make([]ZlibraryBookView, 0, len(res.Books))
	infos, aerr := zlibservice.AnnotateBooks(res.Books)
	for i := range res.Books {
		view := ZlibraryBookView{ZBook: res.Books[i]}
		if aerr == nil && i < len(infos) {
			view.DedupStatus = infos[i].Status
			view.LocalBookID = infos[i].LocalBookID
		} else {
			view.DedupStatus = zlibservice.DedupNone
		}
		views = append(views, view)
	}
	c.JSON(http.StatusOK, gin.H{"total": res.Total, "page": res.Page, "limit": res.Limit, "books": views})
}

// ============================================================
// (6) GET /api/zlibrary/book/:id/:hash
// ============================================================

// ZlibraryBookDetail 详情 + 格式列表 + 已入库标注（FR-3.6）。
func ZlibraryBookDetail(c *gin.Context) {
	id, hash := c.Param("id"), c.Param("hash")
	if id == "" || hash == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "书籍标识缺失"})
		return
	}
	s := zlibservice.GetSettings()
	client := s.Client()
	book, err := client.Book(c.Request.Context(), id, hash)
	if err != nil {
		zmarkAuthFailed(err)
		zlibraryError(c, err, nil)
		return
	}
	// 个人下载域名缓存（FR-2.4）
	if book.DlDomain != "" {
		_ = s.SavePersonalDomain(book.DlDomain)
	}
	if formats, ferr := client.Formats(c.Request.Context(), id, hash); ferr == nil {
		book.Formats = formats
	}
	view := ZlibraryBookView{ZBook: *book, DedupStatus: zlibservice.DedupNone}
	if info, derr := zlibservice.CheckDedup(book.ID, book.Hash); derr == nil {
		view.DedupStatus = info.Status
		view.LocalBookID = info.LocalBookID
	}
	c.JSON(http.StatusOK, gin.H{"book": view})
}

// ============================================================
// (7) POST /api/zlibrary/downloads
// ============================================================

// zresolveLibrary 解析目标书库：显式 > 默认书库 > 扩展名记忆 > 按类型第一个可用。
func zresolveLibrary(requested, remembered, ext string) (*model.Library, error) {
	if requested != "" {
		lib, err := store.GetLibraryByID(requested)
		if err != nil {
			return nil, err
		}
		if lib == nil {
			return nil, zlibrary.NewZError(zlibrary.CodeLibNotFound, "目标书库不存在", nil)
		}
		return lib, nil
	}
	if remembered != "" {
		if lib, err := store.GetLibraryByID(remembered); err == nil && lib != nil && lib.Enabled {
			// s-library: 记忆/默认书库必须与 ext 兼容——不兼容时忽略记忆，
			// 继续按类型找兼容书库（此前 EPUB 会被坏记忆钉死在漫画书库 → Z_EXT_MISMATCH）
			if ext == "" || zallowedTypeForExt(lib.Type, ext) {
				return lib, nil
			}
		}
	}
	// 按扩展名类型匹配的第一个 enabled 书库（FR-4.1）
	libs, err := store.GetAllLibraries()
	if err != nil {
		return nil, err
	}
	for _, lib := range libs {
		if !lib.Enabled {
			continue
		}
		if ext == "" {
			return &lib, nil
		}
		if zallowedTypeForExt(lib.Type, ext) {
			return &lib, nil
		}
	}
	return nil, zlibrary.NewZError(zlibrary.CodeLibNotFound, "没有可用的目标书库，请先创建书库", nil)
}

func zallowedTypeForExt(libType, ext string) bool {
	dot := "." + strings.TrimPrefix(strings.ToLower(ext), ".")
	switch libType {
	case "novel":
		return zcontainsExt(config.NovelExtensions, dot)
	case "mixed":
		return zcontainsExt(config.NovelExtensions, dot) || zcontainsExt(config.SupportedExtensions, dot)
	default:
		return zcontainsExt(config.SupportedExtensions, dot)
	}
}

func zcontainsExt(list []string, ext string) bool {
	for _, e := range list {
		if e == ext {
			return true
		}
	}
	return false
}

// ZlibraryCreateDownload 创建下载任务（前置校验 <1s 同步返回）。
func ZlibraryCreateDownload(c *gin.Context) {
	user, ok := zcurrentUser(c)
	if !ok {
		return
	}
	var req struct {
		ZbookID   string  `json:"zbookId"`
		Hash      string  `json:"hash"`
		Ext       *string `json:"ext"`
		LibraryID *string `json:"libraryId"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.ZbookID == "" || req.Hash == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "zbookId 与 hash 必填"})
		return
	}
	s := zlibservice.GetSettings()
	ext := ""
	if req.Ext != nil {
		ext = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(*req.Ext)), ".")
	}

	// ── 前置校验（架构 §5 步骤 1~5，顺序执行）──
	// 1. 登录态
	if !s.Connected() {
		zlibraryError(c, zlibrary.NewZError(zlibrary.CodeAuthRequired,
			"Z-Library 未连接，请先在设置中登录", nil), nil)
		return
	}
	// 2. 配额本地预检
	if err := s.QuotaPrecheck(); err != nil {
		zlibraryError(c, err, nil)
		return
	}
	// 3. 目标书库解析（显式 > 默认 > 记忆 > 类型匹配）
	behavior := s.Behavior()
	remembered := behavior.DefaultLibraryID
	if remembered == "" {
		remembered = behavior.LastLibraryByExt[ext]
	}
	lib, err := zresolveLibrary(strings.TrimSpace(derefStr(req.LibraryID, "")), remembered, ext)
	if err != nil {
		zlibraryError(c, err, nil)
		return
	}
	if !lib.Enabled {
		zlibraryError(c, zlibrary.NewZError(zlibrary.CodeLibNotFound, "目标书库已停用", nil), nil)
		return
	}
	// 4. 目标书库 canManage（c 档语义：入库仍要求 canManage，PM §2 US-M1-08）
	if user.Role != "admin" {
		if canManage, perr := store.UserCanManageLibrary(user.ID, lib.ID); perr != nil || !canManage {
			zlibraryError(c, zlibrary.NewZError(zlibrary.CodeForbidden,
				"没有该目标书库的管理权限", nil), nil)
			return
		}
	}
	// 5. 扩展名与书库类型匹配（ext 未指定时由流水线在 fetch_info 后兜底校验）
	if ext != "" && !zallowedTypeForExt(lib.Type, ext) {
		zlibraryError(c, zlibrary.NewZError(zlibrary.CodeExtMismatch,
			"."+ext+" 无法加入"+zlibraryTypeLabel(lib.Type), nil), nil)
		return
	}
	// 6. 去重（id+hash）
	dedup, err := zlibservice.CheckDedup(req.ZbookID, req.Hash)
	if err != nil {
		zlibraryError(c, zlibrary.NewZError(zlibrary.CodeUpstream, "去重检查失败", err), nil)
		return
	}
	switch dedup.Status {
	case zlibservice.DedupImported:
		zlibraryError(c, zlibrary.NewZError(zlibrary.CodeDupImported, "该书已入库", nil),
			gin.H{"localBookId": dedup.LocalBookID})
		return
	case zlibservice.DedupQueued:
		zlibraryError(c, zlibrary.NewZError(zlibrary.CodeDupQueued, "该书已在下载队列中", nil), nil)
		return
	}

	// 创建任务 → 立即 202
	task := &model.ZlibraryDownloadTask{
		UserID:    user.ID,
		ZbookID:   req.ZbookID,
		ZbookHash: req.Hash,
		Ext:       ext,
		LibraryID: lib.ID,
		Status:    model.ZlibTaskStatusQueued,
		Stage:     model.ZlibStageFetchInfo,
		MetaJSON:  "{}",
	}
	if err := zlibservice.DefaultQueue().Enqueue(task); err != nil {
		zlibraryError(c, zlibrary.NewZError(zlibrary.CodeUpstream, "创建任务失败", err), nil)
		return
	}
	// 记忆书库选择（FR-4.1）
	s.RememberLibrary(ext, lib.ID)
	c.JSON(http.StatusAccepted, gin.H{"task": task})
}

func zlibraryTypeLabel(t string) string {
	switch t {
	case "novel":
		return "小说书库"
	case "mixed":
		return "混合书库"
	default:
		return "漫画书库"
	}
}

// ============================================================
// (8) GET /api/zlibrary/downloads
// ============================================================

// ZlibraryListDownloads 任务列表（admin 见全部 + user 字段；普通用户仅自己）。
func ZlibraryListDownloads(c *gin.Context) {
	user, ok := zcurrentUser(c)
	if !ok {
		return
	}
	opts := store.ZlibraryTaskListOptions{
		Status: c.Query("status"),
		Page:   atoiOr(c.Query("page"), 1),
		Limit:  clampInt(atoiOr(c.Query("limit"), 50), 1, 100),
	}
	if user.Role != "admin" {
		opts.UserID = user.ID // WHERE 注入而非过滤（契约 (8)）
	}
	tasks, total, err := store.ListZlibraryDownloadTasks(opts)
	if err != nil {
		zlibraryError(c, zlibrary.NewZError(zlibrary.CodeUpstream, "读取任务列表失败", err), nil)
		return
	}
	// admin 视图填充用户名
	if user.Role == "admin" {
		nameCache := map[string]string{}
		for _, t := range tasks {
			if t.UserID == "" {
				continue
			}
			name, has := nameCache[t.UserID]
			if !has {
				if u, err := store.GetUserByID(t.UserID); err == nil && u != nil {
					name = u.Username
				}
				nameCache[t.UserID] = name
			}
			t.Username = name
		}
	}
	c.JSON(http.StatusOK, gin.H{"tasks": tasks, "total": total})
}

// ============================================================
// (9)(10) 任务重试 / 取消删除
// ============================================================

// zloadOwnedTask 加载任务并校验发起人或 admin。
func zloadOwnedTask(c *gin.Context) (*model.ZlibraryDownloadTask, bool) {
	user, ok := zcurrentUser(c)
	if !ok {
		return nil, false
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "任务 ID 非法"})
		return nil, false
	}
	task, err := store.GetZlibraryDownloadTask(id)
	if err != nil {
		zlibraryError(c, zlibrary.NewZError(zlibrary.CodeUpstream, "读取任务失败", err), nil)
		return nil, false
	}
	if task == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "任务不存在", "code": zlibrary.CodeNotFound})
		return nil, false
	}
	if user.Role != "admin" && task.UserID != user.ID {
		zlibraryError(c, zlibrary.NewZError(zlibrary.CodeForbidden, "只能操作自己的任务", nil), nil)
		return nil, false
	}
	return task, true
}

// ZlibraryRetryDownload 重试（failed 才可重试；重新前置校验；retryCount+1）。
func ZlibraryRetryDownload(c *gin.Context) {
	task, ok := zloadOwnedTask(c)
	if !ok {
		return
	}
	if task.Status != model.ZlibTaskStatusFailed {
		// DEF-4（QA-REPORT.md）：契约 §4(9) 约定非 failed 重试 → 409，非 500
		zlibraryError(c, zlibrary.NewZError(zlibrary.CodeInvalidState,
			"仅失败的任务可以重试", nil), nil)
		return
	}
	s := zlibservice.GetSettings()
	// 重试前重新执行创建时的前置校验（FR-4.4）
	if err := s.QuotaPrecheck(); err != nil {
		zlibraryError(c, err, nil)
		return
	}
	lib, err := store.GetLibraryByID(task.LibraryID)
	if err != nil || lib == nil || !lib.Enabled {
		zlibraryError(c, zlibrary.NewZError(zlibrary.CodeLibNotFound,
			"目标书库不存在或已停用，请重新选择书库", nil), nil)
		return
	}
	if task.Ext != "" && !zallowedTypeForExt(lib.Type, task.Ext) {
		zlibraryError(c, zlibrary.NewZError(zlibrary.CodeExtMismatch,
			"."+task.Ext+" 无法加入"+zlibraryTypeLabel(lib.Type), nil), nil)
		return
	}
	dedup, err := zlibservice.CheckDedup(task.ZbookID, task.ZbookHash)
	if err == nil && dedup.Status == zlibservice.DedupImported {
		zlibraryError(c, zlibrary.NewZError(zlibrary.CodeDupImported, "该书已入库", nil),
			gin.H{"localBookId": dedup.LocalBookID})
		return
	}
	okRetry, err := store.RetryZlibraryTask(task.ID)
	if err != nil || !okRetry {
		zlibraryError(c, zlibrary.NewZError(zlibrary.CodeUpstream, "重试失败", err), nil)
		return
	}
	zlibservice.DefaultQueue().Wake()
	fresh, _ := store.GetZlibraryDownloadTask(task.ID)
	c.JSON(http.StatusAccepted, gin.H{"task": fresh})
}

// ZlibraryDeleteDownload 取消/删除：queued 直删、downloading 取消、终态删记录。
func ZlibraryDeleteDownload(c *gin.Context) {
	task, ok := zloadOwnedTask(c)
	if !ok {
		return
	}
	switch task.Status {
	case model.ZlibTaskStatusQueued:
		if err := store.DeleteZlibraryTask(task.ID); err != nil {
			zlibraryError(c, zlibrary.NewZError(zlibrary.CodeUpstream, "删除任务失败", err), nil)
			return
		}
	case model.ZlibTaskStatusDownloading:
		zlibservice.DefaultQueue().Cancel(task.ID)
	default:
		// 终态任务删除记录（200 条内自由清理）
		if err := store.DeleteZlibraryTask(task.ID); err != nil {
			zlibraryError(c, zlibrary.NewZError(zlibrary.CodeUpstream, "删除任务失败", err), nil)
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// ============================================================
// (11) POST /api/zlibrary/domains/health-check
// ============================================================

// ZlibraryHealthCheck 手动探测全部启用域名（admin）。
func ZlibraryHealthCheck(c *gin.Context) {
	client := zlibservice.GetSettings().Client()
	if client == nil {
		zlibraryError(c, zlibrary.NewZError(zlibrary.CodeDomainUnreachable, "客户端未就绪", nil), nil)
		return
	}
	results := client.CheckDomains(c.Request.Context(), 10*time.Second)
	c.JSON(http.StatusOK, gin.H{"results": results})
}

// ============================================================
// 小工具
// ============================================================

func derefInt(p *int, def int) int {
	if p == nil || *p <= 0 {
		return def
	}
	return *p
}

func derefStr(p *string, def string) string {
	if p == nil {
		return def
	}
	return *p
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func atoiOr(s string, def int) int {
	if s == "" {
		return def
	}
	if v, err := strconv.Atoi(s); err == nil && v > 0 {
		return v
	}
	return def
}

// zCoverHostSuffixes s-library: 封面代理允许的域名后缀（防 SSRF）。
// 实测封面 CDN 为 s3proxy-*.cdn-zlib.sk，占位图在各镜像域名的 /img/ 下。
var zCoverHostSuffixes = []string{
	"zlib.sk", "cdn-zlib.sk", "z-lib.by", "z-lib.sk", "z-lib.fm", "zlibrary.sk", "singlelogin.re", "singlelogin.me",
}

// ZlibraryCoverProxy 封面图片代理：GET /api/zlibrary/cover?u=<https URL>。
// 上游流量仅由服务端发起（需求 §8），规避浏览器侧 CSP 与可达性问题；
// 域名白名单校验防 SSRF，响应带 7 天缓存头。
func ZlibraryCoverProxy(c *gin.Context) {
	raw := c.Query("u")
	if raw == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "缺少 u 参数"})
		return
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "封面地址非法"})
		return
	}
	host := strings.ToLower(u.Hostname())
	allowed := false
	for _, suf := range zCoverHostSuffixes {
		if host == suf || strings.HasSuffix(host, "."+suf) {
			allowed = true
			break
		}
	}
	if !allowed {
		c.JSON(http.StatusBadRequest, gin.H{"error": "封面域名不在允许列表"})
		return
	}
	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodGet, raw, nil)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "构造请求失败"})
		return
	}
	req.Header.Set("User-Agent", zlibrary.DefaultUserAgent)
	// DefaultClient→DefaultTransport 自动走 HTTPS_PROXY 出站
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "封面获取失败"})
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		c.JSON(http.StatusBadGateway, gin.H{"error": "封面上游状态异常"})
		return
	}
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		c.Header("Content-Type", ct)
	}
	c.Header("Cache-Control", "public, max-age=604800")
	c.Status(http.StatusOK)
	_, _ = io.Copy(c.Writer, io.LimitReader(resp.Body, 3<<20))
}
