package direct

import (
	"strconv"
	"testing"

	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/assert"
)

func TestDirectTimeline_GetKeyFormat(t *testing.T) {
	account := &models.Account{Id: 123}
	timeline := &DirectTimeline{Account: account}

	assert.Equal(t, "direct:%s", timeline.GetKeyFormat())
}

func TestDirectTimeline_GetTimelineId(t *testing.T) {
	account := &models.Account{Id: 456}
	timeline := &DirectTimeline{Account: account}

	assert.Equal(t, "456", timeline.GetTimelineId())
}

func TestDirectTimeline_Rebuild(t *testing.T) {
	account := &models.Account{Id: 789}
	timeline := &DirectTimeline{Account: account}

	// Rebuild should always return nil for direct timeline
	err := timeline.Rebuild()
	assert.NoError(t, err)
}

func TestDirectTimeline_Struct(t *testing.T) {
	account := &models.Account{
		Id:       1,
		Username: "testuser",
	}

	timeline := &DirectTimeline{Account: account}

	assert.Equal(t, account, timeline.Account)
	assert.Equal(t, uint64(1), timeline.Account.Id)
	assert.Equal(t, "testuser", timeline.Account.Username)
}

func TestDirectTimeline_VariousAccountIds(t *testing.T) {
	tests := []uint64{1, 100, 999999, 0}

	for _, id := range tests {
		t.Run(strconv.FormatUint(id, 10), func(t *testing.T) {
			account := &models.Account{Id: id}
			timeline := &DirectTimeline{Account: account}

			assert.Equal(t, strconv.FormatUint(id, 10), timeline.GetTimelineId())
			assert.Equal(t, "direct:%s", timeline.GetKeyFormat())
		})
	}
}

func TestDirectTimeline_NilAccount(t *testing.T) {
	timeline := &DirectTimeline{Account: nil}

	assert.Equal(t, "direct:%s", timeline.GetKeyFormat())
	// GetTimelineId will panic with nil Account, so we don't test that
}
