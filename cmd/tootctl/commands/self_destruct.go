package commands

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kasuganosora/community/activitypub"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/urfave/cli/v2"
	"gorm.io/gorm"
)

const (
	SelfDestructModeKey    = "self_destruct_mode"
	SelfDestructStartedKey = "self_destruct_started_at"
)

// SelfDestructCommand implements tootctl self-destruct
// This command erases the server from the federation by broadcasting
// account Delete activities to all known other servers.
func SelfDestructCommand() *cli.Command {
	return &cli.Command{
		Name:  "self-destruct",
		Usage: "Erase this server from the federation",
		Description: `Erase this server from the federation by broadcasting account Delete 
activities to all known other servers. This allows a "clean exit" from 
running a Mastodon server, as it leaves next to no cache behind on other servers.

This command is always interactive and requires confirmation twice.

IMPORTANT WARNINGS:
- This operation is NOT reversible
- The server will be in a BROKEN STATE after this command finishes
- No local data is actually deleted (emptying the database is faster)
- A running Sidekiq process (or delivery queue) is required

Version history:
2.8.0 - added
4.3.0 - introduced self-destruct mode`,
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "force",
				Usage: "Skip confirmation prompts (dangerous)",
			},
			&cli.IntFlag{
				Name:    "batch-size",
				Aliases: []string{"b"},
				Value:   100,
				Usage:   "Number of accounts to process per batch",
			},
			&cli.IntFlag{
				Name:    "workers",
				Aliases: []string{"w"},
				Value:   5,
				Usage:   "Number of concurrent workers for sending Delete activities",
			},
			&cli.BoolFlag{
				Name:    "continue-mode",
				Aliases: []string{"c"},
				Usage:   "Continue from where previous self-destruct left off",
			},
		},
		Action: runSelfDestruct,
	}
}

// SelfDestructStatus represents the current status of self-destruct operation.
type SelfDestructStatus struct {
	ModeEnabled            bool       `json:"mode_enabled"`
	StartedAt              *time.Time `json:"started_at,omitempty"`
	TotalAccounts          int64      `json:"total_accounts"`
	ProcessedAccounts      int64      `json:"processed_accounts"`
	RemainingAccounts      int64      `json:"remaining_accounts"`
	TotalInboxes           int64      `json:"total_inboxes"`
	DeleteActivitiesQueued int64      `json:"delete_activities_queued"`
	DeliveryStatus         string     `json:"delivery_status"`
}

func runSelfDestruct(c *cli.Context) error {
	ctx := context.Background()

	// Check current self-destruct mode status
	status := getSelfDestructStatus()

	if status.ModeEnabled {
		// Server is already in self-destruct mode
		return handleSelfDestructModeActive(c, ctx, status)
	}

	// First time running self-destruct - show instructions and enable mode
	return initiateSelfDestruct(c, ctx)
}

func handleSelfDestructModeActive(c *cli.Context, ctx context.Context, status SelfDestructStatus) error {
	fmt.Println("\n" + strings.Repeat("=", 60))
	fmt.Println("⚠️  SERVER IS IN SELF-DESTRUCT MODE")
	fmt.Println(strings.Repeat("=", 60))

	if status.StartedAt != nil {
		fmt.Printf("Self-destruct started at: %s\n", status.StartedAt.Format(time.RFC3339))
	}

	fmt.Printf("\nProgress:\n")
	fmt.Printf("  Total local accounts:     %d\n", status.TotalAccounts)
	fmt.Printf("  Processed accounts:       %d\n", status.ProcessedAccounts)
	fmt.Printf("  Remaining accounts:       %d\n", status.RemainingAccounts)
	fmt.Printf("  Known remote inboxes:     %d\n", status.TotalInboxes)
	fmt.Printf("  Delete activities queued: %d\n", status.DeleteActivitiesQueued)

	// Check delivery queue status
	fmt.Printf("\nDelivery Status: %s\n", status.DeliveryStatus)

	// Check if continue flag is set
	if c.Bool("continue-mode") {
		return continueSelfDestruct(c, ctx, status)
	}

	fmt.Println("\nTo continue the self-destruct process, run:")
	fmt.Println("  tootctl self-destruct --continue-mode")
	fmt.Println("\nOr to start fresh:")
	fmt.Println("  tootctl self-destruct --continue-mode --force")

	return nil
}

func initiateSelfDestruct(c *cli.Context, ctx context.Context) error { //nolint:unparam // param reserved for future use
	fmt.Println("\n" + strings.Repeat("=", 60))
	fmt.Println("⚠️  SELF-DESTRUCT INITIATION")
	fmt.Println(strings.Repeat("=", 60))

	fmt.Print(`
This command will:

1. Enable "self-destruct mode" on this server
2. Broadcast Delete activities for ALL local accounts to ALL known 
   remote instances
3. Allow users to log in and download their data, but prevent them
   from performing any other actions

IMPORTANT WARNINGS:

- This operation is NOT reversible
- The server will be in a BROKEN STATE after completion
- No local data is actually deleted (you should empty the database 
  or delete the entire VPS afterwards)
- If you continue operating the instance after self-destruct, there 
  will be state mismatches causing federation issues
- A running delivery queue is required - do NOT shut down the server 
  until all Delete activities are delivered

Estimated time depends on:
- Number of local accounts
- Number of known remote instances
- Network connectivity
`)

	// First confirmation
	if !c.Bool("force") {
		fmt.Print("\nDo you want to continue? (type 'yes' to confirm): ")

		reader := bufio.NewReader(os.Stdin)
		response, _ := reader.ReadString('\n')
		response = strings.TrimSpace(strings.ToLower(response))

		if response != "yes" { //nolint:goconst
			fmt.Println("Self-destruct cancelled.")

			return nil
		}
	}

	// Show statistics before final confirmation
	stats, err := gatherSelfDestructStats()
	if err != nil {
		return fmt.Errorf("failed to gather statistics: %w", err)
	}

	fmt.Printf("\nServer Statistics:\n")
	fmt.Printf("  Local accounts:           %d\n", stats.LocalAccounts)
	fmt.Printf("  Known remote instances:   %d\n", stats.RemoteInstances)
	fmt.Printf("  Estimated Delete messages: %d\n", stats.LocalAccounts*stats.RemoteInstances)

	// Second confirmation
	if !c.Bool("force") {
		fmt.Print("\nThis is your LAST CHANCE. Type 'yes' again to begin self-destruct: ")

		reader := bufio.NewReader(os.Stdin)
		response, _ := reader.ReadString('\n')
		response = strings.TrimSpace(strings.ToLower(response))

		if response != "yes" {
			fmt.Println("Self-destruct cancelled.")

			return nil
		}
	}

	// Enable self-destruct mode
	if err := enableSelfDestructMode(); err != nil {
		return fmt.Errorf("failed to enable self-destruct mode: %w", err)
	}

	fmt.Println("\n✓ Self-destruct mode enabled")
	fmt.Println("\nTo start sending Delete activities, run:")
	fmt.Println("  tootctl self-destruct --continue-mode")

	return nil
}

func continueSelfDestruct(c *cli.Context, ctx context.Context, status SelfDestructStatus) error { //nolint:unparam // param reserved for future use
	fmt.Println("\n" + strings.Repeat("=", 60))
	fmt.Println("🚀 STARTING SELF-DESTRUCT PROCESS")
	fmt.Println(strings.Repeat("=", 60))

	batchSize := c.Int("batch-size")
	workers := c.Int("workers")

	// Get all local accounts
	var accounts []*models.Account

	err := db.GetDB().Where("domain IS NULL").FindInBatches(&accounts, batchSize, func(tx *gorm.DB, batch int) error {
		log.Info("Processing batch %d: %d accounts", batch, len(accounts))

		return nil
	}).Error
	if err != nil {
		return fmt.Errorf("failed to fetch local accounts: %w", err)
	}

	// Get all known remote inboxes
	inboxes, err := getAllKnownInboxes()
	if err != nil {
		return fmt.Errorf("failed to fetch known inboxes: %w", err)
	}

	fmt.Printf("\nSending Delete activities to %d remote inboxes for %d accounts\n",
		len(inboxes), len(accounts))
	fmt.Printf("Using %d workers, batch size %d\n", workers, batchSize)

	// Final confirmation
	if !c.Bool("force") {
		fmt.Printf("\nEstimated total messages: %d\n", len(accounts)*len(inboxes))
		fmt.Print("Begin broadcasting Delete activities? (type 'yes'): ")

		reader := bufio.NewReader(os.Stdin)
		response, _ := reader.ReadString('\n')
		response = strings.TrimSpace(strings.ToLower(response))

		if response != "yes" {
			fmt.Println("Operation cancelled.")

			return nil
		}
	}

	// Start the broadcast process
	return broadcastDeleteActivities(ctx, accounts, inboxes, workers)
}

func broadcastDeleteActivities(ctx context.Context, accounts []*models.Account, inboxes []string, workers int) error {
	domain := systemsettings.GetString(systemsettings.KeyDomain, "localhost")
	baseURL := "https://" + domain

	// Create delivery queue
	queue := activitypub.NewDeliveryQueue(nil, baseURL, workers)

	queue.Start() //nolint:contextcheck // CLI command, no context propagation needed
	defer queue.Stop()

	fmt.Println("\nBroadcasting Delete activities...")

	var (
		processedCount int64
		errorCount     int64
		mu             sync.Mutex
	)

	// Process each account
	for i, account := range accounts {
		actorURL := baseURL + "/users/" + account.Username

		// Build Delete activity
		now := time.Now()
		deleteActivity := map[string]any{
			"@context": []string{"https://www.w3.org/ns/activitystreams"}, //nolint:goconst
			"id":       actorURL + "#delete",
			"type":     "Delete", //nolint:goconst
			"actor":    actorURL,
			"object": map[string]any{
				"id":      actorURL,
				"type":    "Person",
				"deleted": now.Format(time.RFC3339),
			},
			"published": now.Format(time.RFC3339), //nolint:goconst
			"to":        []string{"https://www.w3.org/ns/activitystreams#Public"},
		}

		// Enqueue delivery to all known inboxes
		activityID := actorURL + "#delete"
		err := queue.EnqueueToMany(activityID, inboxes, deleteActivity)
		if err != nil {
			log.Error("Failed to enqueue Delete for account %s: %v", account.Username, err)
			mu.Lock()
			errorCount++
			mu.Unlock()

			continue
		}

		mu.Lock()
		processedCount++
		mu.Unlock()

		// Progress update
		if (i+1)%10 == 0 || i+1 == len(accounts) {
			fmt.Printf("\rProgress: %d/%d accounts processed", i+1, len(accounts))
		}
	}

	fmt.Printf("\n\n✓ Enqueued Delete activities for %d accounts\n", processedCount)

	if errorCount > 0 {
		fmt.Printf("⚠️  %d accounts had errors\n", errorCount)
	}

	fmt.Printf("\nDelete activities have been queued for delivery.\n")
	fmt.Printf("The delivery queue will process them in the background.\n")
	fmt.Printf("\nYou can monitor progress with:\n")
	fmt.Printf("  tootctl self-destruct\n")

	// Wait for deliveries to complete or user to interrupt
	fmt.Printf("\nWaiting for deliveries to complete (press Ctrl+C to stop monitoring)...\n")

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			pending := countPendingDeliveries()
			if pending == 0 {
				fmt.Println("\n✓ All Delete activities delivered!")
				fmt.Println("\nThe server is now in a broken state.")
				fmt.Println("You should now:")
				fmt.Println("  1. Export any remaining user data")
				fmt.Println("  2. Empty the database")
				fmt.Println("  3. Decommission the server")

				return nil
			}

			fmt.Printf("\rPending deliveries: %d   ", pending)
		}
	}
}

func getSelfDestructStatus() SelfDestructStatus {
	status := SelfDestructStatus{}

	// Check if self-destruct mode is enabled
	modeEnabled := systemsettings.GetBool(SelfDestructModeKey, false)
	status.ModeEnabled = modeEnabled

	if modeEnabled {
		// Get start time
		startedAtStr := systemsettings.GetString(SelfDestructStartedKey, "")
		if startedAtStr != "" {
			if t, err := time.Parse(time.RFC3339, startedAtStr); err == nil {
				status.StartedAt = &t
			}
		}

		// Count local accounts
		db.GetDB().Model(&models.Account{}).Where("domain IS NULL").Count(&status.TotalAccounts)

		// Count processed accounts (we'll track this in a separate table)
		// For now, we'll estimate based on deliveries
		status.ProcessedAccounts = countProcessedAccounts()
		status.RemainingAccounts = status.TotalAccounts - status.ProcessedAccounts

		// Count known inboxes
		status.TotalInboxes = int64(len(getCachedInboxes()))

		// Count pending deliveries
		status.DeleteActivitiesQueued = countPendingDeliveries()

		// Set delivery status
		switch {
		case status.DeleteActivitiesQueued > 0:
			status.DeliveryStatus = "In progress"
		case status.ProcessedAccounts > 0:
			status.DeliveryStatus = "Completed"
		default:
			status.DeliveryStatus = "Not started"
		}
	}

	return status
}

func enableSelfDestructMode() error {
	now := time.Now().Format(time.RFC3339)

	systemsettings.Set(SelfDestructModeKey, true)
	systemsettings.Set(SelfDestructStartedKey, now)

	return systemsettings.SaveToDB(SelfDestructModeKey, SelfDestructStartedKey)
}

type selfDestructStats struct {
	LocalAccounts   int64
	RemoteInstances int64
}

func gatherSelfDestructStats() (*selfDestructStats, error) {
	stats := &selfDestructStats{}

	// Count local accounts
	err := db.GetDB().Model(&models.Account{}).Where("domain IS NULL").Count(&stats.LocalAccounts).Error
	if err != nil {
		return nil, err
	}

	// Count distinct remote domains
	err = db.GetDB().Model(&models.Account{}).
		Where("domain IS NOT NULL AND domain != ''").
		Distinct("domain").
		Count(&stats.RemoteInstances).Error
	if err != nil {
		return nil, err
	}

	return stats, nil
}

func getAllKnownInboxes() ([]string, error) {
	inboxes := make(map[string]bool)

	// Get inboxes from remote accounts
	var accounts []*models.Account
	err := db.GetDB().Where("domain IS NOT NULL AND domain != ''").Find(&accounts).Error
	if err != nil {
		return nil, err
	}

	for _, account := range accounts {
		if account.Domain != nil && *account.Domain != "" {
			// Construct inbox URL
			inboxURL := "https://" + *account.Domain + "/users/" + account.Username + "/inbox"
			inboxes[inboxURL] = true

			// Also add shared inbox
			sharedInbox := "https://" + *account.Domain + "/inbox"
			inboxes[sharedInbox] = true
		}
	}

	// Convert to slice
	result := make([]string, 0, len(inboxes))
	for inbox := range inboxes {
		result = append(result, inbox)
	}

	return result, nil
}

func getCachedInboxes() []string {
	inboxes, err := getAllKnownInboxes()
	if err != nil {
		return []string{}
	}

	return inboxes
}

func countPendingDeliveries() int64 {
	var count int64
	db.GetDB().Model(&activitypub.Delivery{}).
		Where("status IN ?", []activitypub.DeliveryStatus{
			activitypub.DeliveryStatusPending,
			activitypub.DeliveryStatusProcessing,
		}).
		Count(&count)

	return count
}

func countProcessedAccounts() int64 {
	// Count distinct account IDs from delivered Delete activities
	var count int64
	db.GetDB().Raw(`
		SELECT COUNT(DISTINCT JSON_EXTRACT(payload, '$.actor'))
		FROM activitypub_deliveries
		WHERE status = 'delivered'
		AND JSON_EXTRACT(payload, '$.type') = 'Delete'
	`).Scan(&count)

	return count
}

// VersionCommand implements tootctl --version.
func VersionCommand(version string) *cli.Command {
	return &cli.Command{
		Name:  "version",
		Usage: "Show the version of the currently running Community instance",
		Action: func(c *cli.Context) error {
			domain := systemsettings.GetString(systemsettings.KeyDomain, "unknown")

			fmt.Printf("Community (Mastodon-compatible) %s\n", version)
			fmt.Printf("Domain: %s\n", domain)

			// Get database info
			var dbVersion string
			err := db.GetDB().Raw("SELECT VERSION()").Scan(&dbVersion).Error
			if err == nil {
				fmt.Printf("Database: %s\n", dbVersion)
			}

			return nil
		},
	}
}

// AccountsCommand implements tootctl accounts subcommands.
func AccountsCommand() *cli.Command {
	return &cli.Command{
		Name:  "accounts", //nolint:goconst
		Usage: "Manage accounts",
		Subcommands: []*cli.Command{
			{
				Name:  "create",
				Usage: "Create a new account",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "username", Required: true}, //nolint:goconst
					&cli.StringFlag{Name: "email", Required: true},    //nolint:goconst
					&cli.BoolFlag{Name: "confirmed"},
					&cli.BoolFlag{Name: "admin"},
				},
				Action: createAccount,
			},
			{
				Name:  "delete",
				Usage: "Delete an account",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "username", Required: true},
					&cli.BoolFlag{Name: "email", Required: true},
				},
				Action: deleteAccount,
			},
			{
				Name:  "modify",
				Usage: "Modify an account",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "username"},
					&cli.StringFlag{Name: "email"},
					&cli.BoolFlag{Name: "confirm"},
					&cli.BoolFlag{Name: "approve"},
					&cli.BoolFlag{Name: "disable"},
					&cli.BoolFlag{Name: "enable"},
					&cli.BoolFlag{Name: "admin"},
					&cli.BoolFlag{Name: "unadmin"},
					&cli.BoolFlag{Name: "reset-password"},
				},
				Action: modifyAccount,
			},
		},
	}
}

func createAccount(c *cli.Context) error {
	username := c.String("username")
	email := c.String("email")

	// Check if username already exists
	var existingAccount models.Account
	if err := db.GetDB().Where("username = ? AND domain IS NULL", username).First(&existingAccount).Error; err == nil {
		return fmt.Errorf("username '%s' already exists", username)
	}

	// Create user
	now := time.Now()
	user := &models.User{
		UserName:       username,
		IsMailVerified: c.Bool("confirmed"),
		CreatedAt:      &now,
		UpdatedAt:      &now,
	}

	if err := db.GetDB().Create(user).Error; err != nil {
		return fmt.Errorf("failed to create user: %w", err)
	}

	// Create user secret data with email
	salt := strconv.FormatInt(time.Now().UnixNano(), 10)

	password, err := models.EncryptPassword("changeme", salt)
	if err != nil {
		return fmt.Errorf("failed to encrypt password: %w", err)
	}

	userSecret := &models.UserSecretData{
		UserId:   user.Id,
		Salt:     salt,
		Password: password,
		Email:    email,
	}

	if err := db.GetDB().Create(userSecret).Error; err != nil {
		return fmt.Errorf("failed to create user secret: %w", err)
	}

	// Create account
	account := &models.Account{
		UserId:    user.Id,
		Username:  username,
		CreatedAt: &now,
		UpdatedAt: &now,
	}

	if err := db.GetDB().Create(account).Error; err != nil {
		return fmt.Errorf("failed to create account: %w", err)
	}

	fmt.Printf("Account created: %s\n", username)

	return nil
}

func deleteAccount(c *cli.Context) error {
	username := c.String("username")

	var account models.Account
	err := db.GetDB().Where("username = ? AND domain IS NULL", username).First(&account).Error
	if err != nil {
		return errors.New("account not found")
	}

	// Delete user (cascade will handle related data)
	err = db.GetDB().Delete(&models.User{}, account.UserId).Error
	if err != nil {
		return fmt.Errorf("failed to delete user: %w", err)
	}

	fmt.Printf("Account deleted: %s\n", username)

	return nil
}

func modifyAccount(c *cli.Context) error {
	username := c.String("username")

	var account models.Account
	err := db.GetDB().Where("username = ? AND domain IS NULL", username).First(&account).Error
	if err != nil {
		return errors.New("account not found")
	}

	var user models.User
	err = db.GetDB().First(&user, account.UserId).Error
	if err != nil {
		return errors.New("user not found")
	}

	updates := make(map[string]any)

	if c.Bool("confirm") {
		updates["is_mail_verified"] = true
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

	if c.Bool("approve") {
		updates["status"] = models.UserStatusNormal
	}

	if len(updates) > 0 {
		err := db.GetDB().Model(&user).Updates(updates).Error
		if err != nil {
			return fmt.Errorf("failed to update user: %w", err)
		}
	}

	fmt.Printf("Account modified: %s\n", username)

	return nil
}
