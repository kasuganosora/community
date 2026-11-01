package serializer

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
)

func TestStatusWireAlwaysCarriesLocalOnly(t *testing.T) {
	created := time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)
	status := &models.Status{
		Id: 100, Content: models.StatusContent{Content: "Hello"},
		Visibility: models.StatusVisibilityPublic, CreatedAt: &created,
		Account: &models.Account{Id: 7, Username: "poster"},
	}

	released, err := ToStatusBase(status)
	require.NoError(t, err)
	raw, err := json.Marshal(released)
	require.NoError(t, err)
	require.Contains(t, string(raw), `"local_only":false`)

	status.LocalOnly = true
	held, err := ToStatusBase(status)
	require.NoError(t, err)
	raw, err = json.Marshal(held)
	require.NoError(t, err)
	require.Contains(t, string(raw), `"local_only":true`)
}
