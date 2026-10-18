package datatransfer

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// PrivateDirMode / PrivateFileMode are used for every work-dir, spool and
// staging file: they can contain password hashes, signing keys and plaintext
// TOTP secrets of the source server.
const (
	PrivateDirMode  fs.FileMode = 0o700
	PrivateFileMode fs.FileMode = 0o600
)

// EnsurePrivateDir creates dir (and parents) and forces mode 0700 on dir
// itself, also when it already existed with looser permissions.
func EnsurePrivateDir(dir string) error {
	if err := os.MkdirAll(dir, PrivateDirMode); err != nil {
		return err
	}
	return os.Chmod(dir, PrivateDirMode)
}

// ensurePrivateFile creates path with mode 0600 if missing and tightens an
// existing file to 0600.
func ensurePrivateFile(path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, PrivateFileMode)
	if err != nil {
		return err
	}
	_ = f.Close()
	return os.Chmod(path, PrivateFileMode)
}

// TightenTree sets 0700 on directories and 0600 on regular files below root.
func TightenTree(root string) error {
	return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			return nil
		case d.IsDir():
			return os.Chmod(p, PrivateDirMode)
		case d.Type().IsRegular():
			return os.Chmod(p, PrivateFileMode)
		}
		return nil
	})
}

// WipeFile overwrites a regular file with zeros, syncs it and removes it.
// This is best effort: copy-on-write filesystems, SSD wear levelling and
// snapshots can keep old blocks, so encrypt the disk for real guarantees.
func WipeFile(path string) error {
	st, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if st.Mode().IsRegular() && st.Size() > 0 {
		if f, oerr := os.OpenFile(path, os.O_WRONLY, 0); oerr == nil {
			zero := make([]byte, 1<<16)
			for left := st.Size(); left > 0; {
				n := int64(len(zero))
				if left < n {
					n = left
				}
				if _, werr := f.Write(zero[:n]); werr != nil {
					break
				}
				left -= n
			}
			_ = f.Sync()
			_ = f.Close()
		}
	}
	return os.Remove(path)
}

// WipeTree wipes every regular file below root and removes root.
func WipeTree(root string) error {
	if _, err := os.Lstat(root); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	var first error
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			if werr := WipeFile(p); werr != nil && first == nil {
				first = werr
			}
		}
		return nil
	})
	if err := os.RemoveAll(root); err != nil && first == nil {
		first = err
	}
	return first
}

// SecretSpoolPaths lists the files in a direct-mode work directory that hold
// source credential material (spool database, exporter staging state and
// partial bundle spools). Copied media is not included.
func SecretSpoolPaths(workDir string) []string {
	paths := []string{}
	for _, name := range []string{"spool.sqlite3", "spool.sqlite3-wal", "spool.sqlite3-shm", "spool.sqlite3-journal", "state"} {
		paths = append(paths, filepath.Join(workDir, name))
	}
	entries, _ := os.ReadDir(workDir)
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "out-") {
			continue
		}
		dir := filepath.Join(workDir, e.Name())
		for _, name := range []string{".spool", "bundle.json", "bundle.json.partial"} {
			paths = append(paths, filepath.Join(dir, name))
		}
	}
	return paths
}

// WipeSecretSpool removes SecretSpoolPaths(workDir) and returns the paths
// that existed and were wiped.
func WipeSecretSpool(workDir string) ([]string, error) {
	var wiped []string
	var first error
	for _, p := range SecretSpoolPaths(workDir) {
		st, err := os.Lstat(p)
		if err != nil {
			continue
		}
		if st.IsDir() {
			err = WipeTree(p)
		} else {
			err = WipeFile(p)
		}
		if err != nil && first == nil {
			first = err
		}
		wiped = append(wiped, p)
	}
	return wiped, first
}
