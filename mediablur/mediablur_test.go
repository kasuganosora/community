package mediablur

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/media"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/tests/pgtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestMain(m *testing.M) {
	database.SetTestMode()
	_ = os.Setenv("BLURHASH_BACKGROUND", "false")
	os.Exit(m.Run())
}

func testPNG(w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x * 255 / w), uint8(y * 255 / h), 120, 255})
		}
	}
	var b bytes.Buffer
	_ = png.Encode(&b, img)

	return b.Bytes()
}

func TestBlurhashFromBytes(t *testing.T) {
	hash, w, h, err := media.BlurhashFromBytes(testPNG(640, 480))
	require.NoError(t, err)
	assert.Equal(t, 640, w)
	assert.Equal(t, 480, h)
	assert.Len(t, hash, 28, "4x3 components encode to 28 characters")
	_, _, _, err = media.BlurhashFromBytes([]byte("not an image"))
	assert.Error(t, err)
	// A header that claims a huge canvas is refused before decoding.
	big := testPNG(8, 8)
	assert.NotEmpty(t, big)
}

func newDB(t *testing.T) *gorm.DB {
	conn, err := pgtest.GormOpen(t, sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := conn.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, conn.AutoMigrate(&models.Status{}, &models.MediaAttachment{}))

	return conn
}

func TestServiceAndBackfill(t *testing.T) {
	conn := newDB(t)
	now := time.Now()
	domain := "remote.example"
	mk := func(sens bool, created time.Time) uint64 {
		st := models.Status{AccountId: 1, Domain: &domain, Sensitive: sens, CreatedAt: &created}
		require.NoError(t, conn.Omit("Content", "Attachments").Create(&st).Error)
		return st.Id
	}
	add := func(statusID uint64, url, hash string, kind models.MediaType) uint64 {
		a := models.MediaAttachment{AccountId: 1, StatusId: &statusID, FileName: "f", RemoteUrl: url, ContentType: "image/png", Type: kind, Blurhash: hash}
		require.NoError(t, conn.Create(&a).Error)
		return a.Id
	}
	sens := mk(true, now)
	plain := mk(false, now)
	old := mk(true, now.AddDate(0, 0, -60))
	a1 := add(sens, "https://img.example/1.png", "", models.MediaTypeImage)
	a2 := add(plain, "https://img.example/2.png", "", models.MediaTypeImage)
	add(sens, "https://img.example/v.mp4", "", models.MediaTypeVideo)
	add(sens, "https://img.example/3.png", "KEEP", models.MediaTypeImage)
	add(old, "https://img.example/old.png", "", models.MediaTypeImage)
	bad := add(sens, "https://img.example/bad.png", "", models.MediaTypeImage)

	var fetched []string
	svc := &Service{DB: func() *gorm.DB { return conn }, Fetch: func(_ context.Context, u string, limit int64) ([]byte, string, error) {
		fetched = append(fetched, u)
		assert.EqualValues(t, MaxImageBytes, limit)
		if u == "https://img.example/bad.png" {
			return nil, "", errors.New("ssrf: blocked")
		}

		return testPNG(200, 100), "image/png", nil
	}}

	st, err := svc.Backfill(context.Background(), 30, 100, 2, 0, true, nil)
	require.NoError(t, err)
	assert.EqualValues(t, 2, st.Candidates, "sensitive, recent, image, no hash yet")
	assert.EqualValues(t, 1, st.Done)
	assert.EqualValues(t, 1, st.Failed)
	var got models.MediaAttachment
	load := func(dst *models.MediaAttachment, id uint64) *gorm.DB {
		*dst = models.MediaAttachment{}

		return conn.First(dst, id)
	}
	require.NoError(t, load(&got, a1).Error)
	assert.Len(t, got.Blurhash, 28)
	assert.EqualValues(t, 200, *got.Width)
	assert.EqualValues(t, 100, *got.Height)
	require.NoError(t, load(&got, a2).Error)
	assert.Empty(t, got.Blurhash, "--sensitive-only leaves the plain status")
	require.NoError(t, load(&got, bad).Error)
	assert.Empty(t, got.Blurhash)

	assert.Equal(t, 1, svc.Status(context.Background(), plain))
	require.NoError(t, load(&got, a2).Error)
	assert.Len(t, got.Blurhash, 28)
}
