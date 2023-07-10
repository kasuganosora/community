package main

import (
	"flag"
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/migrate"
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

	app := gin.Default()

	// Set router
	community.SetRouter(app)

	log.Info("Server is running on %s", config.C.Listen)
	log.FatalIf(app.Run(config.C.Listen))
}
