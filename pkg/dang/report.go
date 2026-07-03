package dang

import (
	"errors"
	"os"
	"strings"
)

// ErrorReport is a structured, renderer-neutral description of an error as
// the boundary printer presents it: one section per annotated site — the
// primary error first, then any cause-chain links and concurrent sibling
// failures. Non-terminal frontends (the docs build, the wasm playground)
// render it as HTML/DOM instead of ANSI; the terminal renderers in
// errors.go and uncaught.go remain the source of truth for wording, and
// this extraction mirrors them.
type ErrorReport struct {
	Sections []ErrorReportSection
}

// Section roles, named after the labels the terminal boundary printer uses.
const (
	ReportPrimary = "error"
	ReportCause   = "cause"   // rendered as "caused by:"
	ReportSibling = "sibling" // rendered as "also failed:"
)

// ErrorReportSection is one annotated site of a report. Location may be
// non-nil while Snippet is nil when the source text couldn't be resolved
// (renderers degrade to a bare location arrow, like annotate in
// uncaught.go); both are nil for sites with no recorded location, such as
// causes taken from an explicit `cause` field.
type ErrorReportSection struct {
	Role     string
	Message  string
	Fields   []ErrorReportField
	Location *SourceLocation
	Snippet  *ErrorSnippet
}

// ErrorReportField is one public stored data field of an uncaught error
// value, with its Repr'd value.
type ErrorReportField struct {
	Name  string
	Value string
}

// ErrorSnippet quotes the source around a section's location: the same ±2
// context-line window formatSourceAnnotation shows (the two must stay in
// lockstep), split into lines for renderers that draw their own gutter and
// underline.
type ErrorSnippet struct {
	StartLine int // 1-based line number of Lines[0]
	Lines     []string
}

// ErrorReporter extracts structured reports for a frontend that evaluates
// separately-parsed units against accumulated state — REPL entries, the
// docs build's literate blocks, the playground's chain replay. Filename and
// Source describe the unit that was just processed; Sources carries earlier
// units by their synthetic filenames, so a failure whose location points
// into an earlier unit (a raise inside a function defined blocks ago) still
// quotes the right source. Locations in files known to neither resolve
// from disk, mirroring uncaughtErrorReport.sourceFor.
type ErrorReporter struct {
	Filename string
	Source   string
	Sources  map[string]string
}

// NewErrorReport extracts a structured report from any error produced by
// parsing, inference, or evaluation of a single self-contained unit —
// ErrorReporter without the cross-unit sources.
func NewErrorReport(err error, filename, source string) *ErrorReport {
	return ErrorReporter{Filename: filename, Source: source}.Report(err)
}

// Report extracts the structured report for an error the reporter's unit
// just produced.
func (r ErrorReporter) Report(err error) *ErrorReport {
	rep := &ErrorReport{}

	// Multiple type errors: one primary section each, matching the terminal's
	// "N inference errors" listing. Groups nest — interface-implementation
	// checks collect one inner group per type — so flatten recursively, or a
	// nested group would surface only its first member.
	var inferErrs *InferenceErrors
	if errors.As(err, &inferErrs) && len(inferErrs.Errors) > 0 {
		var flatten func(errs []error)
		flatten = func(errs []error) {
			for _, e := range errs {
				var nested *InferenceErrors
				if errors.As(e, &nested) && len(nested.Errors) > 0 {
					flatten(nested.Errors)
					continue
				}
				rep.Sections = append(rep.Sections, r.primarySection(e))
			}
		}
		flatten(inferErrs.Errors)
		return rep
	}

	rep.Sections = append(rep.Sections, r.primarySection(err))

	// Cause chain and concurrent siblings, in the order the boundary
	// printer shows them (uncaughtErrorReport.Error).
	var raised *RaisedError
	if errors.As(err, &raised) {
		for _, link := range causeChain(raised) {
			rep.Sections = append(rep.Sections, ErrorReportSection{
				Role:     ReportCause,
				Message:  errorSummary(link.Value),
				Fields:   errorFields(link.Value),
				Location: link.Location,
				Snippet:  r.snippetFor(link.Location, ""),
			})
		}
	}
	var parallel *parallelFailure
	if errors.As(err, &parallel) {
		for _, sibling := range parallel.Also {
			loc, siteSource := errorLocation(sibling)
			rep.Sections = append(rep.Sections, ErrorReportSection{
				Role:     ReportSibling,
				Message:  siblingSummary(sibling),
				Location: loc,
				Snippet:  r.snippetFor(loc, siteSource),
			})
		}
	}

	return rep
}

// primarySection describes the error itself: the bare message the docs and
// playground have always shown, plus the location and snippet the terminal
// annotates it with. Raised errors get the boundary printer's "uncaught
// TypeName: message" summary and their public data fields.
func (r ErrorReporter) primarySection(err error) ErrorReportSection {
	sec := ErrorReportSection{Role: ReportPrimary}

	var raised *RaisedError
	var assertion *AssertionError
	var sourceErr *SourceError
	var inferErr *InferError
	switch {
	case errors.As(err, &raised):
		sec.Message = "uncaught " + errorSummary(raised.Value)
		sec.Fields = errorFields(raised.Value)
		sec.Location = raised.Location
	case errors.As(err, &assertion):
		// AssertionError.Error() appends a "Location:" line; the location is
		// carried structurally here instead.
		sec.Message = assertion.Message
		sec.Location = assertion.Location
	case errors.As(err, &sourceErr):
		sec.Message = sourceErr.Inner.Error()
		sec.Location = sourceErr.Location
		sec.Snippet = r.snippetFor(sec.Location, sourceErr.Source)
		return sec
	case errors.As(err, &inferErr):
		// An InferError that never became a SourceError — ConvertInferError
		// couldn't read its (synthetic) filename. The caller-provided source
		// stands in below.
		sec.Message = inferErr.Inner.Error()
		sec.Location = inferErr.Location
	default:
		sec.Message = err.Error()
		sec.Location, _ = boundaryLocation(err)
	}

	sec.Snippet = r.snippetFor(sec.Location, "")
	return sec
}

// boundaryLocation recovers a location from errors that carry one outside
// the SourceError convention: control-flow sentinels escaping the program
// (mirroring translateBoundaryEvalError).
func boundaryLocation(err error) (*SourceLocation, bool) {
	var returned *ReturnException
	if errors.As(err, &returned) {
		return returned.Location, true
	}
	var broken *BreakException
	if errors.As(err, &broken) {
		return broken.Location, true
	}
	var continued *ContinueException
	if errors.As(err, &continued) {
		return continued.Location, true
	}
	return nil, false
}

// snippetFor resolves the quoted source window for a location.
// siteSource, when non-empty, is source text recorded alongside the
// location (e.g. by a sibling's SourceError) and wins; otherwise a
// location in the current unit — matching filename, or none recorded at
// all, as with locations from unnamed parses — uses the unit's source, an
// earlier unit resolves through Sources, and anything else falls back to
// the file on disk (a no-op in the browser). Returns nil when the source
// can't be resolved or the line is out of range, so renderers degrade the
// way annotate does.
func (r ErrorReporter) snippetFor(loc *SourceLocation, siteSource string) *ErrorSnippet {
	if loc == nil {
		return nil
	}

	src := siteSource
	if src == "" {
		if loc.Filename == "" || loc.Filename == r.Filename {
			src = r.Source
		} else if earlier, ok := r.Sources[loc.Filename]; ok {
			src = earlier
		} else if contents, err := os.ReadFile(loc.Filename); err == nil {
			src = string(contents)
		}
	}
	if src == "" {
		return nil
	}

	lines := strings.Split(src, "\n")
	if loc.Line < 1 || loc.Line > len(lines) {
		return nil
	}

	// The same ±2 window as formatSourceAnnotation.
	start := max(1, loc.Line-2)
	end := min(len(lines), loc.Line+2)
	return &ErrorSnippet{
		StartLine: start,
		Lines:     lines[start-1 : end],
	}
}
