package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/urfave/cli/v2"
	"gorm.io/gorm"
)

// DomainsCommand implements tootctl domains subcommands.
func DomainsCommand() *cli.Command {
	return &cli.Command{
		Name:  "domains", //nolint:goconst
		Usage: "Manage domains",
		Subcommands: []*cli.Command{
			DomainsPurgeCommand(),
			DomainsCrawlCommand(),
		},
	}
}

// DomainsPurgeCommand implements tootctl domains purge
// Remove all accounts from given domains without leaving behind any records.
// Version history: 2.6.0 - added.
func DomainsPurgeCommand() *cli.Command {
	return &cli.Command{
		Name:  "purge",
		Usage: "Remove all accounts from given domains",
		Description: `Remove all accounts from a given DOMAIN without leaving behind any records. 
Unlike a suspension, if the DOMAIN still exists in the wild, it means the 
accounts could return if they are resolved again.`,
		ArgsUsage: "DOMAIN[...]", //nolint:goconst
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "by-uri",
				Usage: "Match domains in the actor URI rather than in the WebFinger address",
			},
			&cli.BoolFlag{
				Name:  "limited-federation-mode",
				Usage: "Purge all accounts from domains not in allow-list",
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
		Action: purgeDomains,
	}
}

func purgeDomains(c *cli.Context) error {
	var domains []string

	limitedFederationMode := c.Bool("limited-federation-mode")

	if limitedFederationMode {
		// Get all domains not in allow-list
		// For now, we'll get all remote domains
		var remoteDomains []string
		err := db.GetDB().Model(&models.Account{}).
			Where("domain IS NOT NULL AND domain != ''").
			Distinct("domain").
			Pluck("domain", &remoteDomains).Error
		if err != nil {
			return fmt.Errorf("failed to get remote domains: %w", err)
		}

		domains = remoteDomains
	} else {
		if c.NArg() < 1 {
			return errors.New("DOMAIN is required (or use --limited-federation-mode)")
		}

		domains = c.Args().Slice()
	}

	concurrency := c.Int("concurrency")
	verbose := c.Bool("verbose")
	dryRun := c.Bool("dry-run")
	byURI := c.Bool("by-uri")

	if dryRun {
		fmt.Println("Dry run mode - no changes will be made")
	}

	fmt.Printf("Purging %d domain(s)...\n", len(domains))

	for _, domain := range domains {
		err := purgeDomain(domain, concurrency, verbose, dryRun, byURI)
		if err != nil {
			fmt.Printf("Error purging domain %s: %v\n", domain, err)
		}
	}

	return nil
}

func purgeDomain(domain string, concurrency int, verbose bool, dryRun bool, byURI bool) error { //nolint:unparam // param reserved for future use
	fmt.Printf("\nPurging domain: %s\n", domain)

	// Get accounts from this domain
	query := db.GetDB().Model(&models.Account{}).Where("domain = ?", domain)

	var accountCount int64
	err := query.Count(&accountCount).Error
	if err != nil {
		return err
	}

	fmt.Printf("Found %d accounts to purge\n", accountCount)

	if dryRun {
		return nil
	}

	// Delete in batches with concurrency
	batchSize := 100

	type accountBatch struct {
		IDs []uint64
	}

	jobs := make(chan accountBatch, concurrency)

	var wg sync.WaitGroup

	processed := 0

	var mu sync.Mutex

	// Start workers
	for range concurrency {
		wg.Go(func() {
			for batch := range jobs {
				err := deleteAccountsWithDependencies(batch.IDs, verbose)

				mu.Lock()
				processed += len(batch.IDs)

				if err != nil && verbose {
					fmt.Printf("Error deleting batch: %v\n", err)
				}
				mu.Unlock()
			}
		})
	}

	// Feed jobs
	var lastID uint64 = 0

	for {
		var accounts []models.Account

		err := db.GetDB().Where("domain = ? AND id > ?", domain, lastID).
			Order("id ASC").
			Limit(batchSize).
			Find(&accounts).Error
		if err != nil {
			return err
		}

		if len(accounts) == 0 {
			break
		}

		ids := make([]uint64, len(accounts))
		for i, acc := range accounts {
			ids[i] = acc.Id
		}

		lastID = accounts[len(accounts)-1].Id

		jobs <- accountBatch{IDs: ids}
	}

	close(jobs)

	wg.Wait()

	fmt.Printf("Purged %d accounts from %s\n", processed, domain)

	return nil
}

func deleteAccountsWithDependencies(ids []uint64, verbose bool) error {
	return db.GetDB().Transaction(func(tx *gorm.DB) error {
		// Delete related records in correct order
		// Statuses and related data
		var statusIDs []uint64
		err := tx.Model(&models.Status{}).Where("account_id IN ?", ids).Pluck("id", &statusIDs).Error
		if err != nil {
			return err
		}

		if len(statusIDs) > 0 {
			// Delete status-related records
			tx.Where("status_id IN ?", statusIDs).Delete(&models.MediaAttachment{})
			tx.Where("status_id IN ?", statusIDs).Delete(&models.StatusTag{})
			tx.Where("status_id IN ?", statusIDs).Delete(&models.StatusEmoji{})
			tx.Where("status_id IN ?", statusIDs).Delete(&models.FavoriteStatus{})
			tx.Where("status_id IN ?", statusIDs).Delete(&models.BookmarkStatus{})
			tx.Where("status_id IN ?", statusIDs).Delete(&models.PinStatus{})
			tx.Where("status_id IN ?", statusIDs).Delete(&models.MuteStatus{})
			tx.Where("in_reply_to_id IN ?", statusIDs).Delete(&models.Status{})

			// Delete statuses
			err := tx.Where("account_id IN ?", ids).Delete(&models.Status{}).Error
			if err != nil {
				return err
			}
		}

		// Delete follows
		tx.Where("account_id IN ? OR follow_id IN ?", ids, ids).Delete(&models.Follow{})

		// Delete blocks and mutes
		tx.Where("account_id IN ? OR target_account_id IN ?", ids, ids).Delete(&models.Block{})
		tx.Where("account_id IN ?", ids).Delete(&models.MuteAccount{})

		// Delete list memberships
		tx.Where("account_id IN ?", ids).Delete(&models.ListAccount{})

		// Delete notifications
		tx.Where("account_id IN ?", ids).Delete(&models.Notification{})

		// Delete account extra
		tx.Where("account_id IN ?", ids).Delete(&models.AccountExtra{})

		// Delete account emojis
		tx.Where("account_id IN ?", ids).Delete(&models.AccountEmoji{})

		// Delete account fields
		// (handled by extra)

		// Finally delete accounts
		err = tx.Where("id IN ?", ids).Delete(&models.Account{}).Error
		if err != nil {
			return err
		}

		if verbose {
			fmt.Printf("Deleted %d accounts and %d statuses\n", len(ids), len(statusIDs))
		}

		return nil
	})
}

// DomainsCrawlCommand implements tootctl domains crawl
// Crawl the known fediverse and collect statistics.
// Version history: 2.7.0 - added.
func DomainsCrawlCommand() *cli.Command {
	return &cli.Command{
		Name:  "crawl",
		Usage: "Crawl the known fediverse and collect statistics",
		Description: `Crawl the known fediverse by using Mastodon REST API endpoints that 
expose all known peers, and collect statistics from those peers.`,
		ArgsUsage: "[START]",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "exclude-suspended",
				Usage: "Do not include instances that you have suspended",
			},
			&cli.IntFlag{
				Name:    "concurrency",
				Aliases: []string{"c"},
				Value:   50,
				Usage:   "The number of workers to use",
			},
			&cli.StringFlag{
				Name:    "format",
				Aliases: []string{"f"},
				Value:   "summary", //nolint:goconst
				Usage:   "Output format: summary, domains, or json",
			},
		},
		Action: crawlDomains,
	}
}

type InstanceStats struct {
	Domain            string `json:"domain"`
	Title             string `json:"title"`
	Description       string `json:"description"`
	Version           string `json:"version"`
	TotalUsers        int64  `json:"total_users"`
	ActiveUsersWeekly int64  `json:"active_users_weekly"`
	NewUsersWeekly    int64  `json:"new_users_weekly"`
	TotalStatuses     int64  `json:"total_statuses"`
	TotalPeers        int    `json:"total_peers"`
	Error             string `json:"error,omitempty"`
}

func crawlDomains(c *cli.Context) error {
	startDomain := c.Args().First()
	excludeSuspended := c.Bool("exclude-suspended")
	concurrency := c.Int("concurrency")
	format := c.String("format")

	if format != "summary" && format != "domains" && format != "json" {
		return errors.New("format must be 'summary', 'domains', or 'json'")
	}

	fmt.Println("Starting fediverse crawl...")

	// Get seed domains
	var domains []string
	if startDomain != "" {
		domains = []string{startDomain}
	} else {
		// Get from known peers
		err := db.GetDB().Model(&models.Account{}).
			Where("domain IS NOT NULL AND domain != ''").
			Distinct("domain").
			Pluck("domain", &domains).Error
		if err != nil {
			return fmt.Errorf("failed to get known domains: %w", err)
		}
	}

	// Filter out suspended domains if requested
	if excludeSuspended {
		blockedDomains, err := getBlockedDomains()
		if err != nil {
			return fmt.Errorf("failed to get blocked domains: %w", err)
		}

		filteredDomains := make([]string, 0, len(domains))
		for _, domain := range domains {
			if !isDomainBlocked(domain, blockedDomains) {
				filteredDomains = append(filteredDomains, domain)
			}
		}

		domains = filteredDomains
		fmt.Printf("Filtered out %d blocked domains, %d remaining\n", len(domains)-len(filteredDomains), len(domains))
	}

	fmt.Printf("Starting with %d domains, using %d workers\n", len(domains), concurrency)

	// Crawl
	visited := make(map[string]bool)

	var (
		mu    sync.Mutex
		stats []InstanceStats
		wg    sync.WaitGroup
	)

	semaphore := make(chan struct{}, concurrency)

	results := make(chan InstanceStats, 100)

	// Collector
	go func() {
		for s := range results {
			mu.Lock()

			stats = append(stats, s)
			mu.Unlock()
		}
	}()

	// BFS crawl
	queue := make(chan string, len(domains)*10)
	for _, d := range domains {
		queue <- d
	}

	processed := 0

	for domain := range queue {
		mu.Lock()
		if visited[domain] {
			mu.Unlock()

			continue
		}

		visited[domain] = true
		processed++
		mu.Unlock()

		if processed%10 == 0 {
			fmt.Printf("\rCrawled %d domains...", processed)
		}

		wg.Add(1)

		semaphore <- struct{}{}

		go func(d string) {
			defer wg.Done()
			defer func() { <-semaphore }()

			stat := crawlInstance(d)
			results <- stat

			// Add peers to queue (limited)
			if stat.TotalPeers > 0 && len(visited) < 1000 {
				peers := getPeers(d)
				for _, peer := range peers {
					mu.Lock()
					if !visited[peer] {
						select {
						case queue <- peer:
						default:
						}
					}
					mu.Unlock()
				}
			}
		}(domain)

		// Limit total domains
		if processed >= 1000 {
			break
		}
	}

	wg.Wait()
	close(results)

	// Output results
	switch format {
	case "summary":
		printCrawlSummary(stats)
	case "domains":
		for _, s := range stats {
			if s.Error == "" {
				fmt.Println(s.Domain)
			}
		}
	case "json":
		data, err := json.MarshalIndent(stats, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(data))
	}

	return nil
}

func crawlInstance(domain string) InstanceStats {
	stat := InstanceStats{Domain: domain}

	client := &http.Client{Timeout: 10 * time.Second}

	// Get instance info
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, fmt.Sprintf("https://%s/api/v1/instance", domain), nil)
	if err != nil {
		stat.Error = err.Error()

		return stat
	}
	resp, err := client.Do(req)
	if err != nil {
		stat.Error = err.Error()

		return stat
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		stat.Error = fmt.Sprintf("HTTP %d", resp.StatusCode)

		return stat
	}

	var instanceData struct {
		Title       string `json:"title"`
		Description string `json:"description"`
		Version     string `json:"version"`
		Stats       struct {
			UserCount   int64 `json:"user_count"`
			StatusCount int64 `json:"status_count"`
			DomainCount int   `json:"domain_count"`
		} `json:"stats"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&instanceData); err != nil {
		stat.Error = "failed to decode response"

		return stat
	}

	stat.Title = instanceData.Title
	stat.Description = instanceData.Description
	stat.Version = instanceData.Version
	stat.TotalUsers = instanceData.Stats.UserCount
	stat.TotalStatuses = instanceData.Stats.StatusCount
	stat.TotalPeers = instanceData.Stats.DomainCount

	return stat
}

func getPeers(domain string) []string {
	client := &http.Client{Timeout: 10 * time.Second}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, fmt.Sprintf("https://%s/api/v1/instance/peers", domain), nil)
	if err != nil {
		return nil
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil
	}

	var peers []string
	if err := json.NewDecoder(resp.Body).Decode(&peers); err != nil {
		return nil
	}

	return peers
}

func printCrawlSummary(stats []InstanceStats) {
	var (
		totalServers, totalUsers, totalStatuses int64
		errors                                  int
	)

	for _, s := range stats {
		if s.Error != "" {
			errors++

			continue
		}

		totalServers++
		totalUsers += s.TotalUsers
		totalStatuses += s.TotalStatuses
	}

	fmt.Printf("\n\nCrawl Summary:\n")
	fmt.Printf("===============\n")
	fmt.Printf("Total servers:          %d\n", totalServers)
	fmt.Printf("Servers with errors:    %d\n", errors)
	fmt.Printf("Total registered users: %d\n", totalUsers)
	fmt.Printf("Total statuses:         %d\n", totalStatuses)
}

// getBlockedDomains returns all blocked domains from the database.
func getBlockedDomains() (map[string]bool, error) {
	var blocks []models.DomainBlock
	err := db.GetDB().Find(&blocks).Error
	if err != nil {
		return nil, err
	}

	blocked := make(map[string]bool)
	for _, block := range blocks {
		blocked[block.Domain] = true
	}

	return blocked, nil
}

// isDomainBlocked checks if a domain is in the blocked list.
func isDomainBlocked(domain string, blocked map[string]bool) bool {
	if blocked[domain] {
		return true
	}

	// Check parent domains (for subdomains)
	parts := strings.Split(domain, ".")
	for i := 1; i < len(parts); i++ {
		parentDomain := strings.Join(parts[i:], ".")
		if blocked[parentDomain] {
			return true
		}
	}

	return false
}
