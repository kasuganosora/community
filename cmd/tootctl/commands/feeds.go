package commands

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/kasuganosora/community/db"
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
	redisClient := db.GetRedis()
	if redisClient == nil {
		return 0, errors.New("redis not available")
	}
	var userLists []*models.List
	if err := db.GetDB().WithContext(ctx).Where("account_id = ?", account.Id).Find(&userLists).Error; err != nil {
		return 0, fmt.Errorf("failed to query account lists: %w", err)
	}
	homeTimeline := &home.HomeTimeline{Account: account}
	key := fmt.Sprintf(homeTimeline.GetKeyFormat(), homeTimeline.GetTimelineId())
	if err := repairLegacyFeedScores(ctx, key); err != nil {
		return 0, fmt.Errorf("failed to repair home timeline scores: %w", err)
	}
	if err := homeTimeline.RebuildContext(ctx); err != nil {
		return 0, fmt.Errorf("failed to rebuild home timeline: %w", err)
	}
	for _, userList := range userLists {
		listTimeline := &list.ListTimeline{List: userList}
		listKey := fmt.Sprintf(listTimeline.GetKeyFormat(), listTimeline.GetTimelineId())
		if err := repairLegacyFeedScores(ctx, listKey); err != nil {
			return 0, fmt.Errorf("failed to repair list %d scores: %w", userList.Id, err)
		}
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

// Correct only malformed scores in the exact canonical feed. The atomic script
// preserves valid concurrent pushes; it does not delete/recreate the whole key.
func repairLegacyFeedScores(ctx context.Context, key string) error {
	const script = `
local rows=redis.call('ZRANGE',KEYS[1],0,-1,'WITHSCORES')
for i=1,#rows,2 do
 local id=tonumber(rows[i])
 if not id then
  redis.call('ZREM',KEYS[1],rows[i])
 elseif id~=tonumber(rows[i+1]) then
  redis.call('ZADD',KEYS[1],id,rows[i])
 end
end
return 1
`
	return db.GetRedis().Eval(ctx, script, []string{key}).Err()
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

func clearFeeds(c *cli.Context) error {
	redis := db.GetRedis()
	if redis == nil {
		return errors.New("redis is not available")
	}

	ctx := context.Background()
	startTime := time.Now()

	// Clear home feeds
	var (
		cursor uint64
		err    error
	)

	totalCleared := 0

	patterns := []string{
		"feed:home:*",
		"feed:list:*",
	}

	for _, pattern := range patterns {
		cursor = 0

		for {
			var keys []string

			keys, cursor, err = redis.Scan(ctx, cursor, pattern, 1000).Result()
			if err != nil {
				return fmt.Errorf("failed to scan keys: %w", err)
			}

			if len(keys) > 0 {
				deleted, err := redis.Del(ctx, keys...).Result()
				if err != nil {
					return fmt.Errorf("failed to delete keys: %w", err)
				}

				totalCleared += int(deleted)
			}

			if cursor == 0 {
				break
			}
		}
	}

	elapsed := time.Since(startTime)
	fmt.Printf("Feeds cleared. %d keys removed in %s\n", totalCleared, elapsed)

	return nil
}

// GetFeedStats returns feed statistics.
func GetFeedStats() (map[string]any, error) {
	redis := db.GetRedis()
	if redis == nil {
		return map[string]any{
			"redis_available": false,
		}, nil
	}

	ctx := context.Background()
	stats := map[string]any{
		"redis_available": true,
	}

	// Count home feeds
	var (
		cursor uint64
		keys   []string
		err    error
	)

	homeCount := 0

	cursor = 0
	for {
		keys, cursor, err = redis.Scan(ctx, cursor, "feed:home:*", 1000).Result()
		if err != nil {
			break
		}

		homeCount += len(keys)

		if cursor == 0 {
			break
		}
	}

	stats["home_feeds"] = homeCount

	// Count list feeds
	listCount := 0

	cursor = 0
	for {
		keys, cursor, err = redis.Scan(ctx, cursor, "feed:list:*", 1000).Result()
		if err != nil {
			break
		}

		listCount += len(keys)

		if cursor == 0 {
			break
		}
	}

	stats["list_feeds"] = listCount

	return stats, nil //nolint:nilerr // may return partial results on redis scan error
}
