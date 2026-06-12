package dangdocs

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"sync"

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
}

var (
	literateMu       sync.Mutex
	literateSessions = map[string]*literateSession{}
)

// literateFencesPartial is the section partial under which \literate-fences
// records itself. Partials are booklit's "arbitrary named content" slot, the
// closest plugin-side analogue to how \split-sections sets a flag on its
// section; templates only read partials by explicit name, so the marker
// never renders.
const literateFencesPartial = "LiterateFences"

// literateSessionFor returns the shared session for the given source file,
// creating it from fresh standard-library scopes on first use.
func literateSessionFor(path string) *literateSession {
	literateMu.Lock()
	defer literateMu.Unlock()
	s := literateSessions[path]
	if s == nil {
		typeScope, valueScope := dang.BuildScopesFromImports("", nil)
		s = &literateSession{typeScope: typeScope, valueScope: valueScope}
		literateSessions[path] = s
	}
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

// literateBlock evaluates and renders one literate snippet; label names the
// originating syntax in build errors.
func (p Plugin) literateBlock(code booklit.Content, label string) (booklit.Content, error) {
	source := strings.TrimRight(code.String(), "\n")
	sess := literateSessionFor(p.section.FilePath())

	stdout, value, err := literateEval(source, sess)
	if err != nil {
		return nil, fmt.Errorf("%s in %s: %w", label, p.section.FilePath(), err)
	}

	partials := booklit.Partials{}
	if stdout != "" {
		partials["Stdout"] = booklit.String(stdout)
	}
	if value != "" {
		partials["Value"] = booklit.String(value)
	}

	return booklit.Styled{
		Style:    "dang-literate",
		Content:  p.highlightDang(source),
		Partials: partials,
		Block:    true,
	}, nil
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
	parsed, err := dang.ParseWithRecovery("literate", []byte(source))
	if err != nil {
		return "", "", err
	}
	file, ok := parsed.(*dang.FileBlock)
	if !ok {
		return "", "", fmt.Errorf("unexpected parse result")
	}
	forms := file.Forms

	fresh := hm.NewSimpleFresher()
	if _, err := dang.InferFormsWithPhases(context.Background(), forms, sess.typeScope, fresh); err != nil {
		return "", "", err
	}

	var out bytes.Buffer
	ctx := ioctx.StdoutToContext(context.Background(), &out)
	ctx = ioctx.StderrToContext(ctx, &out)

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
		value = lastVal.String()
	}
	return strings.TrimRight(out.String(), "\n"), value, nil
}
