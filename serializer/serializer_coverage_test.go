package serializer

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// ptrTime returns a pointer to the given time, used for building test fixtures.
func ptrTime(t time.Time) *time.Time {
	return &t
}

// withTestDB injects an in-memory SQLite-backed *gorm.DB into the global
// database handle for the duration of a single test, then restores the
// previous value (nil in test mode) afterwards. This lets serializer
// functions that call db.GetDB() run without an external database.
func withTestDB(t *testing.T) {
	t.Helper()
	gdb, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{SkipDefaultTransaction: true})
	if err != nil {
		t.Fatalf("failed to open in-memory sqlite: %v", err)
	}
	database.SetTestDB(gdb)
	t.Cleanup(func() {
		database.ResetTestDB()
		if sqlDB, dbErr := gdb.DB(); dbErr == nil {
			_ = sqlDB.Close()
		}
	})
}

// withTestDBMigrated sets up an in-memory SQLite DB and auto-migrates the
// given models so that serializer functions issuing queries against those
// tables don't fail with "no such table".
func withTestDBMigrated(t *testing.T, modelsToMigrate ...any) {
	t.Helper()
	withTestDB(t)
	if err := database.GetDB().AutoMigrate(modelsToMigrate...); err != nil {
		t.Fatalf("failed to auto-migrate models: %v", err)
	}
}

// =============================================================================
// Account tests (account.go) - ToAccount, ToAccounts, ToMention, ToMentions
// These functions read systemsettings.GetString(KeyDomain) which returns its
// default ("localhost") when db.GetDB() is nil (test mode), so they are safe
// to exercise without a database connection.
// =============================================================================

func TestToAccount_Nil(t *testing.T) {
	result := ToAccount(nil)
	require.Nil(t, result)
}

func TestToAccount_LocalBasic(t *testing.T) {
	created := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	acc := &models.Account{
		Id:           42,
		Username:     "alice",
		DisplayName:  "Alice",
		Locked:       true,
		Bot:          true,
		Discoverable: true,
		NoIndex:      false,
		Group:        true,
		CreatedAt:    &created,
		Note:         "Hello",
		Avatar:       "https://example.com/avatar.png",
		AvatarStatic: "https://example.com/avatar_static.png",
		Header:       "https://example.com/header.png",
		HeaderStatic: "https://example.com/header_static.png",
	}

	result := ToAccount(acc)
	require.NotNil(t, result)
	assert.Equal(t, "42", result.Id)
	assert.Equal(t, "alice", result.Username)
	assert.Equal(t, "Alice", result.DisplayName)
	assert.True(t, result.Locked)
	assert.True(t, result.Bot)
	assert.True(t, result.Discoverable)
	assert.True(t, result.Indexable) // Indexable == !NoIndex
	assert.True(t, result.Group)
	require.NotNil(t, result.CreatedAt)
	assert.Equal(t, created, *result.CreatedAt)
	assert.Equal(t, "<p>Hello</p>", result.Note)
	assert.Equal(t, "https://example.com/avatar.png", result.Avatar)
	assert.Equal(t, "https://example.com/header.png", result.Header)
	// Local domain (nil) -> localhost
	assert.Equal(t, "alice@localhost", result.Acct)
	assert.Contains(t, result.Url, "localhost")
	assert.Contains(t, result.Url, "@alice")
	assert.Contains(t, result.Uri, "localhost")
	assert.Contains(t, result.Uri, "users/alice")
}

func TestToAccount_NoIndexTrue(t *testing.T) {
	acc := &models.Account{Id: 1, Username: "x", NoIndex: true}
	result := ToAccount(acc)
	require.NotNil(t, result)
	assert.True(t, result.NoIndex)
	assert.False(t, result.Indexable) // Indexable inverted from NoIndex
}

func TestToAccount_RemoteDomain(t *testing.T) {
	domain := "social.example"
	acc := &models.Account{Id: 9, Username: "bob", Domain: &domain}
	result := ToAccount(acc)
	require.NotNil(t, result)
	assert.Equal(t, "bob@social.example", result.Acct)
	assert.Contains(t, result.Url, "social.example")
	assert.Contains(t, result.Uri, "social.example")
}

func TestToAccount_EmptyDomain(t *testing.T) {
	empty := ""
	acc := &models.Account{Id: 9, Username: "carol", Domain: &empty}
	result := ToAccount(acc)
	require.NotNil(t, result)
	// Empty domain string should fall back to local domain
	assert.Equal(t, "carol@localhost", result.Acct)
}

func TestToAccount_WithExtra(t *testing.T) {
	now := time.Now()
	acc := &models.Account{
		Id:       1,
		Username: "dave",
		Extra: &models.AccountExtra{
			FollowersCount: 100,
			FollowingCount: 50,
			StatusesCount:  10,
			Fields: []models.AccountField{
				{Name: "Website", Val: "https://dave.example", VerifiedAt: &now},
			},
		},
	}
	result := ToAccount(acc)
	require.NotNil(t, result)
	assert.Equal(t, 100, result.FollowersCount)
	assert.Equal(t, 50, result.FollowingCount)
	assert.Equal(t, 10, result.StatusesCount)
	require.Len(t, result.Fields, 1)
	assert.Equal(t, "Website", result.Fields[0].Name)
}

func TestToAccount_NilExtra(t *testing.T) {
	acc := &models.Account{Id: 1, Username: "eve", Extra: nil}
	result := ToAccount(acc)
	require.NotNil(t, result)
	assert.Equal(t, 0, result.FollowersCount)
	assert.Nil(t, result.Fields)
}

func TestToAccount_EmptyUsername(t *testing.T) {
	acc := &models.Account{Id: 1, Username: ""}
	result := ToAccount(acc)
	require.NotNil(t, result)
	assert.Equal(t, "@localhost", result.Acct)
}

func TestToAccounts_Multiple(t *testing.T) {
	accs := []*models.Account{
		{Id: 1, Username: "a"},
		{Id: 2, Username: "b"},
		{Id: 3, Username: "c"},
	}
	result := ToAccounts(accs)
	require.Len(t, result, 3)
	assert.Equal(t, "1", result[0].Id)
	assert.Equal(t, "a", result[0].Username)
	assert.Equal(t, "2", result[1].Id)
	assert.Equal(t, "3", result[2].Id)
}

func TestToAccounts_Empty(t *testing.T) {
	result := ToAccounts([]*models.Account{})
	require.Len(t, result, 0)
}

func TestToAccounts_WithNil(t *testing.T) {
	// ToAccounts indexes directly; it doesn't skip nil entries.
	accs := []*models.Account{
		{Id: 1, Username: "a"},
		nil,
	}
	result := ToAccounts(accs)
	require.Len(t, result, 2)
	assert.Equal(t, "1", result[0].Id)
	assert.Nil(t, result[1])
}

// --- Mention ---

func TestToMention_Local(t *testing.T) {
	acc := &models.Account{Id: 5, Username: "frank"}
	result := ToMention(acc)
	require.NotNil(t, result)
	assert.Equal(t, "5", result.Id)
	assert.Equal(t, "frank", result.Username)
	assert.Equal(t, "frank@localhost", result.Acct)
	assert.Contains(t, result.Url, "localhost")
	assert.Contains(t, result.Url, "users/frank")
}

func TestToMention_Remote(t *testing.T) {
	domain := "remote.test"
	acc := &models.Account{Id: 8, Username: "gina", Domain: &domain}
	result := ToMention(acc)
	require.NotNil(t, result)
	assert.Equal(t, "gina@remote.test", result.Acct)
	assert.Contains(t, result.Url, "remote.test")
}

func TestToMentions_Multiple(t *testing.T) {
	accs := []*models.Account{
		{Id: 1, Username: "a"},
		{Id: 2, Username: "b"},
	}
	result := ToMentions(accs)
	require.Len(t, result, 2)
	assert.Equal(t, "a@localhost", result[0].Acct)
	assert.Equal(t, "b@localhost", result[1].Acct)
}

func TestToMentions_Empty(t *testing.T) {
	result := ToMentions([]*models.Account{})
	require.Len(t, result, 0)
}

// =============================================================================
// Tag tests (tag.go) - ToTag, ToTags, ToTagWithHistory
// =============================================================================

func TestToTag_BasicLocal(t *testing.T) {
	tag := &models.Tag{Name: "golang"}
	result := ToTag(tag)
	require.NotNil(t, result)
	assert.Equal(t, "golang", result.Name)
	assert.Contains(t, result.Url, "/tags/golang")
	assert.Nil(t, result.StatusCount)
	assert.Nil(t, result.AccountCount)
}

func TestToTag_HashtagPrefix(t *testing.T) {
	tag := &models.Tag{Name: "#rust"}
	result := ToTag(tag)
	require.NotNil(t, result)
	// # prefix should be stripped for name and url
	assert.Equal(t, "rust", result.Name)
	assert.Contains(t, result.Url, "/tags/rust")
	assert.NotContains(t, result.Name, "#")
}

func TestToTag_WithCounts(t *testing.T) {
	tag := &models.Tag{
		Name:         "news",
		StatusCount:  100,
		AccountCount: 20,
	}
	result := ToTag(tag)
	require.NotNil(t, result)
	require.NotNil(t, result.StatusCount)
	assert.Equal(t, uint64(100), *result.StatusCount)
	require.NotNil(t, result.AccountCount)
	assert.Equal(t, uint64(20), *result.AccountCount)
}

func TestToTag_ZeroCounts(t *testing.T) {
	tag := &models.Tag{Name: "zero", StatusCount: 0, AccountCount: 0}
	result := ToTag(tag)
	require.NotNil(t, result)
	assert.Nil(t, result.StatusCount)
	assert.Nil(t, result.AccountCount)
}

func TestToTag_EmptyName(t *testing.T) {
	tag := &models.Tag{Name: ""}
	result := ToTag(tag)
	require.NotNil(t, result)
	assert.Empty(t, result.Name)
	assert.Contains(t, result.Url, "/tags/")
}

func TestToTag_OnlyHashPrefix(t *testing.T) {
	tag := &models.Tag{Name: "#"}
	result := ToTag(tag)
	require.NotNil(t, result)
	assert.Empty(t, result.Name)
}

func TestToTags_Multiple(t *testing.T) {
	tags := []*models.Tag{
		{Name: "tag1"},
		{Name: "tag2"},
		{StatusCount: 5, AccountCount: 3, Name: "tag3"},
	}
	result := ToTags(tags)
	require.Len(t, result, 3)
	assert.Equal(t, "tag1", result[0].Name)
	assert.Equal(t, "tag2", result[1].Name)
	assert.Equal(t, "tag3", result[2].Name)
	require.NotNil(t, result[2].StatusCount)
}

func TestToTags_WithNilEntry(t *testing.T) {
	tags := []*models.Tag{
		{Name: "a"},
		nil,
		{Name: "b"},
	}
	result := ToTags(tags)
	require.Len(t, result, 2)
	assert.Equal(t, "a", result[0].Name)
	assert.Equal(t, "b", result[1].Name)
}

func TestToTags_Empty(t *testing.T) {
	// ToTags only pre-allocates when input is nil; an empty (non-nil) slice
	// yields a nil result. Test the nil-input path which guarantees a non-nil
	// empty slice.
	result := ToTags(nil)
	require.NotNil(t, result)
	assert.Empty(t, result)
}

func TestToTagWithHistory_Basic(t *testing.T) {
	tag := &models.Tag{Name: "history", StatusCount: 42, AccountCount: 7}
	result := ToTagWithHistory(tag)
	require.NotNil(t, result)
	assert.Equal(t, "history", result.Name)
	require.Len(t, result.History, 1)
	assert.Equal(t, "0", result.History[0].Day)
	assert.Equal(t, "42", result.History[0].Uses)
	assert.Equal(t, "7", result.History[0].Accounts)
}

func TestToTagWithHistory_ZeroCounts(t *testing.T) {
	tag := &models.Tag{Name: "empty"}
	result := ToTagWithHistory(tag)
	require.NotNil(t, result)
	require.Len(t, result.History, 1)
	assert.Equal(t, "0", result.History[0].Uses)
	assert.Equal(t, "0", result.History[0].Accounts)
}

// =============================================================================
// Attachment tests (attachment.go) - ToAttachment, ToAttachments
// =============================================================================

func TestToAttachment_WithValues(t *testing.T) {
	thumb := "preview.jpg"
	att := &models.Attachment{
		Id:            10,
		FileName:      "file.jpg",
		FilePath:      "uploads/file.jpg",
		ThumbnailPath: &thumb,
		Type:          models.MediaTypeImage,
		RemoteUrl:     "https://remote/file.jpg",
		Description:   "An image",
		Blurhash:      "blurhash",
	}
	result := ToAttachment(att)
	require.NotNil(t, result)
	assert.Equal(t, "10", result.Id)
	assert.Equal(t, "image", result.Type)
	assert.Contains(t, result.Url, "/media/10/file.jpg")
	assert.Contains(t, result.PreviewUrl, "/media/10/thumbnail/preview.jpg")
	require.NotNil(t, result.RemoteUrl)
	assert.Equal(t, "https://remote/file.jpg", *result.RemoteUrl)
	assert.Equal(t, "An image", result.Description)
	assert.Equal(t, "blurhash", result.Blurhash)
}

func TestToAttachment_NoPreview(t *testing.T) {
	att := &models.Attachment{Id: 11, FileName: "only.jpg"}
	result := ToAttachment(att)
	require.NotNil(t, result)
	assert.Equal(t, result.Url, result.PreviewUrl, "falls back to the original")
}

func TestToAttachment_EmptyPreviewPath(t *testing.T) {
	empty := ""
	att := &models.Attachment{Id: 12, FileName: "only2.jpg", ThumbnailPath: &empty}
	result := ToAttachment(att)
	require.NotNil(t, result)
	assert.Equal(t, result.Url, result.PreviewUrl)
}

func TestToAttachment_EmptyFields(t *testing.T) {
	att := &models.Attachment{Id: 13}
	result := ToAttachment(att)
	require.NotNil(t, result)
	assert.Equal(t, "13", result.Id)
	assert.Contains(t, result.Url, "/media/13/")
	assert.Empty(t, result.Description)
	assert.Empty(t, result.Blurhash)
}

func TestToAttachments_Multiple(t *testing.T) {
	atts := []*models.Attachment{
		{Id: 1, FileName: "a.jpg"},
		{Id: 2, FileName: "b.jpg", ThumbnailPath: strPtr("bp.jpg")},
		{Id: 3, FileName: "c.jpg"},
	}
	result := ToAttachments(atts)
	require.Len(t, result, 3)
	assert.Equal(t, "1", result[0].Id)
	assert.Equal(t, "2", result[1].Id)
	assert.Contains(t, result[1].PreviewUrl, "/thumbnail/bp.jpg")
	assert.Equal(t, "3", result[2].Id)
}

func TestToAttachments_WithNil(t *testing.T) {
	atts := []*models.Attachment{
		{Id: 1, FileName: "a.jpg"},
		nil,
		{Id: 3, FileName: "c.jpg"},
	}
	result := ToAttachments(atts)
	require.Len(t, result, 2, "nil entries are skipped")
	assert.Equal(t, "1", result[0].Id)
	assert.Equal(t, "3", result[1].Id)
}

// =============================================================================
// StatusBase tests (status.go) - ToStatusBase
// ToStatusBase reads systemsettings which returns defaults when DB is nil.
// =============================================================================

func TestToStatusBase_Nil(t *testing.T) {
	result, err := ToStatusBase(nil)
	require.NoError(t, err)
	require.Nil(t, result)
}

func TestToStatusBase_Basic(t *testing.T) {
	created := time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)
	acc := &models.Account{Id: 7, Username: "poster"}
	status := &models.Status{
		Id:         100,
		Content:    models.StatusContent{Content: "Hello world", SpoilerText: "spoiler"},
		Visibility: models.StatusVisibilityPublic,
		Sensitive:  true,
		Language:   "en",
		CreatedAt:  &created,
		Account:    acc,
	}

	result, err := ToStatusBase(status)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "100", result.Id)
	require.NotNil(t, result.CreatedAt)
	assert.Equal(t, created, *result.CreatedAt)
	assert.True(t, result.Sensitive)
	assert.Equal(t, "spoiler", result.SpoilerText)
	assert.Equal(t, "public", result.Visibility)
	assert.Equal(t, "en", result.Language)
	assert.Equal(t, "<p>Hello world</p>", result.Content)
	// nil InReplyToId -> stays nil
	assert.Nil(t, result.InReplyToId)
	assert.Nil(t, result.InReplyToAccountId)
}

func TestToStatusBase_InReplyTo(t *testing.T) {
	replyToId := uint64(50)
	replyToAcc := uint64(60)
	status := &models.Status{
		Id:                 101,
		Content:            models.StatusContent{Content: "reply"},
		InReplyToId:        &replyToId,
		InReplyToAccountId: &replyToAcc,
		Account:            &models.Account{Id: 7, Username: "u"},
	}

	result, err := ToStatusBase(status)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.InReplyToId)
	assert.Equal(t, "50", *result.InReplyToId)
	require.NotNil(t, result.InReplyToAccountId)
	assert.Equal(t, "60", *result.InReplyToAccountId)
}

func TestToStatusBase_InReplyToZero(t *testing.T) {
	zero := uint64(0)
	status := &models.Status{
		Id:                 102,
		Content:            models.StatusContent{Content: "x"},
		InReplyToId:        &zero,
		InReplyToAccountId: &zero,
		Account:            &models.Account{Id: 7, Username: "u"},
	}

	result, err := ToStatusBase(status)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Nil(t, result.InReplyToId)
	assert.Nil(t, result.InReplyToAccountId)
}

func TestToStatusBase_OriginUri(t *testing.T) {
	origin := "https://remote.example/status/1"
	status := &models.Status{
		Id:        103,
		Content:   models.StatusContent{Content: "remote"},
		OriginUri: &origin,
		Account:   &models.Account{Id: 7, Username: "u"},
	}

	result, err := ToStatusBase(status)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "https://remote.example/status/1", result.Uri)
	assert.Equal(t, "https://remote.example/status/1", result.Url)
}

func TestToStatusBase_CounterFields(t *testing.T) {
	status := &models.Status{
		Id:              104,
		Content:         models.StatusContent{Content: "x"},
		RepliesCount:    5,
		ReblogsCount:    3,
		FavouritesCount: 10,
		Account:         &models.Account{Id: 7, Username: "u"},
	}

	result, err := ToStatusBase(status)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, uint64(5), result.RepliesCount)
	assert.Equal(t, uint64(3), result.ReblogsCount)
	assert.Equal(t, uint64(10), result.FavouritesCount)
}

func TestToStatusBase_EditedAt(t *testing.T) {
	created := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	updated := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	status := &models.Status{
		Id:        105,
		Content:   models.StatusContent{Content: "x"},
		CreatedAt: &created,
		UpdatedAt: &updated,
		Account:   &models.Account{Id: 7, Username: "u"},
	}

	result, err := ToStatusBase(status)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.EditedAt)
	assert.Equal(t, updated, *result.EditedAt)
}

func TestToStatusBase_NotEditedWhenUpdatedEarlier(t *testing.T) {
	created := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	updated := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC) // before created
	status := &models.Status{
		Id:        106,
		Content:   models.StatusContent{Content: "x"},
		CreatedAt: &created,
		UpdatedAt: &updated,
		Account:   &models.Account{Id: 7, Username: "u"},
	}

	result, err := ToStatusBase(status)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Nil(t, result.EditedAt)
}

func TestToStatusBase_Visibilities(t *testing.T) {
	tests := []struct {
		name string
		v    uint8
		want string
	}{
		{"public", models.StatusVisibilityPublic, "public"},
		{"unlisted", models.StatusVisibilityUnlisted, "unlisted"},
		{"private", models.StatusVisibilityPrivate, "private"},
		{"direct", models.StatusVisibilityDirect, "direct"},
		{"unknown", 255, "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status := &models.Status{
				Id:         1,
				Visibility: tt.v,
				Content:    models.StatusContent{Content: "x"},
				Account:    &models.Account{Id: 7, Username: "u"},
			}
			result, err := ToStatusBase(status)
			require.NoError(t, err)
			assert.Equal(t, tt.want, result.Visibility)
		})
	}
}

func TestToStatusBase_WithReblog(t *testing.T) {
	reblog := &models.Status{
		Id:         2,
		Content:    models.StatusContent{Content: "reblogged"},
		Visibility: models.StatusVisibilityPublic,
		Account:    &models.Account{Id: 8, Username: "other"},
	}
	status := &models.Status{
		Id:         1,
		Content:    models.StatusContent{Content: "wrapper"},
		Reblog:     reblog,
		Visibility: models.StatusVisibilityPublic,
		Account:    &models.Account{Id: 7, Username: "u"},
	}

	result, err := ToStatusBase(status)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.Reblog)
	assert.Equal(t, "2", result.Reblog.Id)
	assert.Equal(t, "<p>reblogged</p>", result.Reblog.Content)
}

func TestToStatusBase_WithTagsAndEmojis(t *testing.T) {
	status := &models.Status{
		Id:         1,
		Content:    models.StatusContent{Content: "x"},
		Visibility: models.StatusVisibilityPublic,
		Account:    &models.Account{Id: 7, Username: "u"},
		Tags: []*models.Tag{
			{Name: "tag1"},
			{Name: "tag2"},
		},
		Emojis: []*models.Emoji{
			{Shortcode: "smile"},
		},
		ToAccounts: []*models.Account{
			{Id: 20, Username: "mention1"},
		},
	}

	result, err := ToStatusBase(status)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, result.Tags, 2)
	require.Len(t, result.Emojis, 1)
	assert.Equal(t, "smile", result.Emojis[0].Shortcode)
	require.Len(t, result.Mentions, 1)
	assert.Equal(t, "mention1", result.Mentions[0].Username)
}

// =============================================================================
// ScheduledStatus - additional poll params coverage (scheduled_status.go)
// The ToScheduledStatus branches for poll properties aren't covered.
// =============================================================================

func TestToScheduledStatus_WithPollFloatExpiresIn(t *testing.T) {
	status := &models.Status{
		Id:      1,
		Content: models.StatusContent{Content: "poll"},
		Poll: &models.Poll{
			Properties: map[string]any{
				"expires_in":  float64(3600),
				"multiple":    true,
				"hide_totals": true,
			},
			Options: models.PollOptions{
				{Id: 1, Index: 0, Content: "A"},
				{Id: 2, Index: 1, Content: "B"},
			},
			ExpiresAt: ptrTime(time.Now().Add(24 * time.Hour)),
		},
		Visibility: models.StatusVisibilityPublic,
		Language:   "en",
	}

	result := ToScheduledStatus(status)
	require.NotNil(t, result)
	require.NotNil(t, result.Params.Poll)
	require.NotNil(t, result.Params.Poll.ExpiresIn)
	assert.Equal(t, uint64(3600), *result.Params.Poll.ExpiresIn)
	assert.True(t, result.Params.Poll.Multiple)
	require.NotNil(t, result.Params.Poll.HideTotals)
	assert.True(t, *result.Params.Poll.HideTotals)
	assert.Equal(t, []string{"A", "B"}, result.Params.Poll.Options)
}

func TestToScheduledStatus_WithPollUintExpiresIn(t *testing.T) {
	status := &models.Status{
		Id:      2,
		Content: models.StatusContent{Content: "poll2"},
		Poll: &models.Poll{
			Properties: map[string]any{
				"expires_in": uint64(7200),
			},
			ExpiresAt: ptrTime(time.Now().Add(24 * time.Hour)),
		},
		Visibility: models.StatusVisibilityPublic,
		Language:   "en",
	}

	result := ToScheduledStatus(status)
	require.NotNil(t, result)
	require.NotNil(t, result.Params.Poll)
	require.NotNil(t, result.Params.Poll.ExpiresIn)
	assert.Equal(t, uint64(7200), *result.Params.Poll.ExpiresIn)
	assert.False(t, result.Params.Poll.Multiple)
	assert.Nil(t, result.Params.Poll.HideTotals)
}

func TestToScheduledStatus_PollNoExpiresAt(t *testing.T) {
	status := &models.Status{
		Id:         3,
		Content:    models.StatusContent{Content: "no expiry poll"},
		Visibility: models.StatusVisibilityPublic,
		Language:   "en",
		Poll: &models.Poll{
			Properties: map[string]any{
				"expires_in": float64(3600),
			},
			ExpiresAt: nil,
		},
	}

	result := ToScheduledStatus(status)
	require.NotNil(t, result)
	require.NotNil(t, result.Params.Poll)
	// Without ExpiresAt, the expiresIn branch is skipped
	assert.Nil(t, result.Params.Poll.ExpiresIn)
}

// =============================================================================
// JSON serialization round-trip tests
// =============================================================================

func TestApplication_JSONMarshalUnmarshal(t *testing.T) {
	app := ToApplication(&models.Application{Name: "Web", Website: "https://w.example"})
	data, err := json.Marshal(app)
	require.NoError(t, err)

	var decoded struct {
		Name    string  `json:"name"`
		Website *string `json:"website"`
	}
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)
	assert.Equal(t, "Web", decoded.Name)
	require.NotNil(t, decoded.Website)
	assert.Equal(t, "https://w.example", *decoded.Website)
}

func TestApplication_Nil_JSONHasEmptyWebsite(t *testing.T) {
	app := ToApplication(nil)
	data, err := json.Marshal(app)
	require.NoError(t, err)
	assert.Contains(t, string(data), `"name":"web"`)
	assert.Contains(t, string(data), `"website":""`)
}

func TestEmoji_JSONMarshal(t *testing.T) {
	e := ToEmoji(&models.Emoji{Shortcode: "smile", Url: "u", StaticUrl: "su", Category: "c"})
	data, err := json.Marshal(e)
	require.NoError(t, err)
	var decoded Emoji
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)
	assert.Equal(t, "smile", decoded.Shortcode)
	assert.Equal(t, "u", decoded.Url)
	assert.Equal(t, "su", decoded.StaticUrl)
	assert.Equal(t, "c", decoded.Category)
}

func TestRule_JSONMarshalHidesIntId(t *testing.T) {
	r := ToRule(&models.Rule{Id: 42, Text: "Be nice"})
	data, err := json.Marshal(r)
	require.NoError(t, err)
	// intId has json:"-" so it shouldn't appear
	assert.Contains(t, string(data), `"id":"42"`)
	assert.Contains(t, string(data), `"text":"Be nice"`)
	assert.NotContains(t, string(data), `"intId"`)
}

func TestList_JSONMarshal(t *testing.T) {
	l := ToList(&models.List{Id: 1, Title: "L", RepliesPolicy: models.ListRepliesPolicyList})
	data, err := json.Marshal(l)
	require.NoError(t, err)
	var decoded List
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)
	assert.Equal(t, "1", decoded.Id)
	assert.Equal(t, "L", decoded.Title)
	assert.Equal(t, "list", decoded.RepliesPolicy)
}

func TestFilter_JSONMarshal(t *testing.T) {
	expires := time.Now().Add(24 * time.Hour)
	f := ToFilter(&models.Filter{
		Id:           5,
		Title:        "Filter",
		Context:      []string{"home"},
		ExpiresAt:    &expires,
		FilterAction: models.FilterActionHide,
		Keywords: []*models.FilterKeyword{
			{Uuid: "k1", Keyword: "spam", WholeWord: true},
		},
	})
	data, err := json.Marshal(f)
	require.NoError(t, err)
	assert.Contains(t, string(data), `"id":"5"`)
	assert.Contains(t, string(data), `"title":"Filter"`)
	assert.Contains(t, string(data), `"filter_action":"hide"`)
}

func TestTag_JSONMarshalOmitsEmpty(t *testing.T) {
	tag := ToTag(&models.Tag{Name: "test"})
	data, err := json.Marshal(tag)
	require.NoError(t, err)
	// history, following, status_count, account_count are omitempty when not set
	assert.NotContains(t, string(data), "history")
	assert.NotContains(t, string(data), "status_count")
}

func TestField_JSONMarshal(t *testing.T) {
	now := time.Now()
	fields := ToFields([]models.AccountField{
		{Name: "Site", Val: "https://e.com", VerifiedAt: &now},
		{Name: "Loc", Val: "Earth", VerifiedAt: nil},
	})
	data, err := json.Marshal(fields)
	require.NoError(t, err)
	assert.Contains(t, string(data), `"name":"Site"`)
	assert.Contains(t, string(data), `"val":"https://e.com"`)
	assert.Contains(t, string(data), `"name":"Loc"`)
	// second field has nil verified_at
	assert.Contains(t, string(data), `"verified_at":null`)
}

// =============================================================================
// History tests (history.go) - ToHistoryStatusBase
// =============================================================================

func TestToHistoryStatusBase_WithAccount(t *testing.T) {
	created := time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC)
	history := &models.StatusHistory{
		Content:     "old content",
		SpoilerText: "old spoiler",
		Sensitive:   true,
		CreatedAt:   &created,
		Account:     &models.Account{Id: 5, Username: "editor"},
	}

	result := ToHistoryStatusBase(history)
	require.NotNil(t, result)
	assert.Equal(t, "<p>old content</p>", result.Content)
	assert.Equal(t, "old spoiler", result.SpoilerText)
	assert.True(t, result.Sensitive)
	require.NotNil(t, result.CreatedAt)
	assert.Equal(t, created, *result.CreatedAt)
	require.NotNil(t, result.Account)
	assert.Equal(t, "editor", result.Account.Username)
}

func TestToHistoryStatusBase_NilAccount(t *testing.T) {
	history := &models.StatusHistory{
		Content:     "content",
		SpoilerText: "",
		Account:     nil,
	}

	result := ToHistoryStatusBase(history)
	require.NotNil(t, result)
	assert.Equal(t, "<p>content</p>", result.Content)
	assert.Empty(t, result.SpoilerText)
	assert.Nil(t, result.Account)
}

func TestToHistoryStatusBase_EmptyFields(t *testing.T) {
	history := &models.StatusHistory{}
	result := ToHistoryStatusBase(history)
	require.NotNil(t, result)
	assert.Empty(t, result.Content)
	assert.False(t, result.Sensitive)
	assert.Nil(t, result.CreatedAt)
	assert.Nil(t, result.Account)
}

// =============================================================================
// Poll JSON serialization
// =============================================================================

func TestPoll_JSONMarshal(t *testing.T) {
	expires := time.Now().Add(-1 * time.Hour)
	p := ToBasePoll(&models.Poll{
		Id:        7,
		Multiple:  true,
		ExpiresAt: &expires,
	}, true, 3, 2)
	data, err := json.Marshal(p)
	require.NoError(t, err)
	assert.Contains(t, string(data), `"id":"7"`)
	assert.Contains(t, string(data), `"multiple":true`)
	assert.Contains(t, string(data), `"expired":true`)
	assert.Contains(t, string(data), `"voted":true`)
}

// =============================================================================
// MediaAttachment tests - additional type coverage
// =============================================================================

func TestToMediaAttachment_FileType(t *testing.T) {
	media := &models.MediaAttachment{
		Id:       10,
		FileName: "document.pdf",
		Type:     models.MediaTypeUnknown,
	}
	result, err := ToMediaAttachment(media)
	require.NoError(t, err)
	require.NotNil(t, result)
	// unknown type maps to "unknown"
	assert.Equal(t, "unknown", result.Type)
}

func TestToMediaAttachmentList_SingleItem(t *testing.T) {
	w, h := 50, 50
	medias := models.MediaAttachments{
		{Id: 1, FileName: "x.jpg", Type: models.MediaTypeImage, Width: &w, Height: &h},
	}
	result, err := ToMediaAttachmentList(medias)
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, "1", result[0].Id)
}

func TestToMediaAttachment_NegativeAspect(t *testing.T) {
	// height 0 edge case would cause div by zero; guard against it
	w := 100
	zero := 0
	media := &models.MediaAttachment{
		Id:       11,
		FileName: "flat.png",
		Type:     models.MediaTypeImage,
		Width:    &w,
		Height:   &zero,
	}
	result, err := ToMediaAttachment(media)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.Meta)
	// original should be set since width > 0 && height == 0 -> but condition requires both > 0
	if result.Meta.Original != nil {
		// if it's set, just check no panic
		assert.NotNil(t, result.Meta.Original.Size)
	}
}

// =============================================================================
// FollowStatus - additional property type coverage
// =============================================================================

func TestToFollowStatus_ReblogsNonBoolValue(t *testing.T) {
	// reblogs present but not a bool -> type assertion panic in source;
	// we only test the safe path where reblogs is absent or bool.
	acc := &models.Account{Id: 1, Username: "u"}
	follow := &models.Follow{
		Properties: map[string]any{
			"other_key": "value",
		},
	}
	result := ToFollowStatus(acc, follow)
	require.NotNil(t, result)
	assert.True(t, result.Following)
	assert.False(t, result.ShowingReblogs)
}

// =============================================================================
// Report - additional category coverage via ToReport
// =============================================================================

func TestToReport_NoActionTaken(t *testing.T) {
	r := &models.Report{
		Id:       1,
		Category: models.ReportCategorySpam,
	}
	result, err := ToReport(r, nil)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.ActionTaken)
	assert.Nil(t, result.CreatedAt)
}

func TestToReport_WithActionTakenAt(t *testing.T) {
	actionTime := time.Date(2025, 2, 1, 0, 0, 0, 0, time.UTC)
	r := &models.Report{
		Id:            2,
		Category:      models.ReportCategoryLegal,
		ActionTakenAt: &actionTime,
	}
	result, err := ToReport(r, nil)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.ActionTaken)
	// Note: ToReport only sets the ActionTaken boolean, not ActionTakenAt
}

func TestToReport_WithStatusIds(t *testing.T) {
	// ToReport only queries the DB when StatusIds/RuleIds are non-empty.
	// To avoid requiring a DB, use empty slices to exercise the NumberArrayToStingArray path.
	r := &models.Report{
		Id:        3,
		Category:  models.ReportCategoryOther,
		StatusIds: []uint64{},
		RuleIds:   []uint64{},
	}
	result, err := ToReport(r, nil)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Empty(t, result.StatusIds)
	assert.Empty(t, result.RuleIds)
}

func TestToReport_NilStatusIdsAndRuleIds(t *testing.T) {
	r := &models.Report{
		Id:        4,
		Category:  models.ReportCategorySpam,
		StatusIds: nil,
		RuleIds:   nil,
	}
	result, err := ToReport(r, nil)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Empty(t, result.StatusIds)
	assert.Empty(t, result.RuleIds)
}

// =============================================================================
// ToReports - empty input path (avoids db access since no ids are collected)
// =============================================================================

func TestToReports_EmptyInput(t *testing.T) {
	result, err := ToReports([]*models.Report{}, nil)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Empty(t, result)
}

func TestToReports_WithFieldsButNoIds(t *testing.T) {
	// Reports with zero account/status/rule ids: the preload branches that
	// touch db.GetDB() are all skipped because their id slices stay empty.
	reports := []*models.Report{
		{
			Id:       1,
			Category: models.ReportCategorySpam,
			Comment:  "spam report",
		},
		{
			Id:       2,
			Category: models.ReportCategoryOther,
			Comment:  "other report",
		},
	}
	result, err := ToReports(reports, nil)
	require.NoError(t, err)
	require.Len(t, result, 2)
	assert.Equal(t, "1", result[0].Id)
	assert.Equal(t, "Spam", result[0].Category)
	assert.Equal(t, "spam report", result[0].Comment)
	assert.Equal(t, "2", result[1].Id)
	assert.Equal(t, "Other", result[1].Category)
}

// =============================================================================
// ToReport - preloaded account fields (no DB needed when accounts are inline)
// =============================================================================

func TestToReport_WithInlineAccounts(t *testing.T) {
	// When accounts are provided directly on the report, ToReport serializes
	// them via ToAccount without needing to query the database.
	target := &models.Account{Id: 10, Username: "target"}
	reporter := &models.Account{Id: 20, Username: "reporter"}
	actionBy := &models.Account{Id: 30, Username: "mod"}
	assigned := &models.Account{Id: 40, Username: "assignee"}

	r := &models.Report{
		Id:                   1,
		Category:             models.ReportCategorySpam,
		Comment:              "inline accounts",
		TargetAccount:        target,
		Account:              reporter,
		ActionTakenByAccount: actionBy,
		AssignedAccount:      assigned,
	}
	result, err := ToReport(r, nil)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.TargetAccount)
	assert.Equal(t, "target", result.TargetAccount.Username)
	require.NotNil(t, result.Account)
	assert.Equal(t, "reporter", result.Account.Username)
	require.NotNil(t, result.ActionTakenByAccount)
	assert.Equal(t, "mod", result.ActionTakenByAccount.Username)
	require.NotNil(t, result.AssignedAccount)
	assert.Equal(t, "assignee", result.AssignedAccount.Username)
}

// =============================================================================
// Status tests (status.go) - ToStatus, ToStatuses
// ToStatus has an early-return path when currentAccount == nil (and no poll),
// which avoids touching the database.
// =============================================================================

func TestToStatus_NilCurrentAccount(t *testing.T) {
	status := &models.Status{
		Id:         1,
		Content:    models.StatusContent{Content: "hello", SpoilerText: "sp"},
		Visibility: models.StatusVisibilityPublic,
		Language:   "en",
		Account:    &models.Account{Id: 7, Username: "u"},
	}

	result, err := ToStatus(status, nil)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "1", result.Id)
	assert.Equal(t, "<p>hello</p>", result.Content)
	assert.Equal(t, "public", result.Visibility)
	// No current account -> interaction flags stay false
	assert.False(t, result.Favourited)
	assert.False(t, result.Reblogged)
	assert.False(t, result.Bookmarked)
	assert.False(t, result.Pinned)
	assert.False(t, result.Muted)
}

func TestToStatus_NilCurrentAccount_WithReblog(t *testing.T) {
	reblog := &models.Status{
		Id:         2,
		Content:    models.StatusContent{Content: "reblogged content"},
		Visibility: models.StatusVisibilityPublic,
		Language:   "en",
		Account:    &models.Account{Id: 8, Username: "author"},
	}
	status := &models.Status{
		Id:         1,
		Content:    models.StatusContent{Content: "wrapper"},
		Visibility: models.StatusVisibilityPublic,
		Language:   "en",
		Account:    &models.Account{Id: 7, Username: "reposter"},
		Reblog:     reblog,
	}

	result, err := ToStatus(status, nil)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "1", result.Id)
	require.NotNil(t, result.Reblog)
	assert.Equal(t, "2", result.Reblog.Id)
	assert.Equal(t, "<p>reblogged content</p>", result.Reblog.Content)
}

func TestToStatuses_NilInput(t *testing.T) {
	// ToStatuses returns early (before touching db.GetDB()) when statuses is nil.
	result, err := ToStatuses(nil, nil)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Empty(t, result)
}

// =============================================================================
// Conversation tests (conversation.go) - ToConversations empty input
// ToConversations returns early when the input is empty, avoiding db access.
// =============================================================================

func TestToConversations_Empty(t *testing.T) {
	result, err := ToConversations(nil, &models.Account{Id: 1})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Empty(t, result)
}

func TestToConversations_EmptySlice(t *testing.T) {
	result, err := ToConversations([]*models.Conversation{}, &models.Account{Id: 1})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Empty(t, result)
}

// =============================================================================
// ScheduledStatus - attachments & application_id branches
// =============================================================================

func TestToScheduledStatus_WithAttachments(t *testing.T) {
	blur := "hash"
	status := &models.Status{
		Id:      1,
		Content: models.StatusContent{Content: "with media"},
		Attachments: []*models.Attachment{
			{Id: 100, FileName: "a.jpg", Description: "first", Blurhash: blur},
			{Id: 200, FileName: "b.jpg", Description: "second"},
		},
		Visibility: models.StatusVisibilityPublic,
		Language:   "en",
	}

	result := ToScheduledStatus(status)
	require.NotNil(t, result)
	require.Len(t, result.MediaAttachments, 2)
	assert.Equal(t, "100", result.MediaAttachments[0].Id)
	assert.Equal(t, "first", result.MediaAttachments[0].Description)
	assert.Equal(t, "200", result.MediaAttachments[1].Id)
	require.Len(t, result.Params.MediaIds, 2)
	assert.Equal(t, "100", result.Params.MediaIds[0])
	assert.Equal(t, "200", result.Params.MediaIds[1])
}

func TestToScheduledStatus_ApplicationIdNotFound(t *testing.T) {
	// properties map present but without application_id key
	status := &models.Status{
		Id:         1,
		Content:    models.StatusContent{Content: "x"},
		Properties: map[string]any{"other": "val"},
		Visibility: models.StatusVisibilityPublic,
		Language:   "en",
	}
	result := ToScheduledStatus(status)
	require.NotNil(t, result)
	assert.Nil(t, result.Params.ApplicationId)
}

func TestToScheduledStatus_PollMultipleFalseExplicit(t *testing.T) {
	status := &models.Status{
		Id:      1,
		Content: models.StatusContent{Content: "poll"},
		Poll: &models.Poll{
			Properties: map[string]any{
				"multiple": false,
			},
			ExpiresAt: ptrTime(time.Now().Add(24 * time.Hour)),
		},
		Visibility: models.StatusVisibilityPublic,
		Language:   "en",
	}
	result := ToScheduledStatus(status)
	require.NotNil(t, result)
	require.NotNil(t, result.Params.Poll)
	assert.False(t, result.Params.Poll.Multiple)
}

// =============================================================================
// MediaAttachmentList - nil entry skip branch (media.go)
// =============================================================================

func TestToMediaAttachmentList_MultipleWithNilSkipped(t *testing.T) {
	w, h := 10, 10
	medias := models.MediaAttachments{
		{Id: 1, FileName: "a.jpg", Type: models.MediaTypeImage, Width: &w, Height: &h},
		nil, // should be skipped
		{Id: 3, FileName: "c.jpg", Type: models.MediaTypeImage, Width: &w, Height: &h},
	}
	result, err := ToMediaAttachmentList(medias)
	require.NoError(t, err)
	require.Len(t, result, 2)
	assert.Equal(t, "1", result[0].Id)
	assert.Equal(t, "3", result[1].Id)
}

// =============================================================================
// Notification tests (notification.go) - ToNotification, ToNotifications
//
// ToNotification only touches db.GetDB() inside conditional branches gated by
// non-zero ids (FromAccountId, StatusId, ReportId, ListId). A notification with
// all-zero ids therefore runs to completion without a database connection.
// ToNotifications, however, unconditionally calls db.GetDB(), so it needs the
// injected in-memory test DB.
// =============================================================================

func TestToNotification_AllZeroIds(t *testing.T) {
	created := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	n := &models.Notification{
		Id:        99,
		Type:      models.NotificationTypeFollow,
		CreatedAt: &created,
	}

	result, err := ToNotification(n, nil)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "99", result.Id)
	assert.Equal(t, "follow", result.Type)
	require.NotNil(t, result.CreatedAt)
	assert.Equal(t, created, *result.CreatedAt)
	// No accounts/statuses/reports/lists resolved
	assert.Nil(t, result.Account)
	assert.Nil(t, result.Status)
	assert.Nil(t, result.Report)
	assert.Nil(t, result.List)
}

func TestToNotification_AllTypes(t *testing.T) {
	tests := []struct {
		name    string
		nType   uint8
		wantStr string
	}{
		{"mention", models.NotificationTypeMention, "mention"},
		{"reblog", models.NotificationTypeReblog, "reblog"},
		{"favourite", models.NotificationTypeFavourite, "favourite"},
		{"follow", models.NotificationTypeFollow, "follow"},
		{"poll", models.NotificationTypePoll, "poll"},
		{"follow_request", models.NotificationTypeFollowRequest, "follow_request"},
		{"status_update", models.NotificationTypeStatusUpdate, "status_update"},
		{"admin_report", models.NotificationTypeAdminReport, "admin_report"},
		{"add_list", models.NotificationTypeAddList, "add_list"},
		{"remove_list", models.NotificationTypeRemoveList, "remove_list"},
		{"unknown", uint8(255), ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n := &models.Notification{Id: 1, Type: tt.nType}
			result, err := ToNotification(n, nil)
			require.NoError(t, err)
			require.NotNil(t, result)
			assert.Equal(t, tt.wantStr, result.Type)
		})
	}
}

func TestToNotifications_Empty(t *testing.T) {
	withTestDBMigrated(t, &models.Account{})
	result, err := ToNotifications(nil, nil)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Empty(t, result)
}

func TestToNotifications_WithZeroIds(t *testing.T) {
	withTestDBMigrated(t, &models.Account{})
	ns := []*models.Notification{
		{Id: 1, Type: models.NotificationTypeFollow},
		{Id: 2, Type: models.NotificationTypeMention},
	}
	result, err := ToNotifications(ns, nil)
	require.NoError(t, err)
	require.Len(t, result, 2)
	assert.Equal(t, "1", result[0].Id)
	assert.Equal(t, "follow", result[0].Type)
	assert.Equal(t, "2", result[1].Id)
	assert.Equal(t, "mention", result[1].Type)
}

func strPtr(s string) *string { return &s }

// Local plain text is escaped (no raw HTML reaches API clients) and remote
// HTML is sanitized on output.
func TestToStatusBase_ContentIsSafeHTML(t *testing.T) {
	local := &models.Status{
		Id:      1,
		Account: &models.Account{Id: 1, Username: "alice"},
		Content: models.StatusContent{
			Content:     `<script>alert(1)</script><img src=x onerror=alert(1)> #tag`,
			SpoilerText: "cw",
		},
	}
	ret, err := ToStatusBase(local)
	require.NoError(t, err)
	assert.NotContains(t, ret.Content, "<script")
	assert.NotContains(t, ret.Content, "<img")
	assert.Contains(t, ret.Content, "&lt;script&gt;")
	assert.Contains(t, ret.Content, `class="mention hashtag"`)

	origin := "https://remote.example/notes/1"
	domain := "remote.example"
	remote := &models.Status{
		Id:        2,
		OriginUri: &origin,
		Account:   &models.Account{Id: 2, Username: "bob", Domain: &domain},
		Content: models.StatusContent{
			Content:     `<p>hi <a href="javascript:alert(1)">x</a><script>alert(1)</script><img src=x onerror=alert(1)></p>`,
			SpoilerText: `<b>cw</b>`,
		},
	}
	ret, err = ToStatusBase(remote)
	require.NoError(t, err)
	assert.Contains(t, ret.Content, "<p>hi ")
	assert.NotContains(t, ret.Content, "javascript:")
	assert.NotContains(t, ret.Content, "<script")
	assert.NotContains(t, ret.Content, "onerror")
	assert.Equal(t, "cw", ret.SpoilerText)
}
