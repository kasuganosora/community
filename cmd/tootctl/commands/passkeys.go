package commands

import (
	"fmt"

	passkeyapi "github.com/kasuganosora/community/controllers/api/passkey"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/db/dialect"
	"github.com/urfave/cli/v2"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// misskeySource opens the Misskey database; replaced in tests.
var misskeySource = func(dsn string) (*gorm.DB, func(), error) {
	conn, err := gorm.Open(dialect.OpenPostgres(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		return nil, nil, err
	}

	sqlDB, err := conn.DB()
	if err != nil {
		return nil, nil, err
	}
	// The Misskey DB usually lives on the same PostgreSQL server, so its pool
	// is carved out of this process's DB_MAX_OPEN_CONNS budget.
	n, release := database.ReserveAuxConnections("Misskey source database", 2)
	database.ConfigureAuxPool(sqlDB, n)

	return conn, func() {
		_ = sqlDB.Close()
		release()
	}, nil
}

// PasskeysCommand implements tootctl passkeys.
func PasskeysCommand() *cli.Command {
	return &cli.Command{
		Name:  "passkeys",
		Usage: "Manage passkeys (WebAuthn credentials)",
		Subcommands: []*cli.Command{
			{
				Name:  "import-misskey",
				Usage: "Import passkeys of local users from a Misskey PostgreSQL database (matched by username)",
				Description: `Reads Misskey's user_security_key table (local users only) and stores each
key as a Community passkey of the local account with the same username
(case-insensitive). Credentials that already exist are skipped, so it is
safe to run again. Imported passkeys only work when WEBAUTHN_RP_ID equals
the Misskey server's domain (e.g. maid.lat). Key material is never printed.`,
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "dsn", EnvVars: []string{"MISSKEY_DSN"}, Usage: "Misskey PostgreSQL DSN/URL (or $MISSKEY_DSN)"},
					&cli.BoolFlag{Name: "dry-run", Usage: "only report what would be imported"},
					&cli.BoolFlag{Name: "verbose", Aliases: []string{"v"}, Usage: "print one line per key (username and key name only)"},
				},
				Action: func(c *cli.Context) error {
					dsn := c.String("dsn")
					if dsn == "" {
						return cli.Exit("--dsn (or MISSKEY_DSN) is required", 1)
					}

					src, closeSrc, err := misskeySource(dsn)
					if err != nil {
						return cli.Exit("could not connect to the Misskey database", 1)
					}
					defer closeSrc()

					return importMisskeyPasskeys(c, src.WithContext(c.Context), db.GetDB().WithContext(c.Context))
				},
			},
		},
	}
}

func importMisskeyPasskeys(c *cli.Context, src, dst *gorm.DB) error {
	out := c.App.Writer
	keys, err := passkeyapi.LoadMisskeySecurityKeys(src)
	if err != nil {
		return err
	}

	dryRun := c.Bool("dry-run")
	res, err := passkeyapi.ImportMisskeySecurityKeys(dst, keys, dryRun)
	if err != nil {
		return err
	}

	if c.Bool("verbose") {
		for _, line := range res.Details {
			_, _ = fmt.Fprintln(out, line)
		}
	}

	prefix := ""
	if dryRun {
		prefix = "[dry run] "
	}

	_, _ = fmt.Fprintf(out, "%smisskey keys: %d, imported: %d, skipped (already present): %d, unmatched: %d, invalid: %d\n",
		prefix, len(keys), res.Imported, res.Skipped, res.Unmatched, res.Invalid)

	return nil
}
