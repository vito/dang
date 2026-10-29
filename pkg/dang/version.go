package dang

import (
	"cmp"
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
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
// Dang versions are major.minor: from now on, patch releases must not change
// the language, so a patch in `dang = "..."` is accepted and ignored. Before
// versions were declared, some did: 2.1.x patches switched inline fragment
// bodies to double braces and replaced try/catch with postfix `rescue`. So a
// version means the syntax of that minor's latest release, e.g. 2.1 is
// 2.1.4's, and code relying on an earlier patch's spelling may need fixing by
// hand.
//
// The same type holds the other versions dang.toml declares, which are
// written by the same rules (see parseVersion) and may need the patch and
// the prerelease.
//
// As a Dang version, the zero Version means CurrentVersion (String and Before
// treat it so); other versions check IsZero themselves.
type Version struct {
	Major, Minor, Patch int

	// Prerelease is the semver prerelease, without its leading "-", e.g.
	// "beta.19" for v1.0.0-beta.19. Only Dagger versions keep it: Dagger
	// serves its v1.0.0 prereleases different APIs. A Dang version never has
	// one (ParseVersion drops it).
	Prerelease string
}

var (
	// Version1_0 is the language of the github.com/vito/dang v1 line, where
	// `recv.{ a, b }` is object selection and there is no dot-block
	// application.
	Version1_0 = Version{Major: 1, Minor: 0}

	// Version2_0 changed object selection to `recv.{{ a, b }}`, so that
	// `recv.{ ... }` applies a block to the receiver.
	Version2_0 = Version{Major: 2, Minor: 0}

	// Version2_1 removed `for` loops and commas between forms, made a case
	// that matches nothing yield null instead of raising, spelled inline
	// fragment bodies with double braces, and replaced try/catch with
	// postfix `rescue`, reserving the word.
	Version2_1 = Version{Major: 2, Minor: 1}

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

// String returns the version as written in dang.toml: major.minor, e.g.
// "2.1", and the patch too if it has one, e.g. "0.19.1", or a prerelease,
// e.g. "1.0.0-beta.19".
func (v Version) String() string {
	v = v.orCurrent()
	switch {
	case v.Prerelease != "":
		return fmt.Sprintf("%d.%d.%d-%s", v.Major, v.Minor, v.Patch, v.Prerelease)
	case v.Patch != 0:
		return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
	default:
		return fmt.Sprintf("%d.%d", v.Major, v.Minor)
	}
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

// Before reports whether v is older than other, by semver precedence: a
// prerelease is older than its release, and prereleases of the same release
// compare identifier by identifier (beta.9 is older than beta.10).
func (v Version) Before(other Version) bool {
	v, other = v.orCurrent(), other.orCurrent()
	if v.Major != other.Major {
		return v.Major < other.Major
	}
	if v.Minor != other.Minor {
		return v.Minor < other.Minor
	}
	if v.Patch != other.Patch {
		return v.Patch < other.Patch
	}
	return comparePrerelease(v.Prerelease, other.Prerelease) < 0
}

// base returns v without its prerelease: the release it is a prerelease of.
func (v Version) base() Version {
	v.Prerelease = ""
	return v
}

// comparePrerelease compares semver prereleases (without the leading "-")
// by precedence, returning -1, 0 or +1. No prerelease ("") is a release,
// which follows all of its prereleases. Identifiers compare in order:
// numeric ones numerically and before alphanumeric ones, alphanumeric ones
// in ASCII order, and a prefix before a longer prerelease.
func comparePrerelease(a, b string) int {
	switch {
	case a == b:
		return 0
	case a == "":
		return 1
	case b == "":
		return -1
	}
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		if c := compareIdentifier(as[i], bs[i]); c != 0 {
			return c
		}
	}
	return cmp.Compare(len(as), len(bs))
}

func compareIdentifier(a, b string) int {
	aNum, bNum := isNumericIdentifier(a), isNumericIdentifier(b)
	switch {
	case aNum && bNum:
		// Without leading zeros (see versionPattern), a longer number is
		// larger; this also avoids overflowing an int.
		if c := cmp.Compare(len(a), len(b)); c != 0 {
			return c
		}
		return strings.Compare(a, b)
	case aNum:
		return -1
	case bNum:
		return 1
	default:
		return strings.Compare(a, b)
	}
}

func isNumericIdentifier(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

// versionPattern matches a version as written in dang.toml: an optional "v",
// major.minor with an optional patch, and optional semver prerelease and
// build suffixes, e.g. "2.1", "v0.19.0" or "v1.0.0-beta.19".
var versionPattern = regexp.MustCompile(`^v?(0|[1-9]\d*)\.(0|[1-9]\d*)(?:\.(0|[1-9]\d*))?` +
	`(?:-((?:0|[1-9]\d*|\d*[A-Za-z-][0-9A-Za-z-]*)(?:\.(?:0|[1-9]\d*|\d*[A-Za-z-][0-9A-Za-z-]*))*))?` +
	`(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$`)

// parseVersion parses a version by the rules every version in dang.toml is
// written by (see versionPattern). A missing patch is 0. The prerelease is
// kept, e.g. "beta.19" of v1.0.0-beta.19, for callers that need it to drop.
// Build metadata is dropped: it has no precedence in semver, and Dagger
// ignores it when picking an API view. what names the version in errors,
// e.g. "Dang".
func parseVersion(what, s string) (Version, error) {
	m := versionPattern.FindStringSubmatch(s)
	if m == nil {
		return Version{}, fmt.Errorf("invalid %s version %q: want major.minor or major.minor.patch, optionally with a leading v", what, s)
	}
	var v Version
	for i, field := range []*int{&v.Major, &v.Minor, &v.Patch} {
		if m[i+1] == "" {
			continue
		}
		n, err := strconv.Atoi(m[i+1])
		if err != nil {
			return Version{}, fmt.Errorf("invalid %s version %q: %w", what, s, err)
		}
		*field = n
	}
	v.Prerelease = m[4]
	return v, nil
}

// ParseVersion parses and validates a Dang version as written in dang.toml
// (see parseVersion), e.g. "2.1", "v2.1" or "2.1.4". Its patch and any
// prerelease are dropped, since patch releases don't change the language.
// It must be between Version1_0 and CurrentVersion: a newer one needs a
// newer Dang.
func ParseVersion(s string) (Version, error) {
	v, err := parseVersion("Dang", s)
	if err != nil {
		return Version{}, err
	}
	v.Patch = 0
	v.Prerelease = ""
	// Compare fields directly: Before treats the zero Version ("0.0") as
	// CurrentVersion.
	if v.Major < Version1_0.Major {
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

// reservedWords maps each reserved word, a word that is not an identifier
// (see WordToken), to the version that reserved it. Before that version it is
// an ordinary identifier; from then on, an identifier spelled like it is
// escaped as `$word` (see RawWordToken), which is how `dang migrate` keeps
// such names, public ones included, when moving past that version. Where only
// a name can go, e.g. a named argument, it needs no escape (see ReservedName).
//
// To reserve a new keyword: add it here with the version that reserves it,
// match its token only where it is reserved (`FooToken <- "foo" !WordChar
// FooKeyword`, with FooKeyword a version predicate listed in tsPredicates, as
// for RescueToken), exclude it from WordToken, and list its token in
// ReservedName. A keyword that is not reserved needs none of this: ordered
// choice tries its syntax first, and it stays an identifier everywhere else,
// like `if` or `case`.
var reservedWords = map[string]Version{
	"null":   Version1_0,
	"rescue": Version2_1,
}

// reserves reports whether word is a reserved word in this version, i.e. not
// an identifier (see reservedWords).
func (v Version) reserves(word string) bool {
	since, ok := reservedWords[word]
	return ok && !v.Before(since)
}

// postfixRescue reports whether `rescue` is a keyword, giving postfix
// `expr rescue ...`, as since 2.1, which replaced `try { } catch { }` with it.
// Before 2.1, `rescue` is an ordinary identifier and try/catch is the error
// handling syntax; both spellings parse into a RescueExpr.
func (v Version) postfixRescue() bool {
	return v.reserves("rescue")
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

// versionPin is the version set by ContextWithVersion.
type versionPin struct {
	version Version
	// bound reports whether the source the pin was set for has been reached
	// (see bindContextVersion), and config is then the dang.toml without a
	// `dang` key that governs it, if any: the pin applies to everything that
	// dang.toml governs, including the modules it imports by path.
	bound  bool
	config string
}

// ContextWithVersion sets the version of the source evaluated directly with
// ctx (the directory or file passed to RunDir, DeclareDir or RunFile, or
// source with no path at all, such as REPL input or a completion fragment)
// when no dang.toml declares one, in place of CurrentVersion (no dang.toml)
// or DefaultVersion (a dang.toml without a `dang` key). Embedders use it for
// source whose version they know some other way, e.g. a Dagger SDK pinning a
// module's version from its engine version: such a module may well have a
// dang.toml for its imports from before the key existed.
//
// It never overrides a version a dang.toml declares (see VersionForDir). It
// reaches the modules that source imports only if the same dang.toml without
// a `dang` key governs them: otherwise an imported module's version depends
// only on its own path, so an ungoverned import is CurrentVersion.
func ContextWithVersion(ctx context.Context, v Version) context.Context {
	return context.WithValue(ctx, versionKey{}, versionPin{version: v})
}

// withoutContextVersion clears ContextWithVersion, for crossing into an
// imported module.
func withoutContextVersion(ctx context.Context) context.Context {
	return ContextWithVersion(ctx, Version{})
}

// bindContextVersion binds a ContextWithVersion pin to the source in dir it
// was set for, if it isn't bound yet: from then on, it carries over into an
// imported module only if the dang.toml without a `dang` key that governs dir
// also governs that module (see importContextVersion).
func bindContextVersion(ctx context.Context, dir string) context.Context {
	pin, ok := ctx.Value(versionKey{}).(versionPin)
	if !ok || pin.version.IsZero() || pin.bound {
		return ctx
	}
	pin.bound = true
	if configPath, cfg, err := FindProjectConfig(dir); err == nil && cfg != nil && cfg.Dang.IsZero() {
		pin.config = configPath
	}
	return context.WithValue(ctx, versionKey{}, pin)
}

// importContextVersion returns ctx with the ContextWithVersion pin it should
// carry into the module imported from dir: kept if the pin is bound to the
// dang.toml without a `dang` key that governs dir too, cleared otherwise.
func importContextVersion(ctx context.Context, dir string) context.Context {
	pin, ok := ctx.Value(versionKey{}).(versionPin)
	if !ok || pin.version.IsZero() {
		return ctx
	}
	if pin.bound && pin.config != "" {
		if configPath, _, err := FindProjectConfig(dir); err == nil && configPath == pin.config {
			return ctx
		}
	}
	return withoutContextVersion(ctx)
}

// contextVersion returns the version set by ContextWithVersion, if any.
func contextVersion(ctx context.Context) (Version, bool) {
	pin, ok := ctx.Value(versionKey{}).(versionPin)
	if !ok || pin.version.IsZero() {
		return Version{}, false
	}
	return pin.version, true
}

// VersionFromContext returns the version of source that no dang.toml
// governs: the version set by ContextWithVersion, or CurrentVersion.
func VersionFromContext(ctx context.Context) Version {
	if v, ok := contextVersion(ctx); ok {
		return v
	}
	return CurrentVersion
}

// VersionForDir returns the version of the source files in dir. It depends
// only on where the source lives: the nearest dang.toml walking up from dir
// decides, regardless of who imports the source or which dang.toml
// configured the evaluation. If that dang.toml declares no version, it is the
// version set by ContextWithVersion, or DefaultVersion. If discovery reaches
// a repository boundary (.git) or the filesystem root first, no dang.toml
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
	if cfg.Dang.IsZero() {
		if v, ok := contextVersion(ctx); ok {
			return v
		}
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
