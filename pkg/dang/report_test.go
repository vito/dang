package dang

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/vito/dang/v2/pkg/hm"
	"github.com/vito/dang/v2/pkg/ioctx"
)

// snippetFilename is a synthetic filename like the docs build's "literate"
// and the playground's "playground": nothing on disk answers to it, so
// snippets must resolve from the source handed to NewErrorReport.
const snippetFilename = "snippet"

// snippetError parses, infers, and evaluates source the way the docs
// literate build and the wasm playground do, returning the error (which the
// test requires) and the stage it came from.
func snippetError(t *testing.T, source string) error {
	t.Helper()

	parsed, err := Parse(snippetFilename, []byte(source))
	if err != nil {
		return err
	}
	file, ok := parsed.(*FileBlock)
	if !ok {
		t.Fatalf("unexpected parse result %T", parsed)
	}

	typeScope, valueScope := BuildScopesFromImports("", nil)
	fresh := hm.NewSimpleFresher()
	if _, err := InferFormsWithPhases(context.Background(), file.Forms, typeScope, fresh); err != nil {
		return err
	}

	var out bytes.Buffer
	ctx := ioctx.StdoutToContext(context.Background(), &out)
	ctx = ioctx.StderrToContext(ctx, &out)
	ctx = WithEvalContext(ctx, NewEvalContext(snippetFilename, source))

	for _, node := range file.Forms {
		if _, err := EvalNode(ctx, valueScope, node); err != nil {
			return err
		}
	}
	t.Fatalf("expected source to fail, but it succeeded:\n%s", source)
	return nil
}

// A type error's location must resolve against the provided source even
// though its synthetic filename can't be read from disk (the trap
// ConvertInferError falls into for the docs build).
func TestErrorReportTypeError(t *testing.T) {
	source := "let one = 1\nlet x = undefinedName"
	err := snippetError(t, source)

	rep := NewErrorReport(err, snippetFilename, source)
	if len(rep.Sections) != 1 {
		t.Fatalf("got %d sections, want 1: %+v", len(rep.Sections), rep.Sections)
	}
	sec := rep.Sections[0]
	if sec.Role != ReportPrimary {
		t.Errorf("role = %q, want %q", sec.Role, ReportPrimary)
	}
	if !strings.Contains(sec.Message, "undefinedName") {
		t.Errorf("message %q does not name the missing symbol", sec.Message)
	}
	if strings.Contains(sec.Message, "failed to read file") {
		t.Errorf("message %q leaks the unreadable synthetic filename", sec.Message)
	}
	if sec.Location == nil || sec.Location.Line != 2 {
		t.Fatalf("location = %+v, want line 2", sec.Location)
	}
	if sec.Snippet == nil {
		t.Fatal("no snippet despite the source being provided")
	}
	if got := sec.Snippet.Lines[sec.Location.Line-sec.Snippet.StartLine]; got != "let x = undefinedName" {
		t.Errorf("snippet error line = %q", got)
	}
}

// An uncaught raise reports like the CLI boundary: "uncaught Type: message",
// public data fields, the raise site, and the recorded cause chain.
func TestErrorReportUncaughtWithCause(t *testing.T) {
	source := strings.Join([]string{
		`type DeployError implements Error {`,
		`  message: String!`,
		`  stage: String!`,
		`}`,
		``,
		`push: String! { raise "connection refused" }`,
		``,
		`push rescue {`,
		`  err: Error => raise DeployError(message: "deploy failed", stage: "push")`,
		`}`,
	}, "\n")
	err := snippetError(t, source)

	rep := NewErrorReport(err, snippetFilename, source)
	if len(rep.Sections) != 2 {
		t.Fatalf("got %d sections, want primary + cause: %+v", len(rep.Sections), rep.Sections)
	}

	primary := rep.Sections[0]
	if primary.Message != "uncaught DeployError: deploy failed" {
		t.Errorf("primary message = %q", primary.Message)
	}
	if len(primary.Fields) != 1 || primary.Fields[0].Name != "stage" || primary.Fields[0].Value != `"push"` {
		t.Errorf("primary fields = %+v, want stage: \"push\"", primary.Fields)
	}
	if primary.Location == nil || primary.Location.Line != 9 {
		t.Errorf("primary location = %+v, want the raise on line 9", primary.Location)
	}
	if primary.Snippet == nil {
		t.Error("primary section has no snippet")
	}

	cause := rep.Sections[1]
	if cause.Role != ReportCause {
		t.Errorf("second section role = %q, want %q", cause.Role, ReportCause)
	}
	if cause.Message != "error: connection refused" {
		t.Errorf("cause message = %q", cause.Message)
	}
	if cause.Location == nil || cause.Location.Line != 6 {
		t.Errorf("cause location = %+v, want the inner raise on line 6", cause.Location)
	}
	if cause.Snippet == nil {
		t.Error("cause section has no snippet")
	}
}

// Concurrent sibling failures from a `{{ }}` become "also failed:" sections.
func TestErrorReportParallelSiblings(t *testing.T) {
	source := `{{ a: raise "first", b: raise "second" }}`
	err := snippetError(t, source)

	rep := NewErrorReport(err, snippetFilename, source)
	if len(rep.Sections) != 2 {
		t.Fatalf("got %d sections, want primary + sibling: %+v", len(rep.Sections), rep.Sections)
	}
	if rep.Sections[0].Message != "uncaught error: first" {
		t.Errorf("primary message = %q", rep.Sections[0].Message)
	}
	sibling := rep.Sections[1]
	if sibling.Role != ReportSibling {
		t.Errorf("second section role = %q, want %q", sibling.Role, ReportSibling)
	}
	if sibling.Message != "error: second" {
		t.Errorf("sibling message = %q", sibling.Message)
	}
	if sibling.Location == nil || sibling.Snippet == nil {
		t.Errorf("sibling not annotated: location = %+v, snippet = %+v", sibling.Location, sibling.Snippet)
	}
}

// The snippet window matches formatSourceAnnotation's: ±2 lines, clamped to
// the file, nil when the source can't be resolved or the line is out of it.
func TestErrorReportSnippetWindow(t *testing.T) {
	source := "l1\nl2\nl3\nl4\nl5\nl6"
	mk := func(line int) *SourceError {
		return NewSourceError(errors.New("boom"), &SourceLocation{
			Filename: snippetFilename, Line: line, Column: 1, Length: 2,
		}, "")
	}

	middle := NewErrorReport(mk(3), snippetFilename, source).Sections[0].Snippet
	if middle == nil || middle.StartLine != 1 || len(middle.Lines) != 5 {
		t.Errorf("line 3 window = %+v, want lines 1-5", middle)
	}

	top := NewErrorReport(mk(1), snippetFilename, source).Sections[0].Snippet
	if top == nil || top.StartLine != 1 || len(top.Lines) != 3 {
		t.Errorf("line 1 window = %+v, want lines 1-3", top)
	}

	bottom := NewErrorReport(mk(6), snippetFilename, source).Sections[0].Snippet
	if bottom == nil || bottom.StartLine != 4 || len(bottom.Lines) != 3 {
		t.Errorf("line 6 window = %+v, want lines 4-6", bottom)
	}

	if out := NewErrorReport(mk(99), snippetFilename, source).Sections[0].Snippet; out != nil {
		t.Errorf("out-of-range line got a snippet: %+v", out)
	}

	if noSrc := NewErrorReport(mk(3), snippetFilename, "").Sections[0].Snippet; noSrc != nil {
		t.Errorf("unresolvable source got a snippet: %+v", noSrc)
	}
}

// A SourceError that recorded its own source (e.g. a parse error) uses it
// even when the report is built with different unit source.
func TestErrorReportSourceErrorOwnSource(t *testing.T) {
	recorded := "recorded line"
	srcErr := NewSourceError(errors.New("syntax error: nope"), &SourceLocation{
		Filename: "elsewhere.dang", Line: 1, Column: 1, Length: 4,
	}, recorded)

	sec := NewErrorReport(srcErr, snippetFilename, "other text").Sections[0]
	if sec.Snippet == nil || sec.Snippet.Lines[0] != recorded {
		t.Errorf("snippet = %+v, want the error's own recorded source", sec.Snippet)
	}
}

// A location pointing into an earlier unit resolves through the reporter's
// Sources map — the docs literate chain and REPL sessions record every
// block's source by its synthetic filename.
func TestErrorReporterCrossUnitSources(t *testing.T) {
	first := "line one\nraise site\nline three"
	rep := ErrorReporter{
		Filename: "snippet-2",
		Source:   "call it",
		Sources:  map[string]string{"snippet-1": first, "snippet-2": "call it"},
	}

	err := NewSourceError(errors.New("boom"), &SourceLocation{
		Filename: "snippet-1", Line: 2, Column: 1, Length: 5,
	}, "")
	sec := rep.Report(err).Sections[0]
	if sec.Snippet == nil {
		t.Fatal("cross-unit location did not resolve through Sources")
	}
	if got := sec.Snippet.Lines[2-sec.Snippet.StartLine]; got != "raise site" {
		t.Errorf("quoted %q from the wrong unit", got)
	}
}

// Multiple inference errors become one section apiece, in order.
func TestErrorReportMultipleInferenceErrors(t *testing.T) {
	source := "let x = undefinedA\nlet y = undefinedB"
	err := snippetError(t, source)

	var inferErrs *InferenceErrors
	if !errors.As(err, &inferErrs) || len(inferErrs.Errors) < 2 {
		t.Skipf("expected multiple inference errors, got %v", err)
	}

	rep := NewErrorReport(err, snippetFilename, source)
	if len(rep.Sections) != len(inferErrs.Errors) {
		t.Fatalf("got %d sections for %d errors", len(rep.Sections), len(inferErrs.Errors))
	}
	for i, sec := range rep.Sections {
		if sec.Role != ReportPrimary {
			t.Errorf("section %d role = %q, want %q", i, sec.Role, ReportPrimary)
		}
		if sec.Snippet == nil {
			t.Errorf("section %d (%s) has no snippet", i, sec.Message)
		}
	}
	if !strings.Contains(rep.Sections[0].Message, "undefinedA") || !strings.Contains(rep.Sections[1].Message, "undefinedB") {
		t.Errorf("sections out of order or mislabeled: %q, %q",
			rep.Sections[0].Message, rep.Sections[1].Message)
	}
}

// Interface-implementation checks nest an InferenceErrors group per type;
// the report must flatten them so every missing member gets a section, the
// way the terminal's "N inference errors" listing shows them all.
func TestErrorReportNestedInferenceErrors(t *testing.T) {
	source := strings.Join([]string{
		`interface Contact {`,
		`  email: String!`,
		`  phone: String!`,
		`  name: String!`,
		`}`,
		``,
		`type Person implements Contact {`,
		`  name: String!`,
		`}`,
	}, "\n")
	err := snippetError(t, source)

	rep := NewErrorReport(err, snippetFilename, source)
	if len(rep.Sections) != 2 {
		t.Fatalf("got %d sections, want one per missing member: %+v", len(rep.Sections), rep.Sections)
	}
	var got []string
	for _, sec := range rep.Sections {
		got = append(got, sec.Message)
	}
	joined := strings.Join(got, "\n")
	if !strings.Contains(joined, "email") || !strings.Contains(joined, "phone") {
		t.Errorf("sections missing a member: %q", got)
	}
}

// An assertion failure carries its location structurally, without the
// "Location:" suffix AssertionError.Error() appends for terminals.
func TestErrorReportAssertion(t *testing.T) {
	source := "assert { 1 == 2 }"
	err := snippetError(t, source)

	sec := NewErrorReport(err, snippetFilename, source).Sections[0]
	if strings.Contains(sec.Message, "Location:") {
		t.Errorf("message %q includes the terminal-only Location suffix", sec.Message)
	}
	if sec.Location == nil {
		t.Fatal("assertion location lost")
	}
	if sec.Snippet == nil {
		t.Error("assertion has no snippet")
	}
}

// Errors with no location still produce a section, so every failure renders.
func TestErrorReportPlainError(t *testing.T) {
	rep := NewErrorReport(fmt.Errorf("just a message"), snippetFilename, "src")
	if len(rep.Sections) != 1 {
		t.Fatalf("got %d sections, want 1", len(rep.Sections))
	}
	sec := rep.Sections[0]
	if sec.Message != "just a message" {
		t.Errorf("message = %q", sec.Message)
	}
	if sec.Location != nil || sec.Snippet != nil {
		t.Errorf("locationless error got annotated: %+v", sec)
	}
}
