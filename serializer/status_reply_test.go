package serializer

import (
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestReplyPreviewsRespectVisibilityAndStayOneLevel(t *testing.T) {
	conn := timelineBatchDB(t)
	require.NoError(t, conn.AutoMigrate(&models.AccountExtra{}, &models.StatusHistory{}))
	author := &models.Account{Id: 20, Username: "parent"}
	writer := &models.Account{Id: 21, Username: "reply"}
	viewer := &models.Account{Id: 22, Username: "viewer"}
	require.NoError(t, conn.Create(author).Error)
	parent := batchStatus(200, author, models.StatusVisibilityPublic)
	grandparentID := uint64(999)
	parent.InReplyToId = &grandparentID
	parent.Content.StatusId = parent.Id
	parent.Content.Content = "context body"
	parent.Content.SpoilerText = "warning"
	require.NoError(t, conn.Omit("Account", "ToAccounts").Create(parent).Error)
	child := batchStatus(201, writer, models.StatusVisibilityPublic)
	child.InReplyToId = &parent.Id
	get := func(viewer *models.Account) *Status {
		out, err := ToStatuses(models.Statuses{child}, viewer)
		require.NoError(t, err)
		require.Len(t, out, 1)
		return out[0]
	}
	out := get(viewer)
	require.NotNil(t, out.Reply)
	require.Equal(t, "warning", out.Reply.SpoilerText)
	require.Contains(t, out.Reply.Content, "context body")
	wire, err := json.Marshal(out.Reply)
	require.NoError(t, err)
	require.NotContains(t, string(wire), `"reply"`)
	require.NotContains(t, string(wire), `"media_attachments"`)
	// Public replies cannot launder private, blocked, muted or deleted parents.
	require.NoError(t, conn.Model(parent).UpdateColumn("visibility", models.StatusVisibilityDirect).Error)
	require.Nil(t, get(viewer).Reply)
	require.Nil(t, get(nil).Reply)
	require.NoError(t, conn.Create(&models.StatusToAccount{StatusId: parent.Id, AccountId: viewer.Id}).Error)
	require.NotNil(t, get(viewer).Reply)
	require.NoError(t, conn.Create(&models.Block{AccountId: author.Id, TargetAccountId: viewer.Id}).Error)
	require.Nil(t, get(viewer).Reply)
	require.NoError(t, conn.Where("account_id = ?", author.Id).Delete(&models.Block{}).Error)
	require.NoError(t, conn.Create(&models.MuteAccount{AccountId: viewer.Id, TargetId: author.Id}).Error)
	require.Nil(t, get(viewer).Reply)
	require.NoError(t, conn.Where("account_id = ?", viewer.Id).Delete(&models.MuteAccount{}).Error)
	now := time.Now()
	require.NoError(t, conn.Model(parent).UpdateColumn("deleted_at", now).Error)
	require.Nil(t, get(viewer).Reply)
}

func TestReplyPreviewsAreBatchedForRepliesAndBoosts(t *testing.T) {
	conn := timelineBatchDB(t)
	require.NoError(t, conn.AutoMigrate(&models.AccountExtra{}, &models.StatusHistory{}))
	author := &models.Account{Id: 10, Username: "parent"}
	require.NoError(t, conn.Create(author).Error)
	for i := uint64(1); i <= 80; i++ {
		parent := batchStatus(i, author, models.StatusVisibilityPublic)
		parent.Content.StatusId = i
		require.NoError(t, conn.Omit("Account", "ToAccounts").Create(parent).Error)
	}
	counter := &timelineSQLCounter{Interface: logger.Discard}
	counts := []int{}
	for _, count := range []int{20, 80} {
		out := make([]*Status, count)
		for i := range out {
			id := strconv.FormatUint(uint64(i+1), 10)
			child := &Status{Id: strconv.FormatUint(uint64(i+101), 10), InReplyToId: &id}
			if i%2 == 0 {
				out[i] = &Status{Id: "boost", Reblog: child}
			} else {
				out[i] = child
			}
		}
		counter.queries = nil
		require.NoError(t, attachReplyPreviews(conn.Session(&gorm.Session{Logger: counter}), out, nil))
		counts = append(counts, len(counter.queries))
		require.NotNil(t, out[0].Reblog.Reply)
		require.NotNil(t, out[1].Reply)
	}
	require.Equal(t, counts[0], counts[1], "reply count must not multiply database round trips")
}
