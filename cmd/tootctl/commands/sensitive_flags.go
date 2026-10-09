package commands

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/kasuganosora/community/activitypub"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/mediablur"
	"github.com/kasuganosora/community/models"
	"github.com/urfave/cli/v2"
	"golang.org/x/time/rate"
	"gorm.io/gorm"
)

// newStatusFlagsFetcher is replaced in tests.
var newStatusFlagsFetcher = func() activitypub.ActorFetcher { return activitypub.NewFetchClient() }

func statusesRefreshSensitiveCommand() *cli.Command {
	return &cli.Command{
		Name:  "refresh-sensitive",
		Usage: "Re-fetch remote statuses and restore their sensitive flags and attachment blurhashes",
		Flags: []cli.Flag{
			&cli.IntFlag{Name: "days", Value: 30, Usage: "only statuses from the last N days (0 = any age)"},
			&cli.IntFlag{Name: "limit", Value: 5000, Usage: "maximum statuses to check (0 = no limit)"},
			&cli.IntFlag{Name: "concurrency", Aliases: []string{"c"}, Value: 4, Usage: "objects fetched in parallel"},
			&cli.Float64Flag{Name: "rate", Value: 4, Usage: "maximum fetches started per second (0 = unlimited)"},
			&cli.BoolFlag{Name: "dry-run", Usage: "report what would change without writing"},
			&cli.BoolFlag{Name: "verbose", Aliases: []string{"v"}, Usage: "print every status"},
		},
		Action: refreshSensitive,
	}
}

type sensitiveSummary struct {
	mu                                              sync.Mutex
	checked, raised, files, gone, failed, suspended int
}

func refreshSensitive(c *cli.Context) error {
	conn := db.GetDB().WithContext(c.Context)
	q := conn.Model(&models.Status{}).
		Where("deleted_at IS NULL AND domain IS NOT NULL AND origin_uri IS NOT NULL AND reblog_id IS NULL").
		Order("id DESC")
	if d := c.Int("days"); d > 0 {
		q = q.Where("created_at >= ?", time.Now().AddDate(0, 0, -d))
	}
	if l := c.Int("limit"); l > 0 {
		q = q.Limit(l)
	}
	var statuses []*models.Status
	if err := q.Select("id", "account_id", "origin_uri", "domain", "sensitive").Find(&statuses).Error; err != nil {
		return err
	}
	suspended, _, err := models.DomainSeverities(conn)
	if err != nil {
		return err
	}
	dryRun, verbose, out := c.Bool("dry-run"), c.Bool("verbose"), c.App.Writer
	fetcher := newStatusFlagsFetcher()
	sum := &sensitiveSummary{}
	var lim *rate.Limiter
	if r := c.Float64("rate"); r > 0 {
		lim = rate.NewLimiter(rate.Limit(r), 1)
	}
	var pmu sync.Mutex
	say := func(format string, a ...any) {
		pmu.Lock()
		defer pmu.Unlock()
		_, _ = fmt.Fprintf(out, format, a...)
	}
	jobs := make(chan *models.Status)
	var wg sync.WaitGroup
	for range max(1, c.Int("concurrency")) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for st := range jobs {
				refreshStatusFlagsOne(c.Context, conn, fetcher, st, dryRun, verbose, sum, say)
			}
		}()
	}
	for _, st := range statuses {
		if suspended[*st.Domain] {
			sum.mu.Lock()
			sum.suspended++
			sum.mu.Unlock()

			continue
		}
		if lim != nil && lim.Wait(c.Context) != nil {
			break
		}
		jobs <- st
	}
	close(jobs)
	wg.Wait()
	prefix := ""
	if dryRun {
		prefix = "[dry run] "
	}
	_, _ = fmt.Fprintf(out, "%schecked %d, newly sensitive %d, attachments updated %d, gone %d, failed %d, skipped (suspended domain) %d\n",
		prefix, sum.checked, sum.raised, sum.files, sum.gone, sum.failed, sum.suspended)

	return nil
}

func refreshStatusFlagsOne(ctx context.Context, conn *gorm.DB, fetcher activitypub.ActorFetcher, st *models.Status, dryRun, verbose bool, sum *sensitiveSummary, say func(string, ...any)) {
	res, err := activitypub.RefreshStatusFlags(ctx, conn, fetcher, st, dryRun)
	sum.mu.Lock()
	defer sum.mu.Unlock()
	switch {
	case err != nil:
		sum.failed++
		say("status %d: %v\n", st.Id, err)
	case res.Gone:
		sum.gone++
		if verbose {
			say("status %d: gone\n", st.Id)
		}
	default:
		sum.checked++
		if res.SensitiveChanged {
			sum.raised++
		}
		sum.files += res.FilesUpdated
		if verbose || res.SensitiveChanged || res.FilesUpdated > 0 {
			say("status %d: sensitive=%v (was %v), %d attachment(s) updated\n", st.Id, res.Sensitive, st.Sensitive, res.FilesUpdated)
		}
	}
}

// MediaBlurhashCommand implements tootctl media blurhash.
func MediaBlurhashCommand() *cli.Command {
	return &cli.Command{
		Name:  "blurhash",
		Usage: "Generate blurhash placeholders for remote images that have none",
		Flags: []cli.Flag{
			&cli.IntFlag{Name: "days", Value: 30, Usage: "only statuses from the last N days (0 = any age)"},
			&cli.IntFlag{Name: "limit", Value: 5000, Usage: "maximum attachments (0 = no limit)"},
			&cli.BoolFlag{Name: "sensitive-only", Usage: "only sensitive statuses or files"},
			&cli.IntFlag{Name: "concurrency", Aliases: []string{"c"}, Value: 4, Usage: "images fetched in parallel"},
			&cli.Float64Flag{Name: "rate", Value: 5, Usage: "maximum images per second (0 = unlimited)"},
		},
		Action: func(c *cli.Context) error {
			done := 0
			st, err := mediablur.NewService().Backfill(c.Context, c.Int("days"), c.Int("limit"), c.Int("concurrency"), c.Float64("rate"), c.Bool("sensitive-only"),
				func(uint64, bool) {
					if done++; done%50 == 0 {
						_, _ = fmt.Fprintf(c.App.Writer, "... %d processed\n", done)
					}
				})
			_, _ = fmt.Fprintf(c.App.Writer, "Candidates: %d, hashed: %d, failed: %d\n", st.Candidates, st.Done, st.Failed)

			return err
		},
	}
}
