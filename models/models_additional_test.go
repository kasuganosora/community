package models

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================================
// domain_block.go
// ============================================================================

func TestAdditional_DomainBlock_TableName(t *testing.T) {
	db := DomainBlock{}
	assert.Equal(t, "domain_blocks", db.TableName())
}

func TestAdditional_DomainBlock_Fields(t *testing.T) {
	db := DomainBlock{
		Id:             1,
		Domain:         "example.com",
		Severity:       "suspend",
		RejectMedia:    true,
		RejectReports:  true,
		PrivateComment: "spam", //nolint:goconst
		PublicComment:  "spam domain",
		Obfuscate:      false,
	}
	assert.Equal(t, uint64(1), db.Id)
	assert.Equal(t, "example.com", db.Domain)
	assert.Equal(t, "suspend", db.Severity)
	assert.True(t, db.RejectMedia)
	assert.True(t, db.RejectReports)
	assert.Equal(t, "spam", db.PrivateComment)
	assert.Equal(t, "spam domain", db.PublicComment)
	assert.False(t, db.Obfuscate)
	assert.Nil(t, db.CreatedAt)
	assert.Nil(t, db.UpdatedAt)
}

func TestAdditional_DomainBlocks_Type(t *testing.T) {
	// DomainBlocks is a []*DomainBlock
	dbs := make(DomainBlocks, 0, 1)

	assert.Nil(t, dbs)
	dbs = append(dbs, &DomainBlock{Domain: "bad.com"})
	assert.Len(t, dbs, 1)
	assert.Equal(t, "bad.com", dbs[0].Domain)
}

// ============================================================================
// rule.go
// ============================================================================

func TestAdditional_Rule_TableName(t *testing.T) {
	r := Rule{}
	assert.Equal(t, "rules", r.TableName())
}

func TestAdditional_Rule_Fields(t *testing.T) {
	now := time.Now()
	r := Rule{
		Id:        1,
		Priority:  10,
		Text:      "Be nice",
		CreatedAt: &now,
		UpdatedAt: &now,
	}
	assert.Equal(t, uint64(1), r.Id)
	assert.Equal(t, uint(10), r.Priority)
	assert.Equal(t, "Be nice", r.Text)
	assert.NotNil(t, r.CreatedAt)
	assert.NotNil(t, r.UpdatedAt)
}

// ============================================================================
// system_setting.go
// ============================================================================

func TestAdditional_SystemSetting_TableName(t *testing.T) {
	s := SystemSetting{}
	assert.Equal(t, "system_settings", s.TableName())
}

func TestAdditional_SystemSetting_Fields(t *testing.T) {
	s := SystemSetting{
		ID:  1,
		Key: "site_title",
		Val: "My Community",
	}
	assert.Equal(t, int64(1), s.ID)
	assert.Equal(t, "site_title", s.Key)
	assert.Equal(t, "My Community", s.Val)
}

// ============================================================================
// follow.go
// ============================================================================

func TestAdditional_Follow_TableName(t *testing.T) {
	f := Follow{}
	assert.Equal(t, "follows", f.TableName())
}

func TestAdditional_Follow_Constants(t *testing.T) {
	assert.Equal(t, 0, FollowStatusFollowed)
	assert.Equal(t, 1, FollowStatusRequested)
	assert.Equal(t, "reblogs", FollowPropertyKeyReblogs)
}

func TestAdditional_Follows_GetMapByFollowId(t *testing.T) {
	follows := Follows{
		{Id: 1, AccountId: 10, FollowId: 20},
		{Id: 2, AccountId: 10, FollowId: 30},
		{Id: 3, AccountId: 40, FollowId: 20},
	}

	m := follows.GetMapByFollowId()
	assert.Len(t, m, 2)
	assert.Len(t, m[20], 2)
	assert.Len(t, m[30], 1)
	assert.Equal(t, uint64(1), m[20][0].Id)
	assert.Equal(t, uint64(3), m[20][1].Id)
	assert.Equal(t, uint64(2), m[30][0].Id)
}

func TestAdditional_Follows_GetMapByFollowId_Empty(t *testing.T) {
	follows := Follows{}
	m := follows.GetMapByFollowId()
	assert.NotNil(t, m)
	assert.Empty(t, m)
}

func TestAdditional_Follows_GetMapByAccountId(t *testing.T) {
	follows := Follows{
		{Id: 1, AccountId: 10, FollowId: 20},
		{Id: 2, AccountId: 10, FollowId: 30},
		{Id: 3, AccountId: 40, FollowId: 50},
	}

	m := follows.GetMapByAccountId()
	assert.Len(t, m, 2)
	assert.Len(t, m[10], 2)
	assert.Len(t, m[40], 1)
}

func TestAdditional_Follows_GetMapByAccountId_Empty(t *testing.T) {
	follows := Follows{}
	m := follows.GetMapByAccountId()
	assert.NotNil(t, m)
	assert.Empty(t, m)
}

func TestAdditional_Follows_IsFollowed(t *testing.T) {
	follows := Follows{
		{Id: 1, AccountId: 10, FollowId: 20},
		{Id: 2, AccountId: 30, FollowId: 40},
	}

	assert.True(t, follows.IsFollowed(10, 20))
	assert.True(t, follows.IsFollowed(30, 40))
	assert.False(t, follows.IsFollowed(10, 40))
	assert.False(t, follows.IsFollowed(99, 99))
}

func TestAdditional_Follows_IsFollowed_Empty(t *testing.T) {
	follows := Follows{}
	assert.False(t, follows.IsFollowed(1, 2))
}

func TestAdditional_Follows_GetFollows(t *testing.T) {
	follows := Follows{
		{Id: 1, AccountId: 10, FollowId: 20},
		{Id: 2, AccountId: 10, FollowId: 30},
		{Id: 3, AccountId: 40, FollowId: 50},
	}

	result := follows.GetFollows(10)
	assert.Len(t, result, 2)
	assert.Equal(t, uint64(1), result[0].Id)
	assert.Equal(t, uint64(2), result[1].Id)

	result = follows.GetFollows(40)
	assert.Len(t, result, 1)

	result = follows.GetFollows(99)
	assert.Empty(t, result)
}

func TestAdditional_Follows_GetFollows_Empty(t *testing.T) {
	follows := Follows{}
	result := follows.GetFollows(1)
	assert.NotNil(t, result)
	assert.Empty(t, result)
}

func TestAdditional_Follow_Fields(t *testing.T) {
	props := map[string]any{"reblogs": true}
	f := Follow{
		Id:         1,
		AccountId:  10,
		FollowId:   20,
		Status:     FollowStatusFollowed,
		Properties: props,
		CreatedAt:  1700000000,
	}
	assert.Equal(t, uint64(1), f.Id)
	assert.Equal(t, uint64(10), f.AccountId)
	assert.Equal(t, uint64(20), f.FollowId)
	assert.Equal(t, FollowStatusFollowed, f.Status)
	assert.True(t, f.Properties["reblogs"].(bool)) //nolint:forcetypeassert
	assert.Equal(t, int64(1700000000), f.CreatedAt)
}

// ============================================================================
// list.go
// ============================================================================

func TestAdditional_List_TableName(t *testing.T) {
	l := List{}
	assert.Equal(t, "lists", l.TableName())
}

func TestAdditional_ListAccount_TableName(t *testing.T) {
	la := ListAccount{}
	assert.Equal(t, "list_accounts", la.TableName())
}

func TestAdditional_ListFollowAccount_TableName(t *testing.T) {
	lfa := ListFollowAccount{}
	assert.Equal(t, "list_follow_accounts", lfa.TableName())
}

func TestAdditional_List_Constants(t *testing.T) {
	assert.Equal(t, 0, ListRepliesPolicyList)
	assert.Equal(t, 1, ListRepliesPolicyFollowed)
	assert.Equal(t, 2, ListRepliesPolicyNone)
	assert.Equal(t, 0, ListVisibilityPublic)
	assert.Equal(t, 1, ListVisibilityPrivate)
	assert.Equal(t, 2, ListVisibilityFollowers)
}

func TestAdditional_List_SetRepliesPolicy(t *testing.T) {
	t.Run("list", func(t *testing.T) {
		l := &List{}
		l.SetRepliesPolicy("list")
		assert.Equal(t, uint8(ListRepliesPolicyList), l.RepliesPolicy)
	})
	t.Run("followed", func(t *testing.T) {
		l := &List{}
		l.SetRepliesPolicy("followed")
		assert.Equal(t, uint8(ListRepliesPolicyFollowed), l.RepliesPolicy)
	})
	t.Run("none", func(t *testing.T) {
		l := &List{}
		l.SetRepliesPolicy("none")
		assert.Equal(t, uint8(ListRepliesPolicyNone), l.RepliesPolicy)
	})
	t.Run("default (unknown)", func(t *testing.T) {
		l := &List{}
		l.SetRepliesPolicy("unknown")
		assert.Equal(t, uint8(ListRepliesPolicyList), l.RepliesPolicy)
	})
	t.Run("empty string defaults to list", func(t *testing.T) {
		l := &List{}
		l.SetRepliesPolicy("")
		assert.Equal(t, uint8(ListRepliesPolicyList), l.RepliesPolicy)
	})
}

func TestAdditional_List_GetRepliesPolicy(t *testing.T) {
	t.Run("list", func(t *testing.T) {
		l := &List{RepliesPolicy: ListRepliesPolicyList}
		assert.Equal(t, "list", l.GetRepliesPolicy())
	})
	t.Run("followed", func(t *testing.T) {
		l := &List{RepliesPolicy: ListRepliesPolicyFollowed}
		assert.Equal(t, "followed", l.GetRepliesPolicy())
	})
	t.Run("none", func(t *testing.T) {
		l := &List{RepliesPolicy: ListRepliesPolicyNone}
		assert.Equal(t, "none", l.GetRepliesPolicy())
	})
	t.Run("default unknown", func(t *testing.T) {
		l := &List{RepliesPolicy: 99}
		assert.Equal(t, "list", l.GetRepliesPolicy())
	})
}

func TestAdditional_ListAccount_Fields(t *testing.T) {
	la := ListAccount{
		ID:        1,
		ListId:    10,
		AccountId: 20,
	}
	assert.Equal(t, uint64(1), la.ID)
	assert.Equal(t, uint64(10), la.ListId)
	assert.Equal(t, uint64(20), la.AccountId)
}

func TestAdditional_ListFollowAccount_Fields(t *testing.T) {
	lfa := ListFollowAccount{
		ID:        1,
		ListId:    10,
		AccountId: 20,
	}
	assert.Equal(t, uint64(1), lfa.ID)
	assert.Equal(t, uint64(10), lfa.ListId)
	assert.Equal(t, uint64(20), lfa.AccountId)
}

// ============================================================================
// notification.go
// ============================================================================

func TestAdditional_Notification_TableName(t *testing.T) {
	n := &Notification{}
	assert.Equal(t, "notifications", n.TableName())
}

func TestAdditional_Notification_Constants(t *testing.T) {
	assert.Equal(t, 0, NotificationTypeMention)
	assert.Equal(t, 1, NotificationTypeReblog)
	assert.Equal(t, 2, NotificationTypeFavourite)
	assert.Equal(t, 3, NotificationTypeFollow)
	assert.Equal(t, 4, NotificationTypePoll)
	assert.Equal(t, 5, NotificationTypeFollowRequest)
	assert.Equal(t, 6, NotificationTypeStatusUpdate)
	assert.Equal(t, 7, NotificationTypeAdminReport)
	assert.Equal(t, 8, NotificationTypeAddList)
	assert.Equal(t, 9, NotificationTypeRemoveList)
}

func TestAdditional_Notification_GetTypeString(t *testing.T) {
	tests := []struct {
		name     string
		typ      uint8
		expected string
	}{
		{"mention", NotificationTypeMention, "mention"},                     //nolint:goconst
		{"reblog", NotificationTypeReblog, "reblog"},                        //nolint:goconst
		{"favourite", NotificationTypeFavourite, "favourite"},               //nolint:goconst
		{"follow", NotificationTypeFollow, "follow"},                        //nolint:goconst
		{"poll", NotificationTypePoll, "poll"},                              //nolint:goconst
		{"follow_request", NotificationTypeFollowRequest, "follow_request"}, //nolint:goconst
		{"status_update", NotificationTypeStatusUpdate, "status_update"},    //nolint:goconst
		{"admin_report", NotificationTypeAdminReport, "admin_report"},       //nolint:goconst
		{"add_list", NotificationTypeAddList, "add_list"},                   //nolint:goconst
		{"remove_list", NotificationTypeRemoveList, "remove_list"},          //nolint:goconst
		{"unknown", 99, ""}, //nolint:goconst
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n := &Notification{Type: tt.typ}
			assert.Equal(t, tt.expected, n.GetTypeString())
		})
	}
}

func TestAdditional_Notification_Fields(t *testing.T) {
	n := &Notification{
		Id:            1,
		Type:          NotificationTypeFollow,
		ActivityType:  "follow",
		AccountId:     10,
		FromAccountId: 20,
		StatusId:      30,
		ReportId:      0,
		ListId:        0,
	}
	assert.Equal(t, uint64(1), n.Id)
	assert.Equal(t, uint8(NotificationTypeFollow), n.Type)
	assert.Equal(t, "follow", n.ActivityType)
	assert.Equal(t, uint64(10), n.AccountId)
	assert.Equal(t, uint64(20), n.FromAccountId)
	assert.Equal(t, uint64(30), n.StatusId)
	assert.Nil(t, n.Status)
	assert.Nil(t, n.Account)
	assert.Nil(t, n.FromAccount)
}

func TestAdditional_Notifications_Type(t *testing.T) {
	ns := make(Notifications, 0, 1)

	assert.Nil(t, ns)
	ns = append(ns, &Notification{Id: 1})
	assert.Len(t, ns, 1)
}

// ============================================================================
// report.go
// ============================================================================

func TestAdditional_Report_TableName(t *testing.T) {
	r := Report{}
	assert.Equal(t, "reports", r.TableName())
}

func TestAdditional_Report_Constants(t *testing.T) {
	assert.Equal(t, 0, ReportCategoryOther)
	assert.Equal(t, 1, ReportCategorySpam)
	assert.Equal(t, 2, ReportCategoryLegal)
	assert.Equal(t, 3, ReportCategoryViolation)
}

func TestAdditional_Report_GetStatusString(t *testing.T) {
	t.Run("unresolved (nil ActionTakenAt)", func(t *testing.T) {
		r := &Report{ActionTakenAt: nil}
		assert.Equal(t, "Unresolved", r.GetStatusString())
	})
	t.Run("resolved (non-nil ActionTakenAt)", func(t *testing.T) {
		now := time.Now()
		r := &Report{ActionTakenAt: &now}
		assert.Equal(t, "Resolved", r.GetStatusString())
	})
}

func TestAdditional_Report_ToCategoryString(t *testing.T) {
	tests := []struct {
		name     string
		cat      uint16
		expected string
	}{
		{"other", ReportCategoryOther, "Other"},             //nolint:goconst
		{"spam", ReportCategorySpam, "Spam"},                //nolint:goconst
		{"legal", ReportCategoryLegal, "Legal"},             //nolint:goconst
		{"violation", ReportCategoryViolation, "Violation"}, //nolint:goconst
		{"unknown", 99, "Unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &Report{Category: tt.cat}
			assert.Equal(t, tt.expected, r.ToCategoryString())
		})
	}
}

func TestAdditional_Report_SetCategory(t *testing.T) {
	tests := []struct {
		input    string
		expected uint16
	}{
		{"Other", ReportCategoryOther},
		{"Spam", ReportCategorySpam},
		{"Legal", ReportCategoryLegal},
		{"Violation", ReportCategoryViolation},
		{"unknown", ReportCategoryOther}, // default
		{"", ReportCategoryOther},        // default
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			r := &Report{}
			r.SetCategory(tt.input)
			assert.Equal(t, tt.expected, r.Category)
		})
	}
}

func TestAdditional_Report_Fields(t *testing.T) {
	r := &Report{
		Id:                     1,
		StatusIds:              []uint64{10, 20},
		Comment:                "bad content", //nolint:goconst
		AccountId:              30,
		ActionTakenByAccountId: 0,
		TargetAccountId:        40,
		AssignedAccountId:      0,
		Forwarded:              false,
		Forward:                false,
		Category:               ReportCategorySpam,
		RuleIds:                []uint64{1, 2},
		Uri:                    "https://example.com/reports/1",
	}
	assert.Equal(t, uint64(1), r.Id)
	assert.Equal(t, []uint64{10, 20}, r.StatusIds)
	assert.Equal(t, "bad content", r.Comment)
	assert.Equal(t, uint64(30), r.AccountId)
	assert.Equal(t, uint64(40), r.TargetAccountId)
	assert.Equal(t, uint16(ReportCategorySpam), r.Category)
	assert.Equal(t, []uint64{1, 2}, r.RuleIds)
	assert.Equal(t, "https://example.com/reports/1", r.Uri)
}

// ============================================================================
// relay.go
// ============================================================================

func TestAdditional_Relay_TableName(t *testing.T) {
	r := Relay{}
	assert.Equal(t, "relays", r.TableName())
}

func TestAdditional_Relay_Constants(t *testing.T) {
	assert.Equal(t, RelayStateIdle, RelayState(0))
	assert.Equal(t, RelayStatePending, RelayState(1))
	assert.Equal(t, RelayStateAccepted, RelayState(2))
	assert.Equal(t, RelayStateRejected, RelayState(3))
}

func TestAdditional_Relay_IsEnabled(t *testing.T) {
	t.Run("accepted is enabled", func(t *testing.T) {
		r := &Relay{State: RelayStateAccepted}
		assert.True(t, r.IsEnabled())
	})
	t.Run("idle is not enabled", func(t *testing.T) {
		r := &Relay{State: RelayStateIdle}
		assert.False(t, r.IsEnabled())
	})
	t.Run("pending is not enabled", func(t *testing.T) {
		r := &Relay{State: RelayStatePending}
		assert.False(t, r.IsEnabled())
	})
	t.Run("rejected is not enabled", func(t *testing.T) {
		r := &Relay{State: RelayStateRejected}
		assert.False(t, r.IsEnabled())
	})
}

func TestAdditional_Relay_IsPending(t *testing.T) {
	t.Run("pending", func(t *testing.T) {
		r := &Relay{State: RelayStatePending}
		assert.True(t, r.IsPending())
	})
	t.Run("not pending", func(t *testing.T) {
		r := &Relay{State: RelayStateAccepted}
		assert.False(t, r.IsPending())
	})
}

func TestAdditional_Relay_SetState(t *testing.T) {
	r := &Relay{State: RelayStateIdle}
	r.SetState(RelayStateAccepted)
	assert.Equal(t, RelayStateAccepted, r.State)
	r.SetState(RelayStateRejected)
	assert.Equal(t, RelayStateRejected, r.State)
}

func TestAdditional_Relay_Fields(t *testing.T) {
	r := Relay{
		ID:               1,
		InboxURL:         "https://relay.example.com/inbox",
		FollowActivityID: "https://example.com/activity/1",
		State:            RelayStateAccepted,
	}
	assert.Equal(t, uint64(1), r.ID)
	assert.Equal(t, "https://relay.example.com/inbox", r.InboxURL)
	assert.Equal(t, "https://example.com/activity/1", r.FollowActivityID)
	assert.Equal(t, RelayStateAccepted, r.State)
}

// ============================================================================
// invite_code.go
// ============================================================================

func TestAdditional_InviteCode_TableName(t *testing.T) {
	inv := InviteCode{}
	assert.Equal(t, "invite_codes", inv.TableName())
}

func TestAdditional_InviteCode_IsAvailable(t *testing.T) {
	t.Run("available - no restrictions", func(t *testing.T) {
		inv := &InviteCode{
			Disabled:    false,
			MaxUseCount: 0,
			UseCount:    0,
			ExpireAt:    nil,
		}
		assert.True(t, inv.IsAvailable())
	})

	t.Run("disabled", func(t *testing.T) {
		inv := &InviteCode{
			Disabled:    true,
			MaxUseCount: 0,
			UseCount:    0,
			ExpireAt:    nil,
		}
		assert.False(t, inv.IsAvailable())
	})

	t.Run("max use count reached", func(t *testing.T) {
		inv := &InviteCode{
			Disabled:    false,
			MaxUseCount: 5,
			UseCount:    5,
			ExpireAt:    nil,
		}
		assert.False(t, inv.IsAvailable())
	})

	t.Run("max use count exceeded", func(t *testing.T) {
		inv := &InviteCode{
			Disabled:    false,
			MaxUseCount: 5,
			UseCount:    10,
			ExpireAt:    nil,
		}
		assert.False(t, inv.IsAvailable())
	})

	t.Run("still under max use count", func(t *testing.T) {
		inv := &InviteCode{
			Disabled:    false,
			MaxUseCount: 5,
			UseCount:    3,
			ExpireAt:    nil,
		}
		assert.True(t, inv.IsAvailable())
	})

	t.Run("expired", func(t *testing.T) {
		past := time.Now().Add(-1 * time.Hour)
		inv := &InviteCode{
			Disabled:    false,
			MaxUseCount: 0,
			UseCount:    0,
			ExpireAt:    &past,
		}
		assert.False(t, inv.IsAvailable())
	})

	t.Run("not yet expired", func(t *testing.T) {
		future := time.Now().Add(1 * time.Hour)
		inv := &InviteCode{
			Disabled:    false,
			MaxUseCount: 0,
			UseCount:    0,
			ExpireAt:    &future,
		}
		assert.True(t, inv.IsAvailable())
	})
}

func TestAdditional_InviteCode_Fields(t *testing.T) {
	inv := InviteCode{
		Id:          1,
		Code:        "ABC123",
		UserId:      10,
		AccountId:   "acct:user@example.com",
		MaxUseCount: 10,
		UseCount:    3,
		Disabled:    false,
	}
	assert.Equal(t, int64(1), inv.Id)
	assert.Equal(t, "ABC123", inv.Code)
	assert.Equal(t, uint64(10), inv.UserId)
	assert.Equal(t, "acct:user@example.com", inv.AccountId)
	assert.Equal(t, 10, inv.MaxUseCount)
	assert.Equal(t, 3, inv.UseCount)
}

// ============================================================================
// proxy_follow.go
// ============================================================================

func TestAdditional_ProxyFollow_TableName(t *testing.T) {
	pf := ProxyFollow{}
	assert.Equal(t, "proxy_follows", pf.TableName())
}

func TestAdditional_AccountKey_TableName(t *testing.T) {
	ak := AccountKey{}
	assert.Equal(t, "account_keys", ak.TableName())
}

func TestAdditional_ProxyFollow_Constants(t *testing.T) {
	assert.Equal(t, 0, ProxyFollowStatusPending)
	assert.Equal(t, 1, ProxyFollowStatusAccepted)
	assert.Equal(t, 2, ProxyFollowStatusRejected)
	assert.Equal(t, "list", ProxyFollowReasonList)
}

func TestAdditional_ProxyFollow_Fields(t *testing.T) {
	pf := ProxyFollow{
		ID:               1,
		AccountID:        10,
		Reason:           ProxyFollowReasonList,
		ReasonID:         20,
		FollowActivityID: "https://example.com/activity/1",
		Status:           ProxyFollowStatusAccepted,
	}
	assert.Equal(t, uint64(1), pf.ID)
	assert.Equal(t, uint64(10), pf.AccountID)
	assert.Equal(t, "list", pf.Reason)
	assert.Equal(t, uint64(20), pf.ReasonID)
	assert.Equal(t, "https://example.com/activity/1", pf.FollowActivityID)
	assert.Equal(t, ProxyFollowStatusAccepted, pf.Status)
}

func TestAdditional_AccountKey_Fields(t *testing.T) {
	ak := AccountKey{
		ID:         1,
		AccountID:  10,
		PublicKey:  "pub-key-data",
		PrivateKey: "priv-key-data",
	}
	assert.Equal(t, uint64(1), ak.ID)
	assert.Equal(t, uint64(10), ak.AccountID)
	assert.Equal(t, "pub-key-data", ak.PublicKey)
	assert.Equal(t, "priv-key-data", ak.PrivateKey)
}

// ============================================================================
// status.go (additional tests for models not covered in status_test.go)
// ============================================================================

func TestAdditional_StatusContent_TableName(t *testing.T) {
	sc := &StatusContent{}
	assert.Equal(t, "status_contents", sc.TableName())
}

func TestAdditional_StatusMediaAttachment_TableName(t *testing.T) {
	sma := &StatusMediaAttachment{}
	assert.Equal(t, "status_media_attachments", sma.TableName())
}

func TestAdditional_StatusTag_TableName(t *testing.T) {
	st := &StatusTag{}
	assert.Equal(t, "status_tags", st.TableName())
}

func TestAdditional_StatusEmoji_TableName(t *testing.T) {
	se := &StatusEmoji{}
	assert.Equal(t, "status_emojis", se.TableName())
}

func TestAdditional_StatusToAccount_TableName(t *testing.T) {
	sta := StatusToAccount{}
	assert.Equal(t, "status_to_accounts", sta.TableName())
}

func TestAdditional_FavoriteStatus_TableName(t *testing.T) {
	fs := &FavoriteStatus{}
	assert.Equal(t, "favorite_statuses", fs.TableName())
}

func TestAdditional_BookmarkStatus_TableName(t *testing.T) {
	bs := &BookmarkStatus{}
	assert.Equal(t, "bookmark_statuses", bs.TableName())
}

func TestAdditional_PinStatus_TableName(t *testing.T) {
	ps := &PinStatus{}
	assert.Equal(t, "pin_statuses", ps.TableName())
}

func TestAdditional_StatusHistory_TableName(t *testing.T) {
	sh := StatusHistory{}
	assert.Equal(t, "status_histories", sh.TableName())
}

func TestAdditional_Status_IsScheduled(t *testing.T) {
	t.Run("scheduled", func(t *testing.T) {
		future := time.Now().Add(1 * time.Hour)
		s := &Status{ScheduledAt: &future}
		assert.True(t, s.IsScheduled())
	})
	t.Run("not scheduled - nil", func(t *testing.T) {
		s := &Status{ScheduledAt: nil}
		assert.False(t, s.IsScheduled())
	})
}

func TestAdditional_Status_SetVisibilityByString(t *testing.T) {
	tests := []struct {
		input    string
		expected uint8
		hasErr   bool
	}{
		{"public", StatusVisibilityPublic, false}, //nolint:goconst
		{"Public", StatusVisibilityPublic, false},
		{"PUBLIC", StatusVisibilityPublic, false},
		{"unlisted", StatusVisibilityUnlisted, false}, //nolint:goconst
		{"Unlisted", StatusVisibilityUnlisted, false},
		{"private", StatusVisibilityPrivate, false}, //nolint:goconst
		{"Private", StatusVisibilityPrivate, false},
		{"direct", StatusVisibilityDirect, false}, //nolint:goconst
		{"Direct", StatusVisibilityDirect, false},
		{"invalid", 0, true},
		{"", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			s := &Status{}

			err := s.SetVisibilityByString(tt.input)
			if tt.hasErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expected, s.Visibility)
			}
		})
	}
}

func TestAdditional_Status_CanView(t *testing.T) {
	t.Run("own status - always visible", func(t *testing.T) {
		s := &Status{AccountId: 1, Visibility: StatusVisibilityPrivate}
		account := &Account{Id: 1}
		assert.True(t, s.CanView(account, nil))
	})

	t.Run("scheduled status - not own", func(t *testing.T) {
		future := time.Now().Add(1 * time.Hour)
		s := &Status{AccountId: 1, Visibility: StatusVisibilityPublic, ScheduledAt: &future}
		account := &Account{Id: 2}
		assert.False(t, s.CanView(account, nil))
	})

	t.Run("public - anyone can see", func(t *testing.T) {
		s := &Status{AccountId: 1, Visibility: StatusVisibilityPublic}
		account := &Account{Id: 2}
		assert.True(t, s.CanView(account, nil))
	})

	t.Run("unlisted - anyone can see", func(t *testing.T) {
		s := &Status{AccountId: 1, Visibility: StatusVisibilityUnlisted}
		account := &Account{Id: 2}
		assert.True(t, s.CanView(account, nil))
	})

	t.Run("private - follower can see", func(t *testing.T) {
		s := &Status{AccountId: 1, Visibility: StatusVisibilityPrivate}
		account := &Account{Id: 2}
		follows := map[uint64]*Follow{
			1: {AccountId: 2, FollowId: 1, Status: FollowStatusFollowed},
		}
		assert.True(t, s.CanView(account, follows))
	})

	t.Run("private - non-follower cannot see", func(t *testing.T) {
		s := &Status{AccountId: 1, Visibility: StatusVisibilityPrivate}
		account := &Account{Id: 2}
		assert.False(t, s.CanView(account, nil))
	})

	t.Run("private - mentioned user can see", func(t *testing.T) {
		s := &Status{
			AccountId:  1,
			Visibility: StatusVisibilityPrivate,
			ToAccounts: Accounts{{Id: 2}},
		}
		account := &Account{Id: 2}
		assert.True(t, s.CanView(account, nil))
	})

	t.Run("direct - mentioned user can see", func(t *testing.T) {
		s := &Status{
			AccountId:  1,
			Visibility: StatusVisibilityDirect,
			ToAccounts: Accounts{{Id: 2}},
		}
		account := &Account{Id: 2}
		assert.True(t, s.CanView(account, nil))
	})

	t.Run("direct - non-mentioned cannot see", func(t *testing.T) {
		s := &Status{
			AccountId:  1,
			Visibility: StatusVisibilityDirect,
			ToAccounts: Accounts{{Id: 3}},
		}
		account := &Account{Id: 2}
		assert.False(t, s.CanView(account, nil))
	})

	t.Run("direct - empty ToAccounts", func(t *testing.T) {
		s := &Status{
			AccountId:  1,
			Visibility: StatusVisibilityDirect,
			ToAccounts: Accounts{},
		}
		account := &Account{Id: 2}
		assert.False(t, s.CanView(account, nil))
	})
}

func TestAdditional_Status_SetConversationId(t *testing.T) {
	t.Run("set from nil to value", func(t *testing.T) {
		s := &Status{ConversationId: nil}
		s.SetConversationId(42)
		assert.NotNil(t, s.ConversationId)
		assert.Equal(t, uint64(42), *s.ConversationId)
		assert.True(t, s.conversationIdChanged)
	})

	t.Run("set from same value (no change)", func(t *testing.T) {
		id := uint64(42)
		s := &Status{ConversationId: &id}
		s.SetConversationId(42)
		assert.Equal(t, uint64(42), *s.ConversationId)
		assert.False(t, s.conversationIdChanged)
	})

	t.Run("set from nil to 0 (no change)", func(t *testing.T) {
		s := &Status{ConversationId: nil}
		s.SetConversationId(0)
		assert.Nil(t, s.ConversationId)
		assert.False(t, s.conversationIdChanged)
	})

	t.Run("change to different value", func(t *testing.T) {
		id := uint64(10)
		s := &Status{ConversationId: &id}
		s.SetConversationId(20)
		assert.Equal(t, uint64(20), *s.ConversationId)
		assert.True(t, s.conversationIdChanged)
	})

	t.Run("set to 0 clears conversation id", func(t *testing.T) {
		id := uint64(10)
		s := &Status{ConversationId: &id}
		s.SetConversationId(0)
		assert.Nil(t, s.ConversationId)
		assert.True(t, s.conversationIdChanged)
	})
}

func TestAdditional_Statuses_GetIds(t *testing.T) {
	ss := Statuses{
		{Id: 1},
		{Id: 2},
		{Id: 3},
	}
	ids := ss.GetIds()
	assert.Equal(t, []uint64{1, 2, 3}, ids)
}

func TestAdditional_Statuses_GetIds_Empty(t *testing.T) {
	ss := Statuses{}
	ids := ss.GetIds()
	assert.Nil(t, ids)
}

func TestAdditional_Statuses_GetIdsWithoutReblogWarp(t *testing.T) {
	reblogId := uint64(100)
	ss := Statuses{
		{Id: 1, ReblogId: nil},
		{Id: 2, ReblogId: &reblogId},
		{Id: 3, ReblogId: nil},
	}
	ids := ss.GetIdsWithoutReblogWarp()
	assert.Equal(t, []uint64{1, 100, 3}, ids)
}

func TestAdditional_Statuses_GetIdsWithoutReblogWarp_Empty(t *testing.T) {
	ss := Statuses{}
	ids := ss.GetIdsWithoutReblogWarp()
	assert.Nil(t, ids)
}

func TestAdditional_Statuses_GetAccountIds(t *testing.T) {
	ss := Statuses{
		{Id: 1, AccountId: 10},
		{Id: 2, AccountId: 20},
		{Id: 3, AccountId: 10}, // duplicate
	}
	ids := ss.GetAccountIds()
	assert.Len(t, ids, 2)
	// Order depends on UniqueSlice implementation, just check both values present
	found10 := false
	found20 := false

	for _, id := range ids {
		if id == 10 {
			found10 = true
		}

		if id == 20 {
			found20 = true
		}
	}

	assert.True(t, found10, "should contain account id 10")
	assert.True(t, found20, "should contain account id 20")
}

func TestAdditional_Statuses_GetAccountIds_Empty(t *testing.T) {
	ss := Statuses{}
	ids := ss.GetAccountIds()
	assert.NotNil(t, ids)
	assert.Empty(t, ids)
}

func TestAdditional_Statuses_Filter(t *testing.T) {
	ss := Statuses{
		{Id: 1, AccountId: 10, Visibility: StatusVisibilityPublic},
		{Id: 2, AccountId: 20, Visibility: StatusVisibilityPrivate},
		{Id: 3, AccountId: 30, Visibility: StatusVisibilityPublic},
	}

	result := ss.Filter(func(s *Status) bool {
		return s.Visibility == StatusVisibilityPublic
	})
	assert.Len(t, result, 2)
	assert.Equal(t, uint64(1), result[0].Id)
	assert.Equal(t, uint64(3), result[1].Id)
}

func TestAdditional_Statuses_Filter_Empty(t *testing.T) {
	ss := Statuses{}
	result := ss.Filter(func(s *Status) bool { return true })
	assert.NotNil(t, result)
	assert.Empty(t, result)
}

func TestAdditional_Statuses_Filter_None(t *testing.T) {
	ss := Statuses{
		{Id: 1, Visibility: StatusVisibilityPublic},
	}
	result := ss.Filter(func(s *Status) bool { return false })
	assert.NotNil(t, result)
	assert.Empty(t, result)
}

func TestAdditional_Status_Fields(t *testing.T) {
	content := StatusContent{
		StatusId:    1,
		Content:     "Hello world", //nolint:goconst
		SpoilerText: "spoiler",
		Card:        map[string]any{"type": "link"},
	}
	assert.Equal(t, uint64(1), content.StatusId)
	assert.Equal(t, "Hello world", content.Content)
	assert.Equal(t, "spoiler", content.SpoilerText)
	assert.Equal(t, "link", content.Card["type"])
}

func TestAdditional_StatusMediaAttachment_Fields(t *testing.T) {
	sma := StatusMediaAttachment{Id: 1, StatusId: 10, MediaId: 20}
	assert.Equal(t, uint64(1), sma.Id)
	assert.Equal(t, uint64(10), sma.StatusId)
	assert.Equal(t, uint64(20), sma.MediaId)
}

func TestAdditional_StatusTag_Fields(t *testing.T) {
	st := StatusTag{Id: 1, StatusId: 10, TagId: 20}
	assert.Equal(t, uint64(1), st.Id)
	assert.Equal(t, uint64(10), st.StatusId)
	assert.Equal(t, uint64(20), st.TagId)
}

func TestAdditional_StatusEmoji_Fields(t *testing.T) {
	se := StatusEmoji{Id: 1, StatusId: 10, EmojiId: 20}
	assert.Equal(t, uint64(1), se.Id)
	assert.Equal(t, uint64(10), se.StatusId)
	assert.Equal(t, uint64(20), se.EmojiId)
}

func TestAdditional_StatusToAccount_Fields(t *testing.T) {
	sta := StatusToAccount{Id: 1, StatusId: 10, AccountId: 20}
	assert.Equal(t, uint64(1), sta.Id)
	assert.Equal(t, uint64(10), sta.StatusId)
	assert.Equal(t, uint64(20), sta.AccountId)
}

func TestAdditional_FavoriteStatus_Fields(t *testing.T) {
	fs := FavoriteStatus{Id: 1, AccountId: 10, StatusId: 20}
	assert.Equal(t, uint64(1), fs.Id)
	assert.Equal(t, uint64(10), fs.AccountId)
	assert.Equal(t, uint64(20), fs.StatusId)
}

func TestAdditional_BookmarkStatus_Fields(t *testing.T) {
	bs := BookmarkStatus{Id: 1, AccountId: 10, StatusId: 20}
	assert.Equal(t, uint64(1), bs.Id)
	assert.Equal(t, uint64(10), bs.AccountId)
	assert.Equal(t, uint64(20), bs.StatusId)
}

func TestAdditional_PinStatus_Fields(t *testing.T) {
	ps := PinStatus{Id: 1, AccountId: 10, StatusId: 20}
	assert.Equal(t, uint64(1), ps.Id)
	assert.Equal(t, uint64(10), ps.AccountId)
	assert.Equal(t, uint64(20), ps.StatusId)
}

func TestAdditional_StatusHistory_Fields(t *testing.T) {
	sh := StatusHistory{
		Id:                  1,
		StatusId:            10,
		AccountId:           20,
		Content:             "old content",
		SpoilerText:         "old spoiler",
		Sensitive:           false,
		MediaAttachmentsIds: []uint64{1, 2},
		EmojisIds:           []uint64{3, 4},
	}
	assert.Equal(t, uint64(1), sh.Id)
	assert.Equal(t, uint64(10), sh.StatusId)
	assert.Equal(t, uint64(20), sh.AccountId)
	assert.Equal(t, "old content", sh.Content)
	assert.Equal(t, "old spoiler", sh.SpoilerText)
	assert.Equal(t, []uint64{1, 2}, sh.MediaAttachmentsIds)
	assert.Equal(t, []uint64{3, 4}, sh.EmojisIds)
}
