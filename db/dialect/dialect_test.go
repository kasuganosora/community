package dialect

import (
	"sync"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func dryRun(t *testing.T, d gorm.Dialector) *gorm.DB {
	t.Helper()
	conn, err := gorm.Open(d, &gorm.Config{DryRun: true, DisableAutomaticPing: true, SkipDefaultTransaction: true})
	require.NoError(t, err)

	return conn
}

func TestHelpersPerDialect(t *testing.T) {
	pg := dryRun(t, OpenPostgres("host=127.0.0.1 user=x dbname=x sslmode=disable"))
	lite := dryRun(t, sqlite.Open(":memory:"))

	assert.True(t, IsPostgres(pg))
	assert.True(t, IsSQLite(lite))
	assert.True(t, IsMySQL(nil))

	assert.Equal(t, `"key"`, Quote(pg, "key"))
	assert.Equal(t, "`key`", Quote(nil, "key"))

	assert.Equal(t, "ILIKE", ILike(pg))
	assert.Equal(t, "LIKE", ILike(lite))
	assert.Equal(t, `ESCAPE '\'`, LikeEscape(pg))
	assert.Equal(t, `ESCAPE '\'`, LikeEscape(lite))
	assert.Equal(t, `ESCAPE '\\'`, LikeEscape(nil))

	assert.Equal(t, "payload->>'type'", JSONText(pg, "payload", "type"))
	assert.Equal(t, "json_extract(payload, '$.type')", JSONText(lite, "payload", "type"))
}

func TestPostgresType(t *testing.T) {
	for in, want := range map[string]string{
		"tinyint(1)":          "smallint",
		"tinyint unsigned":    "smallint",
		"smallint unsigned":   "integer",
		"int(11)":             "integer",
		"int unsigned":        "bigint",
		"bigint(20) unsigned": "bigint",
		"datetime":            "timestamptz",
		"datetime(3)":         "timestamptz(3)",
		"timestamp":           "timestamptz",
		"longtext":            "text",
		"mediumblob":          "bytea",
		"varbinary(64)":       "bytea",
		"json":                "jsonb",
		"char(36)":            "bpchar(36)",
		"double":              "double precision",
	} {
		got, ok := PostgresType(in)
		assert.True(t, ok, in)
		assert.Equal(t, want, got, in)
	}

	for _, in := range []string{"varchar(255)", "text", "boolean", "bigint_x", ""} {
		_, ok := PostgresType(in)
		assert.False(t, ok, in)
	}
}

type typedModel struct {
	ID        uint64 `gorm:"primaryKey;type:bigint(20) unsigned;autoIncrement"`
	AccountID uint64 `gorm:"type:varchar(64)"`
	Name      string `gorm:"type:varchar(32)"`
	Flag      int    `gorm:"type:tinyint(1)"`
	Score     float64
	Payload   string `gorm:"type:json"`
	At        time.Time
}

func TestPostgresDataTypeOf(t *testing.T) {
	s, err := schema.Parse(&typedModel{}, &sync.Map{}, schema.NamingStrategy{})
	require.NoError(t, err)
	d := PostgresDialector{}
	got := map[string]string{}
	for _, f := range s.Fields {
		got[f.DBName] = d.DataTypeOf(f)
	}
	assert.Equal(t, "bigserial", got["id"])
	assert.Equal(t, "bigint", got["account_id"], "integer ids in varchar columns stay integral")
	assert.Equal(t, "varchar(32)", got["name"])
	assert.Equal(t, "smallint", got["flag"])
	assert.Equal(t, "decimal", got["score"])
	assert.Equal(t, "jsonb", got["payload"])
	assert.Equal(t, "timestamptz", got["at"])
	assert.Equal(t, schema.DataType("bigint(20) unsigned"), s.LookUpField("id").DataType, "DataTypeOf must not mutate the field")
}

type nulModel struct {
	ID   uint64
	Text string
}

func TestPostgresStripsNUL(t *testing.T) {
	pg := dryRun(t, OpenPostgres("host=127.0.0.1 user=x dbname=x sslmode=disable"))
	rows := []nulModel{{Text: "a\x00b"}, {Text: "ok"}}
	require.NoError(t, pg.Create(&rows).Error)
	assert.Equal(t, "ab", rows[0].Text)
	assert.Equal(t, "ok", rows[1].Text)

	updates := map[string]any{"text": "c\x00d"}
	require.NoError(t, pg.Model(&nulModel{ID: 1}).Updates(updates).Error)
	assert.Equal(t, "cd", updates["text"])
}

type ciTag struct {
	ID   uint64
	Name string `gorm:"type:varchar(64)"`
}

func (ciTag) TableName() string { return "tags" }

func TestPostgresCaseInsensitiveColumns(t *testing.T) {
	s, err := schema.Parse(&ciTag{}, &sync.Map{}, schema.NamingStrategy{})
	require.NoError(t, err)
	assert.Equal(t, "citext", PostgresDialector{}.DataTypeOf(s.LookUpField("name")))
}
