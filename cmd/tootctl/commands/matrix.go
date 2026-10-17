package commands

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/urfave/cli/v2"
	"gorm.io/gorm"
)

// legacyMatrixDefaultPassword is the fixed password older builds assigned to
// Matrix registrations that omitted a password. It is only used to detect
// affected accounts; it is never assigned any more.
const legacyMatrixDefaultPassword = "complement_default_password"

// MatrixCommand groups Matrix homeserver maintenance subcommands.
func MatrixCommand() *cli.Command {
	return &cli.Command{
		Name:  "matrix",
		Usage: "Matrix homeserver maintenance",
		Subcommands: []*cli.Command{
			{
				Name:  "invalidate-default-passwords",
				Usage: "Invalidate passwords of accounts still using the legacy Matrix default password",
				Description: `Checks every account's stored password against the legacy default password
that empty-password Matrix registrations used to receive. Matching accounts
get an unusable password hash (the owner must reset it) and all of their
Matrix access tokens/devices are revoked. Only the count and user ids are
printed. By default only Matrix-registered accounts are checked; use
--all-users to check every account (slower: one bcrypt verify per row).`,
				Flags: []cli.Flag{
					&cli.BoolFlag{Name: "dry-run", Usage: "only report affected accounts"},
					&cli.BoolFlag{Name: "all-users", Usage: "check all accounts, not only Matrix-registered ones"},
				},
				Action: func(c *cli.Context) error {
					return invalidateDefaultMatrixPasswords(c, db.GetDB().WithContext(c.Context), c.Bool("dry-run"), c.Bool("all-users"))
				},
			},
		},
	}
}

type defaultPasswordCandidate struct {
	UserId         uint64
	UserName       string
	Salt           string
	Password       string
	PasswordScheme string
}

func invalidateDefaultMatrixPasswords(c *cli.Context, conn *gorm.DB, dryRun, allUsers bool) error {
	out := c.App.Writer

	q := conn.Table("user_secret_data AS s").
		Select("s.user_id, u.username AS user_name, s.salt, s.password, s.password_scheme").
		Joins("JOIN users AS u ON u.id = s.user_id")
	if !allUsers {
		q = q.Where(`u.email_hash LIKE ? ESCAPE '!'`, "matrix!_%")
	}

	var rows []defaultPasswordCandidate
	if err := q.Order("s.user_id").Scan(&rows).Error; err != nil {
		return cli.Exit(fmt.Sprintf("query failed: %v", err), 1)
	}

	var affected []defaultPasswordCandidate
	for _, r := range rows {
		secret := models.UserSecretData{UserId: r.UserId, Salt: r.Salt, Password: r.Password, PasswordScheme: r.PasswordScheme}
		if secret.CheckPassword(legacyMatrixDefaultPassword) == nil {
			affected = append(affected, r)
		}
	}

	for _, r := range affected {
		if !dryRun {
			if err := invalidateAccountPassword(conn, r); err != nil {
				return cli.Exit(fmt.Sprintf("failed to invalidate user %d: %v", r.UserId, err), 1)
			}
		}
		fmt.Fprintf(out, "user_id=%d username=%s\n", r.UserId, r.UserName)
	}

	verb := "invalidated"
	if dryRun {
		verb = "would invalidate (dry run)"
	}
	fmt.Fprintf(out, "checked %d accounts, %s %d\n", len(rows), verb, len(affected))

	return nil
}

func invalidateAccountPassword(conn *gorm.DB, r defaultPasswordCandidate) error {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return err
	}
	// Not valid base64 and lacks the v2 prefix, so no password can verify.
	unusable := "!invalidated!" + hex.EncodeToString(buf)

	return conn.Transaction(func(tx *gorm.DB) error {
		if err := tx.Table("user_secret_data").Where("user_id = ?", r.UserId).
			Updates(map[string]any{"password": unusable, "password_scheme": ""}).Error; err != nil {
			return err
		}
		// Matrix sessions double as devices; drop them plus pending to-device batches.
		var mxids []string
		if err := tx.Table("matrix_user_sessions").Where(`local_account_id IN (?) OR user_id LIKE ? ESCAPE '!'`,
			tx.Table("accounts").Select("id").Where("user_id = ?", r.UserId), "@"+likeEscape(r.UserName)+":%").
			Distinct().Pluck("user_id", &mxids).Error; err != nil {
			return err
		}
		if len(mxids) == 0 {
			return nil
		}
		if tx.Migrator().HasTable("matrix_sync_to_device_batches") {
			if err := tx.Exec("DELETE FROM matrix_sync_to_device_batches WHERE user_id IN ?", mxids).Error; err != nil {
				return err
			}
		}
		return tx.Exec("DELETE FROM matrix_user_sessions WHERE user_id IN ?", mxids).Error
	})
}

func likeEscape(s string) string {
	return strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(s)
}
