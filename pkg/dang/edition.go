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

	// DefaultEdition is the edition of a module that does not declare one.
	//
	// It is pinned to the edition current when editions were introduced and
	// must NOT advance when a new edition is added: a dang.toml or script
	// without an `edition` key would otherwise silently change meaning.
	DefaultEdition = Edition2
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
// Parsing without it uses DefaultEdition.
func WithEdition(e Edition) Option {
	return GlobalStore(editionStoreKey, e)
}

// editionFromStore reads the edition from the parser's global store.
func editionFromStore(store map[string]any) Edition {
	if e, ok := store[editionStoreKey].(Edition); ok && e != "" {
		return e
	}
	return DefaultEdition
}

type editionKey struct{}

// ContextWithEdition sets the edition for source that is not governed by a
// dang.toml. Embedders (e.g. a Dagger SDK pinning a module's edition from its
// engine version) use it to choose the edition when no dang.toml is read;
// a dang.toml found during evaluation takes precedence over it.
func ContextWithEdition(ctx context.Context, e Edition) context.Context {
	return context.WithValue(ctx, editionKey{}, e)
}

// EditionFromContext returns the edition in effect: the edition of the
// project config attached to ctx if there is one (DefaultEdition when it
// declares none), otherwise the edition set by ContextWithEdition, otherwise
// DefaultEdition.
func EditionFromContext(ctx context.Context) Edition {
	if _, cfg := projectConfigFromContext(ctx); cfg != nil {
		return cfg.EffectiveEdition()
	}
	if e, ok := ctx.Value(editionKey{}).(Edition); ok && e != "" {
		return e
	}
	return DefaultEdition
}

// EditionForDir returns the edition governing source files in dir, found the
// same way as dang.toml imports: the nearest dang.toml walking up from dir.
func EditionForDir(dir string) (Edition, error) {
	_, cfg, err := FindProjectConfig(dir)
	if err != nil {
		return DefaultEdition, err
	}
	return cfg.EffectiveEdition(), nil
}
