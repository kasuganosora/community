package mention

import (
	"strconv"
	"testing"

	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/assert"
)

func TestMentionTimeline_GetKeyFormat(t *testing.T) {
	account := &models.Account{Id: 123}
	timeline := &MentionTimeline{Account: account}

	assert.Equal(t, "feed:mention:%s", timeline.GetKeyFormat())
}

func TestMentionTimeline_GetTimelineId(t *testing.T) {
	account := &models.Account{Id: 456}
	timeline := &MentionTimeline{Account: account}

	assert.Equal(t, "456", timeline.GetTimelineId())
}

func TestMentionTimeline_Struct(t *testing.T) {
	account := &models.Account{
		Id:       1,
		Username: "testuser",
	}

	timeline := &MentionTimeline{Account: account}

	assert.Equal(t, account, timeline.Account)
	assert.Equal(t, uint64(1), timeline.Account.Id)
	assert.Equal(t, "testuser", timeline.Account.Username)
}

func TestMentionTimeline_VariousAccountIds(t *testing.T) {
	tests := []uint64{1, 100, 999999, 0}

	for _, id := range tests {
		t.Run(strconv.FormatUint(id, 10), func(t *testing.T) {
			account := &models.Account{Id: id}
			timeline := &MentionTimeline{Account: account}

			assert.Equal(t, strconv.FormatUint(id, 10), timeline.GetTimelineId())
			assert.Equal(t, "feed:mention:%s", timeline.GetKeyFormat())
		})
	}
}

func TestMentionTimeline_NilAccount(t *testing.T) {
	timeline := &MentionTimeline{Account: nil}

	assert.Equal(t, "feed:mention:%s", timeline.GetKeyFormat())
}
