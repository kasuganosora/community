package models

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestFilter_GetFilterActionString(t *testing.T) {
	tests := []struct {
		action   uint8
		expected string
	}{
		{FilterActionNone, "none"}, //nolint:goconst
		{FilterActionWarn, "warn"},
		{FilterActionHide, "hide"},
		{99, "none"}, // default
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			filter := &Filter{FilterAction: tt.action}
			result := filter.GetFilterActionString()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestFilter_GetStatusFilterAction(t *testing.T) {
	t.Run("No filter action", func(t *testing.T) {
		filter := &Filter{
			FilterAction: FilterActionNone,
			Keywords: []*FilterKeyword{
				{Keyword: "test", WholeWord: false}, //nolint:goconst
			},
		}
		status := &Status{Content: StatusContent{Content: "test content"}}
		result := filter.GetStatusFilterAction(status)
		assert.Equal(t, uint8(FilterActionNone), result)
	})

	t.Run("Whole word match", func(t *testing.T) {
		filter := &Filter{
			FilterAction: FilterActionWarn,
			Keywords: []*FilterKeyword{
				{Keyword: "spam", WholeWord: true}, //nolint:goconst
			},
		}
		status := &Status{Content: StatusContent{Content: "spam"}}
		result := filter.GetStatusFilterAction(status)
		assert.Equal(t, uint8(FilterActionWarn), result)
	})

	t.Run("Partial word match", func(t *testing.T) {
		filter := &Filter{
			FilterAction: FilterActionHide,
			Keywords: []*FilterKeyword{
				{Keyword: "bad", WholeWord: false},
			},
		}
		status := &Status{Content: StatusContent{Content: "This is badword"}}
		result := filter.GetStatusFilterAction(status)
		assert.Equal(t, uint8(FilterActionHide), result)
	})

	t.Run("Match in spoiler text", func(t *testing.T) {
		filter := &Filter{
			FilterAction: FilterActionWarn,
			Keywords: []*FilterKeyword{
				{Keyword: "warning", WholeWord: false},
			},
		}
		status := &Status{Content: StatusContent{Content: "Content", SpoilerText: "This is a warning"}}
		result := filter.GetStatusFilterAction(status)
		assert.Equal(t, uint8(FilterActionWarn), result)
	})

	t.Run("No match", func(t *testing.T) {
		filter := &Filter{
			FilterAction: FilterActionHide,
			Keywords: []*FilterKeyword{
				{Keyword: "blocked", WholeWord: false},
			},
		}
		status := &Status{Content: StatusContent{Content: "Good content"}}
		result := filter.GetStatusFilterAction(status)
		assert.Equal(t, uint8(FilterActionNone), result)
	})

	t.Run("Whole word inside a sentence", func(t *testing.T) {
		filter := &Filter{
			FilterAction: FilterActionWarn,
			Keywords:     []*FilterKeyword{{Keyword: "spam", WholeWord: true}},
		}
		status := &Status{Content: StatusContent{Content: "this is SPAM today"}}
		assert.Equal(t, uint8(FilterActionWarn), filter.GetStatusFilterAction(status))
	})

	t.Run("Whole word does not match inside another word", func(t *testing.T) {
		filter := &Filter{
			FilterAction: FilterActionHide,
			Keywords:     []*FilterKeyword{{Keyword: "spam", WholeWord: true}},
		}
		status := &Status{Content: StatusContent{Content: "antispam"}}
		assert.Equal(t, uint8(FilterActionNone), filter.GetStatusFilterAction(status))
	})
}

func TestFilters_GetStatusFilters(t *testing.T) {
	now := time.Now()
	account := &Account{Id: 1}
	otherAccount := &Account{Id: 2}

	filters := Filters{
		{
			Id:           1,
			AccountId:    0, // global filter
			FilterAction: FilterActionWarn,
			Keywords: []*FilterKeyword{
				{Keyword: "bad", WholeWord: false},
			},
		},
		{
			Id:           2,
			AccountId:    0,
			FilterAction: FilterActionHide,
			Keywords: []*FilterKeyword{
				{Keyword: "spam", WholeWord: false},
			},
		},
	}

	t.Run("Filter other users' statuses", func(t *testing.T) {
		status := &Status{AccountId: 2, Content: StatusContent{Content: "bad content"}} //nolint:goconst
		result := filters.GetStatusFilters(status, account)
		assert.Len(t, result, 1)
		assert.Equal(t, uint64(1), result[0].Id)
	})

	t.Run("Skip own statuses", func(t *testing.T) {
		status := &Status{AccountId: 1, Content: StatusContent{Content: "bad content"}}
		result := filters.GetStatusFilters(status, account)
		assert.Empty(t, result)
	})

	t.Run("No account - still filter", func(t *testing.T) {
		status := &Status{AccountId: 2, Content: StatusContent{Content: "bad content"}}
		result := filters.GetStatusFilters(status, nil)
		assert.Len(t, result, 1)
	})

	t.Run("No matching filters", func(t *testing.T) {
		status := &Status{AccountId: 2, Content: StatusContent{Content: "good content"}}
		result := filters.GetStatusFilters(status, otherAccount)
		assert.Empty(t, result)
	})

	t.Run("Own boost still filters the original", func(t *testing.T) {
		original := &Status{AccountId: 2, Content: StatusContent{Content: "bad content"}}
		boost := &Status{AccountId: account.Id, Reblog: original}
		result := filters.GetStatusFilters(boost, account)
		assert.Len(t, result, 1)
	})

	t.Run("Context limits where a filter applies", func(t *testing.T) {
		scoped := Filters{{
			Id:           9,
			FilterAction: FilterActionHide,
			Context:      []string{"home"},
			Keywords:     []*FilterKeyword{{Keyword: "bad"}},
		}}
		status := &Status{AccountId: 2, Content: StatusContent{Content: "bad content"}}
		assert.Empty(t, scoped.GetStatusFiltersIn(status, account, "public"))
		assert.Len(t, scoped.GetStatusFiltersIn(status, account, "home"), 1)
	})

	_ = now
}

func TestFilters_GetFilterActions(t *testing.T) {
	filters := Filters{
		{Id: 1, FilterAction: FilterActionWarn},
		{Id: 2, FilterAction: FilterActionHide},
		{Id: 3, FilterAction: FilterActionNone},
	}

	result := filters.GetFilterActions()
	assert.Len(t, result, 2)
	assert.Contains(t, result, uint8(FilterActionWarn))
	assert.Contains(t, result, uint8(FilterActionHide))
	assert.NotContains(t, result, uint8(FilterActionNone))
}

func TestFilters_GetFilterActionsName(t *testing.T) {
	filters := Filters{
		{Id: 1, FilterAction: FilterActionWarn},
		{Id: 2, FilterAction: FilterActionHide},
		{Id: 3, FilterAction: FilterActionNone},
	}

	result := filters.GetFilterActionsName()
	assert.Len(t, result, 2)
	assert.Contains(t, result, "warn")
	assert.Contains(t, result, "hide")
	assert.NotContains(t, result, "none")
}

func TestFilters_GetHeightLevelFilter(t *testing.T) {
	t.Run("Return highest level filter", func(t *testing.T) {
		filters := Filters{
			{Id: 1, FilterAction: FilterActionWarn},
			{Id: 2, FilterAction: FilterActionHide},
			{Id: 3, FilterAction: FilterActionNone},
		}
		result := filters.GetHeightLevelFilter()
		assert.NotNil(t, result)
		assert.Equal(t, uint64(2), result.Id)
		assert.Equal(t, uint8(FilterActionHide), result.FilterAction)
	})

	t.Run("Empty filters", func(t *testing.T) {
		filters := Filters{}
		result := filters.GetHeightLevelFilter()
		assert.Nil(t, result)
	})

	t.Run("All none action", func(t *testing.T) {
		filters := Filters{
			{Id: 1, FilterAction: FilterActionNone},
			{Id: 2, FilterAction: FilterActionNone},
		}
		result := filters.GetHeightLevelFilter()
		assert.Nil(t, result)
	})
}

func TestFilterKeyword_Hash(t *testing.T) {
	kw1 := &FilterKeyword{Keyword: "test", WholeWord: true}
	kw2 := &FilterKeyword{Keyword: "test", WholeWord: true}
	kw3 := &FilterKeyword{Keyword: "test", WholeWord: false}
	kw4 := &FilterKeyword{Keyword: "other", WholeWord: true}

	// Same keyword and wholeWord should produce same hash
	assert.Equal(t, kw1.Hash(), kw2.Hash())

	// Different wholeWord should produce different hash
	assert.NotEqual(t, kw1.Hash(), kw3.Hash())

	// Different keyword should produce different hash
	assert.NotEqual(t, kw1.Hash(), kw4.Hash())
}

func TestFilter_TableName(t *testing.T) {
	filter := Filter{}
	assert.Equal(t, "filters", filter.TableName())
}
