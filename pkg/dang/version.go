package dang

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
)

// Version is a Dang language version: the major.minor of the Dang release
// whose syntax a project's source is written in. A dang.toml declares it as
//
//	dang = "2.1"
//
// Every version runs on the same interpreter, because every version parses
// into the same AST: a form a newer version removed or changed stays in the
// grammar behind a version gate, and the gated rule's action builds the AST
// of the current equivalent (e.g. 2.0's `for (c) { ... }` parses as a
// `loop`). Old versions therefore cost parsing rules, never interpreter
// branches, and modules of different versions import each other freely.
//
// The same property makes migration free: `dang migrate` parses a file as its
// version and formats the AST as the target version (see MigrateSource).
// Changes that only make the language stricter have no gate; old code that
// trips them stops compiling and has to be fixed by hand.
//
// Gate syntax on a named feature method (see singleBraceSelection) defined by
// comparing against the version that changed it, e.g. v.Before(Version2_0).
// Versions are major.minor: from now on, patch releases must not change the
// language. Before versions were declared, some did: 2.1.x patches switched
// inline fragment bodies to double braces and reserved `rescue`. So a
// version means the syntax of that minor's latest release, e.g. 2.1 is
// 2.1.4's, and code relying on an earlier patch's spelling may need fixing by
// hand (`rescue` used as a name is such a case: it has no gate).
//
// The zero Version means CurrentVersion.
type Version struct {
	Major, Minor int
}

var (
	// Version1_0 is the language of the github.com/vito/dang v1 line, where
	// `recv.{ a, b }` is object selection and there is no dot-block
	// application.
	Version1_0 = Version{1, 0}

	// Version2_0 changed object selection to `recv.{{ a, b }}`, so that
	// `recv.{ ... }` applies a block to the receiver.
	Version2_0 = Version{2, 0}

	// Version2_1 removed `for` loops and commas between forms, made a case
	// that matches nothing yield null instead of raising, and spelled inline
	// fragment bodies with double braces.
	Version2_1 = Version{2, 1}

	// CurrentVersion is the language version this build of Dang implements:
	// the major.minor that main will release as. Bump it in the commit that
	// adds the first gate for a new version (a gate on a version newer than
	// CurrentVersion could never be enabled, since ParseVersion refuses that
	// version), and at the latest when releasing a new minor or major.
	// Source no dang.toml governs is written in it (see VersionForDir), and
	// `dang init` stamps it into new projects.
	CurrentVersion = Version2_1

	// DefaultVersion is the version of a dang.toml that does not declare
	// one, i.e. one written before the `dang` key existed. It is the version
	// Dang implemented when the key was introduced, so such a dang.toml
	// keeps meaning what it meant then. It is pinned and must NOT advance:
	// such a dang.toml would otherwise silently change meaning. `dang init`
	// and `dang migrate` stamp it explicitly.
	DefaultVersion = Version2_1
)

// String returns the version as written in dang.toml, e.g. "2.1".
func (v Version) String() string {
	v = v.orCurrent()
	return fmt.Sprintf("%d.%d", v.Major, v.Minor)
}

// IsZero reports whether v is unset.
func (v Version) IsZero() bool {
	return v == Version{}
}

func (v Version) orCurrent() Version {
	if v.IsZero() {
		return CurrentVersion
	}
	return v
}

// Before reports whether v is older than other.
func (v Version) Before(other Version) bool {
	v, other = v.orCurrent(), other.orCurrent()
	if v.Major != other.Major {
		return v.Major < other.Major
	}
	return v.Minor < other.Minor
}

var versionPattern = regexp.MustCompile(`^(\d+)\.(\d+)$`)

// ParseVersion parses and validates a version as written in dang.toml. It
// must be a major.minor between Version1_0 and CurrentVersion: a newer one
// needs a newer Dang.
func ParseVersion(s string) (Version, error) {
	m := versionPattern.FindStringSubmatch(s)
	if m == nil {
		return Version{}, fmt.Errorf("invalid Dang version %q: want major.minor, e.g. %q", s, CurrentVersion.String())
	}
	major, err := strconv.Atoi(m[1])
	if err != nil {
		return Version{}, fmt.Errorf("invalid Dang version %q: %w", s, err)
	}
	minor, err := strconv.Atoi(m[2])
	if err != nil {
		return Version{}, fmt.Errorf("invalid Dang version %q: %w", s, err)
	}
	v := Version{major, minor}
	// Compare fields directly: Before treats the zero Version ("0.0") as
	// CurrentVersion.
	if major < Version1_0.Major {
		return Version{}, fmt.Errorf("unsupported Dang version %q: the oldest supported version is %s", s, Version1_0)
	}
	if CurrentVersion.Before(v) {
		return Version{}, fmt.Errorf("this project needs Dang %s, but this is Dang %s; upgrade Dang", v, CurrentVersion)
	}
	return v, nil
}

// UnmarshalTOML decodes and validates the `dang` key of dang.toml. It
// rejects non-strings explicitly so that `dang = 2.1` gets a pointed error
// instead of a generic type mismatch.
func (v *Version) UnmarshalTOML(data any) error {
	s, ok := data.(string)
	if !ok {
		return fmt.Errorf("the Dang version must be a string, e.g. dang = %q", CurrentVersion.String())
	}
	parsed, err := ParseVersion(s)
	if err != nil {
		return err
	}
	*v = parsed
	return nil
}

// singleBraceSelection reports whether `recv.{ ... }` is object selection
// (and dot-block application is unavailable), as before 2.0.
func (v Version) singleBraceSelection() bool {
	return v.Before(Version2_0)
}

// singleBraceFragments reports whether inline fragment bodies use single
// braces (`... on User { name }`), as before 2.1 (where they are
// `... on User {{ name }}`, like the selection around them).
func (v Version) singleBraceFragments() bool {
	return v.Before(Version2_1)
}

// forLoops reports whether `for (cond) { ... }` and `for { ... }` are loops,
// as before 2.1 removed them (where `for` is an ordinary identifier). They
// parse as the equivalent `loop` call; see desugarForLoop.
func (v Version) forLoops() bool {
	return v.Before(Version2_1)
}

// unmatchedCaseRaises reports whether a case that matches no clause raises,
// as before 2.1 (where it yields null, making the case's type nullable
// unless its clauses are exhaustive). See versionCaseClauses.
func (v Version) unmatchedCaseRaises() bool {
	return v.Before(Version2_1)
}

// deadClausesAfterElse reports whether case clauses may follow the else
// clause, as before 2.1 (where they are an unreachable-clause error, since
// 2.1.x). Clauses match in order, so they never ran; see versionCaseClauses.
func (v Version) deadClausesAfterElse() bool {
	return v.Before(Version2_1)
}

// commaFormSep reports whether a comma separates forms in blocks, block
// arguments and the file top level, as before 2.1 (where only `;` or a
// newline does).
func (v Version) commaFormSep() bool {
	return v.Before(Version2_1)
}

// selectionBraces returns the delimiters for object selections in this
// version.
func (v Version) selectionBraces() (openBrace, closeBrace string) {
	if v.singleBraceSelection() {
		return "{", "}"
	}
	return "{{", "}}"
}

// fragmentBraces returns the delimiters for inline fragment bodies in this
// version.
func (v Version) fragmentBraces() (openBrace, closeBrace string) {
	if v.singleBraceFragments() {
		return "{", "}"
	}
	return "{{", "}}"
}

const versionStoreKey = "dangVersion"

// WithVersion is a parser option selecting the Dang version to parse source
// as. Parsing without it uses CurrentVersion.
func WithVersion(v Version) Option {
	return GlobalStore(versionStoreKey, v)
}

// versionFromStore reads the version from the parser's global store.
func versionFromStore(store map[string]any) Version {
	if v, ok := store[versionStoreKey].(Version); ok {
		return v.orCurrent()
	}
	return CurrentVersion
}

type versionKey struct{}

// ContextWithVersion sets the version of the source evaluated directly with
// ctx (the directory or file passed to RunDir, DeclareDir or RunFile, or
// source with no path at all, such as REPL input or a completion fragment)
// when no dang.toml governs it, in place of CurrentVersion. Embedders use it
// for source whose version they know some other way, e.g. a Dagger SDK
// pinning a module's version from its engine version.
//
// It never overrides a dang.toml (see VersionForDir), and it does not reach
// the modules that source imports: an imported module's version depends only
// on its own path, so an ungoverned import is CurrentVersion.
func ContextWithVersion(ctx context.Context, v Version) context.Context {
	return context.WithValue(ctx, versionKey{}, v)
}

// withoutContextVersion clears ContextWithVersion, for crossing into an
// imported module.
func withoutContextVersion(ctx context.Context) context.Context {
	return ContextWithVersion(ctx, Version{})
}

// VersionFromContext returns the version of source that no dang.toml
// governs: the version set by ContextWithVersion, or CurrentVersion.
func VersionFromContext(ctx context.Context) Version {
	if v, ok := ctx.Value(versionKey{}).(Version); ok {
		return v.orCurrent()
	}
	return CurrentVersion
}

// VersionForDir returns the version of the source files in dir. It depends
// only on where the source lives: the nearest dang.toml walking up from dir
// decides (DefaultVersion if it declares none), regardless of who imports the
// source or which dang.toml configured the evaluation. If discovery reaches a
// repository boundary (.git) or the filesystem root first, no dang.toml
// governs the source, and it is VersionFromContext(ctx).
func VersionForDir(ctx context.Context, dir string) (Version, error) {
	_, cfg, err := FindProjectConfig(dir)
	if err != nil {
		return VersionFromContext(ctx), err
	}
	return VersionForConfig(ctx, cfg), nil
}

// VersionForConfig returns the version of source governed by cfg, the result
// of FindProjectConfig for the source's directory (nil if none was found).
// Callers that already looked up the config use it to avoid a second lookup.
func VersionForConfig(ctx context.Context, cfg *ProjectConfig) Version {
	if cfg == nil {
		return VersionFromContext(ctx)
	}
	return cfg.EffectiveVersion()
}

// unmatchedCaseError is the message raised before 2.1 when no case clause
// matched. Those versions went on to print the operand's value, which the
// desugared clause can't refer to without evaluating the operand a second
// time.
const unmatchedCaseError = "no case clause matched"

// versionCaseClauses builds the current equivalent of a case's clauses as
// written in version v. Where clauses may follow the else clause (see
// deadClausesAfterElse), they are dropped, since they could never match.
// Where an unmatched case raises (see unmatchedCaseRaises), a case without an
// else clause gets
//
//	else => raise "no case clause matched"
//
// so that it raises the same way and its type stays non-null. It takes the
// last clause's location, so the formatter places comments around it exactly
// as around the last clause.
func versionCaseClauses(v Version, clauses []*CaseClause, loc *SourceLocation) []*CaseClause {
	for i, clause := range clauses {
		if clause.IsElse {
			if v.deadClausesAfterElse() {
				return clauses[:i+1]
			}
			return clauses
		}
	}
	if !v.unmatchedCaseRaises() || len(clauses) == 0 {
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

// desugarForLoop builds the current equivalent of the `for` loops of Dang
// before 2.1: `for { body }` is `loop { body }`, and `for (cond) { body }` is
//
//	loop { if (cond) { body } else { break } }
//
// Keeping the body in the then-branch keeps the condition narrowing it, as
// it did before.
//
// Two differences remain: a `for` yielded its last body value when it ended
// (by its condition going false, or a bare `break`), where `loop` yields
// null. Only `break value` gives both the same result. And the desugared
// call names the `loop` builtin, so a module that binds its own `loop`
// (legal before 2.1, which added the builtin) shadows it.
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
