package setup

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"

	"github.com/joho/godotenv"
)

// writeFileAtomic writes data to a temporary file in the target directory,
// fsyncs it and renames it over path, so readers see either the old or the
// complete new file.
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(name)
		}
	}()
	if err = tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err = tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err = tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	if err = os.Rename(name, path); err != nil {
		return err
	}
	ok = true
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

// WriteEnvFile merges values into the .env file at path (keeping unrelated
// keys already present) and replaces it atomically with mode 0600. Values
// are quoted by godotenv, so passwords may contain any character.
func WriteEnvFile(path string, values map[string]string) error {
	merged := map[string]string{}
	if existing, err := godotenv.Read(path); err == nil {
		merged = existing
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for k, v := range values {
		if v == "" {
			delete(merged, k)
			continue
		}
		merged[k] = v
	}
	content, err := godotenv.Marshal(merged)
	if err != nil {
		return err
	}
	header := "# Written by the Community setup wizard. Keep this file private (mode 0600).\n" +
		"# Only core settings belong here; everything else is edited in the admin web UI.\n"
	if err = os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return writeFileAtomic(path, []byte(header+content+"\n"), 0o600)
}

// randomSecret returns n random bytes, base64url encoded without padding.
func randomSecret(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// generatedSecrets are created once by the wizard. ENCRYPTED_KEY encrypts
// stored secrets (and signs media proxy tokens unless MEDIA_PROXY_KEY is
// set); MEDIA_PROXY_KEY is kept separate so the encryption key never has to
// be rotated together with proxy tokens.
func generatedSecrets(existing map[string]string) map[string]string {
	out := map[string]string{}
	for _, key := range []string{"ENCRYPTED_KEY", "MEDIA_PROXY_KEY"} {
		if existing[key] == "" {
			out[key] = randomSecret(48)
		}
	}
	return out
}
