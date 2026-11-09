// s-library: Z-Library 路由注册 + 权限档中间件（FR-10，契约 §4）。
// 凭据/域名/设置类端点恒为 admin；其余端点经 ZlibraryAccessRequired 三档判定。
package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/nowen-reader/nowen-reader/internal/middleware"
	zlibrary "github.com/nowen-reader/nowen-reader/internal/zlibrary"
	zlibservice "github.com/nowen-reader/nowen-reader/internal/zlibrary/service"
)

// ZlibraryAccessRequired 权限档判定（FR-10.2）：admin 恒通过；
// canManage 档要求对任意书库有 canManage；all 档仅要求已登录。
func ZlibraryAccessRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		user := middleware.GetCurrentUser(c)
		if user == nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
			return
		}
		if !zlibservice.GetSettings().CanUse(user) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"error": "无权使用 Z-Library 功能",
				"code":  zlibrary.CodeForbidden,
			})
			return
		}
		c.Next()
	}
}

// registerZlibraryRoutes 挂载 /api/zlibrary 路由组（由 router.go.SetupRoutes 调用）。
func registerZlibraryRoutes(api *gin.RouterGroup) {
	g := api.Group("/zlibrary")
	g.Use(middleware.AuthRequired())

	// 状态 / 搜索 / 详情 / 任务：权限档判定（默认仅管理员，决策 D1）
	g.GET("/status", ZlibraryAccessRequired(), ZlibraryStatus)
	g.POST("/search", ZlibraryAccessRequired(), ZlibrarySearch)
	g.GET("/book/:id/:hash", ZlibraryAccessRequired(), ZlibraryBookDetail)
	g.GET("/cover", ZlibraryAccessRequired(), ZlibraryCoverProxy) // s-library: 封面代理（上游流量仅走服务端，需求 §8）

	// 配置类端点：恒为 admin（FR-10.1）
	g.GET("/settings", middleware.AdminRequired(), ZlibraryGetSettings) // s-library: DEF-1 修复——配置回显
	g.PUT("/settings", middleware.AdminRequired(), ZlibrarySaveSettings)
	g.POST("/auth/test", middleware.AdminRequired(), ZlibraryTestAuth)
	g.DELETE("/auth", middleware.AdminRequired(), ZlibraryDisconnectAuth)
	g.POST("/domains/health-check", middleware.AdminRequired(), ZlibraryHealthCheck)

	// 任务中心
	downloads := g.Group("/downloads", ZlibraryAccessRequired())
	{
		downloads.POST("", ZlibraryCreateDownload)
		downloads.GET("", ZlibraryListDownloads)
		downloads.POST("/:id/retry", ZlibraryRetryDownload)
		downloads.DELETE("/:id", ZlibraryDeleteDownload)
	}
}
