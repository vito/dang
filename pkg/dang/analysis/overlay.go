package analysis

import (
	"bytes"
	"errors"
	"io/fs"
	"path"
	"sort"
	"time"
)

// Overlay returns a Source that reads files from base, except for the paths in
// files, whose contents it serves instead: the LSP lays its open buffers over
// the disk this way, and a snippet can be checked as one more file of a
// directory. Overlaid files need not exist in base, and are listed by ReadDir
// with their directory's other files. They report no modification time.
func Overlay(base fs.FS, files map[string]string) Source {
	o := overlay{base: base, files: make(map[string]string, len(files))}
	for p, text := range files {
		o.files[path.Clean(p)] = text
	}
	return o
}

type overlay struct {
	base  fs.FS
	files map[string]string
}

var (
	_ fs.ReadDirFS  = overlay{}
	_ fs.ReadFileFS = overlay{}
	_ fs.StatFS     = overlay{}
)

func (o overlay) Open(name string) (fs.File, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}
	if text, ok := o.files[name]; ok {
		return &overlayFile{info: overlayInfo{name: path.Base(name), size: int64(len(text))}, r: bytes.NewReader([]byte(text))}, nil
	}
	return o.base.Open(name)
}

func (o overlay) ReadFile(name string) ([]byte, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "readfile", Path: name, Err: fs.ErrInvalid}
	}
	if text, ok := o.files[name]; ok {
		return []byte(text), nil
	}
	return fs.ReadFile(o.base, name)
}

func (o overlay) Stat(name string) (fs.FileInfo, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "stat", Path: name, Err: fs.ErrInvalid}
	}
	if text, ok := o.files[name]; ok {
		return overlayInfo{name: path.Base(name), size: int64(len(text))}, nil
	}
	return fs.Stat(o.base, name)
}

func (o overlay) ReadDir(name string) ([]fs.DirEntry, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "readdir", Path: name, Err: fs.ErrInvalid}
	}
	entries, err := fs.ReadDir(o.base, name)
	var overlaid []fs.DirEntry
	for p, text := range o.files {
		if path.Dir(p) == name {
			overlaid = append(overlaid, fs.FileInfoToDirEntry(overlayInfo{name: path.Base(p), size: int64(len(text))}))
		}
	}
	if err != nil && (!errors.Is(err, fs.ErrNotExist) || len(overlaid) == 0) {
		return nil, err
	}
	seen := make(map[string]bool, len(overlaid))
	for _, e := range overlaid {
		seen[e.Name()] = true
	}
	for _, e := range entries {
		if !seen[e.Name()] {
			overlaid = append(overlaid, e)
		}
	}
	sort.Slice(overlaid, func(i, j int) bool { return overlaid[i].Name() < overlaid[j].Name() })
	return overlaid, nil
}

type overlayFile struct {
	info overlayInfo
	r    *bytes.Reader
}

func (f *overlayFile) Stat() (fs.FileInfo, error) { return f.info, nil }
func (f *overlayFile) Read(p []byte) (int, error) { return f.r.Read(p) }
func (f *overlayFile) Close() error               { return nil }

type overlayInfo struct {
	name string
	size int64
}

func (i overlayInfo) Name() string       { return i.name }
func (i overlayInfo) Size() int64        { return i.size }
func (i overlayInfo) Mode() fs.FileMode  { return 0o444 }
func (i overlayInfo) ModTime() time.Time { return time.Time{} }
func (i overlayInfo) IsDir() bool        { return false }
func (i overlayInfo) Sys() any           { return nil }
