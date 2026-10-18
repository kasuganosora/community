package commands

import (
	"fmt"
	"strings"

	"github.com/kasuganosora/community/datatransfer"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/twofactor"
	"github.com/urfave/cli/v2"
	"gorm.io/gorm"
)

// TwoFactorCommand implements tootctl two-factor (status / reset).
func TwoFactorCommand() *cli.Command {
	userFlags := []cli.Flag{
		&cli.StringFlag{Name: "username", Usage: "local username (or first argument)"},
		&cli.StringFlag{Name: "email", Usage: "account e-mail"},
	}

	return &cli.Command{
		Name:  "two-factor",
		Usage: "Inspect or reset TOTP two-factor authentication of local accounts",
		Subcommands: []*cli.Command{
			{
				Name:  "scrub-preserved",
				Usage: "Redact plaintext TOTP secrets/backup codes from data-transfer preserved raw records (imports made before 2FA support)",
				Flags: []cli.Flag{&cli.BoolFlag{Name: "dry-run", Usage: "only count affected records"}},
				Action: func(c *cli.Context) error {
					n, err := scrubPreservedSecrets(db.GetDB().WithContext(c.Context), c.Bool("dry-run"))
					if err != nil {
						return err
					}
					verb := "redacted"
					if c.Bool("dry-run") {
						verb = "would redact"
					}
					_, _ = fmt.Fprintf(c.App.Writer, "%s %d preserved record(s)\n", verb, n)

					return nil
				},
			},
			{
				Name:      "status",
				Usage:     "Show whether two-factor authentication is enabled",
				ArgsUsage: "[username]",
				Flags:     userFlags,
				Action: func(c *cli.Context) error {
					account, user, err := twoFactorTarget(c)
					if err != nil {
						return cli.Exit(err.Error(), 1)
					}

					row, err := twofactor.Load(db.GetDB(), user.Id)
					if err != nil {
						return err
					}

					if !row.Enabled() {
						_, _ = fmt.Fprintf(c.App.Writer, "%s: two-factor disabled\n", account.Username)

						return nil
					}

					n, _ := twofactor.RemainingBackupCodes(db.GetDB(), user.Id)
					_, _ = fmt.Fprintf(c.App.Writer, "%s: two-factor enabled since %s, %d backup codes left\n",
						account.Username, row.EnabledAt.Format("2006-01-02 15:04:05 MST"), n)

					return nil
				},
			},
			{
				Name:      "reset",
				Usage:     "Disable two-factor authentication and delete backup codes (account recovery)",
				ArgsUsage: "[username]",
				Flags:     userFlags,
				Action: func(c *cli.Context) error {
					account, user, err := twoFactorTarget(c)
					if err != nil {
						return cli.Exit(err.Error(), 1)
					}

					if err = twofactor.AdminReset(db.GetDB(), user.Id); err != nil {
						return err
					}

					_, _ = fmt.Fprintf(c.App.Writer, "two-factor authentication reset for %s\n", account.Username)

					return nil
				},
			},
		},
	}
}

func twoFactorTarget(c *cli.Context) (*models.Account, *models.User, error) {
	username := strings.TrimSpace(c.String("username"))
	if username == "" {
		username = strings.TrimSpace(c.Args().First())
	}

	return findLocalAccount(db.GetDB(), strings.TrimPrefix(username, "@"), strings.TrimSpace(c.String("email")))
}

// scrubPreservedSecrets walks preserved raw records in id order and redacts
// second-factor secret fields. Values are never printed.
func scrubPreservedSecrets(conn *gorm.DB, dryRun bool) (int, error) {
	if !conn.Migrator().HasTable(&datatransfer.StoredPreservedRecord{}) {
		return 0, nil
	}
	changed := 0
	var last uint64
	for {
		var rows []datatransfer.StoredPreservedRecord
		if err := conn.Where("id > ?", last).Order("id").Limit(500).Find(&rows).Error; err != nil {
			return changed, err
		}
		if len(rows) == 0 {
			return changed, nil
		}
		for _, r := range rows {
			last = r.ID
			redacted, ok := datatransfer.RedactSecretFields(r.Data)
			if !ok {
				continue
			}
			changed++
			if dryRun {
				continue
			}
			if err := conn.Model(&datatransfer.StoredPreservedRecord{}).Where("id = ?", r.ID).Update("data", []byte(redacted)).Error; err != nil {
				return changed, err
			}
		}
	}
}
