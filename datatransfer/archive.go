// Package datatransfer reads untrusted user backups without restoring server
// identities, authentication secrets, or executable filesystem metadata.
package datatransfer

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

type Limits struct {
	MaxCompressedBytes int64
	MaxExpandedBytes   int64
	MaxFileBytes       int64
	MaxJSONBytes       int64
	MaxFiles           int
	MaxRecords         int
	TempDir            string
	// Skip lists top-level directory entries OpenDirectory ignores (a streamed
	// bundle.json or the exporter's state). AllowEmpty accepts a directory
	// without files (direct mode with media kept in object storage).
	Skip       []string
	AllowEmpty bool
}

func DefaultLimits() Limits {
	return Limits{MaxCompressedBytes: 256 << 20, MaxExpandedBytes: 1 << 30, MaxFileBytes: 128 << 20, MaxJSONBytes: 64 << 20, MaxFiles: 20000, MaxRecords: 100000}
}
func (l Limits) defaults() Limits {
	d := DefaultLimits()
	if l.MaxCompressedBytes <= 0 {
		l.MaxCompressedBytes = d.MaxCompressedBytes
	}
	if l.MaxExpandedBytes <= 0 {
		l.MaxExpandedBytes = d.MaxExpandedBytes
	}
	if l.MaxFileBytes <= 0 {
		l.MaxFileBytes = d.MaxFileBytes
	}
	if l.MaxJSONBytes <= 0 {
		l.MaxJSONBytes = d.MaxJSONBytes
	}
	if l.MaxFiles <= 0 {
		l.MaxFiles = d.MaxFiles
	}
	if l.MaxRecords <= 0 {
		l.MaxRecords = d.MaxRecords
	}
	return l
}

type ArchiveFile struct {
	Name   string `json:"name"`
	Path   string `json:"-"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}
type Archive struct {
	Files    map[string]ArchiveFile
	root     string
	limits   Limits
	expanded int64
}

func (a *Archive) Close() error {
	if a == nil || a.root == "" {
		return nil
	}
	err := os.RemoveAll(a.root)
	a.root = ""
	return err
}
func (a *Archive) Read(name string, max int64) ([]byte, error) {
	f, ok := a.Files[name]
	if !ok {
		return nil, os.ErrNotExist
	}
	if max <= 0 {
		max = a.limits.MaxJSONBytes
	}
	if f.Size > max {
		return nil, fmt.Errorf("backup entry %q exceeds read limit", name)
	}
	file, err := os.Open(f.Path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return io.ReadAll(io.LimitReader(file, max+1))
}

// SafeArchiveName rejects ambiguous names rather than cleaning traversal away.
func SafeArchiveName(name string) (string, error) {
	if name == "" || len(name) > 512 || strings.ContainsAny(name, "\\\x00") || strings.HasPrefix(name, "/") || strings.Contains(name, ":") {
		return "", errors.New("invalid archive entry path")
	}
	trimmed := strings.TrimSuffix(name, "/")
	parts := strings.Split(trimmed, "/")
	if len(parts) > 32 {
		return "", errors.New("archive entry path too deep")
	}
	for _, p := range parts {
		if p == "" || p == "." || p == ".." {
			return "", errors.New("ambiguous archive entry path")
		}
	}
	if path.Clean(trimmed) != trimmed {
		return "", errors.New("invalid archive entry path")
	}
	return trimmed, nil
}

type archiveContextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r archiveContextReader) Read(b []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(b)
}
func (a *Archive) add(ctx context.Context, name string, r io.Reader, declared int64) error {
	canonical, err := SafeArchiveName(name)
	if err != nil {
		return err
	}
	if _, ok := a.Files[canonical]; ok {
		return fmt.Errorf("duplicate archive entry %q", canonical)
	}
	if len(a.Files) >= a.limits.MaxFiles {
		return errors.New("too many backup entries")
	}
	if declared > a.limits.MaxFileBytes {
		return errors.New("backup entry exceeds size limit")
	}
	allowed := min(a.limits.MaxFileBytes, a.limits.MaxExpandedBytes-a.expanded)
	if allowed < 0 {
		return errors.New("backup exceeds expanded size limit")
	}
	file, err := os.CreateTemp(a.root, "entry-")
	if err != nil {
		return err
	}
	h := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(file, h), io.LimitReader(archiveContextReader{ctx, r}, allowed+1))
	closeErr := file.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if n > allowed {
		return errors.New("backup exceeds expanded size or file limit")
	}
	a.expanded += n
	a.Files[canonical] = ArchiveFile{Name: canonical, Path: file.Name(), Size: n, SHA256: hex.EncodeToString(h.Sum(nil))}
	return nil
}

// OpenArchive stages only regular data files into randomly named private files.
// It never follows archive paths, symlinks, hardlinks, or recursively opens
// nested archives. Callers must Close on both successful parsing and job expiry.
func OpenArchive(ctx context.Context, filename string, src io.Reader, limits Limits) (archive *Archive, err error) {
	limits = limits.defaults()
	root, err := os.MkdirTemp(limits.TempDir, "community-transfer-")
	if err != nil {
		return nil, err
	}
	a := &Archive{Files: map[string]ArchiveFile{}, root: root, limits: limits}
	defer func() {
		if err != nil {
			_ = a.Close()
		}
	}()
	packed, err := os.CreateTemp(root, "source-")
	if err != nil {
		return nil, err
	}
	n, copyErr := io.Copy(packed, io.LimitReader(archiveContextReader{ctx, src}, limits.MaxCompressedBytes+1))
	if copyErr != nil {
		_ = packed.Close()
		return nil, copyErr
	}
	if n > limits.MaxCompressedBytes {
		_ = packed.Close()
		return nil, errors.New("backup upload exceeds compressed size limit")
	}
	if _, err = packed.Seek(0, 0); err != nil {
		_ = packed.Close()
		return nil, err
	}
	defer packed.Close()
	reader := bufio.NewReader(packed)
	magic, _ := reader.Peek(4)
	switch {
	case len(magic) >= 4 && string(magic[:2]) == "PK":
		var zr *zip.Reader
		zr, err = zip.NewReader(packed, n)
		if err != nil {
			return nil, err
		}
		if len(zr.File) > limits.MaxFiles {
			return nil, errors.New("too many backup entries")
		}
		seen := map[string]bool{}
		for _, entry := range zr.File {
			name, e := SafeArchiveName(entry.Name)
			if e != nil {
				return nil, e
			}
			if seen[name] {
				return nil, errors.New("duplicate archive path")
			}
			seen[name] = true
			if entry.Flags&1 != 0 {
				return nil, errors.New("encrypted ZIP entries are unsupported")
			}
			if entry.FileInfo().IsDir() {
				continue
			}
			if !entry.Mode().IsRegular() {
				return nil, errors.New("archive links or special entries are forbidden")
			}
			if entry.UncompressedSize64 > uint64(limits.MaxFileBytes) {
				return nil, errors.New("backup entry exceeds size limit")
			}
			body, e := entry.Open()
			if e != nil {
				return nil, e
			}
			e = a.add(ctx, name, body, int64(entry.UncompressedSize64))
			ce := body.Close()
			if e != nil {
				return nil, e
			}
			if ce != nil {
				return nil, ce
			}
		}
	case len(magic) >= 2 && magic[0] == 0x1f && magic[1] == 0x8b:
		gz, e := gzip.NewReader(reader)
		if e != nil {
			return nil, e
		}
		defer gz.Close()
		expandedReader := &io.LimitedReader{R: gz, N: limits.MaxExpandedBytes + 1}
		tr := tar.NewReader(expandedReader)
		seen := map[string]bool{}
		entries := 0
		for {
			header, e := tr.Next()
			if e == io.EOF {
				if _, e = io.Copy(io.Discard, expandedReader); e != nil {
					return nil, e
				}
				if expandedReader.N <= 0 {
					return nil, errors.New("archive stream exceeds expansion limit")
				}
				break
			}
			if e != nil {
				return nil, e
			}
			entries++
			if entries > limits.MaxFiles {
				return nil, errors.New("too many backup entries")
			}
			name, e := SafeArchiveName(header.Name)
			if e != nil {
				return nil, e
			}
			if seen[name] {
				return nil, errors.New("duplicate archive path")
			}
			seen[name] = true
			switch header.Typeflag {
			case tar.TypeDir:
				continue
			case tar.TypeReg, tar.TypeRegA:
			default:
				return nil, errors.New("archive links or special entries are forbidden")
			}
			if e = a.add(ctx, name, tr, header.Size); e != nil {
				return nil, e
			}
		}
	default:
		name := filepath.Base(filename)
		ext := strings.ToLower(filepath.Ext(name))
		if ext != ".json" && ext != ".csv" {
			return nil, errors.New("expected ZIP, tar.gz, JSON, or CSV user backup")
		}
		if err = a.add(ctx, name, reader, n); err != nil {
			return nil, err
		}
	}
	if len(a.Files) == 0 {
		return nil, errors.New("backup contains no data files")
	}
	return a, nil
}
