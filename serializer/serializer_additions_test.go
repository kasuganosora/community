package serializer

import (
	"testing"
	"time"

	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// Application tests (application.go)
// =============================================================================

func TestToApplication_Nil(t *testing.T) {
	result := ToApplication(nil)
	require.NotNil(t, result)
	assert.Equal(t, "web", result.Name)
	assert.NotNil(t, result.Website)
	assert.Empty(t, *result.Website)
}

func TestToApplication_WithValues(t *testing.T) {
	website := "https://example.com"
	app := &models.Application{
		Name:    "TestApp",
		Website: website,
	}

	result := ToApplication(app)
	require.NotNil(t, result)
	assert.Equal(t, "TestApp", result.Name)
	require.NotNil(t, result.Website)
	assert.Equal(t, "https://example.com", *result.Website)
}

func TestToApplication_EmptyWebsite(t *testing.T) {
	app := &models.Application{
		Name:    "MyApp",
		Website: "",
	}

	result := ToApplication(app)
	require.NotNil(t, result)
	assert.Equal(t, "MyApp", result.Name)
	require.NotNil(t, result.Website)
	assert.Empty(t, *result.Website)
}

// =============================================================================
// Source tests (source.go)
// =============================================================================

func TestToSource_Basic(t *testing.T) {
	sc := &models.StatusContent{
		StatusId:    12345,
		Content:     "Hello, world!",
		SpoilerText: "Spoiler alert",
	}

	result := ToSource(sc)
	require.NotNil(t, result)
	assert.Equal(t, "12345", result.Id)
	assert.Equal(t, "Hello, world!", result.Text)
	assert.Equal(t, "Spoiler alert", result.SpoilerText)
}

func TestToSource_EmptyFields(t *testing.T) {
	sc := &models.StatusContent{
		StatusId:    1,
		Content:     "",
		SpoilerText: "",
	}

	result := ToSource(sc)
	require.NotNil(t, result)
	assert.Equal(t, "1", result.Id)
	assert.Empty(t, result.Text)
	assert.Empty(t, result.SpoilerText)
}

func TestToSource_LargeId(t *testing.T) {
	sc := &models.StatusContent{
		StatusId:    18446744073709551615, // max uint64
		Content:     "max id content",
		SpoilerText: "",
	}

	result := ToSource(sc)
	require.NotNil(t, result)
	assert.Equal(t, "18446744073709551615", result.Id)
	assert.Equal(t, "max id content", result.Text)
}

// =============================================================================
// List tests (list.go)
// =============================================================================

func TestToList_Basic(t *testing.T) {
	list := &models.List{
		Id:            42,
		Title:         "My Awesome List",
		RepliesPolicy: models.ListRepliesPolicyList,
	}

	result := ToList(list)
	require.NotNil(t, result)
	assert.Equal(t, "42", result.Id)
	assert.Equal(t, "My Awesome List", result.Title)
	assert.Equal(t, "list", result.RepliesPolicy)
}

func TestToList_FollowedPolicy(t *testing.T) {
	list := &models.List{
		Id:            10,
		Title:         "Followed List",
		RepliesPolicy: models.ListRepliesPolicyFollowed,
	}

	result := ToList(list)
	require.NotNil(t, result)
	assert.Equal(t, "10", result.Id)
	assert.Equal(t, "followed", result.RepliesPolicy)
}

func TestToList_NonePolicy(t *testing.T) {
	list := &models.List{
		Id:            5,
		Title:         "No Replies List",
		RepliesPolicy: models.ListRepliesPolicyNone,
	}

	result := ToList(list)
	require.NotNil(t, result)
	assert.Equal(t, "5", result.Id)
	assert.Equal(t, "none", result.RepliesPolicy)
}

func TestToList_DefaultPolicy(t *testing.T) {
	list := &models.List{
		Id:            99,
		Title:         "Default Policy",
		RepliesPolicy: 255, // unknown policy value
	}

	result := ToList(list)
	require.NotNil(t, result)
	assert.Equal(t, "list", result.RepliesPolicy) // defaults to "list"
}

func TestToLists_Multiple(t *testing.T) {
	lists := []*models.List{
		{Id: 1, Title: "List One", RepliesPolicy: models.ListRepliesPolicyList},
		{Id: 2, Title: "List Two", RepliesPolicy: models.ListRepliesPolicyFollowed},
		{Id: 3, Title: "List Three", RepliesPolicy: models.ListRepliesPolicyNone},
	}

	result := ToLists(lists)
	require.Len(t, result, 3)
	assert.Equal(t, "1", result[0].Id)
	assert.Equal(t, "List One", result[0].Title)
	assert.Equal(t, "list", result[0].RepliesPolicy)

	assert.Equal(t, "2", result[1].Id)
	assert.Equal(t, "List Two", result[1].Title)
	assert.Equal(t, "followed", result[1].RepliesPolicy)

	assert.Equal(t, "3", result[2].Id)
	assert.Equal(t, "List Three", result[2].Title)
	assert.Equal(t, "none", result[2].RepliesPolicy)
}

func TestToLists_Empty(t *testing.T) {
	result := ToLists([]*models.List{})
	assert.NotNil(t, result)
	assert.Empty(t, result)
}

func TestToLists_Nil(t *testing.T) {
	result := ToLists(nil)
	assert.NotNil(t, result)
	assert.Empty(t, result)
}

// =============================================================================
// Rule tests (rule.go) - Additional coverage
// =============================================================================

func TestToRule_IntIdField(t *testing.T) {
	rule := &models.Rule{
		Id:   999,
		Text: "Test rule with large ID",
	}

	result := ToRule(rule)
	require.NotNil(t, result)
	assert.Equal(t, "999", result.Id)
	assert.Equal(t, "Test rule with large ID", result.Text)
	assert.Equal(t, uint64(999), result.intId)
}

func TestToRule_ZeroId(t *testing.T) {
	rule := &models.Rule{
		Id:   0,
		Text: "",
	}

	result := ToRule(rule)
	require.NotNil(t, result)
	assert.Equal(t, "0", result.Id)
	assert.Empty(t, result.Text)
	assert.Equal(t, uint64(0), result.intId)
}

func TestToRule_LargeId(t *testing.T) {
	rule := &models.Rule{
		Id:   18446744073709551615, // max uint64
		Text: "Max ID rule",
	}

	result := ToRule(rule)
	require.NotNil(t, result)
	assert.Equal(t, "18446744073709551615", result.Id)
	assert.Equal(t, "Max ID rule", result.Text)
	assert.Equal(t, uint64(18446744073709551615), result.intId)
}

func TestToRules_SingleItem(t *testing.T) {
	rules := []*models.Rule{
		{Id: 1, Text: "Only rule"},
	}

	result := ToRules(rules)
	require.Len(t, result, 1)
	assert.Equal(t, "1", result[0].Id)
	assert.Equal(t, "Only rule", result[0].Text)
}

// =============================================================================
// FollowStatus tests (follow_status.go)
// =============================================================================

func TestToFollowStatus_NilFollow(t *testing.T) {
	account := &models.Account{
		Id:       42,
		Username: "testuser",
	}

	result := ToFollowStatus(account, nil)
	require.NotNil(t, result)
	assert.Equal(t, uint64(42), result.Id)
	assert.False(t, result.Following)
	assert.False(t, result.ShowingReblogs)
	assert.False(t, result.Notifying)
	assert.Nil(t, result.Languages)
	assert.False(t, result.FollowedBy)
	assert.False(t, result.Blocking)
	assert.False(t, result.BlockedBy)
	assert.False(t, result.Muting)
	assert.False(t, result.MutingNotifications)
	assert.False(t, result.Requested)
	assert.False(t, result.RequestedBy)
	assert.False(t, result.DomainBlocking)
	assert.False(t, result.Endorsed)
	assert.Empty(t, result.Note)
}

func TestToFollowStatus_WithReblogsTrue(t *testing.T) {
	account := &models.Account{
		Id:       1,
		Username: "user1",
	}
	follow := &models.Follow{
		Properties: map[string]any{
			"reblogs": true,
		},
	}

	result := ToFollowStatus(account, follow)
	require.NotNil(t, result)
	assert.Equal(t, uint64(1), result.Id)
	assert.True(t, result.Following)
	assert.True(t, result.ShowingReblogs)
}

func TestToFollowStatus_WithReblogsFalse(t *testing.T) {
	account := &models.Account{
		Id:       2,
		Username: "user2",
	}
	follow := &models.Follow{
		Properties: map[string]any{
			"reblogs": false,
		},
	}

	result := ToFollowStatus(account, follow)
	require.NotNil(t, result)
	assert.True(t, result.Following)
	assert.False(t, result.ShowingReblogs)
}

func TestToFollowStatus_NilProperties(t *testing.T) {
	account := &models.Account{
		Id:       3,
		Username: "user3",
	}
	follow := &models.Follow{
		Properties: nil,
	}

	result := ToFollowStatus(account, follow)
	require.NotNil(t, result)
	assert.True(t, result.Following)
	assert.False(t, result.ShowingReblogs)
}

func TestToFollowStatus_ReblogsKeyNil(t *testing.T) {
	account := &models.Account{
		Id:       4,
		Username: "user4",
	}
	follow := &models.Follow{
		Properties: map[string]any{},
	}

	result := ToFollowStatus(account, follow)
	require.NotNil(t, result)
	assert.True(t, result.Following)
	// When reblogs key doesn't exist, isReblog stays false
	assert.False(t, result.ShowingReblogs)
}

func TestToFollowStatus_ZeroAccountId(t *testing.T) {
	account := &models.Account{
		Id:       0,
		Username: "zero",
	}

	result := ToFollowStatus(account, nil)
	require.NotNil(t, result)
	assert.Equal(t, uint64(0), result.Id)
	assert.False(t, result.Following)
}

// =============================================================================
// ScheduledStatus tests (scheduled_status.go)
// =============================================================================

func TestToScheduledStatus_Basic(t *testing.T) {
	status := &models.Status{
		Id:         100,
		Content:    models.StatusContent{Content: "Hello world", SpoilerText: "warning"},
		Sensitive:  true,
		Visibility: models.StatusVisibilityPublic,
		Language:   "en",
	}

	result := ToScheduledStatus(status)
	require.NotNil(t, result)
	assert.Equal(t, "100", result.Id)
	assert.Equal(t, "Hello world", result.Params.Text)
	assert.Equal(t, "warning", result.Params.SpoilerText)
	assert.True(t, result.Params.Sensitive)
	assert.Equal(t, "public", result.Params.Visibility)
	require.NotNil(t, result.Params.Language)
	assert.Equal(t, "en", *result.Params.Language)
	assert.Nil(t, result.Params.ScheduledAt)
	assert.Nil(t, result.Params.ApplicationId)
	assert.Nil(t, result.Params.InReplyToId)
	assert.NotNil(t, result.MediaAttachments)
	assert.Empty(t, result.MediaAttachments)
	assert.NotNil(t, result.Params.MediaIds)
	assert.Empty(t, result.Params.MediaIds)
}

func TestToScheduledStatus_WithScheduledAt(t *testing.T) {
	scheduledAt := time.Date(2025, 12, 25, 10, 30, 0, 0, time.UTC)
	status := &models.Status{
		Id:          200,
		ScheduledAt: &scheduledAt,
		Content:     models.StatusContent{Content: "Future post"},
		Visibility:  models.StatusVisibilityPrivate,
		Language:    "ja",
	}

	result := ToScheduledStatus(status)
	require.NotNil(t, result)
	assert.Equal(t, "200", result.Id)
	require.NotNil(t, result.ScheduledAt)
	assert.Equal(t, scheduledAt, *result.ScheduledAt)
	require.NotNil(t, result.Params.ScheduledAt)
	assert.Equal(t, "2025-12-25T10:30:00Z", *result.Params.ScheduledAt)
	assert.Equal(t, "private", result.Params.Visibility)
}

func TestToScheduledStatus_WithInReplyToId(t *testing.T) {
	inReplyToId := uint64(500)
	status := &models.Status{
		Id:          300,
		InReplyToId: &inReplyToId,
		Content:     models.StatusContent{Content: "A reply"},
		Visibility:  models.StatusVisibilityDirect,
		Language:    "fr",
	}

	result := ToScheduledStatus(status)
	require.NotNil(t, result)
	assert.Equal(t, "300", result.Id)
	require.NotNil(t, result.Params.InReplyToId)
	assert.Equal(t, "500", *result.Params.InReplyToId)
	assert.Equal(t, "direct", result.Params.Visibility)
}

func TestToScheduledStatus_WithApplicationId(t *testing.T) {
	status := &models.Status{
		Id:         400,
		Content:    models.StatusContent{Content: "App post"},
		Properties: map[string]any{"application_id": uint64(42)},
		Visibility: models.StatusVisibilityUnlisted,
		Language:   "de",
	}

	result := ToScheduledStatus(status)
	require.NotNil(t, result)
	assert.Equal(t, "400", result.Id)
	require.NotNil(t, result.Params.ApplicationId)
	assert.Equal(t, uint64(42), *result.Params.ApplicationId)
	assert.Equal(t, "unlisted", result.Params.Visibility)
}

func TestToScheduledStatus_DifferentVisibilities(t *testing.T) {
	tests := []struct {
		name       string
		visibility uint8
		expected   string
	}{
		{"public", models.StatusVisibilityPublic, "public"}, //nolint:goconst
		{"unlisted", models.StatusVisibilityUnlisted, "unlisted"},
		{"private", models.StatusVisibilityPrivate, "private"},
		{"direct", models.StatusVisibilityDirect, "direct"},
		{"unknown", uint8(255), "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status := &models.Status{
				Id:         1,
				Visibility: tt.visibility,
				Content:    models.StatusContent{Content: "test"},
				Language:   "en",
			}

			result := ToScheduledStatus(status)
			assert.Equal(t, tt.expected, result.Params.Visibility)
		})
	}
}

func TestToScheduledStatuses_Multiple(t *testing.T) {
	scheduledAt := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)
	statuses := []*models.Status{
		{
			Id:          1,
			Content:     models.StatusContent{Content: "First"},
			Language:    "en",
			ScheduledAt: &scheduledAt,
		},
		{
			Id:          2,
			Content:     models.StatusContent{Content: "Second"},
			Language:    "ja",
			ScheduledAt: &scheduledAt,
		},
	}

	result := ToScheduledStatuses(statuses)
	require.Len(t, result, 2)
	assert.Equal(t, "1", result[0].Id)
	assert.Equal(t, "First", result[0].Params.Text)
	assert.Equal(t, "2", result[1].Id)
	assert.Equal(t, "Second", result[1].Params.Text)
}

func TestToScheduledStatuses_Empty(t *testing.T) {
	result := ToScheduledStatuses(nil)
	require.Nil(t, result)
}

func TestToScheduledStatuses_Nil(t *testing.T) {
	result := ToScheduledStatuses([]*models.Status{})
	require.Nil(t, result)
}

func TestToScheduledStatus_WithNilPoll(t *testing.T) {
	status := &models.Status{
		Id:       50,
		Content:  models.StatusContent{Content: "No poll"},
		Poll:     nil,
		Language: "en",
	}

	result := ToScheduledStatus(status)
	require.NotNil(t, result)
	assert.Equal(t, "50", result.Id)
}

// =============================================================================
// Report tests (report.go) - minimal tests without DB
// =============================================================================

func TestToReport_Minimal(t *testing.T) {
	r := &models.Report{
		Id:        1,
		Comment:   "Test report",
		Category:  models.ReportCategorySpam,
		Forwarded: true,
	}

	result, err := ToReport(r, nil)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "1", result.Id)
	assert.Equal(t, "Test report", result.Comment)
	assert.Equal(t, "Spam", result.Category)
	assert.True(t, result.Forwarded)
	assert.False(t, result.ActionTaken)
	assert.NotNil(t, result.Statuses)
	assert.Empty(t, result.Statuses)
	assert.NotNil(t, result.Rules)
	assert.Empty(t, result.Rules)
}

func TestToReport_WithActionTaken(t *testing.T) {
	actionTime := time.Date(2025, 1, 15, 10, 0, 0, 0, time.UTC)
	r := &models.Report{
		Id:            2,
		Comment:       "Resolved report",
		Category:      models.ReportCategoryViolation,
		ActionTakenAt: &actionTime,
		Forwarded:     false,
	}

	result, err := ToReport(r, nil)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "2", result.Id)
	assert.True(t, result.ActionTaken)
	assert.Equal(t, "Violation", result.Category)
	assert.False(t, result.Forwarded)
}

func TestToReport_AllCategories(t *testing.T) {
	categories := []struct {
		category uint16
		expected string
	}{
		{models.ReportCategoryOther, "Other"},
		{models.ReportCategorySpam, "Spam"},
		{models.ReportCategoryLegal, "Legal"},
		{models.ReportCategoryViolation, "Violation"},
		{uint16(255), "Unknown"},
	}

	for _, cat := range categories {
		r := &models.Report{
			Id:       1,
			Category: cat.category,
		}

		result, err := ToReport(r, nil)
		require.NoError(t, err)
		assert.Equal(t, cat.expected, result.Category)
	}
}

func TestToReport_WithCreatedAt(t *testing.T) {
	createdAt := time.Date(2025, 3, 20, 14, 30, 0, 0, time.UTC)
	r := &models.Report{
		Id:        10,
		Category:  models.ReportCategoryOther,
		CreatedAt: &createdAt,
	}

	result, err := ToReport(r, nil)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.CreatedAt)
	assert.Equal(t, createdAt, *result.CreatedAt)
}

func TestToReport_EmptyComment(t *testing.T) {
	r := &models.Report{
		Id:        5,
		Comment:   "",
		Category:  models.ReportCategoryLegal,
		Forwarded: false,
	}

	result, err := ToReport(r, nil)
	require.NoError(t, err)
	assert.Empty(t, result.Comment)
	assert.False(t, result.Forwarded)
}

func TestToReports_Minimal(t *testing.T) {
	reports := []*models.Report{
		{Id: 1, Category: models.ReportCategorySpam, Comment: "Spam account"},
		{Id: 2, Category: models.ReportCategoryOther, Comment: "Other issue"},
		{Id: 3, Category: models.ReportCategoryViolation, Comment: "Rule violation"},
	}

	result, err := ToReports(reports, nil)
	require.NoError(t, err)
	require.Len(t, result, 3)

	assert.Equal(t, "1", result[0].Id)
	assert.Equal(t, "Spam", result[0].Category)
	assert.Equal(t, "Spam account", result[0].Comment)

	assert.Equal(t, "2", result[1].Id)
	assert.Equal(t, "Other", result[1].Category)

	assert.Equal(t, "3", result[2].Id)
	assert.Equal(t, "Violation", result[2].Category)
	assert.Equal(t, "Rule violation", result[2].Comment)
}

func TestToReports_Empty(t *testing.T) {
	result, err := ToReports([]*models.Report{}, nil)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Empty(t, result)
}

func TestToReports_WithStatusIdsAndRuleIds(t *testing.T) {
	// Test the NumberArrayToStingArray conversion with empty slices
	r := &models.Report{
		Id:        10,
		Category:  models.ReportCategorySpam,
		StatusIds: []uint64{},
		RuleIds:   []uint64{},
	}

	result, err := ToReport(r, nil)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.NotNil(t, result.StatusIds)
	assert.Empty(t, result.StatusIds)
	assert.NotNil(t, result.RuleIds)
	assert.Empty(t, result.RuleIds)
}

// =============================================================================
// Media tests (media.go)
// =============================================================================

func TestToMediaAttachment_Nil(t *testing.T) {
	result, err := ToMediaAttachment(nil)
	require.NoError(t, err)
	assert.Nil(t, result)
}

func TestToMediaAttachment_Image(t *testing.T) {
	width := 1920
	height := 1080
	thumbnailPath := "thumb.jpg"
	media := &models.MediaAttachment{
		Id:            1,
		FileName:      "photo.jpg",
		Type:          models.MediaTypeImage,
		Width:         &width,
		Height:        &height,
		ThumbnailPath: &thumbnailPath,
		Description:   "A test photo",
		Blurhash:      "L6Pj0^i_.AyE_3t7t7R**0o#DgR4",
		FocusX:        0.5,
		FocusY:        -0.5,
	}

	result, err := ToMediaAttachment(media)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "1", result.Id)
	assert.Equal(t, "image", result.Type)
	assert.Contains(t, result.Url, "/media/1/photo.jpg")
	assert.Contains(t, result.PreviewUrl, "/media/1/thumbnail/thumb.jpg")
	assert.Equal(t, "A test photo", result.Description)
	assert.Equal(t, "L6Pj0^i_.AyE_3t7t7R**0o#DgR4", result.Blurhash)
	assert.Nil(t, result.RemoteUrl)
	assert.Nil(t, result.PreviewRemoteUrl)
	assert.Nil(t, result.TextUrl)

	// Check meta
	require.NotNil(t, result.Meta)
	require.NotNil(t, result.Meta.Focus)
	assert.InDelta(t, 0.5, result.Meta.Focus.X, 1e-9)
	assert.InDelta(t, -0.5, result.Meta.Focus.Y, 1e-9)

	// Check original dimensions
	require.NotNil(t, result.Meta.Original)
	assert.Equal(t, 1920, result.Meta.Original.Width)
	assert.Equal(t, 1080, result.Meta.Original.Height)
	assert.Equal(t, "1920x1080", result.Meta.Original.Size)
	assert.InDelta(t, 1.7777, result.Meta.Original.Aspect, 0.001)

	// Check small thumbnail dimensions
	require.NotNil(t, result.Meta.Small)
	assert.Equal(t, 400, result.Meta.Small.Width)
	assert.Equal(t, 225, result.Meta.Small.Height)
	assert.Contains(t, result.Meta.Small.Size, "400x")
}

func TestToMediaAttachment_NoThumbnail(t *testing.T) {
	width := 800
	height := 600
	media := &models.MediaAttachment{
		Id:       2,
		FileName: "image.png",
		Type:     models.MediaTypeImage,
		Width:    &width,
		Height:   &height,
	}

	result, err := ToMediaAttachment(media)
	require.NoError(t, err)
	require.NotNil(t, result)
	// When no thumbnail, preview URL should be same as URL
	assert.Equal(t, result.Url, result.PreviewUrl)
}

func TestToMediaAttachment_EmptyThumbnailPath(t *testing.T) {
	width := 800
	height := 600
	emptyPath := ""
	media := &models.MediaAttachment{
		Id:            3,
		FileName:      "image2.png",
		Type:          models.MediaTypeImage,
		Width:         &width,
		Height:        &height,
		ThumbnailPath: &emptyPath,
	}

	result, err := ToMediaAttachment(media)
	require.NoError(t, err)
	require.NotNil(t, result)
	// Empty string thumbnail path → preview URL should be same as URL
	assert.Equal(t, result.Url, result.PreviewUrl)
}

func TestToMediaAttachment_NoDimensions(t *testing.T) {
	media := &models.MediaAttachment{
		Id:       4,
		FileName: "unknown.jpg",
		Type:     models.MediaTypeUnknown,
	}

	result, err := ToMediaAttachment(media)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "unknown", result.Type)
	require.NotNil(t, result.Meta)
	require.NotNil(t, result.Meta.Focus)
	// No original/small when no dimensions
	assert.Nil(t, result.Meta.Original)
	assert.Nil(t, result.Meta.Small)
}

func TestToMediaAttachment_WithDuration(t *testing.T) {
	width := 1920
	height := 1080
	duration := 120.5
	media := &models.MediaAttachment{
		Id:       5,
		FileName: "video.mp4",
		Type:     models.MediaTypeVideo,
		Width:    &width,
		Height:   &height,
		Duration: &duration,
	}

	result, err := ToMediaAttachment(media)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "video", result.Type)
	require.NotNil(t, result.Meta)
	require.NotNil(t, result.Meta.Original)
	assert.InDelta(t, 120.5, result.Meta.Original.Duration, 0.001)
}

func TestToMediaAttachment_DurationWithoutDimensions(t *testing.T) {
	duration := 45.0
	media := &models.MediaAttachment{
		Id:       6,
		FileName: "audio.mp3",
		Type:     models.MediaTypeAudio,
		Duration: &duration,
	}

	result, err := ToMediaAttachment(media)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "audio", result.Type)
	require.NotNil(t, result.Meta)
	require.NotNil(t, result.Meta.Original)
	assert.InDelta(t, 45.0, result.Meta.Original.Duration, 0.001)
}

func TestToMediaAttachment_ZeroDuration(t *testing.T) {
	width := 640
	height := 480
	duration := 0.0
	media := &models.MediaAttachment{
		Id:       7,
		FileName: "video.mp4",
		Type:     models.MediaTypeVideo,
		Width:    &width,
		Height:   &height,
		Duration: &duration,
	}

	result, err := ToMediaAttachment(media)
	require.NoError(t, err)
	require.NotNil(t, result)
	// Duration of 0 should not be included
	require.NotNil(t, result.Meta)
	require.NotNil(t, result.Meta.Original)
	assert.InDelta(t, float64(0), result.Meta.Original.Duration, 1e-9)
}

func TestToMediaAttachment_AudioType(t *testing.T) {
	media := &models.MediaAttachment{
		Id:       8,
		FileName: "sound.wav",
		Type:     models.MediaTypeAudio,
	}

	result, err := ToMediaAttachment(media)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "audio", result.Type)
}

func TestToMediaAttachment_FocusCoordinates(t *testing.T) {
	media := &models.MediaAttachment{
		Id:       9,
		FileName: "photo.jpg",
		Type:     models.MediaTypeImage,
		FocusX:   1.0,
		FocusY:   -1.0,
	}

	result, err := ToMediaAttachment(media)
	require.NoError(t, err)
	require.NotNil(t, result.Meta)
	require.NotNil(t, result.Meta.Focus)
	assert.InDelta(t, 1.0, result.Meta.Focus.X, 0)
	assert.InDelta(t, -1.0, result.Meta.Focus.Y, 0)
}

func TestToMediaAttachmentList_Empty(t *testing.T) {
	result, err := ToMediaAttachmentList(nil)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Empty(t, result)
}

func TestToMediaAttachmentList_WithItems(t *testing.T) {
	width1 := 100
	height1 := 100
	width2 := 200
	height2 := 200

	medias := models.MediaAttachments{
		{Id: 1, FileName: "a.jpg", Type: models.MediaTypeImage, Width: &width1, Height: &height1},
		{Id: 2, FileName: "b.png", Type: models.MediaTypeImage, Width: &width2, Height: &height2},
	}

	result, err := ToMediaAttachmentList(medias)
	require.NoError(t, err)
	require.Len(t, result, 2)
	assert.Equal(t, "1", result[0].Id)
	assert.Equal(t, "image", result[0].Type)
	assert.Equal(t, "2", result[1].Id)
	assert.Equal(t, "image", result[1].Type)
}

func TestToMediaAttachmentList_WithNilItems(t *testing.T) {
	medias := models.MediaAttachments{nil}
	result, err := ToMediaAttachmentList(medias)
	require.NoError(t, err)
	require.Empty(t, result) // nil items should be skipped
}

func TestToMediaAttachment_VerifyBaseURL(t *testing.T) {
	media := &models.MediaAttachment{
		Id:       1,
		FileName: "test.jpg",
		Type:     models.MediaTypeImage,
	}

	result, err := ToMediaAttachment(media)
	require.NoError(t, err)
	require.NotNil(t, result)
	// Should contain the base URL from config
	assert.Contains(t, result.Url, "localhost")
}

// =============================================================================
// History tests (history.go) - ToHistoryStatusBase tests
// Note: ToHistoryStatusBase calls ToAccount which requires DB via systemsettings.
// These tests verify the pure logic portions that don't require DB.
// =============================================================================

func TestHistoryStatusStruct_Fields(t *testing.T) {
	// Verify the HistoryStatus struct has expected fields
	hs := &HistoryStatus{
		Content:          "Test content",
		SpoilerText:      "Test spoiler",
		Sensitive:        true,
		Account:          nil,
		Emojis:           nil,
		MediaAttachments: nil,
	}

	assert.Equal(t, "Test content", hs.Content)
	assert.Equal(t, "Test spoiler", hs.SpoilerText)
	assert.True(t, hs.Sensitive)
}

// =============================================================================
// Conversation tests (conversation.go)
// Note: ToConversation and ToConversations require database access for all code
// paths. These tests verify struct definitions and type behavior.
// =============================================================================

func TestConversationStruct_Fields(t *testing.T) {
	conv := &Conversation{
		Id:       "123",
		Unread:   true,
		Accounts: nil,
	}

	assert.Equal(t, "123", conv.Id)
	assert.True(t, conv.Unread)
	assert.Nil(t, conv.Accounts)
	assert.Nil(t, conv.LastStatus)
}

// =============================================================================
// Notification tests (notification.go)
// Note: ToNotification and ToNotifications require database access for all code
// paths (account loading via systemsettings). These tests verify struct fields.
// =============================================================================

func TestNotificationStruct_Fields(t *testing.T) {
	now := time.Now()
	n := &Notification{
		Id:        "1",
		Type:      "mention",
		CreatedAt: &now,
	}

	assert.Equal(t, "1", n.Id)
	assert.Equal(t, "mention", n.Type)
	require.NotNil(t, n.CreatedAt)
	assert.Nil(t, n.Account)
	assert.Nil(t, n.Status)
	assert.Nil(t, n.Report)
	assert.Nil(t, n.List)
}

func TestNotificationGetTypeString_AllTypes(t *testing.T) {
	tests := []struct {
		notifType uint8
		expected  string
	}{
		{models.NotificationTypeMention, "mention"},
		{models.NotificationTypeReblog, "reblog"},
		{models.NotificationTypeFavourite, "favourite"},
		{models.NotificationTypeFollow, "follow"},
		{models.NotificationTypePoll, "poll"},
		{models.NotificationTypeFollowRequest, "follow_request"},
		{models.NotificationTypeStatusUpdate, "status_update"},
		{models.NotificationTypeAdminReport, "admin_report"},
		{models.NotificationTypeAddList, "add_list"},
		{models.NotificationTypeRemoveList, "remove_list"},
		{uint8(255), ""},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			n := &models.Notification{Type: tt.notifType}
			assert.Equal(t, tt.expected, n.GetTypeString())
		})
	}
}

func TestReportToCategoryString_AllCategories(t *testing.T) {
	tests := []struct {
		category uint16
		expected string
	}{
		{models.ReportCategoryOther, "Other"},
		{models.ReportCategorySpam, "Spam"},
		{models.ReportCategoryLegal, "Legal"},
		{models.ReportCategoryViolation, "Violation"},
		{uint16(99), "Unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			r := &models.Report{Category: tt.category}
			assert.Equal(t, tt.expected, r.ToCategoryString())
		})
	}
}

func TestReportGetStatusString(t *testing.T) {
	actionTime := time.Now()

	t.Run("resolved", func(t *testing.T) {
		r := &models.Report{ActionTakenAt: &actionTime}
		assert.Equal(t, "Resolved", r.GetStatusString())
	})

	t.Run("unresolved", func(t *testing.T) {
		r := &models.Report{ActionTakenAt: nil}
		assert.Equal(t, "Unresolved", r.GetStatusString())
	})
}

func TestListGetRepliesPolicy_AllPolicies(t *testing.T) {
	tests := []struct {
		policy   uint8
		expected string
	}{
		{models.ListRepliesPolicyList, "list"},
		{models.ListRepliesPolicyFollowed, "followed"},
		{models.ListRepliesPolicyNone, "none"},
		{uint8(255), "list"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			l := &models.List{RepliesPolicy: tt.policy}
			assert.Equal(t, tt.expected, l.GetRepliesPolicy())
		})
	}
}
