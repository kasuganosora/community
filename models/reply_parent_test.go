package models

import (
	"regexp"
	"strconv"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func replyParentDB(t *testing.T) *gorm.DB {
	t.Helper()
	conn, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	require.NoError(t, conn.AutoMigrate(&Account{}, &AccountExtra{}, &Status{}, &StatusContent{}, &StatusToAccount{}))
	return conn
}

func TestFindReplyParentLocalForms(t *testing.T) {
	conn := replyParentDB(t)
	now := time.Now()
	base := "https://social.example"
	actor := base + "/users/luna"
	author := &Account{Id: 20, Username: "luna", DisplayName: "Luna", LocalActorURI: actor, CreatedAt: &now, UpdatedAt: &now}
	require.NoError(t, conn.Omit("Extra").Create(author).Error)
	parent := &Status{Id: 200, AccountId: author.Id, Visibility: StatusVisibilityPublic, CreatedAt: &now, UpdatedAt: &now, Content: StatusContent{Content: "original post"}}
	require.NoError(t, conn.Create(parent).Error)
	id := strconv.FormatUint(parent.Id, 10)
	for _, uri := range []string{
		base + "/users/luna/statuses/" + id,
		base + "/@luna/status/" + id,
		base + "/@luna/" + id,
		actor + "/statuses/" + id,
	} {
		found, err := FindReplyParent(conn, base, uri)
		require.NoError(t, err, uri)
		require.Equal(t, parent.Id, found.Id, uri)
	}
	_, err := FindReplyParent(conn, base, "https://remote.example/notes/missing")
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
}

func TestLinkStoredReplyParentFromURIOrBody(t *testing.T) {
	conn := replyParentDB(t)
	now := time.Now()
	base := "https://social.example"
	author := &Account{Id: 20, Username: "luna", CreatedAt: &now, UpdatedAt: &now}
	replier := &Account{Id: 21, Username: "sky", Domain: strPtr("remote.example"), CreatedAt: &now, UpdatedAt: &now}
	require.NoError(t, conn.Omit("Extra").Create(author).Error)
	require.NoError(t, conn.Omit("Extra").Create(replier).Error)
	parent := &Status{Id: 200, AccountId: author.Id, Visibility: StatusVisibilityPublic, CreatedAt: &now, UpdatedAt: &now, Content: StatusContent{Content: "original post"}}
	require.NoError(t, conn.Create(parent).Error)
	other := &Status{Id: 199, AccountId: author.Id, Visibility: StatusVisibilityPublic, CreatedAt: &now, UpdatedAt: &now, Content: StatusContent{Content: "other post"}}
	require.NoError(t, conn.Create(other).Error)
	uri := base + "/@luna/status/" + strconv.FormatUint(parent.Id, 10)
	otherURI := base + "/@luna/status/" + strconv.FormatUint(other.Id, 10)
	stored := &Status{Id: 201, AccountId: replier.Id, Visibility: StatusVisibilityPublic, CreatedAt: &now, UpdatedAt: &now, Properties: map[string]any{StatusPropReplyToURI: uri}, Content: StatusContent{Content: "reply"}}
	require.NoError(t, conn.Create(stored).Error)
	body := &Status{Id: 202, AccountId: replier.Id, Visibility: StatusVisibilityPublic, CreatedAt: &now, UpdatedAt: &now, Content: StatusContent{Content: `<p><a href="` + uri + `">post</a> reply</p>`}}
	require.NoError(t, conn.Create(body).Error)
	ambiguous := &Status{Id: 203, AccountId: replier.Id, Visibility: StatusVisibilityPublic, CreatedAt: &now, UpdatedAt: &now, Content: StatusContent{Content: uri + " " + otherURI}}
	require.NoError(t, conn.Create(ambiguous).Error)

	require.NoError(t, LinkStoredReplyParents(conn, []*Status{stored, body, ambiguous}, base))
	var saved, fromBody, fromAmbiguous Status
	require.NoError(t, conn.First(&saved, stored.Id).Error)
	require.NotNil(t, saved.InReplyToId)
	require.Equal(t, parent.Id, *saved.InReplyToId)
	require.NotNil(t, saved.InReplyToAccountId)
	require.Equal(t, author.Id, *saved.InReplyToAccountId)
	require.Empty(t, ReplyToURI(&saved))
	require.NoError(t, conn.First(&fromBody, body.Id).Error)
	require.NotNil(t, fromBody.InReplyToId)
	require.Equal(t, parent.Id, *fromBody.InReplyToId)
	require.NoError(t, conn.First(&fromAmbiguous, ambiguous.Id).Error)
	require.Nil(t, fromAmbiguous.InReplyToId)

	again, err := RepairUnlinkedReplyParents(conn, base)
	require.NoError(t, err)
	require.Equal(t, 0, again)
}

func TestRepairUnlinkedReplyParentsLinksStoredURIAndBody(t *testing.T) {
	conn := replyParentDB(t)
	now := time.Now()
	base := "https://social.example"
	author := &Account{Id: 20, Username: "luna", CreatedAt: &now, UpdatedAt: &now}
	replier := &Account{Id: 21, Username: "sky", Domain: strPtr("remote.example"), CreatedAt: &now, UpdatedAt: &now}
	require.NoError(t, conn.Omit("Extra").Create(author).Error)
	require.NoError(t, conn.Omit("Extra").Create(replier).Error)
	parent := &Status{Id: 200, AccountId: author.Id, Visibility: StatusVisibilityPublic, CreatedAt: &now, UpdatedAt: &now, Content: StatusContent{Content: "original post"}}
	require.NoError(t, conn.Create(parent).Error)
	uri := base + "/@luna/status/" + strconv.FormatUint(parent.Id, 10)
	stored := &Status{Id: 201, AccountId: replier.Id, Visibility: StatusVisibilityPublic, CreatedAt: &now, UpdatedAt: &now, Properties: map[string]any{StatusPropReplyToURI: uri}, Content: StatusContent{Content: "reply"}}
	require.NoError(t, conn.Create(stored).Error)
	body := &Status{Id: 202, AccountId: replier.Id, Visibility: StatusVisibilityPublic, CreatedAt: &now, UpdatedAt: &now, Content: StatusContent{Content: `<p><a href="` + uri + `">post</a></p>`}}
	require.NoError(t, conn.Create(body).Error)
	unrelated := &Status{Id: 203, AccountId: replier.Id, Visibility: StatusVisibilityPublic, CreatedAt: &now, UpdatedAt: &now, Properties: map[string]any{"other": "value"}, Content: StatusContent{Content: "not a reply"}}
	require.NoError(t, conn.Create(unrelated).Error)

	linked, err := RepairUnlinkedReplyParents(conn, base)
	require.NoError(t, err)
	require.Equal(t, 2, linked)
	var saved, fromBody, skipped Status
	require.NoError(t, conn.First(&saved, stored.Id).Error)
	require.NotNil(t, saved.InReplyToId)
	require.Equal(t, parent.Id, *saved.InReplyToId)
	require.NoError(t, conn.First(&fromBody, body.Id).Error)
	require.NotNil(t, fromBody.InReplyToId)
	require.Equal(t, parent.Id, *fromBody.InReplyToId)
	require.NoError(t, conn.First(&skipped, unrelated.Id).Error)
	require.Nil(t, skipped.InReplyToId)
}

// PostgreSQL stores statuses.properties as jsonb. LIKE compiles to ~~, and
// jsonb has no ~~ operator, so the filter must cast the column to text first.
func TestUnlinkedReplyParentQueryCastsPropertiesBeforeLike(t *testing.T) {
	rawLike := regexp.MustCompile(`(?i)properties\s+(~~|!~|ILIKE|LIKE)\b`)
	cases := []struct {
		name string
		open func() gorm.Dialector
		cast string
	}{
		{
			name: "postgres",
			open: func() gorm.Dialector {
				return postgres.New(postgres.Config{DSN: "host=127.0.0.1 user=u password=p dbname=db port=1 sslmode=disable"})
			},
			cast: "CAST(statuses.properties AS TEXT)",
		},
		{
			name: "mysql",
			open: func() gorm.Dialector {
				return mysql.New(mysql.Config{DSN: "u:p@tcp(127.0.0.1:1)/db", SkipInitializeWithVersion: true})
			},
			cast: "CAST(statuses.properties AS CHAR)",
		},
		{
			name: "sqlite",
			open: func() gorm.Dialector { return sqlite.Open(":memory:") },
			cast: "CAST(statuses.properties AS TEXT)",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			conn, err := gorm.Open(tc.open(), &gorm.Config{DryRun: true, DisableAutomaticPing: true, SkipDefaultTransaction: true, Logger: logger.Default.LogMode(logger.Silent)})
			require.NoError(t, err)
			for _, base := range []string{"https://social.example", ""} {
				var ids []uint64
				query := unlinkedReplyParentQuery(conn.Session(&gorm.Session{DryRun: true}), base).Pluck("statuses.id", &ids)
				sql := query.Statement.SQL.String()
				require.NoError(t, query.Error, sql)
				require.Contains(t, sql, tc.cast, sql)
				require.NotRegexp(t, rawLike, sql)
				require.Contains(t, query.Statement.Vars, "%"+StatusPropReplyToURI+"%")
			}
		})
	}
}

func strPtr(value string) *string { return &value }
