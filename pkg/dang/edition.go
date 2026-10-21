package dang

import (
	"context"
	"fmt"
	"strings"
)

// Edition selects which revision of Dang's surface syntax a module is written
// in. Every edition runs on the same interpreter, because every edition
// parses into the same AST: a form an edition removed or changed stays in the
// grammar behind an edition gate, and the gated rule's action builds the AST
// of the current equivalent (e.g. edition 1's `for (c) { ... }` parses as a
// `loop`). Old editions therefore cost parsing rules, never interpreter
// branches, and modules of different editions import each other freely.
//
// The same property makes migration free: `dang migrate` parses a file as its
// edition and formats the AST as the target edition (see MigrateSource).
// Changes that only make the language stricter have no gate; old code that
// trips them stops compiling and has to be fixed by hand.
//
// Editions are opaque strings, so they can be named by version or date; their
// order is their position in Editions. Gate syntax on a named feature method
// (see singleBraceSelection) defined in terms of that order, never on an
// edition's name.
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

// Editions lists every supported edition, oldest first. Adding an edition
// appends to it; an edition is never removed or reordered.
var Editions = []Edition{Edition1, Edition2}

// index returns e's position in Editions. An unknown edition (e.g. the zero
// value) is treated as LatestEdition.
func (e Edition) index() int {
	for i, known := range Editions {
		if known == e {
			return i
		}
	}
	return LatestEdition.index()
}

// Before reports whether e is older than other.
func (e Edition) Before(other Edition) bool {
	return e.index() < other.index()
}

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
// (and dot-block application is unavailable), as before edition 2.
func (e Edition) singleBraceSelection() bool {
	return e.Before(Edition2)
}

// forLoops reports whether `for (cond) { ... }` and `for { ... }` are loops,
// as before edition 2 removed them (where `for` is an ordinary identifier).
// They parse as the equivalent `loop` call; see desugarForLoop.
func (e Edition) forLoops() bool {
	return e.Before(Edition2)
}

// unmatchedCaseRaises reports whether a case that matches no clause raises,
// as before edition 2 (where it yields null, making the case's type nullable
// unless its clauses are exhaustive). See editionCaseClauses.
func (e Edition) unmatchedCaseRaises() bool {
	return e.Before(Edition2)
}

// deadClausesAfterElse reports whether case clauses may follow the else
// clause, as before edition 2 (where they are an unreachable-clause error).
// Clauses match in order, so they never ran; see editionCaseClauses.
func (e Edition) deadClausesAfterElse() bool {
	return e.Before(Edition2)
}

// commaFormSep reports whether a comma separates forms in blocks, block
// arguments and the file top level, as before edition 2 (where only `;` or a
// newline does).
func (e Edition) commaFormSep() bool {
	return e.Before(Edition2)
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

// unmatchedCaseError is the message edition 1 raised when no case clause
// matched. Edition 1 went on to print the operand's value, which the desugared
// clause can't refer to without evaluating the operand a second time.
const unmatchedCaseError = "no case clause matched"

// editionCaseClauses builds the current equivalent of a case's clauses as
// written in edition e. Where clauses may follow the else clause (see
// deadClausesAfterElse), they are dropped, since they could never match.
// Where an unmatched case raises (see unmatchedCaseRaises), a case without an
// else clause gets
//
//	else => raise "no case clause matched"
//
// so that it raises the same way and its type stays non-null. It takes the
// last clause's location, so the formatter places comments around it exactly
// as around the last clause.
func editionCaseClauses(e Edition, clauses []*CaseClause, loc *SourceLocation) []*CaseClause {
	for i, clause := range clauses {
		if clause.IsElse {
			if e.deadClausesAfterElse() {
				return clauses[:i+1]
			}
			return clauses
		}
	}
	if !e.unmatchedCaseRaises() || len(clauses) == 0 {
		return clauses
	}
	return append(clauses, &CaseClause{
		Expr: &Raise{
			Value: &String{Value: unmatchedCaseError, Loc: loc},
			Loc:   loc,
		},
		IsElse: true,
		Loc:    clauses[len(clauses)-1].Loc,
	})
}

// desugarForLoop builds the current equivalent of edition 1's `for` loops:
// `for { body }` is `loop { body }`, and `for (cond) { body }` is
//
//	loop { if (cond) { body } else { break } }
//
// Keeping the body in the then-branch keeps the condition narrowing it, as
// it did in edition 1.
//
// Two differences remain: a `for` yielded its last body value when it ended
// (by its condition going false, or a bare `break`), where `loop` yields
// null. Only `break value` gives both the same result. And the desugared
// call names the `loop` builtin, so a module that binds its own `loop`
// (legal in edition 1, which had no such builtin) shadows it.
func desugarForLoop(cond Node, body *Block, loc *SourceLocation) Node {
	loopBody := body
	if cond != nil {
		loopBody = &Block{
			Forms: []Node{&Conditional{
				Condition: cond,
				Then:      body,
				Else: &Block{
					Forms: []Node{&Break{Loc: loc}},
					Loc:   loc,
				},
				Loc: loc,
			}},
			Loc: loc,
		}
	}
	return &FunCall{
		Fun: &Symbol{Name: "loop", Loc: loc},
		BlockArg: &BlockArg{
			BodyNode: loopBody,
			Loc:      loc,
		},
		Loc: loc,
	}
}
