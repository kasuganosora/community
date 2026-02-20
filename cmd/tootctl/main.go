package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/kasuganosora/community/cmd/tootctl/commands"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/migrate"
	"github.com/urfave/cli/v2"
)

var version = "1.0.0"

func main() {
	// 设置日志级别
	if config.C.DevMode == "true" {
		log.SetLevel("debug")
	}

	// 迁移数据库
	if err := migrate.Up(db.GetDB()); err != nil {
		log.Warn("Database migration warning: %v", err)
	}

	app := &cli.App{
		Name:     "tootctl",
		Usage:    "Community server command-line interface (Mastodon-compatible)",
		Version:  version,
		Commands: []*cli.Command{
			commands.SelfDestructCommand(),
			commands.VersionCommand(version),
			commands.AccountsCommand(),
			commands.CacheCommand(),
			commands.DomainsCommand(),
			commands.MediaCommand(),
			commands.StatusesCommand(),
		},
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:    "verbose",
				Aliases: []string{"v"},
				Usage:   "Enable verbose output",
			},
		},
		Before: func(c *cli.Context) error {
			if c.Bool("verbose") {
				log.SetLevel("debug")
			}
			return nil
		},
	}

	// 处理中断信号
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		fmt.Println("\nOperation cancelled.")
		os.Exit(130)
	}()

	if err := app.Run(os.Args); err != nil {
		log.Error("%v", err)
		os.Exit(1)
	}
}
