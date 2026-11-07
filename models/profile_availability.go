package models

import (
	"net/url"
	"strings"

	"gorm.io/gorm"
)

const (
	// ProfileAvailabilityAccountSuspended is an administrator suspension of
	// this account: accounts.is_ban, or a local login user's is_ban.
	// A viewer's personal block or mute is never this reason.
	ProfileAvailabilityAccountSuspended = "account_suspended"
	// ProfileAvailabilityDomainSuspended is an instance-wide node suspension.
	// It covers domain_blocks severity "suspend" (and the empty severity,
	// which DomainSeverities treats as suspend) and moderation.blocked_hosts.
	// Silenced hosts and severity "silence" are not suspensions.
	ProfileAvailabilityDomainSuspended = "domain_suspended"
)

// Public profile restriction precedence:
//
//  1. A node suspension on the account domain withholds the profile. Lookup
//     keeps HTTP 404. When the block is disclosable the body reason is
//     domain_suspended, including when the account row is also suspended.
//     An obfuscated domain_blocks row that covers the host suppresses the
//     reason, so the response stays indistinguishable from not found.
//     Private and public admin comments are never copied out.
//  2. Otherwise an account suspension is reported on the existing HTTP 200
//     account document as account_suspended. The status code stays 200.
//  3. Otherwise a disclosable node suspension that covers only the actor
//     origin host (not the acct domain) is attached to that same HTTP 200
//     document as domain_suspended. An obfuscated origin-only block adds
//     nothing and does not change the status code.
//
// blocked_hosts has no obfuscate flag. It is disclosable unless an obfuscated
// domain_blocks suspend row covers the same host. Membership matches the host
// and its parents (HostSet.Contains), the same walk already used for
// moderation.blocked_hosts and domain_blocks. Manual instance suspension
// (instance_metadata) only stops outbound delivery and is not a profile
// suspension.

// AccountSuspended reports whether an administrator has suspended account.
func AccountSuspended(conn *gorm.DB, account *Account) (bool, error) {
	if account == nil {
		return false, nil
	}
	if account.IsBan {
		return true, nil
	}
	if account.Domain != nil && strings.TrimSpace(*account.Domain) != "" {
		return false, nil
	}
	if account.User != nil {
		return account.User.IsBan, nil
	}
	if account.UserId == 0 || conn == nil {
		return false, nil
	}
	var banned bool
	err := queryTolerantOfMissingTable(conn, func(tx *gorm.DB) error {
		return tx.Model(&User{}).Where("id = ?", account.UserId).Pluck("is_ban", &banned).Error
	})
	if err != nil {
		if isMissingRelation(err) {
			return false, nil
		}
		return false, err
	}
	return banned, nil
}

// AccountOriginHost is the hostname of the cached actor id, without a port.
func AccountOriginHost(account *Account) string {
	if account == nil || strings.TrimSpace(account.OriginId) == "" {
		return ""
	}
	parsed, err := url.Parse(account.OriginId)
	if err != nil {
		return ""
	}
	return parsed.Hostname()
}

// NodeSuspension reports whether host is withheld by an instance-wide suspend.
// disclosable is false when an obfuscated domain_blocks suspend row covers
// the host. A blocked_hosts entry with no such row is disclosable.
func NodeSuspension(conn *gorm.DB, host string) (blocked bool, disclosable bool, err error) {
	host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	if host == "" {
		return false, false, nil
	}
	suspend, _, err := DomainSeverities(conn)
	if err != nil {
		return false, false, err
	}
	if !suspend.Contains(host) {
		return false, false, nil
	}
	hidden, err := obfuscatedSuspendCovers(conn, host)
	if err != nil {
		return false, false, err
	}
	return true, !hidden, nil
}

// obfuscatedSuspendCovers reports whether the most specific domain_blocks
// suspend row covering host has obfuscate set. No matching row means the
// suspension came from blocked_hosts and is not obfuscated.
func obfuscatedSuspendCovers(conn *gorm.DB, host string) (bool, error) {
	if conn == nil {
		return false, nil
	}
	var rows []DomainBlock
	err := queryTolerantOfMissingTable(conn, func(tx *gorm.DB) error {
		return tx.Select("domain", "severity", "obfuscate").Find(&rows).Error
	})
	if err != nil {
		if isMissingRelation(err) {
			return false, nil
		}
		return false, err
	}
	obfuscated := HostSet{}
	plain := HostSet{}
	for _, row := range rows {
		domain := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(row.Domain)), ".")
		if domain == "" {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(row.Severity)) {
		case "", "suspend":
			if row.Obfuscate {
				obfuscated[domain] = true
			} else {
				plain[domain] = true
			}
		}
	}
	for host != "" {
		if obfuscated[host] {
			return true, nil
		}
		if plain[host] {
			return false, nil
		}
		i := strings.IndexByte(host, '.')
		if i < 0 {
			return false, nil
		}
		host = host[i+1:]
	}
	return false, nil
}
