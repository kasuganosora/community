package main

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/log"
)

func main() {
	app := gin.Default()

	// Set router
	community.SetRouter(app)

	log.Info("Server is running on %s", config.C.Listen)
	log.ErrorV(app.Run(config.C.Listen))
}
