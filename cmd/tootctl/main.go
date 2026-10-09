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

	app := newApp(version, func() {
		// 迁移数据库
		if err := migrate.Up(db.GetDB()); err != nil {
			log.Warn("Database migration warning: %v", err)
		}
	})

	// 处理中断信号
	sigChan := make(chan os.Signal, 1)

	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigChan
		fmt.Println("\nOperation cancelled.")
		os.Exit(130)
	}()

	if err := app.Run(commands.NormalizeAccountArgs(os.Args)); err != nil {
		log.Error("%v", err)
		os.Exit(1)
	}
}

// newApp builds the CLI. prepare (database migrations in production) runs
// before any command that may touch the database, but not for help output.
func newApp(version string, prepare func()) *cli.App {
	// urfave/cli registers "--version, -v" on the root command by default,
	// which collided with the "-v" alias of --verbose and made every
	// invocation panic with "flag redefined: v". Keep "-v" for verbose (as
	// the subcommands use it) and leave --version without a short alias.
	cli.VersionFlag = &cli.BoolFlag{
		Name:               "version",
		Usage:              "print the version",
		DisableDefaultText: true,
	}

	return &cli.App{
		Name:    "tootctl",
		Usage:   "Community server command-line interface (Mastodon-compatible)",
		Version: version,
		Commands: []*cli.Command{
			commands.SelfDestructCommand(),
			commands.VersionCommand(version),
			commands.AccountsCommand(),
			commands.CacheCommand(),
			commands.DomainsCommand(),
			commands.EmailDomainBlocksCommand(),
			commands.EmojiCommand(),
			commands.FeedsCommand(),
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

			if prepare != nil && needsDatabase(c.Args().Slice()) {
				prepare()
			}

			return nil
		},
	}
}

// needsDatabase reports whether the command line runs a command (as opposed
// to only printing help), so plain help works without a database.
func needsDatabase(args []string) bool {
	if len(args) == 0 {
		return false
	}

	switch args[0] {
	case "help", "h":
		return false
	}

	for _, arg := range args {
		if arg == "--" {
			break
		}

		if arg == "-h" || arg == "--help" {
			return false
		}
	}

	return true
}
