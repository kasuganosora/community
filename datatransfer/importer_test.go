package datatransfer

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"github.com/gin-gonic/gin"
	mediaController "github.com/kasuganosora/community/controllers/api/media"
	"github.com/kasuganosora/community/db/database"
	"golang.org/x/crypto/bcrypt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func importerDB(t *testing.T) *gorm.DB {
	t.Helper()
	conn, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Discard})
	require.NoError(t, err)
	raw, err := conn.DB()
	require.NoError(t, err)
	raw.SetMaxOpenConns(1)
	t.Cleanup(func() { raw.Close() })
	require.NoError(t, conn.AutoMigrate(&models.User{}, &models.UserSecretData{}, &models.UserSetting{}, &models.Account{}, &models.AccountExtra{}, &models.AccountKey{}, &models.AccountFederationKey{}, &models.Status{}, &models.StatusContent{}, &models.StatusToAccount{}, &models.StatusEmoji{}, &models.StatusTag{}, &models.Tag{}, &models.MediaAttachment{}, &models.Emoji{}, &models.Conversation{}, &models.AccountConversation{}, &models.Follow{}, &models.Block{}, &models.MuteAccount{}, &models.List{}, &models.ListAccount{}, &models.ListFollowAccount{}, &models.Poll{}, &models.PollOption{}, &models.PollOptionVote{}, &models.StatusReaction{}, &models.FavoriteStatus{}, &models.BookmarkStatus{}, &models.PinStatus{}, &models.FederationOutbox{}, &ImportReceipt{}, &StoredPreservedRecord{}))
	// SQLite requires INTEGER PRIMARY KEY for auto-generated row IDs; the
	// production MySQL models explicitly use bigint(20) for these two tables.
	for _, table := range []string{"accounts", "follows"} {
		var ddl string
		require.NoError(t, conn.Raw("SELECT sql FROM sqlite_master WHERE type = 'table' AND name = ?", table).Scan(&ddl).Error)
		require.NoError(t, conn.Exec("DROP TABLE "+table).Error)
		ddl = strings.ReplaceAll(ddl, "bigint(20)", "integer")
		require.NoError(t, conn.Exec(ddl).Error)
	}
	return conn
}
func personalFixture(t *testing.T) (*gorm.DB, *Bundle, Options) {
	conn := importerDB(t)
	owner := models.User{UUID: "fixture-owner", UserName: "owner", EmailHash: "fixture-hash"}
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create(&owner).Error)
	account := models.Account{UserId: owner.Id, Username: "owner", Acct: "owner@__LOCAL__"}
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create(&account).Error)
	require.NoError(t, conn.Create(&models.AccountExtra{AccountId: account.Id}).Error)
	b := &Bundle{Schema: Schema, Scope: "personal", Source: Source{Platform: "community", BaseURL: "https://old.example"}, OwnerID: "a", Accounts: []Account{{ID: "a", Username: "alice", Local: true, ActorURI: "https://old.example/users/alice"}, {ID: "b", Username: "bob", Domain: "remote.example", ActorURI: "https://remote.example/users/bob"}}}
	o := Options{VerifiedReferences: map[string]bool{"b": true}, Scope: "personal", OwnerAccountID: account.Id, OwnerUserID: owner.Id, TargetBaseURL: "https://new.example", StorageDir: t.TempDir()}
	return conn, b, o
}
func TestImportPersonalDirectRetryAndNoAudienceExpansion(t *testing.T) {
	conn, b, o := personalFixture(t)
	now := time.Now()
	b.Posts = []Post{{ID: "dm", AuthorID: "a", Text: "hello @outsider@evil.example", Visibility: "direct", RecipientIDs: []string{"b"}, CreatedAt: now}, {ID: "received", AuthorID: "b", Text: "received secret", Visibility: "direct", RecipientIDs: []string{"a"}, CreatedAt: now}}
	b.Relations = []Relation{{Kind: "follow", AccountID: "a", TargetID: "b", ShowReblogs: true}}
	report, err := Import(context.Background(), conn, b, o, nil)

	require.NoError(t, err)
	require.Equal(t, 1, report.Created["post"])
	require.Equal(t, 1, report.Archived["post"])
	var status models.Status
	require.NoError(t, conn.First(&status).Error)
	require.Equal(t, uint8(models.StatusVisibilityDirect), status.Visibility)
	require.Equal(t, o.OwnerAccountID, status.AccountId)
	var grants []models.StatusToAccount
	require.NoError(t, conn.Find(&grants).Error)
	require.Len(t, grants, 1)
	var peer models.Account
	require.NoError(t, conn.First(&peer, grants[0].AccountId).Error)
	require.Equal(t, "bob", peer.Username)
	var extra models.AccountExtra
	require.NoError(t, conn.First(&extra, "account_id = ?", o.OwnerAccountID).Error)
	require.Equal(t, 1, extra.StatusesCount)
	require.Equal(t, 0, extra.FollowingCount)
	report, err = Import(context.Background(), conn, b, o, nil)
	require.NoError(t, err)
	require.Equal(t, 1, report.Skipped["post"])
	var count int64
	require.NoError(t, conn.Model(&models.Status{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
	require.NoError(t, conn.First(&extra, "account_id = ?", o.OwnerAccountID).Error)
	require.Equal(t, 1, extra.StatusesCount)
	b.Posts[0].Text = "changed immutable source"
	_, err = Import(context.Background(), conn, b, o, nil)
	require.Error(t, err)
	var content models.StatusContent
	require.NoError(t, conn.First(&content).Error)
	require.Equal(t, "hello @outsider@evil.example", content.Content)
}
func TestImportPreflightAndLeaseFailClosed(t *testing.T) {
	conn, b, o := personalFixture(t)
	b.Posts = []Post{{ID: "dm", AuthorID: "a", Visibility: "direct", RecipientIDs: []string{"missing"}}}
	report, err := Preview(context.Background(), conn, b, o)
	require.NoError(t, err)
	require.False(t, report.CanExecute)
	b.Posts = nil
	checks := 0
	o.LeaseCheck = func(*gorm.DB) error {
		checks++
		if checks == 2 {
			return errors.New("lost lease")
		}
		return nil
	}
	_, err = Import(context.Background(), conn, b, o, nil)
	require.Error(t, err)
	require.Equal(t, 2, checks)
	var count int64
	require.NoError(t, conn.Model(&ImportReceipt{}).Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, conn.Model(&models.Account{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
}
func TestImportSiteSystemActorAndSigningKey(t *testing.T) {
	conn := importerDB(t)
	private, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	public, err := x509.MarshalPKIXPublicKey(&private.PublicKey)
	require.NoError(t, err)
	key := FederationKey{AccountID: "system", KeyID: "https://site.example/actor#main-key", Primary: true, PublicKey: string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: public})), PrivateKey: string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(private)}))}
	b := &Bundle{Schema: Schema, Scope: "site", Source: Source{Platform: "mastodon", BaseURL: "https://site.example"}, Accounts: []Account{{ID: "system", Username: "instance", Local: true, System: true, ActorURI: "https://site.example/actor"}}, Keys: []FederationKey{key}}
	o := Options{Scope: "site", TargetBaseURL: "https://site.example", StorageDir: t.TempDir()}
	report, err := Import(context.Background(), conn, b, o, nil)

	require.NoError(t, err, fmt.Sprintf("issues: %+v", report.Issues))
	require.Equal(t, 1, report.Created["key"])
	var account models.Account
	require.NoError(t, conn.First(&account).Error)
	require.Zero(t, account.UserId)
	require.Equal(t, "https://site.example/actor", account.LocalActorURI)
	var target models.AccountKey
	require.NoError(t, conn.First(&target).Error)
	require.Equal(t, key.KeyID, target.KeyID)
	require.NoError(t, target.ValidateKeyPair())
}

func TestImportUnknownPersonalOwnerAndUnverifiedReferences(t *testing.T) {
	conn, b, o := personalFixture(t)
	b.Source.BaseURL = ""
	b.Accounts[0] = Account{ID: "a"}
	o.VerifiedReferences = nil
	b.Relations = []Relation{{Kind: "follow", AccountID: "a", TargetID: "b"}}
	b.Posts = []Post{{ID: "unsafe", AuthorID: "a", Visibility: "direct", RecipientIDs: []string{"b"}}}
	report, err := Import(context.Background(), conn, b, o, nil)
	require.NoError(t, err)
	require.Equal(t, 1, report.Archived["account"])
	require.Equal(t, 1, report.Archived["relation"])
	require.Equal(t, 1, report.Archived["post"])
	var count int64
	require.NoError(t, conn.Model(&models.Status{}).Count(&count).Error)
	require.Zero(t, count)
	require.Empty(t, report.Created["archive_post"])
}
func TestImportUntrustedSourceIssueNeverExposesSecrets(t *testing.T) {
	conn, b, o := personalFixture(t)
	b.Source.BaseURL = "https://login:secret@example.test/?access_token=secret"
	b.Source.Version = "password=secret"
	b.Source.SnapshotID = "secret"
	b.Issues = []Issue{{Severity: "error", Code: "secret", Message: "password=secret", SourceID: "secret"}}
	report, err := Preview(context.Background(), conn, b, o)
	require.NoError(t, err)
	require.False(t, report.CanExecute)
	body := fmt.Sprintf("%+v", report)
	require.NotContains(t, body, "secret")
}

func TestImportMediaServedBytesSharedAttachmentsAndRetryCleanup(t *testing.T) {
	t.Chdir(t.TempDir())
	conn, b, o := personalFixture(t)
	o.StorageDir = "uploads/media"
	prior := database.GetDB()
	database.SetTestDB(conn)
	t.Cleanup(func() { database.SetTestDB(prior) })
	bytes := []byte("private offline fixture bytes")
	archive := filepath.Join(t.TempDir(), "asset.bin")
	require.NoError(t, os.WriteFile(archive, bytes, 0600))
	o.Files = map[string]ArchiveFile{"asset.bin": {Path: archive, Size: int64(len(bytes)), SHA256: digestBytes(bytes)}}
	b.Media = []Media{{ID: "file", OwnerID: "a", Path: "asset.bin", Name: "guessable-original.bin", ContentType: "application/octet-stream", Size: int64(len(bytes))}}
	b.Posts = []Post{{ID: "one", AuthorID: "a", Visibility: "direct", RecipientIDs: []string{"b"}, MediaIDs: []string{"file"}, ConversationID: "thread", CreatedAt: time.Now()}, {ID: "two", AuthorID: "a", Visibility: "direct", RecipientIDs: []string{"b"}, MediaIDs: []string{"file"}, ConversationID: "thread", CreatedAt: time.Now().Add(time.Second)}}
	report, err := Import(context.Background(), conn, b, o, nil)
	require.NoError(t, err)
	require.Equal(t, 2, report.Created["post"])
	var attachments []models.MediaAttachment
	require.NoError(t, conn.Order("id").Find(&attachments).Error)
	require.Len(t, attachments, 2)
	require.Equal(t, attachments[0].FilePath, attachments[1].FilePath)
	require.NotEqual(t, *attachments[0].StatusId, *attachments[1].StatusId)
	require.NotEqual(t, "guessable-original.bin", attachments[0].FileName)
	router := gin.New()
	router.GET("/media/:id/*file", mediaController.Serve)
	for _, attachment := range attachments {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest("GET", fmt.Sprintf("/media/%d/%s", attachment.Id, attachment.FileName), nil))
		require.Equal(t, 200, response.Code)
		require.Equal(t, bytes, response.Body.Bytes())
	}
	var conversations []models.Conversation
	require.NoError(t, conn.Find(&conversations).Error)
	require.Len(t, conversations, 1)
	require.Len(t, conversations[0].StatusesIds, 2)
	_, err = Import(context.Background(), conn, b, o, nil)
	require.NoError(t, err)
	files, err := filepath.Glob("uploads/media/imports/*")
	require.NoError(t, err)
	require.Len(t, files, 1, "idempotent retry must remove its unused staged file")
}

func TestImportTypedCollectionsRemainOperationalAndIdempotent(t *testing.T) {
	conn, b, o := personalFixture(t)
	now := time.Now()
	expires := now.Add(time.Hour)
	b.Posts = []Post{{ID: "pollpost", AuthorID: "a", Visibility: "public", Text: "choose", CreatedAt: now}}
	b.Polls = []Poll{{ID: "poll", PostID: "pollpost", ExpiresAt: &expires, Options: []PollOption{{Title: "yes", Votes: 1}, {Title: "no"}}}}
	b.PollVotes = []PollVote{{PollID: "poll", AccountID: "a", Option: 0}}
	b.Lists = []List{{ID: "friends", OwnerID: "a", Title: "Friends", MemberIDs: []string{"b"}}}
	b.Bookmarks = []Bookmark{{AccountID: "a", PostID: "pollpost"}}
	b.Reactions = []Reaction{{ID: "favourite", AccountID: "a", PostID: "pollpost"}, {ID: "emoji", AccountID: "a", PostID: "pollpost", Emoji: "👍"}}
	b.Relations = []Relation{{Kind: "mute", AccountID: "a", TargetID: "b", ExpiresAt: &expires}}
	_, err := Import(context.Background(), conn, b, o, nil)
	require.NoError(t, err)
	_, err = Import(context.Background(), conn, b, o, nil)
	require.NoError(t, err)
	for _, model := range []any{&models.Poll{}, &models.PollOptionVote{}, &models.List{}, &models.ListAccount{}, &models.BookmarkStatus{}, &models.FavoriteStatus{}, &models.StatusReaction{}, &models.MuteAccount{}} {
		var count int64
		require.NoError(t, conn.Model(model).Count(&count).Error)
		require.EqualValues(t, 1, count, fmt.Sprintf("%T", model))
	}
	var option models.PollOption
	require.NoError(t, conn.Where("`index` = ?", 0).First(&option).Error)
	require.EqualValues(t, 1, option.Votes, "imported vote rows must not increment source aggregate votes")
	var status models.Status
	require.NoError(t, conn.First(&status).Error)
	require.EqualValues(t, 1, status.FavouritesCount)
	var mute models.MuteAccount
	require.NoError(t, conn.First(&mute).Error)
	require.NotNil(t, mute.ExpiredAt)
	require.WithinDuration(t, expires, *mute.ExpiredAt, time.Second)
}

func TestImportSiteNoEmailCredentialsLocalOnlyAndCrossOperatorRetry(t *testing.T) {
	conn := importerDB(t)
	private, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	public, err := x509.MarshalPKIXPublicKey(&private.PublicKey)
	require.NoError(t, err)
	publicPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: public}))
	privatePEM := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(private)}))
	password, err := bcrypt.GenerateFromPassword([]byte("fixture passphrase"), bcrypt.MinCost)
	require.NoError(t, err)
	b := &Bundle{Schema: Schema, Scope: "site", Source: Source{Platform: "misskey", BaseURL: "https://site.example"}}
	for _, id := range []string{"alice", "bob"} {
		b.Accounts = append(b.Accounts, Account{ID: id, Username: id, Local: true, ActorURI: "https://site.example/users/" + id, Admin: id == "alice", NoteHTML: true, Note: "<p>Formatted <strong>bio</strong></p>"})
		b.Credentials = append(b.Credentials, Credential{AccountID: id, PasswordHash: string(password), PasswordScheme: "bcrypt"})
		b.Keys = append(b.Keys, FederationKey{AccountID: id, KeyID: "https://site.example/users/" + id + "#main-key", Primary: true, PublicKey: publicPEM, PrivateKey: privatePEM})
	}
	b.Posts = []Post{{ID: "chat", AuthorID: "alice", RecipientIDs: []string{"bob"}, Visibility: "direct", LocalOnly: true, HTML: "<p>Private <strong>formatted</strong> content</p>", CreatedAt: time.Now()}}
	o := Options{Scope: "site", TargetBaseURL: "https://site.example", StorageDir: t.TempDir()}
	_, err = Import(context.Background(), conn, b, o, nil)
	require.NoError(t, err)
	var users []models.User
	require.NoError(t, conn.Order("id").Find(&users).Error)
	require.Len(t, users, 2)
	require.NotEqual(t, users[0].EmailHash, users[1].EmailHash)
	require.True(t, users[0].IsAdmin)
	for _, user := range users {
		require.False(t, user.IsMailVerified)
		require.True(t, strings.HasPrefix(user.EmailHash, "no-email:"))
		var secret models.UserSecretData
		require.NoError(t, conn.First(&secret, "user_id = ?", user.Id).Error)
		require.NoError(t, secret.CheckPassword("fixture passphrase"))
	}
	var post models.Status
	require.NoError(t, conn.First(&post).Error)
	require.True(t, post.LocalOnly)
	require.Empty(t, post.LocalObjectURI)
	require.Contains(t, post.Properties["import_original_html"], "<strong>")
	var count int64
	require.NoError(t, conn.Model(&models.FederationOutbox{}).Count(&count).Error)
	require.Zero(t, count, "historical site import must not broadcast")
	o.OwnerUserID = 999
	o.OwnerAccountID = 999
	o.JobID = "another-operator-job"
	report, err := Import(context.Background(), conn, b, o, nil)
	require.NoError(t, err)
	require.Equal(t, 2, report.Skipped["account"])
	require.Empty(t, report.Created)
}

func TestImportConcurrentRetriesUseOneReceiptAndOnePost(t *testing.T) {
	conn, b, o := personalFixture(t)
	b.Posts = []Post{{ID: "same", AuthorID: "a", Visibility: "public", Text: "one historical post"}}
	var wg sync.WaitGroup
	failures := make(chan error, 8)
	for n := 0; n < 8; n++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := Import(context.Background(), conn, b, o, nil); failures <- err }()
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		require.NoError(t, err)
	}
	var count int64
	require.NoError(t, conn.Model(&models.Status{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
	require.NoError(t, conn.Model(&ImportReceipt{}).Where("kind = ?", "post").Count(&count).Error)
	require.EqualValues(t, 1, count)
	var extra models.AccountExtra
	require.NoError(t, conn.First(&extra, "account_id = ?", o.OwnerAccountID).Error)
	require.Equal(t, 1, extra.StatusesCount)
}

func TestPersonalNativeEmojiAndArchivedPostRoundTrip(t *testing.T) {
	conn, b, o := personalFixture(t)
	b.Source.BaseURL = o.TargetBaseURL
	emoji := models.Emoji{Shortcode: "restore", Url: o.TargetBaseURL + "/media/42/image.png"}
	require.NoError(t, conn.Create(&emoji).Error)
	b.Emojis = []Emoji{{ID: "emoji", Shortcode: emoji.Shortcode, URL: emoji.Url}}
	b.Accounts[0].EmojiIDs = []string{"emoji"}
	b.Posts = []Post{{ID: "own", AuthorID: "a", Text: ":restore:", Visibility: "public", EmojiIDs: []string{"emoji"}}}
	archived := Post{ID: "received", AuthorID: "b", Text: "private archived body", Visibility: "direct", RecipientIDs: []string{"a"}}
	raw, err := json.Marshal(archived)
	require.NoError(t, err)
	b.Records = []PreservedRecord{{Kind: "post", ID: archived.ID, Data: raw}}
	report, err := Import(context.Background(), conn, b, o, nil)
	require.NoError(t, err)
	require.Equal(t, 1, report.Created["post"])
	var count int64
	require.NoError(t, conn.Table("account_emojis").Where("account_id = ? AND emoji_id = ?", o.OwnerAccountID, emoji.Id).Count(&count).Error)
	require.EqualValues(t, 1, count)
	var record StoredPreservedRecord
	require.NoError(t, conn.Where("kind = ?", "post").First(&record).Error)
	var decoded Post
	require.NoError(t, json.Unmarshal(record.Data, &decoded))
	require.Equal(t, archived, decoded)
}

func TestImportLifecycleAndAliasPreflight(t *testing.T) {
	conn, b, o := personalFixture(t)
	o.Scope = "site"
	b.Scope = "site"
	o.TargetBaseURL = b.Source.BaseURL
	// Other missing site credential errors do not obscure the policy checks.
	b.Accounts[0].Settings = map[string]any{"alsoKnownAs": []any{}, "isDeleted": false}
	r, err := Preview(context.Background(), conn, b, o)
	require.NoError(t, err)
	for _, issue := range r.Issues {
		require.NotEqual(t, "unsupported_account_lifecycle", issue.Code)
	}
	b.Accounts[0].Settings["isDeleted"] = true
	b.Source.AccountDomain = "different.example"
	b.Media = []Media{{ID: "badalias", OwnerID: "a", OriginalURL: "https://external.example/files/private"}}
	r, err = Preview(context.Background(), conn, b, o)
	require.NoError(t, err)
	found := map[string]bool{}
	for _, issue := range r.Issues {
		found[issue.Code] = true
	}
	require.True(t, found["unsupported_account_lifecycle"])
	require.True(t, found["unsupported_account_domain"])
	require.True(t, found["media_alias"])
}
