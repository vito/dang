package dang

import (
	"context"
	"fmt"
	"strings"
)

// Edition selects which revision of Dang's surface syntax a module is written
// in. Every edition runs on the same interpreter: an edition only changes how
// source is parsed (and formatted), so modules of different editions can
// import each other freely.
//
// Editions are opaque strings rather than numbers so that future editions can
// be named by date or semver. Code must never compare editions for ordering;
// gate behavior on a feature method instead (see singleBraceSelection).
type Edition string

const (
	// Edition1 is the syntax of the github.com/vito/dang v1 line, where
	// `recv.{ a, b }` is object selection and there is no dot-block
	// application.
	Edition1 Edition = "1"

	// Edition2 is the current syntax: object selection is `recv.{{ a, b }}`
	// and `recv.{ ... }` applies a block to the receiver.
	Edition2 Edition = "2"

	// DefaultEdition is the edition of a dang.toml that does not declare one.
	//
	// It is pinned to the edition current when editions were introduced and
	// must NOT advance when a new edition is added: a dang.toml without an
	// `edition` key would otherwise silently change meaning.
	DefaultEdition = Edition2

	// LatestEdition is the newest edition. Source that no dang.toml governs
	// is written in it (see EditionForDir), so it advances with each new
	// edition.
	LatestEdition = Edition2
)

// Editions lists every supported edition, oldest first.
var Editions = []Edition{Edition1, Edition2}

// ParseEdition validates an edition string.
func ParseEdition(s string) (Edition, error) {
	for _, e := range Editions {
		if string(e) == s {
			return e, nil
		}
	}
	quoted := make([]string, len(Editions))
	for i, e := range Editions {
		quoted[i] = fmt.Sprintf("%q", string(e))
	}
	return "", fmt.Errorf("unknown edition %q (supported: %s)", s, strings.Join(quoted, ", "))
}

// UnmarshalTOML decodes and validates the `edition` key of dang.toml. It
// rejects non-strings explicitly so that `edition = 2` gets a pointed error
// instead of a generic type mismatch.
func (e *Edition) UnmarshalTOML(v any) error {
	s, ok := v.(string)
	if !ok {
		return fmt.Errorf("edition must be a string, e.g. edition = \"%s\"", DefaultEdition)
	}
	parsed, err := ParseEdition(s)
	if err != nil {
		return err
	}
	*e = parsed
	return nil
}

// singleBraceSelection reports whether `recv.{ ... }` is object selection
// (and dot-block application is unavailable), as in edition 1.
func (e Edition) singleBraceSelection() bool {
	return e == Edition1
}

// selectionBraces returns the delimiters for object selections and inline
// fragment bodies in this edition.
func (e Edition) selectionBraces() (openBrace, closeBrace string) {
	if e.singleBraceSelection() {
		return "{", "}"
	}
	return "{{", "}}"
}

const editionStoreKey = "edition"

// WithEdition is a parser option selecting the edition to parse source as.
// Parsing without it uses LatestEdition.
func WithEdition(e Edition) Option {
	return GlobalStore(editionStoreKey, e)
}

// editionFromStore reads the edition from the parser's global store.
func editionFromStore(store map[string]any) Edition {
	if e, ok := store[editionStoreKey].(Edition); ok && e != "" {
		return e
	}
	return LatestEdition
}

type editionKey struct{}

// ContextWithEdition sets the edition of the source evaluated directly with
// ctx (the directory or file passed to RunDir, DeclareDir or RunFile, or
// source with no path at all, such as REPL input or a completion fragment)
// when no dang.toml governs it, in place of LatestEdition. Embedders use it
// for source whose edition they know some other way, e.g. a Dagger SDK
// pinning a module's edition from its engine version.
//
// It never overrides a dang.toml (see EditionForDir), and it does not reach
// the modules that source imports: an imported module's edition depends only
// on its own path, so an ungoverned import is LatestEdition.
func ContextWithEdition(ctx context.Context, e Edition) context.Context {
	return context.WithValue(ctx, editionKey{}, e)
}

// withoutContextEdition clears ContextWithEdition, for crossing into an
// imported module.
func withoutContextEdition(ctx context.Context) context.Context {
	return ContextWithEdition(ctx, "")
}

// EditionFromContext returns the edition of source that no dang.toml
// governs: the edition set by ContextWithEdition, or LatestEdition.
func EditionFromContext(ctx context.Context) Edition {
	if e, ok := ctx.Value(editionKey{}).(Edition); ok && e != "" {
		return e
	}
	return LatestEdition
}

// EditionForDir returns the edition of the source files in dir. It depends
// only on where the source lives: the nearest dang.toml walking up from dir
// decides (DefaultEdition if it declares none), regardless of who imports the
// source or which dang.toml configured the evaluation. If discovery reaches a
// repository boundary (.git) or the filesystem root first, no dang.toml
// governs the source, and it is EditionFromContext(ctx).
func EditionForDir(ctx context.Context, dir string) (Edition, error) {
	_, cfg, err := FindProjectConfig(dir)
	if err != nil {
		return EditionFromContext(ctx), err
	}
	return EditionForConfig(ctx, cfg), nil
}

// EditionForConfig returns the edition of source governed by cfg, the result
// of FindProjectConfig for the source's directory (nil if none was found).
// Callers that already looked up the config use it to avoid a second lookup.
func EditionForConfig(ctx context.Context, cfg *ProjectConfig) Edition {
	if cfg == nil {
		return EditionFromContext(ctx)
	}
	return cfg.EffectiveEdition()
}
