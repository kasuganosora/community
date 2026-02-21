package commands

import (
	"context"
	"fmt"
	"net"
	"strings"

	"github.com/kasuganosora/community/db"
	"github.com/urfave/cli/v2"
)

// EmailDomainBlock represents an email domain block
type EmailDomainBlock struct {
	ID        uint64 `json:"id" gorm:"primaryKey;autoIncrement"`
	Domain    string `json:"domain" gorm:"unique;not null;type:varchar(255)"`
	CreatedAt string `json:"created_at"`
}

func (EmailDomainBlock) TableName() string {
	return "email_domain_blocks"
}

// EmailDomainBlocksCommand implements tootctl email-domain-blocks subcommands
func EmailDomainBlocksCommand() *cli.Command {
	return &cli.Command{
		Name:  "email-domain-blocks",
		Usage: "Manage email domain blocks",
		Subcommands: []*cli.Command{
			EmailDomainBlocksListCommand(),
			EmailDomainBlocksAddCommand(),
			EmailDomainBlocksRemoveCommand(),
		},
	}
}

// EmailDomainBlocksListCommand implements tootctl email-domain-blocks list
// Version history: 3.2.0 - added
func EmailDomainBlocksListCommand() *cli.Command {
	return &cli.Command{
		Name:   "list",
		Usage:  "List all currently blocked email domains",
		Action: listEmailDomainBlocks,
	}
}

func listEmailDomainBlocks(c *cli.Context) error {
	var blocks []EmailDomainBlock
	if err := db.GetDB().Order("domain ASC").Find(&blocks).Error; err != nil {
		return fmt.Errorf("failed to list email domain blocks: %w", err)
	}

	if len(blocks) == 0 {
		fmt.Println("No email domain blocks found.")
		return nil
	}

	fmt.Printf("%-6s %-40s %s\n", "ID", "Domain", "Created At")
	fmt.Println(strings.Repeat("-", 70))

	for _, block := range blocks {
		fmt.Printf("%-6d %-40s %s\n", block.ID, block.Domain, block.CreatedAt)
	}

	fmt.Printf("\nTotal: %d blocked email domains\n", len(blocks))
	return nil
}

// EmailDomainBlocksAddCommand implements tootctl email-domain-blocks add
// Version history: 3.2.0 - added
func EmailDomainBlocksAddCommand() *cli.Command {
	return &cli.Command{
		Name:      "add",
		Usage:     "Add entries to the email domain blocklist",
		ArgsUsage: "DOMAIN[...]",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "with-dns-records",
				Usage: "Also lookup A, AAAA, and MX records and block them",
			},
		},
		Action: addEmailDomainBlocks,
	}
}

func addEmailDomainBlocks(c *cli.Context) error {
	if c.NArg() < 1 {
		return fmt.Errorf("DOMAIN is required")
	}

	domains := c.Args().Slice()
	withDNSRecords := c.Bool("with-dns-records")

	added := 0
	for _, domain := range domains {
		// Normalize domain
		domain = strings.ToLower(strings.TrimSpace(domain))

		// Check if already blocked
		var existing EmailDomainBlock
		if err := db.GetDB().Where("domain = ?", domain).First(&existing).Error; err == nil {
			fmt.Printf("Domain %s is already blocked\n", domain)
			continue
		}

		// Add block
		block := EmailDomainBlock{
			Domain:    domain,
			CreatedAt: "now()",
		}
		if err := db.GetDB().Create(&block).Error; err != nil {
			fmt.Printf("Error blocking %s: %v\n", domain, err)
			continue
		}
		added++
		fmt.Printf("Blocked email domain: %s\n", domain)

		// Lookup DNS records if requested
		if withDNSRecords {
			relatedDomains := lookupDNSRecords(domain)
			for _, relatedDomain := range relatedDomains {
				if err := db.GetDB().Where("domain = ?", relatedDomain).First(&existing).Error; err != nil {
					block := EmailDomainBlock{
						Domain:    relatedDomain,
						CreatedAt: "now()",
					}
					if err := db.GetDB().Create(&block).Error; err == nil {
						added++
						fmt.Printf("  Also blocked: %s\n", relatedDomain)
					}
				}
			}
		}
	}

	fmt.Printf("\nAdded %d email domain blocks\n", added)
	return nil
}

func lookupDNSRecords(domain string) []string {
	var results []string
	ctx := context.Background()

	// Lookup A records
	aRecords, _ := net.DefaultResolver.LookupHost(ctx, domain)
	for _, ip := range aRecords {
		// Reverse lookup to get domain from IP
		names, err := net.DefaultResolver.LookupAddr(ctx, ip)
		if err == nil {
			for _, name := range names {
				name = strings.TrimSuffix(name, ".")
				if name != domain {
					results = append(results, name)
				}
			}
		}
	}

	// Lookup MX records
	mxRecords, _ := net.DefaultResolver.LookupMX(ctx, domain)
	for _, mx := range mxRecords {
		mxDomain := strings.TrimSuffix(mx.Host, ".")
		if mxDomain != domain {
			results = append(results, mxDomain)
		}
	}

	return results
}

// EmailDomainBlocksRemoveCommand implements tootctl email-domain-blocks remove
// Version history: 3.2.0 - added
func EmailDomainBlocksRemoveCommand() *cli.Command {
	return &cli.Command{
		Name:      "remove",
		Usage:     "Remove entries from the email domain blocklist",
		ArgsUsage: "DOMAIN[...]",
		Action:    removeEmailDomainBlocks,
	}
}

func removeEmailDomainBlocks(c *cli.Context) error {
	if c.NArg() < 1 {
		return fmt.Errorf("DOMAIN is required")
	}

	domains := c.Args().Slice()

	removed := 0
	for _, domain := range domains {
		domain = strings.ToLower(strings.TrimSpace(domain))

		result := db.GetDB().Where("domain = ?", domain).Delete(&EmailDomainBlock{})
		if result.Error != nil {
			fmt.Printf("Error unblocking %s: %v\n", domain, result.Error)
			continue
		}

		if result.RowsAffected > 0 {
			removed++
			fmt.Printf("Unblocked email domain: %s\n", domain)
		} else {
			fmt.Printf("Domain %s was not blocked\n", domain)
		}
	}

	fmt.Printf("\nRemoved %d email domain blocks\n", removed)
	return nil
}

// IsEmailDomainBlocked checks if an email domain is blocked
func IsEmailDomainBlocked(email string) bool {
	parts := strings.Split(email, "@")
	if len(parts) != 2 {
		return false
	}

	domain := strings.ToLower(parts[1])

	// Check direct match
	var block EmailDomainBlock
	if err := db.GetDB().Where("domain = ?", domain).First(&block).Error; err == nil {
		return true
	}

	// Check parent domains (e.g., sub.example.com -> example.com)
	domainParts := strings.Split(domain, ".")
	for i := 1; i < len(domainParts)-1; i++ {
		parentDomain := strings.Join(domainParts[i:], ".")
		if err := db.GetDB().Where("domain = ?", parentDomain).First(&block).Error; err == nil {
			return true
		}
	}

	return false
}

// SyncEmailDomainBlocksToCache syncs blocked domains to cache
func SyncEmailDomainBlocksToCache() error {
	redis := db.GetRedis()
	if redis == nil {
		return nil
	}

	var blocks []EmailDomainBlock
	if err := db.GetDB().Find(&blocks).Error; err != nil {
		return err
	}

	ctx := context.Background()
	for _, block := range blocks {
		redis.SAdd(ctx, "blocked_email_domains", block.Domain)
	}

	return nil
}

// GetEmailDomainBlockStats returns statistics about email domain blocks
func GetEmailDomainBlockStats() (map[string]interface{}, error) {
	var count int64
	if err := db.GetDB().Model(&EmailDomainBlock{}).Count(&count).Error; err != nil {
		return nil, err
	}

	return map[string]interface{}{
		"total_blocked_domains": count,
	}, nil
}
