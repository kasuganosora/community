package storage_test

import (
	"context"
	"strings"
	"testing"

	"github.com/kasuganosora/community/storage"
	"github.com/kasuganosora/community/storage/storagetest"
	"github.com/stretchr/testify/require"
)

func TestBackendList(t *testing.T) {
	for name, mk := range map[string]func(t *testing.T) storage.Backend{
		"local": func(t *testing.T) storage.Backend { return &storage.LocalBackend{Root: t.TempDir()} },
		"fake":  func(*testing.T) storage.Backend { return storagetest.NewMemory() },
		"s3": func(t *testing.T) storage.Backend {
			env := storagetest.S3FromEnv(t)

			return storage.NewS3Backend(env.Config(env.NewBucket(t)))
		},
	} {
		t.Run(name, func(t *testing.T) {
			b := mk(t)
			ctx := context.Background()

			objects := map[string]string{
				"media/2026/10/01/a.jpg":   "aaa",
				"media/2026/10/02/b.jpg":   "bbbbb",
				"matrix/2026/10/01/c.png":  "c",
				"emojis/d.png":             "dddd",
				"mediaextra/not-media.txt": "e",
			}
			for k, v := range objects {
				require.NoError(t, b.Put(ctx, k, strings.NewReader(v), int64(len(v)), ""))
			}

			list := func(prefix string) map[string]int64 {
				got := map[string]int64{}
				require.NoError(t, b.List(ctx, prefix, func(o storage.ObjectInfo) error {
					got[o.Key] = o.Size
					require.False(t, o.ModTime.IsZero(), o.Key)

					return nil
				}))

				return got
			}

			require.Len(t, list(""), len(objects))
			require.Equal(t, map[string]int64{"media/2026/10/01/a.jpg": 3, "media/2026/10/02/b.jpg": 5}, list("media/"))
			require.Equal(t, map[string]int64{"media/2026/10/02/b.jpg": 5}, list("media/2026/10/02"))
			require.Len(t, list("media"), 3, "plain string prefix, like S3")
			require.Empty(t, list("nothing/"))

			n := 0
			require.NoError(t, b.List(ctx, "", func(storage.ObjectInfo) error {
				n++

				return storage.ErrStopList
			}))
			require.Equal(t, 1, n)

			require.ErrorIs(t, b.List(ctx, "../etc", func(storage.ObjectInfo) error { return nil }), storage.ErrInvalidKey)
		})
	}
}
