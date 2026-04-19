package models

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// ============================================================================
// Block
// ============================================================================

func TestBlock_TableName(t *testing.T) {
	b := Block{}
	assert.Equal(t, "blocks", b.TableName())
}

func TestBlock_Constants(t *testing.T) {
	assert.Equal(t, BlockTypeAccount, 0)
	assert.Equal(t, BlockTypeDomain, 1)
}

// ============================================================================
// Blocks.IsBlocked
// ============================================================================

func TestBlocks_IsBlocked_Empty(t *testing.T) {
	bb := Blocks{}
	account := &Account{Id: 1}
	assert.False(t, bb.IsBlocked(account))
}

func TestBlocks_IsBlocked_AccountBlock(t *testing.T) {
	bb := Blocks{
		&Block{AccountId: 1, Type: BlockTypeAccount, TargetAccountId: 99},
		&Block{AccountId: 1, Type: BlockTypeAccount, TargetAccountId: 100},
	}

	// Blocked by account 1
	account99 := &Account{Id: 99}
	assert.True(t, bb.IsBlocked(account99))

	account100 := &Account{Id: 100}
	assert.True(t, bb.IsBlocked(account100))

	// Not blocked
	account200 := &Account{Id: 200}
	assert.False(t, bb.IsBlocked(account200))
}

func TestBlocks_IsBlocked_DomainBlock(t *testing.T) {
	domain := "evil.com"
	bb := Blocks{
		&Block{AccountId: 1, Type: BlockTypeDomain, TargetDomain: "evil.com"},
	}

	// Account with matching domain
	account := &Account{Id: 2, Domain: &domain}
	assert.True(t, bb.IsBlocked(account))

	// Account without domain (local)
	localAccount := &Account{Id: 3}
	assert.False(t, bb.IsBlocked(localAccount))

	// Account with different domain
	otherDomain := "good.com"
	otherAccount := &Account{Id: 4, Domain: &otherDomain}
	assert.False(t, bb.IsBlocked(otherAccount))
}

func TestBlocks_IsBlocked_DomainBlock_NilDomain(t *testing.T) {
	bb := Blocks{
		&Block{AccountId: 1, Type: BlockTypeDomain, TargetDomain: "example.com"},
	}

	// nil domain should not match
	account := &Account{Id: 2, Domain: nil}
	assert.False(t, bb.IsBlocked(account))
}

func TestBlocks_IsBlocked_MixedBlockTypes(t *testing.T) {
	domain := "blocked.com"
	bb := Blocks{
		&Block{AccountId: 1, Type: BlockTypeDomain, TargetDomain: "blocked.com"},
		&Block{AccountId: 1, Type: BlockTypeAccount, TargetAccountId: 50},
	}

	// Domain blocked
	accountDomain := &Account{Id: 10, Domain: &domain}
	assert.True(t, bb.IsBlocked(accountDomain))

	// Account blocked
	account50 := &Account{Id: 50}
	assert.True(t, bb.IsBlocked(account50))

	// Neither
	account99 := &Account{Id: 99}
	assert.False(t, bb.IsBlocked(account99))
}

// ============================================================================
// Blocks.IsStatusBlocked
// ============================================================================

func TestBlocks_IsStatusBlocked_Empty(t *testing.T) {
	bb := Blocks{}
	status := &Status{Account: &Account{Id: 1}}
	assert.False(t, bb.IsStatusBlocked(status))
}

func TestBlocks_IsStatusBlocked_DirectAccountBlocked(t *testing.T) {
	bb := Blocks{
		&Block{AccountId: 1, Type: BlockTypeAccount, TargetAccountId: 99},
	}
	status := &Status{Account: &Account{Id: 99}}
	assert.True(t, bb.IsStatusBlocked(status))
}

func TestBlocks_IsStatusBlocked_ReblogAccountBlocked(t *testing.T) {
	bb := Blocks{
		&Block{AccountId: 1, Type: BlockTypeAccount, TargetAccountId: 88},
	}

	// Status without reblog — author not blocked
	statusNoReblog := &Status{
		Account: &Account{Id: 77},
	}
	assert.False(t, bb.IsStatusBlocked(statusNoReblog))

	// Status with reblog — reblog author IS blocked
	statusWithReblog := &Status{
		Account: &Account{Id: 77},
		Reblog: &Status{
			Account: &Account{Id: 88},
		},
	}
	assert.True(t, bb.IsStatusBlocked(statusWithReblog))
}

func TestBlocks_IsStatusBlocked_ReblogNil(t *testing.T) {
	bb := Blocks{
		&Block{AccountId: 1, Type: BlockTypeAccount, TargetAccountId: 99},
	}
	status := &Status{
		Account: &Account{Id: 77},
		Reblog:  nil,
	}
	assert.False(t, bb.IsStatusBlocked(status))
}

func TestBlocks_IsStatusBlocked_BothBlocked(t *testing.T) {
	domain := "spam.com"
	bb := Blocks{
		&Block{AccountId: 1, Type: BlockTypeDomain, TargetDomain: "spam.com"},
		&Block{AccountId: 1, Type: BlockTypeAccount, TargetAccountId: 99},
	}

	// Status author is domain-blocked
	status := &Status{
		Account: &Account{Id: 77, Domain: &domain},
	}
	assert.True(t, bb.IsStatusBlocked(status))

	// Reblog author is account-blocked
	status2 := &Status{
		Account: &Account{Id: 77},
		Reblog: &Status{
			Account: &Account{Id: 99},
		},
	}
	assert.True(t, bb.IsStatusBlocked(status2))
}
