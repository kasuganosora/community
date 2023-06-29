package main

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/migrate"
)

func main() {

	// 执行迁移文明
	log.FatalIf(migrate.Up(db.GetDB()))
	app := gin.Default()

	// Set router
	community.SetRouter(app)

	log.Info("Server is running on %s", config.C.Listen)
	log.FatalIf(app.Run(config.C.Listen))
}
