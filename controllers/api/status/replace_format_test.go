package status

import (
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestReplacementPreservesRichFormatOnlyWhenContentUnchanged(t *testing.T) {
	for _, edit := range []bool{false, true} {
		t.Run(map[bool]string{false: "unchanged", true: "edited"}[edit], func(t *testing.T) {
			conn, fix, owner := replacementDB(t)
			original := fix.CreateStatus(owner, func(s *models.Status) {
				s.Content.Content = "<p>Rich <b>text</b></p>"
				s.Content.ContentFormat = "html"
			})
			request := map[string]any{"spoiler_text": "warning"}
			if edit {
				request["status"] = "Rich text edited"
			}
			id := decodedReplacementID(t, callReplacement(t, owner, original.Id, request))
			var next models.Status
			require.NoError(t, models.StatusLoadAllQuery(conn).First(&next, id).Error)
			if edit {
				require.Equal(t, "plain", next.Content.ContentFormat)
				require.Equal(t, "Rich text edited", next.Content.Content)
			} else {
				require.Equal(t, "html", next.Content.ContentFormat)
				require.Equal(t, original.Content.Content, next.Content.Content)
			}
		})
	}
}
