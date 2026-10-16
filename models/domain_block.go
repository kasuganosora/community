package models

import (
	"strings"
	"time"

	"github.com/kasuganosora/community/db/dialect"
	"gorm.io/gorm"
)

// DomainBlock represents an instance-level domain block (admin setting).
type DomainBlock struct {
	Id             uint64     `gorm:"primary_key;autoIncrement"    json:"id"`
	Domain         string     `gorm:"not null;uniqueIndex;size:255" json:"domain"`
	Severity       string     `gorm:"default:'suspend';size:32"     json:"severity"` // suspend, silence, noop
	RejectMedia    bool       `gorm:"default:true"                  json:"reject_media"`
	RejectReports  bool       `gorm:"default:true"                  json:"reject_reports"`
	PrivateComment string     `gorm:"type:text"                     json:"private_comment"`
	PublicComment  string     `gorm:"type:text"                     json:"public_comment"`
	Obfuscate      bool       `gorm:"default:false"                 json:"obfuscate"`
	CreatedAt      *time.Time `gorm:"autoCreateTime"                json:"created_at"`
	UpdatedAt      *time.Time `gorm:"autoUpdateTime"                json:"updated_at"`
}

func (DomainBlock) TableName() string {
	return "domain_blocks"
}

type DomainBlocks []*DomainBlock

func isMissingRelation(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "no such table") || strings.Contains(msg, "doesn't exist") || strings.Contains(msg, "does not exist") || strings.Contains(msg, "undefined table")
}

// queryTolerantOfMissingTable runs query so that a missing-table error can be
// swallowed by the caller. PostgreSQL aborts the surrounding transaction on
// any error, so there the query runs in its own (nested) transaction, which
// GORM turns into a savepoint when conn is already inside one.
func queryTolerantOfMissingTable(conn *gorm.DB, query func(tx *gorm.DB) error) error {
	if !dialect.IsPostgres(conn) {
		return query(conn)
	}

	return conn.Transaction(query)
}

// DomainSeverities loads instance blocks. suspend hides the domain everywhere;
// silence only drops it from public timelines. A missing table is treated as
// no policy so partial test schemas keep working.
//
// The instance moderation lists (Misskey's blockedHosts / silencedHosts,
// registered through ModerationHostLists) are merged in: a blocked host is
// treated like a suspended domain, a silenced host like a silenced one.
// Both sets match subdomains (see HostSet.Contains).
func DomainSeverities(conn *gorm.DB) (suspend HostSet, silence HostSet, err error) {
	suspend = HostSet{}
	silence = HostSet{}
	if hook := ModerationHostLists; hook != nil {
		blocked, silenced := hook()
		for _, h := range blocked {
			suspend[h] = true
		}
		for _, h := range silenced {
			silence[h] = true
		}
	}
	if conn == nil {
		return suspend, silence, nil
	}
	var rows []DomainBlock
	err = queryTolerantOfMissingTable(conn, func(tx *gorm.DB) error {
		return tx.Select("domain", "severity").Find(&rows).Error
	})
	if err != nil {
		if isMissingRelation(err) {
			return suspend, silence, nil
		}
		return nil, nil, err
	}
	for _, row := range rows {
		domain := strings.ToLower(strings.TrimSpace(row.Domain))
		if domain == "" {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(row.Severity)) {
		case "", "suspend":
			suspend[domain] = true
		case "silence":
			silence[domain] = true
		}
	}
	return suspend, silence, nil
}

func domainListed(set map[string]bool, domain string) bool {
	return HostSet(set).Contains(domain)
}

// ModerationHostLists returns the normalised (lower-case) blocked and
// silenced host lists from the instance moderation settings. It is set by
// the moderation package; nil means no such lists.
var ModerationHostLists func() (blocked, silenced []string)

// HostSet is a set of lower-case host names. Membership matches the host
// itself and every subdomain of it, like Misskey's isBlockedHost:
// ".sub.example.com" ends with ".example.com".
type HostSet map[string]bool

// Contains reports whether host or one of its parent domains is in the set.
func (s HostSet) Contains(host string) bool {
	if len(s) == 0 {
		return false
	}
	host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	for host != "" {
		if s[host] {
			return true
		}
		i := strings.IndexByte(host, '.')
		if i < 0 {
			return false
		}
		host = host[i+1:]
	}
	return false
}

// FilterAccountsByDomainPolicy drops accounts on a suspended instance domain
// or a domain the viewer has blocked.
func FilterAccountsByDomainPolicy(conn *gorm.DB, viewer *Account, accounts []*Account) ([]*Account, error) {
	if len(accounts) == 0 {
		return accounts, nil
	}
	blocked := map[string]bool{}
	suspend, _, err := DomainSeverities(conn)
	if err != nil {
		return nil, err
	}
	for domain := range suspend {
		blocked[domain] = true
	}
	if viewer != nil && conn != nil {
		var rows []Block
		err = queryTolerantOfMissingTable(conn, func(tx *gorm.DB) error {
			return tx.Where("account_id = ? AND type = ?", viewer.Id, BlockTypeDomain).Find(&rows).Error
		})
		if err != nil {
			if !isMissingRelation(err) {
				return nil, err
			}
		} else {
			for _, row := range rows {
				domain := strings.ToLower(strings.TrimSpace(row.TargetDomain))
				if domain != "" {
					blocked[domain] = true
				}
			}
		}
	}
	if len(blocked) == 0 {
		return accounts, nil
	}
	out := make([]*Account, 0, len(accounts))
	for _, account := range accounts {
		if account != nil && account.Domain != nil && domainListed(blocked, *account.Domain) {
			continue
		}
		out = append(out, account)
	}
	return out, nil
}
