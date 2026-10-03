package datatransfer

import (
	"archive/zip"
	"bytes"
	"context"
	"github.com/stretchr/testify/require"
	"testing"
)

func parseFiles(t *testing.T, files map[string]string, opts ParseOptions) *Bundle {
	t.Helper()
	var data bytes.Buffer
	w := zip.NewWriter(&data)
	for name, value := range files {
		e, err := w.Create(name)
		require.NoError(t, err)
		_, err = e.Write([]byte(value))
		require.NoError(t, err)
	}
	require.NoError(t, w.Close())
	a, err := OpenArchive(context.Background(), "backup.zip", bytes.NewReader(data.Bytes()), Limits{})
	require.NoError(t, err)
	defer a.Close()
	b, err := ParseArchive(context.Background(), a, opts)
	require.NoError(t, err)
	return b
}
func TestMisskeyBackupPreservesPrivateUnknownRecipientsAndNumericIDs(t *testing.T) {
	b := parseFiles(t, map[string]string{"notes-2026.json": `[{"id":9007199254740993,"text":"private","visibility":"specified","visibleUserIds":["opaque-user"],"fileIds":["missing"],"createdAt":"2026-10-03T00:00:00Z"},{"id":"public-note","text":"hello","visibility":"public","replyId":"outside-export","poll":{"choices":["a","b"],"votes":[1,2],"multiple":false}}]`}, ParseOptions{Platform: "misskey"})
	require.Equal(t, "9007199254740993", b.Posts[0].ID)
	require.True(t, b.Posts[0].ArchiveOnly)
	require.Empty(t, b.Posts[0].RecipientIDs)
	require.Empty(t, b.Posts[0].MediaIDs)
	require.Empty(t, b.Posts[1].ReplyID)
	require.False(t, b.Posts[1].ArchiveOnly)
	require.Len(t, b.Records, 2)
	require.Len(t, b.Polls, 1)
	require.Empty(t, b.Accounts[0].ActorURI)
	require.Empty(t, b.Credentials)
	require.Empty(t, b.Keys)
}
func TestMastodonBackupMediaVisibilityAndProfileKeyExclusion(t *testing.T) {
	b := parseFiles(t, map[string]string{"actor.json": `{"id":"https://mastodon.test/users/alice","preferredUsername":"alice","followers":"https://mastodon.test/users/alice/followers","publicKey":{"privateKeyPem":"NEVER_COPY"}}`, "outbox.json": `{"orderedItems":[{"type":"Create","object":{"id":"https://mastodon.test/users/alice/statuses/1","type":"Note","content":"<p>hello</p>","to":["https://www.w3.org/ns/activitystreams#Public"],"attachment":[{"url":"media/image.png","mediaType":"image/png","name":"alt"}]}},{"type":"Create","object":{"id":"https://mastodon.test/users/alice/statuses/2","type":"Note","content":"private","to":["https://elsewhere.test/users/bob"]}}]}`, "media/image.png": "imagebytes"}, ParseOptions{})
	require.Equal(t, "public", b.Posts[0].Visibility)
	require.Equal(t, "direct", b.Posts[1].Visibility)
	require.Equal(t, "media/image.png", b.Media[0].Path)
	require.Len(t, b.Media[0].SHA256, 64)
	require.NotContains(t, string(raw(b)), "NEVER_COPY")
	require.Empty(t, b.Keys)
}
func TestMastodonFollowingCSVOptionsAndLists(t *testing.T) {
	b := parseFiles(t, map[string]string{"following.csv": "Account address,Show boosts,Notify on new posts,Languages\nbob@example.test,false,true,en\n", "lists.csv": "Friends,bob@example.test\n"}, ParseOptions{Platform: "mastodon"})
	require.Len(t, b.Relations, 1)
	require.Equal(t, "follow", b.Relations[0].Kind)
	require.False(t, b.Relations[0].ShowReblogs)
	require.Len(t, b.Lists, 1)
	require.Equal(t, "Friends", b.Lists[0].Title)
}
func TestCanonicalMediaChecksumRejectsTampering(t *testing.T) {
	var data bytes.Buffer
	z := zip.NewWriter(&data)
	for name, value := range map[string]string{"bundle.json": `{"schema":"community-transfer/v1","scope":"personal","media":[{"id":"1","path":"media/file","sha256":"bad"}]}`, "media/file": "content"} {
		w, e := z.Create(name)
		require.NoError(t, e)
		_, e = w.Write([]byte(value))
		require.NoError(t, e)
	}
	require.NoError(t, z.Close())
	a, e := OpenArchive(context.Background(), "backup.zip", bytes.NewReader(data.Bytes()), Limits{})
	require.NoError(t, e)
	defer a.Close()
	_, e = ParseArchive(context.Background(), a, ParseOptions{})
	require.ErrorContains(t, e, "checksum")
}

func TestAPIDefaultAutoArchiveRecognizesOfficialFormats(t *testing.T) {
	mastodon := parseFiles(t, map[string]string{"actor.json": `{"id":"https://mastodon.test/users/alice","preferredUsername":"alice"}`, "outbox.json": `{"orderedItems":[]}`}, ParseOptions{Platform: "auto", Kind: "archive"})
	require.Equal(t, "mastodon", mastodon.Source.Platform)
	require.Equal(t, "alice", mastodon.Accounts[0].Username)
	misskey := parseFiles(t, map[string]string{"notes-export.json": `[{"id":"n","text":"default options","visibility":"public"}]`}, ParseOptions{Platform: "auto", Kind: "archive"})
	require.Equal(t, "misskey", misskey.Source.Platform)
	require.Len(t, misskey.Posts, 1)
	require.Equal(t, "default options", misskey.Posts[0].Text)
}
