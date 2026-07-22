package dangdocs

import (
	"bytes"
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/vito/booklit"
	"github.com/vito/dang/v2/pkg/dang"
	"github.com/vito/dang/v2/pkg/hm"
	"github.com/vito/dang/v2/pkg/ioctx"
)

// literateSession is one page's shared Dang environment: a scope pair that
// every \dang-literate block in the same source file evaluates into, in
// document order. It mirrors the wasm REPL's replSession
// (cmd/dang-playground), which backs the same blocks client-side.
type literateSession struct {
	typeScope  dang.TypeScope
	valueScope dang.ValueScope

	// blocks counts the session's evaluated blocks; each parses under the
	// synthetic filename blockFilename(n), and sources records every block's
	// text by that name. Error locations carry the defining block's filename,
	// so a failure in one block can quote a function raised blocks earlier.
	blocks  int
	sources map[string]string
}

// blockFilename names the nth block of a session. The wasm replay
// (cmd/dang-playground) numbers its session entries with the same scheme —
// a page replay visits the same blocks in the same order — so any location
// a message spells out matches what the build baked.
func blockFilename(n int) string {
	return fmt.Sprintf("snippet-%d", n)
}

// nextBlock assigns the next block filename and records its source.
func (s *literateSession) nextBlock(source string) string {
	s.blocks++
	name := blockFilename(s.blocks)
	if s.sources == nil {
		s.sources = map[string]string{}
	}
	s.sources[name] = source
	return name
}

// literateFencesPartial is the section partial under which \literate-fences
// records itself. Partials are booklit's "arbitrary named content" slot, the
// closest plugin-side analogue to how \split-sections sets a flag on its
// section; templates only read partials by explicit name, so the marker
// never renders.
const literateFencesPartial = "LiterateFences"

// literateSessionPartial is the section partial under which the source
// file's literate session is stowed, on the section that carries the file.
// Stowing it on the section rather than in a package-level map ties the
// session's lifetime to one load of the book: booklit's dev server
// (`build.sh -s`) re-loads the whole book on every page request,
// concurrently across requests, and Dang scopes are not safe for concurrent
// use (one request's Infer writes the scope maps another request's Eval is
// reading). Sections are rebuilt per load and a load evaluates on one
// goroutine, so sessions never cross goroutines and every rebuild replays
// the page's chain fresh.
const literateSessionPartial = "LiterateSession"

// literateSessionContent carries a session as a Content so it can live in a
// partial; like the \literate-fences marker it never renders.
type literateSessionContent struct {
	booklit.Content
	session *literateSession
}

// literateSessionFor returns the session shared by every literate block in
// the given section's source file, creating it from fresh standard-library
// scopes on first use.
func literateSessionFor(section *booklit.Section) *literateSession {
	// The session lives on the section that carries the source file: inline
	// \section blocks have no Path of their own, so walk up to the file's
	// section (mirroring FilePath) — the chain spans every heading in the
	// file.
	file := section
	for file.Path == "" && file.Parent != nil {
		file = file.Parent
	}

	if c, ok := file.Partial(literateSessionPartial).(literateSessionContent); ok {
		return c.session
	}

	typeScope, valueScope := dang.BuildScopesFromImports("", nil)
	s := &literateSession{typeScope: typeScope, valueScope: valueScope}
	file.SetPartial(literateSessionPartial, literateSessionContent{booklit.Empty, s})
	return s
}

// LiterateFences turns the ```dang fences that follow it into literate
// blocks, each rendered exactly as if it were a \dang-literate invocation.
// The switch is lexically scoped: it covers the rest of the section it's
// called in, sub-sections included (each markdown heading is its own booklit
// section, linked by Parent), and only fences evaluated after the call —
// booklit evaluates in document order. Call it right after the page title for
// a fully literate page, or under one heading to scope it to that section;
// ```dang fences elsewhere stay plain highlighted Markdown. A ```dang-static
// fence opts a single snippet back out (see CodeBlock).
//
//	\literate-fences
func (p Plugin) LiterateFences() {
	p.section.SetPartial(literateFencesPartial, booklit.Empty)
}

// literateFencesEnabled reports whether \literate-fences was called in sec or
// any of its ancestors before this point in document order. The ancestor walk
// mirrors booklit's SplitSectionsPrevented.
func literateFencesEnabled(sec *booklit.Section) bool {
	for s := sec; s != nil; s = s.Parent {
		if s.Partial(literateFencesPartial) != nil {
			return true
		}
	}
	return false
}

// DangLiterate renders a literate-programming code block. The snippet is
// evaluated at build time against a Dang environment shared by every
// literate block in the same source file — earlier blocks' definitions
// are in scope, like cells of a notebook — and its output (stdout plus the
// value of the last form) is baked into the static page. A block that fails
// to parse, type-check, or evaluate fails the docs build, so literate
// examples can't rot.
//
//	\dang-literate{{{
//	list.each { item, index => print(`${index}: ${item}`) }
//	}}}
//
// In a \literate-fences scope, plain ```dang fences render through this same
// path (see CodeBlock), so pages can stay pure Markdown.
//
// docs/js/playground.js progressively enhances these blocks into editable
// widgets with the same chain semantics: Run replays every literate block on
// the page, top to bottom, in one wasm REPL session. Without JavaScript the
// baked output still shows.
func (p Plugin) DangLiterate(code booklit.Content) (booklit.Content, error) {
	return p.literateBlock(code, `\dang-literate block`)
}

// DangLiterateFailure renders a literate block that is REQUIRED to fail.
// The snippet is evaluated like a literate block but against throwaway forks
// of the page's session — a cloned type scope and a sealed child value scope
// — so whatever it declares or half-evaluates before failing never reaches
// later blocks. The error it raises is baked into the page in place of a
// value; a snippet that succeeds fails the docs build, so expected-failure
// examples are as rot-proof as the passing ones.
//
//	\dang-literate-failure{{{
//	if (1) "yes" else "no"
//	}}}
//
// In a \literate-fences scope, ```dang-failure fences render through this
// same path (see CodeBlock).
func (p Plugin) DangLiterateFailure(code booklit.Content) (booklit.Content, error) {
	return p.literateFailureBlock(code, `\dang-literate-failure block`)
}

// stageLabels mirrors playground.js's STAGE_LABEL: the baked error header must
// read exactly like the one renderReplOutput shows after a client-side
// replay, label and all.
var stageLabels = map[string]string{
	"parse": "Parse error",
	"type":  "Type error",
	"eval":  "Runtime error",
}

// stripANSI removes ANSI SGR escape sequences from build-time captured
// output: escape codes have no business in HTML, and warnings printed
// during evaluation (WarnAtSource) color themselves for a terminal. The
// wasm module strips its captured output the same way (cmd/dang-playground)
// so a replay shows exactly what the build baked.
var ansiSGR = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripANSI(s string) string {
	return ansiSGR.ReplaceAllString(s, "")
}

// literateBlock evaluates and renders one literate snippet; label names the
// originating syntax in build errors.
func (p Plugin) literateBlock(code booklit.Content, label string) (booklit.Content, error) {
	source := strings.TrimRight(code.String(), "\n")
	sess := literateSessionFor(p.section)

	stdout, value, err := literateEval(source, sess)
	if err != nil {
		return nil, fmt.Errorf("%s in %s: %w", label, p.section.FilePath(), err)
	}

	partials := booklit.Partials{}
	if stdout != "" {
		partials["Stdout"] = booklit.String(stdout)
	}
	if value != "" {
		// Highlight the result with bare token spans, matching the client-side
		// highlighting so the baked output and its enhanced replay agree.
		partials["Value"] = highlightResult(value)
	}

	return booklit.Styled{
		Style:    "dang-literate",
		Content:  p.highlightDang(source),
		Partials: partials,
		Block:    true,
	}, nil
}

// literateFailureBlock evaluates and renders one expected-failure snippet;
// label names the originating syntax in build errors. The rendered block
// carries the error under the "Error" partial — its presence is what marks
// the block as an expected failure, both for the template and for
// playground.js's chain replay.
func (p Plugin) literateFailureBlock(code booklit.Content, label string) (booklit.Content, error) {
	source := strings.TrimRight(code.String(), "\n")
	sess := literateSessionFor(p.section)

	stdout, stage, failure, blockName := literateFailEval(source, sess)
	if failure == nil {
		return nil, fmt.Errorf("%s in %s: expected the snippet to fail, but it succeeded — use a plain ```dang fence", label, p.section.FilePath())
	}

	report := dang.ErrorReporter{
		Filename: blockName,
		Source:   source,
		Sources:  sess.sources,
	}.Report(failure)
	partials := booklit.Partials{
		"Error": renderErrorReport(report, stageLabels[stage]),
	}
	if stdout != "" {
		partials["Stdout"] = booklit.String(stdout)
	}

	return booklit.Styled{
		Style:    "dang-literate",
		Content:  p.highlightDang(source),
		Partials: partials,
		Block:    true,
	}, nil
}

// literateFailEval runs source the way literateEval does, but against
// throwaway forks of the session's scopes: a cloned type scope (declarations
// land in the discarded child layer) and a sealed child value scope (even
// reassignments of outer bindings stay local). Failing is the point, and a
// failing block's partial state is unknowable, so it contributes nothing to
// the page's chain — the same isolation dangLiterateFailEval
// (cmd/dang-playground) applies when the page is replayed client-side. It
// returns the captured output, the failing stage ("parse" | "type" |
// "eval") and error, and the block's assigned filename; a nil error means
// the snippet unexpectedly succeeded.
func literateFailEval(source string, sess *literateSession) (string, string, error, string) {
	// The block still takes its place in the session's numbering — the wasm
	// replay counts every block of the chain the same way.
	name := sess.nextBlock(source)

	parsed, err := dang.ParseWithRecovery(name, []byte(source), dang.GlobalStore("filePath", name))
	if err != nil {
		return "", "parse", err, name
	}
	file, ok := parsed.(*dang.FileBlock)
	if !ok {
		return "", "parse", fmt.Errorf("unexpected parse result"), name
	}
	forms := file.Forms

	typeScope := sess.typeScope.Clone()
	valueScope := sess.valueScope.Derive(true)

	fresh := hm.NewSimpleFresher()
	if _, err := dang.InferFormsWithPhases(context.Background(), forms, typeScope, fresh); err != nil {
		return "", "type", err, name
	}

	var out bytes.Buffer
	ctx := ioctx.StdoutToContext(context.Background(), &out)
	ctx = ioctx.StderrToContext(ctx, &out)
	// Runtime faults that aren't raises only carry a location when an
	// EvalContext supplies one, the same wiring RunFile does.
	ctx = dang.WithEvalContext(ctx, dang.NewEvalContext(name, source))

	for _, node := range forms {
		if _, err := dang.EvalNode(ctx, valueScope, node); err != nil {
			return strings.TrimRight(stripANSI(out.String()), "\n"), "eval", err, name
		}
	}
	return strings.TrimRight(stripANSI(out.String()), "\n"), "", nil, name
}

// literateEval parses, type-checks, and evaluates source against the
// session's scopes, mutating them in place so definitions accumulate across
// blocks. It returns captured stdout/stderr and the stringified value of the
// last form. A block whose last form is a declaration yields no value — a
// setup block bakes only what it prints, not the noise of the bound value.
// The same display rule lives in dangLiterateEval (cmd/dang-playground); the
// two must stay in lockstep so enhancing a page client-side doesn't change
// what the reader already saw.
func literateEval(source string, sess *literateSession) (string, string, error) {
	return literateEvalCtx(context.Background(), source, sess)
}

// literateEvalCtx is literateEval with an explicit base context, so callers can
// thread GraphQL import configs (dang.ContextWithImportConfigs) through both
// inference and evaluation — e.g. carousel feature slides that `import` a
// bundled in-process schema. The base context backs both phases so the import's
// schema-module identity is shared between them.
func literateEvalCtx(base context.Context, source string, sess *literateSession) (string, string, error) {
	name := sess.nextBlock(source)

	parsed, err := dang.ParseWithRecovery(name, []byte(source), dang.GlobalStore("filePath", name))
	if err != nil {
		return "", "", err
	}
	file, ok := parsed.(*dang.FileBlock)
	if !ok {
		return "", "", fmt.Errorf("unexpected parse result")
	}
	forms := file.Forms

	fresh := hm.NewSimpleFresher()
	if _, err := dang.InferFormsWithPhases(base, forms, sess.typeScope, fresh); err != nil {
		return "", "", err
	}

	var out bytes.Buffer
	ctx := ioctx.StdoutToContext(base, &out)
	ctx = ioctx.StderrToContext(ctx, &out)
	ctx = dang.WithEvalContext(ctx, dang.NewEvalContext(name, source))

	var last dang.Node
	var lastVal dang.Value
	for _, node := range forms {
		val, err := dang.EvalNode(ctx, sess.valueScope, node)
		if err != nil {
			return "", "", err
		}
		last = node
		lastVal = val
	}

	value := ""
	if lastVal != nil && (last == nil || len(last.DeclaredSymbols()) == 0) {
		value = dang.Repr(lastVal)
	}
	return strings.TrimRight(stripANSI(out.String()), "\n"), value, nil
}
