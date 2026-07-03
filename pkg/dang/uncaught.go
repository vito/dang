package dang

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// uncaughtErrorReport renders an uncaught raised error at the program
// boundary: the raise site with source highlighting, the error's public
// data fields, the cause chain recorded during rescue arms (or carried on
// explicit `cause` fields), and any concurrent sibling failures from a
// `{{ }}`. Everything must render through Error(): the CLI prints the
// returned error verbatim and exits 1.
type uncaughtErrorReport struct {
	Raised *RaisedError
	Source string
	Also   []error
}

func (u *uncaughtErrorReport) Unwrap() error {
	return u.Raised
}

const (
	ansiBold  = "\033[1m"
	ansiDim   = "\033[2m"
	ansiRed   = "\033[31m"
	ansiBlue  = "\033[34m"
	ansiReset = "\033[0m"
)

func (u *uncaughtErrorReport) Error() string {
	var out strings.Builder

	u.annotate(&out, "Error:", fmt.Sprintf("uncaught %s", errorSummary(u.Raised.Value)), u.Raised.Location, "")
	writeErrorFields(&out, u.Raised.Value)

	for _, link := range causeChain(u.Raised) {
		u.annotate(&out, "caused by:", errorSummary(link.Value), link.Location, "")
		writeErrorFields(&out, link.Value)
	}

	for _, sibling := range u.Also {
		loc, source := errorLocation(sibling)
		u.annotate(&out, "also failed:", siblingSummary(sibling), loc, source)
	}

	// Trailing newline matches the SourceError rendering convention.
	return strings.TrimSuffix(out.String(), "\n") + "\n"
}

// annotate writes a labeled section with the same highlighted source
// snippet treatment as the primary error, degrading to a bare location
// arrow when the source can't be shown, and to just the label line when
// there is no location at all (e.g. a cause taken from an explicit field).
func (u *uncaughtErrorReport) annotate(out *strings.Builder, label, message string, loc *SourceLocation, source string) {
	if loc != nil {
		if source == "" {
			source = u.sourceFor(loc)
		}
		if annotated, ok := formatSourceAnnotation(loc, source, label, ansiRed, message); ok {
			out.WriteString(annotated)
			return
		}
	}

	fmt.Fprintf(out, "%s%s%s%s %s\n", ansiBold, ansiRed, label, ansiReset, message)
	if loc != nil {
		fmt.Fprintf(out, "  %s%s--> %s:%d:%d%s\n", ansiDim, ansiBlue,
			loc.Filename, loc.Line, loc.Column, ansiReset)
	}
}

// sourceFor resolves the source text for a location: the boundary's own
// source when the location is in the same file, otherwise the file on
// disk. Empty when unknown, so annotate degrades rather than underlining
// the wrong file's lines.
func (u *uncaughtErrorReport) sourceFor(loc *SourceLocation) string {
	primary := u.Raised.Location
	if u.Source != "" && primary != nil && primary.Filename == loc.Filename {
		return u.Source
	}
	if loc.Filename != "" {
		if contents, err := os.ReadFile(loc.Filename); err == nil {
			return string(contents)
		}
	}
	return ""
}

// errorLocation extracts a source location (and, for SourceErrors, the
// source text it was recorded with) from an arbitrary sibling error.
func errorLocation(err error) (*SourceLocation, string) {
	var raised *RaisedError
	if errors.As(err, &raised) {
		return raised.Location, ""
	}
	var sourceErr *SourceError
	if errors.As(err, &sourceErr) {
		return sourceErr.Location, sourceErr.Source
	}
	return nil, ""
}

// errorSummary renders "TypeName: message" for an error value. BasicError —
// the type behind a bare string raise — keeps the plain "error:" wording so
// simple raises read the way they always have.
func errorSummary(val Value) string {
	message := "unknown error"
	typeName := "error"

	obj, ok := val.(*Object)
	if ok {
		if msg, found := obj.lookupValue("message"); found {
			message = msg.String()
		}
		if typ, ok := obj.Mod.(*Type); ok && typ != BasicErrorType {
			typeName = typ.Named
		}
	}

	return fmt.Sprintf("%s: %s", typeName, message)
}

// writeErrorFields prints the error's public stored data fields (everything
// except message, methods, and computed members).
func writeErrorFields(out *strings.Builder, val Value) {
	for _, f := range errorFields(val) {
		fmt.Fprintf(out, "  %s%s:%s %s\n", ansiDim, f.Name, ansiReset, f.Value)
	}
}

// errorFields collects the error's public stored data fields (everything
// except message, cause, methods, and computed members), using lookupValue
// so pending initializers are never forced. This mirrors objectsEqual's
// non-forcing walk. Shared by the terminal boundary printer and ErrorReport
// extraction (report.go).
func errorFields(val Value) []ErrorReportField {
	obj, ok := val.(*Object)
	if !ok {
		return nil
	}
	typ, ok := obj.Mod.(*Type)
	if !ok {
		return nil
	}

	var fields []ErrorReportField
	for name, scheme := range typ.Bindings(PublicVisibility) {
		if name == "message" || name == "cause" {
			continue
		}
		if t, _ := scheme.Type(); isMethodType(t) {
			continue
		}
		fieldVal, found := obj.lookupValue(name)
		if !found {
			continue
		}
		fields = append(fields, ErrorReportField{Name: name, Value: Repr(fieldVal)})
	}
	return fields
}

// causeChain walks the cause links reachable from an uncaught error,
// preferring an explicit non-null `cause` field on the value over the
// implicit record on the wrapper. Bounded and cycle-guarded: the chain is
// user-constructible.
func causeChain(raised *RaisedError) []*RaisedError {
	const maxDepth = 8

	var chain []*RaisedError
	seen := map[Value]bool{raised.Value: true}
	current := raised
	for len(chain) < maxDepth {
		next := nextCause(current)
		if next == nil || seen[next.Value] {
			break
		}
		seen[next.Value] = true
		chain = append(chain, next)
		current = next
	}
	return chain
}

// nextCause resolves the next link: the value's own non-null `cause` field
// wins; otherwise the implicit record from the rescue arm. Explicit causes
// have no raise site of their own, so their Location is nil.
func nextCause(raised *RaisedError) *RaisedError {
	if obj, ok := raised.Value.(*Object); ok {
		if cv, found := obj.lookupValue("cause"); found {
			if causeObj, ok := cv.(*Object); ok {
				return &RaisedError{Value: causeObj}
			}
		}
	}
	return raised.Cause
}

// siblingSummary renders a one-line description of a concurrent sibling
// failure: typed summary for raised errors, innermost message otherwise.
func siblingSummary(err error) string {
	var raised *RaisedError
	if errors.As(err, &raised) {
		return errorSummary(raised.Value)
	}
	var sourceErr *SourceError
	if errors.As(err, &sourceErr) {
		return sourceErr.Inner.Error()
	}
	return err.Error()
}

// parallelFailure carries the deterministic primary error from a `{{ }}`
// whose fields failed concurrently, plus the completed sibling failures
// that fail-fast would otherwise silently discard. It unwraps to the
// primary so rescue dispatch and errors.Is/As behave exactly as before; a
// rescue that catches the primary still drops the siblings, matching the
// existing single-error semantics.
type parallelFailure struct {
	Primary error
	Also    []error
}

func (p *parallelFailure) Unwrap() error {
	return p.Primary
}

func (p *parallelFailure) Error() string {
	var out strings.Builder
	out.WriteString(p.Primary.Error())
	for _, sibling := range p.Also {
		fmt.Fprintf(&out, "\n%s%salso failed:%s %s", ansiBold, ansiRed, ansiReset, siblingSummary(sibling))
		if loc, _ := errorLocation(sibling); loc != nil {
			fmt.Fprintf(&out, " %s(%s:%d:%d)%s", ansiDim, loc.Filename, loc.Line, loc.Column, ansiReset)
		}
	}
	return out.String()
}
