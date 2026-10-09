package models

import (
	"errors"
	"strings"
	"time"

	"golang.org/x/net/idna"
	"gorm.io/gorm"
)

// EmailDomainBlock forbids sign-ups from an e-mail domain and its subdomains.
type EmailDomainBlock struct {
	ID        uint64     `gorm:"primaryKey;autoIncrement"          json:"id"`
	Domain    string     `gorm:"unique;not null;type:varchar(255)" json:"domain"`
	CreatedAt *time.Time `gorm:"autoCreateTime"                    json:"created_at"`
}

func (EmailDomainBlock) TableName() string { return "email_domain_blocks" }

// NormalizeEmailDomain lowercases a domain, strips a leading "@" and
// converts it to its ASCII (punycode) form.
func NormalizeEmailDomain(domain string) string {
	domain = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(domain), "@")))
	domain = strings.TrimSuffix(domain, ".")
	if ascii, err := idna.Lookup.ToASCII(domain); err == nil {
		domain = ascii
	}
	return domain
}

// EmailDomainCandidates returns the domain of email followed by its parent
// domains ("a.b.example.com" -> a.b.example.com, b.example.com,
// example.com), excluding the bare TLD.
func EmailDomainCandidates(email string) []string {
	at := strings.LastIndex(email, "@")
	if at < 0 {
		return nil
	}
	domain := NormalizeEmailDomain(email[at+1:])
	if domain == "" {
		return nil
	}
	parts := strings.Split(domain, ".")
	out := make([]string, 0, len(parts))
	for i := 0; i < len(parts)-1 || i == 0; i++ {
		out = append(out, strings.Join(parts[i:], "."))
	}
	return out
}

// EmailDomainBlocked reports whether email's domain or a parent domain is
// blocked. A missing table means nothing is blocked.
func EmailDomainBlocked(conn *gorm.DB, email string) (bool, error) {
	candidates := EmailDomainCandidates(email)
	if len(candidates) == 0 {
		return false, nil
	}
	var n int64
	if err := conn.Model(&EmailDomainBlock{}).Where("domain IN ?", candidates).Count(&n).Error; err != nil {
		if !conn.Migrator().HasTable(&EmailDomainBlock{}) {
			return false, nil
		}
		return false, err
	}
	return n > 0, nil
}

// ErrEmailDomainBlocked is the Mastodon-style validation message.
var ErrEmailDomainBlocked = errors.New("E-mail provider is not allowed")
