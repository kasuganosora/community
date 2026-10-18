package homeserver

import (
	"context"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/storage"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestAccountErasureRedactsOwnMessagesAndPurgesOwnMedia(t *testing.T) {
	conn, store, now := mediaGCFixture(t)
	a := &models.Account{Id: 90, Username: "alice"}
	ids, err := accountMatrixIDs(conn, a)
	require.NoError(t, err)
	// Legacy bridge copies may have no sender_id but still contain text.
	for _, table := range []string{"group_messages", "matrix_messages"} {
		require.NoError(t, conn.Exec("CREATE TABLE "+table+" (id INTEGER PRIMARY KEY, matrix_sender_id TEXT, content TEXT)").Error)
		require.NoError(t, conn.Exec("INSERT INTO "+table+" (id, matrix_sender_id, content) VALUES (1, ?, ?), (2, ?, ?)", ids[0], "own private text", "@bob:remote.test", "other text").Error)
	}
	own := seedGCMedia(t, conn, store, now, "own-erasure", "local")
	own.UploaderID = ids[0]
	require.NoError(t, conn.Model(&own).Update("uploader_id", ids[0]).Error)
	event := gcEvent(t, conn, "$own-erasure", own)
	other := seedGCMedia(t, conn, store, now, "other-erasure", "local")
	require.NoError(t, conn.Model(&other).Update("uploader_id", "@bob:remote.test").Error)
	done := false
	for i := 0; i < 5 && !done; i++ {
		done, err = EraseAccountData(context.Background(), conn, a)
		require.NoError(t, err)
	}
	require.True(t, done)
	for _, table := range []string{"group_messages", "matrix_messages"} {
		var remaining []string
		require.NoError(t, conn.Table(table).Pluck("content", &remaining).Error)
		require.Equal(t, []string{"other text"}, remaining)
	}
	require.NoError(t, conn.First(&event, "event_id = ?", event.EventID).Error)
	require.Empty(t, event.Content)
	_, err = store.Stat(context.Background(), own.FilePath)
	require.ErrorIs(t, err, storage.ErrNotFound)
	_, err = store.Stat(context.Background(), other.FilePath)
	require.NoError(t, err)
}
