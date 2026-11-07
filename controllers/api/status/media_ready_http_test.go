package status

import (
	"strconv"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
)

func TestCreateAndReplaceRejectUnreadyMedia(t *testing.T) {
	ts := newTestSetup(t)
	for _, state := range []string{models.ProcessQueued, models.ProcessProcessing, models.ProcessSaving, models.ProcessFailed, models.ProcessCancelled} {
		t.Run(state, func(t *testing.T) {
			m := createMedia(t, ts, ts.acct.Id)
			require.NoError(t, ts.db.Model(m).Update("process_state", state).Error)
			c, w := newGinContextPost(t, nil, CreateStatusRequest{
				Status: "nope", Visibility: "public", MediaIds: []string{strconv.FormatUint(m.Id, 10)},
			})
			setAccount(c, ts.acct)
			safeHandler(c, Create)
			require.Equal(t, 422, w.Code, w.Body.String())
			var linked models.MediaAttachment
			require.NoError(t, ts.db.First(&linked, m.Id).Error)
			require.Nil(t, linked.StatusId)

			c, w = newGinContextPost(t, gin.Params{{Key: "id", Value: strconv.FormatUint(ts.status.Id, 10)}}, map[string]any{
				"status": "edit", "media_ids": []string{strconv.FormatUint(m.Id, 10)},
			})
			setAccount(c, ts.acct)
			safeHandler(c, Replace)
			require.Equal(t, 422, w.Code, w.Body.String())
			var still models.Status
			require.NoError(t, ts.db.First(&still, ts.status.Id).Error)
			require.Equal(t, ts.status.Id, still.Id)
		})
	}
}
