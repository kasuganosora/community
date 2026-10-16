package setup

import (
	"html"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/log"
)

const maintenancePage = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<meta name="robots" content="noindex"><title>Maintenance</title>
<style>body{font-family:system-ui,-apple-system,"Segoe UI",sans-serif;background:#f6f7f9;color:#1f2329;display:grid;place-items:center;min-height:100vh;margin:0}
main{max-width:34rem;padding:2rem;background:#fff;border-radius:16px;box-shadow:0 2px 16px rgba(0,0,0,.06)}h1{font-size:1.4rem;margin:0 0 .75rem}p{line-height:1.6;margin:.4rem 0;color:#4e5969}code{font-size:.85em}
@media (prefers-color-scheme:dark){body{background:#17181a;color:#e5e6eb}main{background:#232427}p{color:#a9aeb8}}</style></head>
<body><main><h1>Down for maintenance</h1>
<p>This server is temporarily unavailable. Please try again in a few minutes.</p>
<p lang="zh-CN">服务器正在维护，请稍后再试。</p><p lang="zh-TW">伺服器正在維護，請稍後再試。</p><p lang="ja">メンテナンス中です。しばらくしてから再度お試しください。</p>
<p lang="ru">Сервер на обслуживании. Повторите попытку позже.</p><p lang="fr">Serveur en maintenance. Réessayez dans quelques minutes.</p><p lang="de">Wartungsarbeiten. Bitte versuchen Sie es in einigen Minuten erneut.</p>
<p><code>%REASON%</code></p></main></body></html>`

// MaintenanceHandler answers every request with 503 and a maintenance page
// (JSON for API clients). It never offers setup.
func MaintenanceHandler(reason string) *gin.Engine {
	app := gin.New()
	app.Use(gin.Recovery())
	page := strings.Replace(maintenancePage, "%REASON%", html.EscapeString(reason), 1)
	app.GET("/live", func(c *gin.Context) { c.JSON(200, gin.H{"status": "maintenance"}) })
	app.NoRoute(func(c *gin.Context) {
		c.Header("Retry-After", "60")
		c.Header("Cache-Control", "no-store")
		accept := c.GetHeader("Accept")
		if strings.HasPrefix(c.Request.URL.Path, "/api/") || strings.Contains(accept, "json") {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Service temporarily unavailable (maintenance)", "error_code": reason})
			return
		}
		c.Data(http.StatusServiceUnavailable, "text/html; charset=utf-8", []byte(page))
	})
	return app
}

// RunMaintenance serves the maintenance page. When recover is not nil it is
// polled every interval; once it returns true the process restarts into
// normal mode (e.g. the database came back).
func RunMaintenance(listen, reason string, recover func() bool, interval time.Duration) error {
	gin.SetMode(gin.ReleaseMode)
	log.Warn("Maintenance mode (%s): serving 503 on %s", reason, listen)
	if recover != nil {
		go func() {
			for {
				time.Sleep(interval)
				if recover() {
					log.Info("Dependency recovered; restarting")
					RestartProcess()
				}
			}
		}()
	}
	srv := &http.Server{Addr: listen, Handler: MaintenanceHandler(reason), ReadHeaderTimeout: 10 * time.Second}
	return srv.ListenAndServe()
}
