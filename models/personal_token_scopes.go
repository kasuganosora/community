package models

import (
	"fmt"

	"gorm.io/gorm"
)

// Scope sources for PersonalAccessToken.ScopeSource.
const (
	ScopeSourceMigration = "migration"
	ScopeSourceBlob      = "blob"
	ScopeSourceEdit      = "edit"
)

// PersonalTokenScopeResult is the outcome of resolving a personal token's
// scopes. Mismatch, when set, describes a DB/blob difference to log once.
type PersonalTokenScopeResult struct {
	Scopes   *ApplicationScope
	TokenID  string
	Mismatch string
}

func scopeString(s *ApplicationScope) string {
	if s == nil {
		return "none"
	}
	return fmt.Sprintf("read=%#x write=%#x admin=%#x other=%#x", s.ReadScope, s.WriteScope, s.AdminScope, s.OtherScope)
}

func sameScopes(a, b *ApplicationScope) bool {
	return a != nil && b != nil && *a == *b
}

// PersonalTokenEffectiveScopes resolves the scopes a personal access token
// carries at request time. blob is the scope set decrypted from the token.
//
//   - Owner-edited bits ("edit") win: that is what editing is for.
//   - Bits copied from the blob ("blob") equal the blob by construction.
//   - Bits derived by the migration ("migration") never override the blob;
//     a difference is reported for ops and nothing is changed.
//   - A row without bits gets the blob persisted once (lazy backfill).
//
// Empty or missing data is never treated as "no scopes" or "all scopes":
// without a usable DB value the blob scopes apply unchanged.
func PersonalTokenEffectiveScopes(conn *gorm.DB, hash string, blob *ApplicationScope) (PersonalTokenScopeResult, error) {
	var meta PersonalAccessToken
	if err := conn.Select("id", "scope_bits", "scope_source").Where("token_hash = ?", hash).Limit(1).Find(&meta).Error; err != nil {
		return PersonalTokenScopeResult{}, err
	}
	res := PersonalTokenScopeResult{Scopes: blob, TokenID: meta.ID}
	if meta.ID == "" {
		return res, nil
	}
	if meta.ScopeBits != nil {
		if meta.ScopeSource == ScopeSourceEdit {
			res.Scopes = meta.ScopeBits.Scope()
		}
		if blob != nil && !sameScopes(meta.ScopeBits.Scope(), blob) {
			winner := "blob"
			if meta.ScopeSource == ScopeSourceEdit {
				winner = "db (edited)"
			}
			res.Mismatch = fmt.Sprintf("db[%s]{%s} blob{%s} using %s", meta.ScopeSource, scopeString(meta.ScopeBits.Scope()), scopeString(blob), winner)
		}
		return res, nil
	}
	if blob == nil {
		return res, nil
	}
	// Lazy backfill, once: the condition keeps a concurrent edit's bits.
	_ = conn.Model(&PersonalAccessToken{}).Where("id = ? AND scope_bits IS NULL", meta.ID).
		Select("scope_bits", "scope_source").Updates(&PersonalAccessToken{ScopeBits: (*ScopeBits)(blob), ScopeSource: ScopeSourceBlob}).Error
	return res, nil
}
