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
	source := u.Source
	if source == "" && u.Raised.Location != nil && u.Raised.Location.Filename != "" {
		if contents, err := os.ReadFile(u.Raised.Location.Filename); err == nil {
			source = string(contents)
		}
	}

	header := fmt.Sprintf("uncaught %s", errorSummary(u.Raised.Value))

	var out strings.Builder
	if annotated, ok := formatSourceAnnotation(u.Raised.Location, source, "Error:", ansiRed, header); ok {
		out.WriteString(annotated)
	} else {
		fmt.Fprintf(&out, "%s%sError:%s %s\n", ansiBold, ansiRed, ansiReset, header)
	}

	writeErrorFields(&out, u.Raised.Value)

	for _, link := range causeChain(u.Raised) {
		fmt.Fprintf(&out, "%s%scaused by:%s %s\n", ansiBold, ansiRed, ansiReset, errorSummary(link.Value))
		if link.Location != nil {
			fmt.Fprintf(&out, "  %s%s--> %s:%d:%d%s\n", ansiDim, ansiBlue,
				link.Location.Filename, link.Location.Line, link.Location.Column, ansiReset)
		}
		writeErrorFields(&out, link.Value)
	}

	for _, sibling := range u.Also {
		fmt.Fprintf(&out, "%s%salso failed:%s %s\n", ansiBold, ansiRed, ansiReset, siblingSummary(sibling))
	}

	// Trailing newline matches the SourceError rendering convention.
	return strings.TrimSuffix(out.String(), "\n") + "\n"
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
// except message, methods, and computed members), using lookupValue so
// pending initializers are never forced. This mirrors objectsEqual's
// non-forcing walk.
func writeErrorFields(out *strings.Builder, val Value) {
	obj, ok := val.(*Object)
	if !ok {
		return
	}
	typ, ok := obj.Mod.(*Type)
	if !ok {
		return
	}

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
		fmt.Fprintf(out, "  %s%s:%s %s\n", ansiDim, name, ansiReset, Repr(fieldVal))
	}
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
	}
	return out.String()
}
