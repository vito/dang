package analysis

import (
	"fmt"
	"io/fs"
	"sync"
	"time"

	"github.com/vito/dang/v2/pkg/dang"
)

// ParseCache reuses parses of unchanged files across analyses. Sibling files
// in a directory don't change on every keystroke, so reusing their parse
// trees turns re-analyzing a directory from O(files) into O(1) parses. Their
// ImportDecls also keep their inferred schema modules, so the imports phase
// doesn't re-introspect on a hit.
//
// The zero value is ready to use. A ParseCache is safe for concurrent use,
// but the blocks it hands out are shared: inference annotates them in place
// (and prepends auto-imports to their forms, idempotently), so analyses
// sharing a cache must not run over the same files concurrently.
//
// Entries are keyed by file name (see Options.Root), so share a cache only
// among analyses of one Source, or of Sources whose Roots tell their files
// apart.
type ParseCache struct {
	mu      sync.Mutex
	entries map[string]parsedFile
}

// parsedFile is a cache entry for one file's parse, keyed by the file's name.
//
// A file whose Source reports a modification time is fingerprinted by
// mtime+size, so a hit skips reading it. One with no modification time (an
// open editor buffer, an in-memory file) is fingerprinted by its text.
type parsedFile struct {
	block   *dang.FileBlock
	text    string
	mtime   time.Time
	size    int64
	version dang.Version // Dang version the block was parsed as
}

func (c *ParseCache) load(name string) (parsedFile, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[name]
	return entry, ok
}

func (c *ParseCache) store(name string, entry parsedFile) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = make(map[string]parsedFile)
	}
	c.entries[name] = entry
}

// parse returns the parsed block and text of the file at path in src, named
// name in positions, parsed as version. With a cache, an unchanged file's
// cached block is reused.
//
// Read errors are returned as is (an *fs.PathError, from the Source); a parse
// error is returned wrapped in a parseError.
func parse(src Source, path, name string, version dang.Version, cache *ParseCache) (*dang.FileBlock, string, error) {
	info, err := fs.Stat(src, path)
	if err != nil {
		return nil, "", err
	}

	var cached parsedFile
	var hit bool
	if cache != nil {
		cached, hit = cache.load(name)
		hit = hit && cached.version == version
	}

	mtime := info.ModTime()
	if hit && !mtime.IsZero() && cached.mtime.Equal(mtime) && cached.size == info.Size() {
		return cached.block, cached.text, nil
	}

	contents, err := fs.ReadFile(src, path)
	if err != nil {
		return nil, "", err
	}
	text := string(contents)
	if hit && mtime.IsZero() && cached.mtime.IsZero() && cached.text == text {
		return cached.block, cached.text, nil
	}

	parsed, err := dang.ParseWithRecovery(name, contents, dang.GlobalStore("filePath", name), dang.WithVersion(version))
	if err != nil {
		return nil, text, parseError{err}
	}
	block, ok := parsed.(*dang.FileBlock)
	if !ok {
		return nil, text, parseError{fmt.Errorf("parsed result for %s is not a FileBlock", path)}
	}
	if cache != nil {
		cache.store(name, parsedFile{
			block:   block,
			text:    text,
			mtime:   mtime,
			size:    info.Size(),
			version: version,
		})
	}
	return block, text, nil
}

// parseError marks an error as a failure to parse a file that was read fine,
// which is reported as a diagnostic rather than failing the analysis.
type parseError struct{ err error }

func (e parseError) Error() string { return e.err.Error() }
func (e parseError) Unwrap() error { return e.err }
