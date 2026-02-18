package main

import (
	"flag"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/event"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/matrix"
	"github.com/kasuganosora/community/matrix/homeserver"
	"github.com/kasuganosora/community/middleware"
	"github.com/kasuganosora/community/migrate"
	_ "github.com/kasuganosora/community/notifications"
)

func main() {
	flag.Parse()
	var err error

	if config.C.DevMode == "true" {
		log.SetLevel("debug")
	}

	// 如果有 --rollback 参数，则回滚
	if flag.Arg(0) == "--rollback" {
		err = migrate.Down(db.GetDB())
		log.FatalIf(err)
		return
	}

	// 迁移数据库
	err = migrate.Up(db.GetDB())
	log.FatalIf(err)

	event.GetEventManager("status")

	// 初始化 Matrix Bridge（如果已配置启用）
	if err := matrix.Init(); err != nil {
		log.Warn("Matrix Bridge initialization failed: %v", err)
		// 不阻止应用启动，仅记录警告
	}

	// 初始化 Matrix Homeserver（完整 Matrix 服务器实现）
	if err := homeserver.InitHomeServer(); err != nil {
		log.Warn("Matrix Homeserver initialization failed: %v", err)
		// 不阻止应用启动，仅记录警告
	}

	app := gin.New()
	app.Use(gin.Recovery())
	app.Use(middleware.HandleErrorsMiddleware())
	// Set router
	community.SetRouter(app)

	log.Info("Server is running on %s", config.C.Listen)
	log.FatalIf(app.Run(config.C.Listen))
}
