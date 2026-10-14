package commands

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/middleware"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/kasuganosora/community/utils"
	"github.com/urfave/cli/v2"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ownerRoleName is the seeded role carrying the Administrator permission bit
// (see migrate/scripts/add_roles.go). --admin grants it together with the
// root administrator flag (users.is_admin).
const ownerRoleName = "owner"

// AccountsCommand implements tootctl accounts subcommands.
func AccountsCommand() *cli.Command {
	passwordFlags := []cli.Flag{
		&cli.BoolFlag{Name: "password-stdin", Usage: "read the password from the first line of stdin"},
		&cli.StringFlag{Name: "password", Usage: "set the password (visible in shell history and ps; prefer --password-stdin)"},
	}

	return &cli.Command{
		Name:  "accounts", //nolint:goconst
		Usage: "Manage accounts",
		Subcommands: []*cli.Command{
			{
				Name:      "create",
				Usage:     "Create a new local account",
				ArgsUsage: "[USERNAME]",
				Description: "Without --password-stdin or --password a random password is generated and printed once.\n" +
					"--admin makes the user a root administrator and assigns the owner role.",
				Flags: append([]cli.Flag{
					&cli.StringFlag{Name: "username", Usage: "username (or pass it as the first argument)"}, //nolint:goconst
					&cli.StringFlag{Name: "email", Required: true},                                          //nolint:goconst
					&cli.BoolFlag{Name: "confirmed", Usage: "mark the e-mail address as confirmed"},
					&cli.BoolFlag{Name: "approve", Usage: "approve the account (accounts created here are always approved; kept for Mastodon compatibility)"},
					&cli.BoolFlag{Name: "admin", Usage: "root administrator: sets the admin flag and assigns the owner role"},
					&cli.StringSliceFlag{Name: "role", Usage: "assign a role by name, e.g. owner, admin, moderator (repeatable)"},
				}, passwordFlags...),
				Action: createAccount,
			},
			{
				Name:      "delete",
				Usage:     "Delete a local account",
				ArgsUsage: "[USERNAME]",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "username", Usage: "username (or pass it as the first argument)"},
					&cli.StringFlag{Name: "email", Usage: "find the account by e-mail instead of username"},
				},
				Action: deleteAccount,
			},
			{
				Name:      "modify",
				Usage:     "Modify a local account",
				ArgsUsage: "[USERNAME]",
				Description: "--reset-password generates a new random password and prints it once; it and the password\n" +
					"flags also revoke the user's existing sessions and tokens.",
				Flags: append([]cli.Flag{
					&cli.StringFlag{Name: "username", Usage: "username (or pass it as the first argument)"},
					&cli.StringFlag{Name: "email", Usage: "change the e-mail address"},
					&cli.BoolFlag{Name: "confirm", Usage: "mark the e-mail address as confirmed"},
					&cli.BoolFlag{Name: "approve", Usage: "approve a pending account"},
					&cli.BoolFlag{Name: "disable", Usage: "disable (ban) the user"},
					&cli.BoolFlag{Name: "enable", Usage: "re-enable the user"},
					&cli.BoolFlag{Name: "admin", Usage: "root administrator: set the admin flag and assign the owner role"},
					&cli.BoolFlag{Name: "unadmin", Usage: "clear the admin flag and remove the owner role"},
					&cli.StringSliceFlag{Name: "role", Usage: "assign a role by name (repeatable)"},
					&cli.BoolFlag{Name: "remove-role", Usage: "remove all roles (applied before --role/--admin)"},
					&cli.BoolFlag{Name: "reset-password", Usage: "generate and print a new random password"},
				}, passwordFlags...),
				Action: modifyAccount,
			},
			accountsRefreshCommand(),
		},
	}
}

func usernameArg(c *cli.Context) (string, error) {
	username := strings.TrimSpace(c.String("username"))
	if username == "" {
		username = strings.TrimSpace(c.Args().First())
	}

	if username == "" {
		return "", errors.New("a username is required (--username NAME or first argument)")
	}

	return username, nil
}

// passwordInput returns the password requested on the command line, or ""
// when none was given.
func passwordInput(c *cli.Context) (string, error) {
	fromStdin := c.Bool("password-stdin")
	inline := c.IsSet("password")

	if fromStdin && inline {
		return "", errors.New("use only one of --password-stdin and --password")
	}

	switch {
	case fromStdin:
		reader := c.App.Reader
		if reader == nil {
			return "", errors.New("stdin is not available")
		}

		line, err := bufio.NewReader(reader).ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return "", fmt.Errorf("read password from stdin: %w", err)
		}

		password := strings.TrimRight(line, "\r\n")
		if password == "" {
			return "", errors.New("no password received on stdin")
		}

		return password, nil
	case inline:
		fmt.Fprintln(c.App.ErrWriter, "Warning: --password is visible in shell history and the process list; prefer --password-stdin.")

		password := c.String("password")
		if password == "" {
			return "", errors.New("--password must not be empty")
		}

		return password, nil
	}

	return "", nil
}

// generatePassword returns a random 32 character hex password (128 bits).
func generatePassword() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate password: %w", err)
	}

	return hex.EncodeToString(buf), nil
}

// resolveRoles maps role names (case-insensitive) to roles, failing on
// unknown names with the list of available ones.
func resolveRoles(conn *gorm.DB, names []string) ([]models.Role, error) {
	var all []models.Role
	if err := conn.Order("id").Find(&all).Error; err != nil {
		return nil, fmt.Errorf("load roles: %w", err)
	}

	byName := make(map[string]models.Role, len(all))
	available := make([]string, 0, len(all))

	for _, role := range all {
		byName[strings.ToLower(role.Name)] = role
		available = append(available, role.Name)
	}

	sort.Strings(available)

	seen := map[int64]bool{}
	out := make([]models.Role, 0, len(names))

	for _, name := range names {
		role, ok := byName[strings.ToLower(strings.TrimSpace(name))]
		if !ok {
			return nil, fmt.Errorf("role %q not found (available: %s)", name, strings.Join(available, ", "))
		}

		if !seen[role.ID] {
			seen[role.ID] = true
			out = append(out, role)
		}
	}

	return out, nil
}

func requestedRoles(c *cli.Context, admin bool) []string {
	names := append([]string{}, c.StringSlice("role")...)
	if admin {
		names = append(names, ownerRoleName)
	}

	return names
}

func assignRoles(tx *gorm.DB, userID uint64, roles []models.Role) error {
	for _, role := range roles {
		err := tx.Clauses(clause.OnConflict{DoNothing: true}).
			Create(&models.RoleUser{RoleID: uint64(role.ID), UserID: userID}).Error //nolint:gosec // role ids are positive
		if err != nil {
			return fmt.Errorf("assign role %s: %w", role.Name, err)
		}
	}

	return nil
}

func roleNames(roles []models.Role) string {
	names := make([]string, 0, len(roles))
	for _, role := range roles {
		names = append(names, role.Name)
	}

	return strings.Join(names, ", ")
}

func emailVerificationRequired() bool {
	return systemsettings.GetBool(systemsettings.KeyRegistrationCheckEmail, false)
}

func createAccount(c *cli.Context) error {
	conn := db.GetDB()
	out := c.App.Writer

	username, err := usernameArg(c)
	if err != nil {
		return err
	}

	email := strings.TrimSpace(c.String("email"))

	if err := models.ValidateUsername(username); err != nil {
		return err
	}

	if err := models.ValidateEmail(email); err != nil {
		return err
	}

	password, err := passwordInput(c)
	if err != nil {
		return err
	}

	generated := password == ""
	if generated {
		if password, err = generatePassword(); err != nil {
			return err
		}
	}

	if err := models.ValidatePassword(password); err != nil {
		return err
	}

	admin := c.Bool("admin")

	roles, err := resolveRoles(conn, requestedRoles(c, admin))
	if err != nil {
		return err
	}

	var taken int64
	if err := conn.Model(&models.User{}).Where("username = ?", username).Count(&taken).Error; err != nil {
		return fmt.Errorf("check username: %w", err)
	}

	if taken == 0 {
		if err := conn.Model(&models.Account{}).Where("username = ? AND domain IS NULL", username).Count(&taken).Error; err != nil {
			return fmt.Errorf("check username: %w", err)
		}
	}

	if taken > 0 {
		return fmt.Errorf("username '%s' already exists", username)
	}

	emailHash := models.GetEmailHash(email)
	if err := conn.Model(&models.User{}).Where("email_hash = ?", emailHash).Count(&taken).Error; err != nil {
		return fmt.Errorf("check email: %w", err)
	}

	if taken > 0 {
		return fmt.Errorf("e-mail '%s' is already in use", email)
	}

	// Same hashing as web registration.
	salt := utils.GetRandomString(64)

	hash, err := models.EncryptPassword(password, salt)
	if err != nil {
		return fmt.Errorf("failed to encrypt password: %w", err)
	}

	user := &models.User{
		UserName:       username,
		EmailHash:      emailHash,
		IsAdmin:        admin,
		IsMailVerified: c.Bool("confirmed"),
		Status:         models.UserStatusNormal,
	}

	err = conn.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(user).Error; err != nil {
			return fmt.Errorf("failed to create user: %w", err)
		}

		secret := &models.UserSecretData{UserId: user.Id, Salt: salt, Password: hash, Email: email}
		if err := tx.Create(secret).Error; err != nil {
			return fmt.Errorf("failed to create user secret: %w", err)
		}

		account := &models.Account{UserId: user.Id, Username: username}
		if err := tx.Create(account).Error; err != nil {
			return fmt.Errorf("failed to create account: %w", err)
		}

		return assignRoles(tx, user.Id, roles)
	})
	if err != nil {
		return err
	}

	fmt.Fprintf(out, "Account created: %s\n", username)

	if admin {
		fmt.Fprintln(out, "Administrator: yes")
	}

	if len(roles) > 0 {
		fmt.Fprintf(out, "Roles: %s\n", roleNames(roles))
	}

	if !user.IsMailVerified && emailVerificationRequired() {
		fmt.Fprintln(c.App.ErrWriter, "Note: e-mail verification is required to log in; re-run with --confirmed or use `accounts modify --confirm`.")
	}

	if generated {
		fmt.Fprintf(out, "New password: %s\n", password)
	}

	return nil
}

func findLocalAccount(conn *gorm.DB, username, email string) (*models.Account, *models.User, error) {
	var user models.User

	switch {
	case username != "":
		var account models.Account
		if err := conn.Where("username = ? AND domain IS NULL", username).First(&account).Error; err != nil {
			return nil, nil, errors.New("account not found")
		}

		if err := conn.First(&user, account.UserId).Error; err != nil {
			return nil, nil, errors.New("user not found")
		}

		return &account, &user, nil
	case email != "":
		if err := conn.Where("email_hash = ?", models.GetEmailHash(email)).First(&user).Error; err != nil {
			return nil, nil, errors.New("account not found")
		}

		var account models.Account
		if err := conn.Where("user_id = ? AND domain IS NULL", user.Id).First(&account).Error; err != nil {
			return nil, nil, errors.New("account not found")
		}

		return &account, &user, nil
	}

	return nil, nil, errors.New("a username (--username or first argument) or --email is required")
}

func deleteAccount(c *cli.Context) error {
	conn := db.GetDB()

	username := strings.TrimSpace(c.String("username"))
	if username == "" {
		username = strings.TrimSpace(c.Args().First())
	}

	account, user, err := findLocalAccount(conn, username, strings.TrimSpace(c.String("email")))
	if err != nil {
		return err
	}

	// Private feedback belongs to this account, while site suppressions are
	// independent of the administrator who created them and must survive.
	if err := conn.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("scope = ? AND owner_id IN (?)", "user", tx.Model(&models.Account{}).Select("id").Where("user_id = ?", user.Id)).Delete(&models.RecommendationFeedback{}).Error; err != nil {
			return err
		}
		return tx.Delete(&models.User{}, user.Id).Error
	}); err != nil {
		return fmt.Errorf("failed to delete user: %w", err)
	}

	fmt.Fprintf(c.App.Writer, "Account deleted: %s\n", account.Username)

	return nil
}

func modifyAccount(c *cli.Context) error {
	conn := db.GetDB()
	out := c.App.Writer

	username, err := usernameArg(c)
	if err != nil {
		return err
	}

	switch {
	case c.Bool("admin") && c.Bool("unadmin"):
		return errors.New("use only one of --admin and --unadmin")
	case c.Bool("disable") && c.Bool("enable"):
		return errors.New("use only one of --disable and --enable")
	case c.Bool("reset-password") && (c.Bool("password-stdin") || c.IsSet("password")):
		return errors.New("--reset-password cannot be combined with --password-stdin or --password")
	}

	account, user, err := findLocalAccount(conn, username, "")
	if err != nil {
		return err
	}

	password, err := passwordInput(c)
	if err != nil {
		return err
	}

	generated := false
	if c.Bool("reset-password") {
		if password, err = generatePassword(); err != nil {
			return err
		}

		generated = true
	}

	if password != "" {
		if err := models.ValidatePassword(password); err != nil {
			return err
		}
	}

	roles, err := resolveRoles(conn, requestedRoles(c, c.Bool("admin")))
	if err != nil {
		return err
	}

	var unadminRoles []models.Role
	if c.Bool("unadmin") {
		if unadminRoles, err = resolveRoles(conn, []string{ownerRoleName}); err != nil {
			return err
		}
	}

	newEmail := strings.TrimSpace(c.String("email"))
	if newEmail != "" {
		if err := models.ValidateEmail(newEmail); err != nil {
			return err
		}

		var taken int64
		if err := conn.Model(&models.User{}).Where("email_hash = ? AND id <> ?", models.GetEmailHash(newEmail), user.Id).Count(&taken).Error; err != nil {
			return fmt.Errorf("check email: %w", err)
		}

		if taken > 0 {
			return fmt.Errorf("e-mail '%s' is already in use", newEmail)
		}
	}

	updates := make(map[string]any)

	if c.Bool("confirm") {
		updates["is_mail_verified"] = true
		if user.Status == models.UserStatusUnverifiedMail {
			updates["status"] = models.UserStatusNormal
		}
	}

	if c.Bool("approve") {
		updates["status"] = models.UserStatusNormal
	}

	if c.Bool("disable") {
		updates["is_ban"] = true
	}

	if c.Bool("enable") {
		updates["is_ban"] = false
	}

	if c.Bool("admin") {
		updates["is_admin"] = true
	}

	if c.Bool("unadmin") {
		updates["is_admin"] = false
	}

	if newEmail != "" {
		updates["email_hash"] = models.GetEmailHash(newEmail)
	}

	var revokedTokens []string

	err = conn.Transaction(func(tx *gorm.DB) error {
		if len(updates) > 0 {
			if err := tx.Model(user).Updates(updates).Error; err != nil {
				return fmt.Errorf("failed to update user: %w", err)
			}
		}

		if newEmail != "" || password != "" {
			var secret models.UserSecretData
			if err := tx.Where("user_id = ?", user.Id).First(&secret).Error; err != nil {
				return fmt.Errorf("failed to load user secret: %w", err)
			}

			// The salt stays: it also keys the encrypted e-mail column.
			secretUpdates := map[string]any{}

			if newEmail != "" {
				encrypted, err := models.EncryptSecretValue(newEmail, secret.Salt)
				if err != nil {
					return fmt.Errorf("failed to encrypt e-mail: %w", err)
				}

				secretUpdates["email"] = encrypted
			}

			if password != "" {
				hash, err := models.EncryptPassword(password, secret.Salt)
				if err != nil {
					return fmt.Errorf("failed to encrypt password: %w", err)
				}

				secretUpdates["password"] = hash
				secretUpdates["password_scheme"] = ""
			}

			// Same column update as the change-password endpoint.
			err := tx.Model(&models.UserSecretData{}).Where("user_id = ?", user.Id).Updates(secretUpdates).Error
			if err != nil {
				return fmt.Errorf("failed to update user secret: %w", err)
			}
		}

		if password != "" {
			if revokedTokens, err = revokeUserTokens(tx, user.Id); err != nil {
				return err
			}
		}

		if c.Bool("remove-role") {
			if err := tx.Where("user_id = ?", user.Id).Delete(&models.RoleUser{}).Error; err != nil {
				return fmt.Errorf("failed to remove roles: %w", err)
			}
		}

		for _, role := range unadminRoles {
			if err := tx.Where("user_id = ? AND role_id = ?", user.Id, role.ID).Delete(&models.RoleUser{}).Error; err != nil {
				return fmt.Errorf("failed to remove role %s: %w", role.Name, err)
			}
		}

		return assignRoles(tx, user.Id, roles)
	})
	if err != nil {
		return err
	}

	for _, token := range revokedTokens {
		middleware.InvalidateTokenCacheHash(token)
	}

	fmt.Fprintf(out, "Account modified: %s\n", account.Username)

	if len(revokedTokens) > 0 {
		fmt.Fprintf(out, "Revoked sessions/tokens: %d\n", len(revokedTokens))
	}

	if len(roles) > 0 {
		fmt.Fprintf(out, "Roles assigned: %s\n", roleNames(roles))
	}

	if generated {
		fmt.Fprintf(out, "New password: %s\n", password)
	}

	return nil
}

// revokeUserTokens revokes every active session and token of the user and
// returns their stored digests so the token cache can be invalidated.
func revokeUserTokens(tx *gorm.DB, userID uint64) ([]string, error) {
	active := clause.Eq{Column: clause.Column{Name: "revoke"}, Value: nil}

	var tokens []string
	if err := tx.Model(&models.AuthToken{}).Where("user_id = ?", userID).Where(active).Pluck("token", &tokens).Error; err != nil {
		return nil, fmt.Errorf("failed to load sessions: %w", err)
	}

	if len(tokens) == 0 {
		return nil, nil
	}

	err := tx.Model(&models.AuthToken{}).Where("user_id = ?", userID).Where(active).Update("revoke", time.Now()).Error
	if err != nil {
		return nil, fmt.Errorf("failed to revoke sessions: %w", err)
	}

	return tokens, nil
}

// NormalizeAccountArgs rewrites the Mastodon form
// "accounts create|modify|delete USERNAME [flags]" to "... --username
// USERNAME [flags]": urfave/cli stops parsing flags at the first positional
// argument, so flags after the username would otherwise be ignored.
func NormalizeAccountArgs(args []string) []string {
	for i, arg := range args {
		if arg == "--" {
			break
		}

		if arg != "accounts" {
			if strings.HasPrefix(arg, "-") || i == 0 {
				continue
			}

			break // another command
		}

		if i+2 >= len(args) {
			break
		}

		switch args[i+1] {
		case "create", "modify", "delete":
		case "refresh":
			return normalizeRefreshArgs(args, i+2)
		default:
			return args
		}

		if name := args[i+2]; name != "" && !strings.HasPrefix(name, "-") {
			out := make([]string, 0, len(args)+1)
			out = append(out, args[:i+2]...)
			out = append(out, "--username", name)

			return append(out, args[i+3:]...)
		}

		break
	}

	return args
}
