package commands

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/kasuganosora/community/activitypub"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/urfave/cli/v2"
	"golang.org/x/time/rate"
	"gorm.io/gorm"
)

// newRefreshFetcher is replaced in tests. The fetch client signs requests
// as the instance actor when a server enforces authorized fetch.
var newRefreshFetcher = func() activitypub.ActorFetcher { return activitypub.NewFetchClient() }

const refreshFetchTimeout = 60 * time.Second

func accountsRefreshCommand() *cli.Command {
	return &cli.Command{
		Name:      "refresh",
		Usage:     "Re-fetch remote accounts' profiles: fields, emoji, avatar/header, follower/following/post totals",
		ArgsUsage: "[USERNAME@DOMAIN...]",
		Description: `Refresh one or more remote accounts, every remote account (--all), or every
account of one server (--domain). Banned accounts, accounts on suspended
domains and accounts whose actor is gone (404/410/Tombstone) are skipped.`,
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "all", Usage: "refresh every remote account"},
			&cli.StringFlag{Name: "domain", Usage: "refresh every account of this domain"},
			&cli.IntFlag{Name: "concurrency", Aliases: []string{"c"}, Value: 4, Usage: "accounts fetched in parallel"},
			&cli.Float64Flag{Name: "rate", Value: 4, Usage: "maximum accounts started per second (0 = unlimited)"},
			&cli.BoolFlag{Name: "dry-run", Usage: "fetch and validate actors without changing the database"},
			&cli.BoolFlag{Name: "verbose", Aliases: []string{"v"}, Usage: "print every account, not only problems"},
		},
		Action: refreshAccounts,
	}
}

// normalizeRefreshArgs moves the flags of "accounts refresh" in front of
// its USERNAME@DOMAIN arguments (urfave/cli stops at the first positional
// argument), so "refresh alice@example.com --dry-run" works.
func normalizeRefreshArgs(args []string, start int) []string {
	valued := map[string]bool{"--domain": true, "-domain": true, "--concurrency": true, "-concurrency": true, "-c": true, "--rate": true, "-rate": true}
	var flags, positional []string
	for i := start; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}
		if strings.HasPrefix(arg, "-") {
			flags = append(flags, arg)
			if valued[arg] && i+1 < len(args) {
				i++
				flags = append(flags, args[i])
			}
			continue
		}
		positional = append(positional, arg)
	}
	out := append([]string{}, args[:start]...)
	out = append(out, flags...)
	return append(out, positional...)
}

type refreshSummary struct {
	mu                                      sync.Mutex
	total, refreshed, gone, skipped, failed int
	notFound                                int
}

func (s *refreshSummary) add(field *int) {
	s.mu.Lock()
	*field++
	s.mu.Unlock()
}

func refreshAccounts(c *cli.Context) error {
	all, domain := c.Bool("all"), strings.ToLower(strings.TrimSpace(c.String("domain")))
	targets := c.Args().Slice()
	modes := 0
	for _, set := range []bool{all, domain != "", len(targets) > 0} {
		if set {
			modes++
		}
	}
	if modes != 1 {
		return errors.New("give USERNAME@DOMAIN arguments, --domain DOMAIN or --all")
	}
	concurrency := c.Int("concurrency")
	if concurrency < 1 {
		concurrency = 1
	}
	dryRun := c.Bool("dry-run")
	out := c.App.Writer
	conn := db.GetDB().WithContext(c.Context)

	suspended, _, err := models.DomainSeverities(conn)
	if err != nil {
		return fmt.Errorf("loading domain blocks: %w", err)
	}

	sum := &refreshSummary{}
	jobs := make(chan *models.Account)
	var limiter *rate.Limiter
	if r := c.Float64("rate"); r > 0 {
		limiter = rate.NewLimiter(rate.Limit(r), 1)
	}
	fetcher := newRefreshFetcher()
	var printMu sync.Mutex
	printf := func(format string, args ...any) {
		printMu.Lock()
		defer printMu.Unlock()
		_, _ = fmt.Fprintf(out, format, args...)
	}

	var wg sync.WaitGroup
	for range concurrency {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for account := range jobs {
				if limiter != nil {
					if err := limiter.Wait(c.Context); err != nil {
						sum.add(&sum.failed)
						continue
					}
				}
				refreshOne(c.Context, conn, fetcher, account, dryRun, c.Bool("verbose") || len(targets) > 0, sum, printf)
			}
		}()
	}

	queue := func(account *models.Account) {
		sum.add(&sum.total)
		label := accountLabel(account)
		switch {
		case account.IsBan:
			sum.add(&sum.skipped)
			printf("skip  %s: suspended\n", label)
		case account.Domain != nil && suspended[strings.ToLower(*account.Domain)]:
			sum.add(&sum.skipped)
			printf("skip  %s: domain suspended\n", label)
		case account.OriginId == "":
			sum.add(&sum.skipped)
			printf("skip  %s: no actor URI\n", label)
		default:
			jobs <- account
		}
		if all || domain != "" {
			if n := sum.snapshot(); n%100 == 0 {
				printf("... %d accounts queued\n", n)
			}
		}
	}

	var selectErr error
	if len(targets) > 0 {
		for _, target := range targets {
			account, err := findRemoteAccount(conn, target)
			if err != nil {
				sum.add(&sum.notFound)
				printf("fail  %s: %v\n", target, err)
				continue
			}
			queue(account)
		}
	} else {
		selectErr = eachRemoteAccount(conn, domain, queue)
	}
	close(jobs)
	wg.Wait()

	verb := "Refreshed"
	if dryRun {
		verb = "Fetched (dry run, nothing saved)"
	}
	_, _ = fmt.Fprintf(out, "\n%s %d of %d accounts: %d gone, %d skipped, %d failed",
		verb, sum.refreshed, sum.total, sum.gone, sum.skipped, sum.failed)
	if sum.notFound > 0 {
		_, _ = fmt.Fprintf(out, ", %d not found", sum.notFound)
	}
	_, _ = fmt.Fprintln(out)

	if selectErr != nil {
		return selectErr
	}
	if len(targets) > 0 && (sum.failed > 0 || sum.notFound > 0) {
		return errors.New("some accounts could not be refreshed")
	}
	return nil
}

func (s *refreshSummary) snapshot() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.total
}

func refreshOne(ctx context.Context, conn *gorm.DB, fetcher activitypub.ActorFetcher, account *models.Account, dryRun, verbose bool,
	sum *refreshSummary, printf func(string, ...any)) {
	ctx, cancel := context.WithTimeout(ctx, refreshFetchTimeout)
	defer cancel()
	label := accountLabel(account)
	err := activitypub.RefreshRemoteAccount(ctx, conn, fetcher, account, dryRun)
	switch {
	case err == nil:
		sum.add(&sum.refreshed)
		if verbose {
			printf("ok    %s\n", label)
		}
	case errors.Is(err, activitypub.ErrActorGone):
		sum.add(&sum.gone)
		printf("gone  %s\n", label)
	case errors.Is(err, activitypub.ErrNotRemoteAccount):
		sum.add(&sum.skipped)
		printf("skip  %s: not a remote account\n", label)
	default:
		sum.add(&sum.failed)
		printf("fail  %s: %v\n", label, err)
	}
}

func accountLabel(account *models.Account) string {
	if account.Domain != nil {
		return account.Username + "@" + *account.Domain
	}
	return account.Username
}

// findRemoteAccount finds USERNAME@DOMAIN (a leading '@' is allowed)
// case-insensitively among remote accounts.
func findRemoteAccount(conn *gorm.DB, target string) (*models.Account, error) {
	username, domain, ok := strings.Cut(strings.TrimPrefix(strings.TrimSpace(target), "@"), "@")
	if !ok || username == "" || domain == "" {
		return nil, errors.New("expected USERNAME@DOMAIN")
	}
	var account models.Account
	err := conn.Where("LOWER(username) = ? AND LOWER(domain) = ?", strings.ToLower(username), strings.ToLower(domain)).
		Order("id").First(&account).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, errors.New("no such account")
	}
	if err != nil {
		return nil, err
	}
	return &account, nil
}

// eachRemoteAccount calls fn for every remote account (of domain, if set)
// in id order, loading them in batches.
func eachRemoteAccount(conn *gorm.DB, domain string, fn func(*models.Account)) error {
	var last uint64
	for {
		query := conn.Where("domain IS NOT NULL AND id > ?", last)
		if domain != "" {
			query = query.Where("LOWER(domain) = ?", domain)
		}
		var batch []*models.Account
		if err := query.Order("id").Limit(200).Find(&batch).Error; err != nil {
			return fmt.Errorf("loading accounts: %w", err)
		}
		if len(batch) == 0 {
			return nil
		}
		for _, account := range batch {
			last = account.Id
			fn(account)
		}
	}
}
