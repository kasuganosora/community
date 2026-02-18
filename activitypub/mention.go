package activitypub

import (
	"net/url"
	"regexp"
	"strings"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
)

// Mention represents a parsed mention from ActivityPub content
type Mention struct {
	Username string
	Domain   string
	URI      string
	Acct     string // username@domain format
}

// mentionRegex matches @username@domain or @username formats
var mentionRegex = regexp.MustCompile(`@([a-zA-Z0-9_]+)(?:@([a-zA-Z0-9.\-]+))?`)

// htmlLinkRegex matches HTML anchor tags to extract mentions
var htmlLinkRegex = regexp.MustCompile(`<a[^>]+href="([^"]+)"[^>]*>@([^<]+)</a>`)

// ParseMentions extracts mentions from ActivityPub content
// It handles both plain text mentions (@user@domain) and HTML links
func ParseMentions(content string, tags []interface{}) []Mention {
	mentions := make(map[string]Mention)

	// First, check for HTML links (canonical way in ActivityPub)
	htmlMentions := parseHTMLMentions(content)
	for _, m := range htmlMentions {
		mentions[m.URI] = m
	}

	// Then check ActivityPub tag array for mentions
	tagMentions := parseTagMentions(tags)
	for _, m := range tagMentions {
		mentions[m.URI] = m
	}

	// Finally, check for plain text mentions (fallback)
	textMentions := parseTextMentions(content)
	for _, m := range textMentions {
		// Only add if not already found via HTML or tags
		if _, exists := mentions[m.URI]; !exists {
			if m.URI != "" {
				mentions[m.URI] = m
			}
		}
	}

	// Convert map to slice
	result := make([]Mention, 0, len(mentions))
	for _, m := range mentions {
		result = append(result, m)
	}

	return result
}

// parseHTMLMentions extracts mentions from HTML anchor tags
func parseHTMLMentions(content string) []Mention {
	var mentions []Mention

	matches := htmlLinkRegex.FindAllStringSubmatch(content, -1)
	for _, match := range matches {
		if len(match) >= 3 {
			href := match[1]
			text := match[2]

			// Check if this is a mention link (contains @ or is a user profile)
			if strings.HasPrefix(text, "@") || strings.Contains(href, "/users/") || strings.Contains(href, "/@") {
				mention := parseMentionFromURI(href)
				if mention.URI != "" {
					mentions = append(mentions, mention)
				}
			}
		}
	}

	return mentions
}

// parseTagMentions extracts mentions from ActivityPub tag array
func parseTagMentions(tags []interface{}) []Mention {
	var mentions []Mention

	for _, tag := range tags {
		tagMap, ok := tag.(map[string]interface{})
		if !ok {
			continue
		}

		tagType, _ := tagMap["type"].(string)
		if tagType != "Mention" {
			continue
		}

		href, _ := tagMap["href"].(string)
		name, _ := tagMap["name"].(string)

		if href == "" {
			continue
		}

		// Parse name (usually @username or @username@domain)
		username, domain := parseMentionName(name)

		// Extract from href if not found in name
		if username == "" {
			mention := parseMentionFromURI(href)
			username = mention.Username
			domain = mention.Domain
		}

		mentions = append(mentions, Mention{
			Username: username,
			Domain:   domain,
			URI:      href,
			Acct:     formatAcct(username, domain),
		})
	}

	return mentions
}

// parseTextMentions extracts plain text mentions from content
func parseTextMentions(content string) []Mention {
	var mentions []Mention
	seen := make(map[string]bool)

	matches := mentionRegex.FindAllStringSubmatch(content, -1)
	for _, match := range matches {
		if len(match) >= 2 {
			username := match[1]
			domain := ""
			if len(match) >= 3 && match[2] != "" {
				domain = match[2]
			}

			acct := formatAcct(username, domain)
			if seen[acct] {
				continue
			}
			seen[acct] = true

			// Build URI
			var uri string
			if domain != "" {
				uri = "https://" + domain + "/users/" + username
			}

			mentions = append(mentions, Mention{
				Username: username,
				Domain:   domain,
				URI:      uri,
				Acct:     acct,
			})
		}
	}

	return mentions
}

// parseMentionFromURI extracts mention info from a URI
func parseMentionFromURI(uri string) Mention {
	mention := Mention{URI: uri}

	parsed, err := url.Parse(uri)
	if err != nil {
		return mention
	}

	mention.Domain = parsed.Host

	// Extract username from path
	path := strings.TrimSuffix(parsed.Path, "/")

	// Handle various URI formats
	// https://domain.com/users/username
	// https://domain.com/@username
	// https://domain.com/profiles/username

	if strings.HasPrefix(path, "/users/") {
		mention.Username = strings.TrimPrefix(path, "/users/")
	} else if strings.HasPrefix(path, "/@") {
		mention.Username = strings.TrimPrefix(path, "/@")
	} else if strings.HasPrefix(path, "/profiles/") {
		mention.Username = strings.TrimPrefix(path, "/profiles/")
	}

	mention.Acct = formatAcct(mention.Username, mention.Domain)

	return mention
}

// parseMentionName extracts username and domain from a mention name
func parseMentionName(name string) (string, string) {
	name = strings.TrimSpace(name)
	name = strings.TrimPrefix(name, "@")

	parts := strings.SplitN(name, "@", 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return parts[0], ""
}

// formatAcct formats username and domain as acct string
func formatAcct(username, domain string) string {
	if domain != "" {
		return username + "@" + domain
	}
	return username
}

// ResolveMentions resolves mentions to account IDs
func ResolveMentions(mentions []Mention) map[string]uint64 {
	result := make(map[string]uint64)

	for _, mention := range mentions {
		var account models.Account

		// Try to find by origin ID first
		if mention.URI != "" {
			if err := db.GetDB().Where("origin_id = ?", mention.URI).First(&account).Error; err == nil {
				result[mention.Acct] = account.Id
				continue
			}
		}

		// Try to find by username and domain
		if mention.Domain != "" {
			if err := db.GetDB().Where("username = ? AND domain = ?", mention.Username, mention.Domain).First(&account).Error; err == nil {
				result[mention.Acct] = account.Id
				continue
			}
		}

		// Try to find local user
		if mention.Domain == "" {
			if err := db.GetDB().Where("username = ? AND domain IS NULL", mention.Username).First(&account).Error; err == nil {
				result[mention.Acct] = account.Id
			}
		}
	}

	return result
}

// GetMentionAccountIDs returns account IDs for all mentions
func GetMentionAccountIDs(content string, tags []interface{}) []uint64 {
	mentions := ParseMentions(content, tags)
	resolved := ResolveMentions(mentions)

	ids := make([]uint64, 0, len(resolved))
	for _, id := range resolved {
		ids = append(ids, id)
	}

	return ids
}
