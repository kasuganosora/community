package commands

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/feed"
	"github.com/kasuganosora/community/feed/home"
	"github.com/kasuganosora/community/feed/list"
	"github.com/kasuganosora/community/models"
	"github.com/urfave/cli/v2"
)

// FeedsCommand implements tootctl feeds subcommands.
func FeedsCommand() *cli.Command {
	return &cli.Command{
		Name:  "feeds",
		Usage: "Manage feeds",
		Subcommands: []*cli.Command{
			FeedsBuildCommand(),
			FeedsClearCommand(),
		},
	}
}

// FeedsBuildCommand implements tootctl feeds build
// Build home and list feeds for one or all users.
// Version history: 2.6.0 - added.
func FeedsBuildCommand() *cli.Command {
	return &cli.Command{
		Name:      "build",
		Usage:     "Build home and list feeds for one or all users",
		ArgsUsage: "[USERNAME]",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "all",
				Usage: "Build feeds for all users instead of just one",
			},
			&cli.IntFlag{
				Name:    "concurrency", //nolint:goconst
				Aliases: []string{"c"},
				Value:   5,
				Usage:   "The number of workers to use", //nolint:goconst
			},
			&cli.BoolFlag{
				Name:    "verbose", //nolint:goconst
				Aliases: []string{"v"},
				Usage:   "Print additional information while processing", //nolint:goconst
			},
			&cli.BoolFlag{
				Name:  "dry-run", //nolint:goconst
				Usage: "Print expected results only, without performing any actions",
			},
		},
		Action: buildFeeds,
	}
}

func buildFeeds(c *cli.Context) error {
	username := c.Args().First()
	buildAll := c.Bool("all")
	concurrency := c.Int("concurrency")
	verbose := c.Bool("verbose")
	dryRun := c.Bool("dry-run")

	if concurrency < 1 {
		return errors.New("concurrency must be greater than zero")
	}

	if username == "" && !buildAll {
		return errors.New("USERNAME is required, or use --all")
	}

	if dryRun {
		fmt.Println("Dry run mode - no changes will be made")
	}

	startTime := time.Now()

	var accounts []*models.Account
	if buildAll {
		// Get all local accounts
		err := db.GetDB().WithContext(c.Context).Preload("User.Settings").Where("domain IS NULL").Find(&accounts).Error
		if err != nil {
			return fmt.Errorf("failed to query accounts: %w", err)
		}
	} else {
		// Get specific account
		err := db.GetDB().WithContext(c.Context).Preload("User.Settings").Where("username = ? AND domain IS NULL", username).First(&accounts).Error
		if err != nil {
			return fmt.Errorf("account not found: %w", err)
		}

		accounts = []*models.Account{accounts[0]}
	}

	fmt.Printf("Building feeds for %d accounts...\n", len(accounts))

	// Create worker pool
	type accountJob struct {
		account *models.Account
	}

	jobs := make(chan accountJob, 100)

	var (
		wg sync.WaitGroup
		mu sync.Mutex
	)

	processed := 0
	errors := 0
	totalFeedItems := 0

	// Start workers
	for range concurrency {
		wg.Go(func() {
			for job := range jobs {
				if dryRun {
					mu.Lock()
					processed++

					if verbose {
						fmt.Printf("[DRY-RUN] Would build feeds for: %s\n", job.account.Username)
					}
					mu.Unlock()

					continue
				}

				items, err := buildAccountFeedsContext(c.Context, job.account)

				mu.Lock()
				processed++

				if err != nil {
					errors++

					if verbose {
						fmt.Printf("Error building feeds for %s: %v\n", job.account.Username, err)
					}
				} else {
					totalFeedItems += items
					if verbose {
						fmt.Printf("Built feeds for %s (%d items)\n", job.account.Username, items)
					}
				}
				mu.Unlock()
			}
		})
	}

	// Feed jobs
	for _, account := range accounts {
		jobs <- accountJob{account: account}
	}

	close(jobs)

	wg.Wait()

	elapsed := time.Since(startTime)
	fmt.Printf("\nFeed build completed in %s\n", elapsed)
	fmt.Printf("Processed: %d accounts, Errors: %d\n", processed, errors)

	if !dryRun {
		fmt.Printf("Total feed items: %d\n", totalFeedItems)
	}

	if errors > 0 {
		return fmt.Errorf("failed to build feeds for %d accounts", errors)
	}
	return nil
}

// buildAccountFeeds expects the account's User.Settings association to be
// loaded, as buildFeeds does in its account query.
func buildAccountFeeds(account *models.Account) (int, error) {
	return buildAccountFeedsContext(context.Background(), account)
}

func buildAccountFeedsContext(ctx context.Context, account *models.Account) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	redisClient := db.GetUniversalRedis()
	if redisClient == nil {
		return 0, errors.New("redis not available")
	}
	var userLists []*models.List
	if err := db.GetDB().WithContext(ctx).Where("account_id = ?", account.Id).Find(&userLists).Error; err != nil {
		return 0, fmt.Errorf("failed to query account lists: %w", err)
	}
	homeTimeline := &home.HomeTimeline{Account: account}
	key := feed.TimelineKey(homeTimeline)

	if err := homeTimeline.RebuildContext(ctx); err != nil {
		return 0, fmt.Errorf("failed to rebuild home timeline: %w", err)
	}
	for _, userList := range userLists {
		listTimeline := &list.ListTimeline{List: userList}

		if err := listTimeline.RebuildContext(ctx); err != nil {
			return 0, fmt.Errorf("failed to rebuild list %d: %w", userList.Id, err)
		}
	}
	count, err := redisClient.ZCard(ctx, key).Result()
	if err != nil {
		return 0, fmt.Errorf("failed to count home timeline: %w", err)
	}
	return int(count), nil
}

// FeedsClearCommand implements tootctl feeds clear
// Remove all home and list feeds from Redis.
// Version history: 2.6.0 - added.
func FeedsClearCommand() *cli.Command {
	return &cli.Command{
		Name:   "clear",
		Usage:  "Remove all home and list feeds from Redis",
		Action: clearFeeds,
	}
}

// scanFeedMetadata visits each cluster primary independently. A SCAN cursor
// belongs to its node; no multi-key command crosses timeline hash slots.
func scanFeedMetadata(ctx context.Context, pattern string, visit func(string) error) error {
	return feed.ScanTimelineMetadata(ctx, db.GetUniversalRedis(), pattern, visit)
}

type feedKeyTimeline string

func (t feedKeyTimeline) GetKeyFormat() string  { return "%s" }
func (t feedKeyTimeline) GetTimelineId() string { return string(t) }
func (t feedKeyTimeline) Rebuild() error        { return feed.ErrNeedsRebuild }

func clearFeeds(c *cli.Context) error {
	start := time.Now()
	var cleared atomic.Int64
	for _, pattern := range []string{"feed:v3:{home:*}:items:meta", "feed:v3:{list:*}:items:meta"} {
		if err := scanFeedMetadata(c.Context, pattern, func(key string) error {
			if err := feed.InvalidateTimelineContext(c.Context, feedKeyTimeline(strings.TrimSuffix(key, ":meta"))); err != nil {
				return err
			}
			cleared.Add(1)
			return nil
		}); err != nil {
			return fmt.Errorf("failed to invalidate feeds: %w", err)
		}
	}
	fmt.Printf("Feeds cleared. %d timelines invalidated in %s\n", cleared.Load(), time.Since(start))
	return nil
}

// GetFeedStats counts v3 timeline metadata, including complete empty feeds.
func GetFeedStats() (map[string]any, error) {
	stats := map[string]any{"redis_available": true}
	for _, kind := range []string{"home", "list"} {
		var count atomic.Int64
		err := scanFeedMetadata(context.Background(), "feed:v3:{"+kind+":*}:items:meta", func(string) error { count.Add(1); return nil })
		if err != nil {
			return nil, fmt.Errorf("failed to count feed metadata: %w", err)
		}
		stats[kind+"_feeds"] = int(count.Load())
	}
	return stats, nil
}
