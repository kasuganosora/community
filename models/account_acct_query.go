package models

import (
	"strings"

	"github.com/kasuganosora/community/db/dialect"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// WhereAcct restricts query to accounts named username on domain, where an
// empty domain means local accounts (domain IS NULL). The username is matched
// case-insensitively, exactly or (prefix=true) as a prefix; the domain is
// always matched exactly, so "luna@remote.example" never returns a local
// "luna" and vice versa. Use OrderAcctExactFirst to rank exact names first.
func WhereAcct(query *gorm.DB, username, domain string, prefix bool) *gorm.DB {
	if prefix {
		query = query.Where("accounts.username "+dialect.ILike(query)+" ? "+dialect.LikeEscape(query), EscapeLike(username)+"%")
	} else {
		query = query.Where("LOWER(accounts.username) = ?", strings.ToLower(username))
	}
	if domain == "" {
		return query.Where("accounts.domain IS NULL")
	}
	return query.Where("LOWER(accounts.domain) = ?", strings.ToLower(domain))
}

// OrderAcctExactFirst orders accounts whose username equals username
// (case-insensitively) before prefix matches.
func OrderAcctExactFirst(query *gorm.DB, username string) *gorm.DB {
	// gorm's Order ignores *gorm.DB / gorm.Expr values; a clause.OrderBy with
	// an expression is the supported way to bind parameters in ORDER BY.
	return query.Order(clause.OrderBy{Expression: clause.Expr{
		SQL:                "CASE WHEN LOWER(accounts.username) = ? THEN 0 ELSE 1 END, accounts.id ASC",
		Vars:               []any{strings.ToLower(username)},
		WithoutParentheses: true,
	}})
}

// EscapeLike escapes LIKE wildcards; pair it with dialect.LikeEscape.
func EscapeLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, "%", `\%`)
	s = strings.ReplaceAll(s, "_", `\_`)
	return s
}
