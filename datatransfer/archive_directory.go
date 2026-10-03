package datatransfer

import (
	"context"
	"errors"
	"io/fs"
	"os"
)

// OpenDirectory snapshots a canonical administrator export into private scratch
// storage. Source files are opened beneath an anchored directory and never
// modified or removed. Import reads only the verified snapshot, not live paths.
func OpenDirectory(ctx context.Context, directory string, limits Limits) (result *Archive, err error) {
	limits = limits.defaults()
	source, e := os.OpenRoot(directory)
	if e != nil {
		return nil, e
	}
	defer source.Close()
	scratch, e := os.MkdirTemp(limits.TempDir, "community-transfer-")
	if e != nil {
		return nil, e
	}
	a := &Archive{Files: map[string]ArchiveFile{}, root: scratch, limits: limits}
	defer func() {
		if err != nil {
			a.Close()
		}
	}()
	entries := 0
	err = fs.WalkDir(source.FS(), ".", func(name string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if e := ctx.Err(); e != nil {
			return e
		}
		if name == "." {
			return nil
		}
		if _, e := SafeArchiveName(name); e != nil {
			return e
		}
		entries++
		if entries > limits.MaxFiles {
			return errors.New("too many backup entries")
		}
		if d.Type()&os.ModeSymlink != 0 {
			return errors.New("backup symlink is forbidden")
		}
		if d.IsDir() {
			return nil
		}
		info, e := d.Info()
		if e != nil {
			return e
		}
		if !info.Mode().IsRegular() {
			return errors.New("backup special file is forbidden")
		}
		f, e := source.Open(name)
		if e != nil {
			return e
		}
		defer f.Close()
		actual, e := f.Stat()
		if e != nil {
			return e
		}
		if !actual.Mode().IsRegular() || !os.SameFile(info, actual) {
			return errors.New("backup entry changed during snapshot")
		}
		return a.add(ctx, name, f, actual.Size())
	})
	if err != nil {
		return nil, err
	}
	if len(a.Files) == 0 {
		return nil, errors.New("backup contains no data files")
	}
	return a, nil
}
