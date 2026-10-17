package commands

import (
	"context"
	"errors"
	"fmt"
	"github.com/kasuganosora/community/feed"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/kasuganosora/community/activitypub"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/urfave/cli/v2"
)

func statusesBackfillCommands() []*cli.Command {
	return []*cli.Command{
		{
			Name:  "fetch-quotes",
			Usage: "Import the quoted posts of recent remote quote posts and link them",
			Flags: []cli.Flag{
				&cli.IntFlag{Name: "days", Value: 7, Usage: "only look at statuses created in the last N days"},
				&cli.IntFlag{Name: "limit", Value: 200, Usage: "fetch at most N quoted posts"},
			},
			Action: func(c *cli.Context) error {
				linked, err := activitypub.BackfillQuotes(context.Background(), c.Int("days"), c.Int("limit"))
				if err != nil {
					return err
				}
				fmt.Printf("Linked %d quoted posts\n", linked)
				return nil
			},
		},
		{
			Name:      "refetch",
			Usage:     "Fetch remote polls again from their origin and fix their stored tallies",
			ArgsUsage: "<status id> [<status id>...]",
			Action: func(c *cli.Context) error {
				if c.NArg() == 0 {
					return errors.New("give at least one local status id")
				}
				failed := 0
				for _, arg := range c.Args().Slice() {
					id, err := strconv.ParseUint(arg, 10, 64)
					if err != nil {
						return fmt.Errorf("invalid status id %q", arg)
					}
					ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
					err = activitypub.RefetchRemotePoll(ctx, id)
					cancel()
					if err != nil {
						failed++
						fmt.Printf("%d: %v\n", id, err)
						continue
					}
					fmt.Printf("%d: refetched\n", id)
				}
				if failed > 0 {
					return fmt.Errorf("%d status(es) failed", failed)
				}
				return nil
			},
		},
		{
			Name:  "mark-backfilled",
			Usage: "Keep remote posts that were imported long after publication out of public, tag, home and list timelines",
			Description: "Marks remote statuses (never edited) whose last update is more than --gap hours after their publish time,\n" +
				"which is how posts fetched late (boost originals, reply context) look. Safe to re-run; then clears timeline caches.",
			Flags: []cli.Flag{
				&cli.IntFlag{Name: "gap", Value: 24, Usage: "hours between publish time and import"},
				&cli.IntFlag{Name: "days", Value: 60, Usage: "only look at statuses imported in the last N days"},
				&cli.BoolFlag{Name: "dry-run", Usage: "print the count only"},
			},
			Action: markBackfilled,
		},
	}
}

func markBackfilled(c *cli.Context) error {
	if c.Int("gap") <= 0 || c.Int("days") <= 0 {
		return errors.New("gap and days must be positive")
	}
	conn := db.GetDB()
	gap := time.Duration(c.Int("gap")) * time.Hour
	since := time.Now().AddDate(0, 0, -c.Int("days"))
	ctx := context.Background()

	edited := map[uint64]bool{}
	if conn.Migrator().HasTable(&models.StatusHistory{}) {
		var ids []uint64
		if err := conn.WithContext(ctx).Model(&models.StatusHistory{}).Distinct().Pluck("status_id", &ids).Error; err != nil {
			return err
		}
		for _, id := range ids {
			edited[id] = true
		}
	}

	var cursor uint64
	marked, scanned := 0, 0
	for {
		var rows []models.Status
		err := conn.WithContext(ctx).Select("id", "created_at", "updated_at", "properties").
			Where("id > ? AND domain IS NOT NULL AND reblog_id IS NULL AND updated_at > ?", cursor, since).
			Order("id ASC").Limit(500).Find(&rows).Error
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			break
		}
		for i := range rows {
			s := &rows[i]
			cursor = s.Id
			scanned++
			if s.IsBackfilled() || edited[s.Id] || s.CreatedAt == nil || s.UpdatedAt == nil || s.UpdatedAt.Sub(*s.CreatedAt) <= gap {
				continue
			}
			marked++
			if c.Bool("dry-run") {
				continue
			}
			s.MarkBackfilled()
			if err := conn.WithContext(ctx).Model(&models.Status{}).Where("id = ?", s.Id).UpdateColumn("properties", s.Properties).Error; err != nil {
				return err
			}
		}
	}
	fmt.Printf("Scanned %d remote statuses, marked %d as backfilled\n", scanned, marked)
	if c.Bool("dry-run") || marked == 0 {
		return nil
	}

	var cleared atomic.Int64
	for _, kind := range []string{"public", "hashtag", "home", "list", "mention"} {
		if err := scanFeedMetadata(ctx, "feed:v3:{"+kind+":*}:items:meta", func(key string) error {
			if err := feed.InvalidateTimelineContext(ctx, feedKeyTimeline(strings.TrimSuffix(key, ":meta"))); err != nil {
				return err
			}
			cleared.Add(1)
			return nil
		}); err != nil {
			return fmt.Errorf("invalidate %s: %w", kind, err)
		}
	}
	fmt.Printf("Invalidated %d timeline caches; they rebuild on next read\n", cleared.Load())
	return nil
}
