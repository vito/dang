package analysis

import (
	"errors"
	"fmt"
	"sync"

	"github.com/vito/dang/v2/pkg/dang"
)

// Severity is how serious a diagnostic is.
type Severity string

const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
)

// Code classifies a diagnostic by where it comes from.
type Code string

const (
	// CodeSyntax is a parse error.
	CodeSyntax Code = "syntax"
	// CodeType is a type error found by inference.
	CodeType Code = "type"
	// CodeWarning is an inference warning: code that type checks but very
	// likely doesn't do what its author meant (e.g. a rescue that can never
	// fire).
	CodeWarning Code = "warning"
	// CodeDeprecated is a call of a deprecated builtin.
	CodeDeprecated Code = "deprecated"
)

// Diagnostic is a problem found in a source file.
type Diagnostic struct {
	// Path is the file the diagnostic is in, as a path in the Source. It is
	// empty for an error that names no file.
	Path     string   `json:"path,omitempty"`
	Range    Range    `json:"range"`
	Severity Severity `json:"severity"`
	Code     Code     `json:"code"`
	Message  string   `json:"message"`

	// Replacement, when non-empty, is text that replaces Range to fix the
	// problem mechanically: for a deprecated builtin, the callee that
	// supersedes it (e.g. "JSON.encode" for toJSON).
	Replacement string `json:"replacement,omitempty"`

	// Context, for a type error on a selection or a name, is what that
	// name names, or what could have been meant instead. Only found with
	// Options.DiagnosticContext.
	Context *DiagnosticContext `json:"context,omitempty"`

	// Pitfall, when the diagnostic is a common mistake Dang recognizes
	// (see dang.Pitfall), is its ID, e.g. "line-comment"; Hint says what to
	// write instead, and Docs links the docs about it.
	Pitfall string `json:"pitfall,omitempty"`
	Hint    string `json:"hint,omitempty"`
	Docs    string `json:"docs,omitempty"`
}

// setPitfall notes the pitfall p on d, if any.
func (d *Diagnostic) setPitfall(p *dang.Pitfall) {
	if p == nil {
		return
	}
	d.Pitfall = p.ID
	d.Hint = p.Hint
	d.Docs = p.Docs
}

func (d Diagnostic) String() string {
	msg := fmt.Sprintf("%s: %s: %s", d.Range.Start, d.Severity, d.Message)
	if d.Path != "" {
		msg = d.Path + ":" + msg
	}
	if d.Replacement != "" {
		msg += fmt.Sprintf(" (quick fix: %s)", d.Replacement)
	}
	return msg
}

// errorDiagnostics converts a Dang error into diagnostics, one per error an
// *dang.InferenceErrors collects. pathOf maps the filename of an error's
// location to its Source path, or to "" for a file outside the Source whose
// errors are still reported (with no path, naming the file in the message);
// an error it doesn't want (ok false) is dropped. fallback is the path of an
// error with no location. contextOf, when set, finds the context of an
// inference error in the file at a Source path ("" for a file outside it).
func errorDiagnostics(err error, code Code, fallback string, pathOf func(filename string) (string, bool), contextOf func(*dang.InferError, string) *DiagnosticContext) []Diagnostic {
	var inferErrs *dang.InferenceErrors
	if errors.As(err, &inferErrs) {
		var ds []Diagnostic
		for _, e := range inferErrs.Errors {
			ds = append(ds, errorDiagnostics(e, code, fallback, pathOf, contextOf)...)
		}
		return ds
	}

	d := Diagnostic{
		Path:     fallback,
		Range:    Range{Start: Pos{Line: 1, Column: 1}, End: Pos{Line: 1, Column: 2}},
		Severity: SeverityError,
		Code:     code,
	}

	// Never format a *dang.SourceError whole: that renders a source snippet,
	// reading its file from disk when it has no source of its own.
	var sourceErr *dang.SourceError
	var parseErr interface {
		ParseErrorLocation() *dang.SourceLocation
	}
	var inferErr *dang.InferError
	var loc *dang.SourceLocation
	switch {
	case errors.As(err, &sourceErr):
		loc = sourceErr.Location
		d.Message = sourceErr.Inner.Error()
	case errors.As(err, &parseErr):
		loc = parseErr.ParseErrorLocation()
		d.Message = err.Error()
	case errors.As(err, &inferErr):
		loc = inferErr.Location
		// Leave out what's joined to it, e.g. a dump of a node that has no
		// location.
		d.Message = inferErr.Error()
	default:
		d.Message = err.Error()
	}
	if inferErr == nil && contextOf != nil {
		// Its context is found from the node it is on, also when it was
		// converted to a *dang.SourceError.
		_ = errors.As(err, &inferErr)
	}
	d.setPitfall(dang.PitfallOf(err))

	if loc != nil {
		if loc.Filename != "" {
			path, ok := pathOf(loc.Filename)
			if !ok {
				return nil
			}
			if path == "" {
				// A file outside the Source, e.g. an imported Dang module's.
				d.Message = foreignMessage(loc, d.Message)
				if inferErr != nil && contextOf != nil {
					d.Context = contextOf(inferErr, "")
					if d.Context != nil {
						// Not in the Source: there is nothing to edit.
						d.Context.NameRange = nil
					}
				}
				return []Diagnostic{d}
			}
			d.Path = path
		}
		d.Range = locRange(loc)
	}

	if inferErr != nil && contextOf != nil {
		d.Context = contextOf(inferErr, d.Path)
	}

	return []Diagnostic{d}
}

// foreignMessage prefixes a message about a file outside the Source with
// where in it the problem is.
func foreignMessage(loc *dang.SourceLocation, message string) string {
	return fmt.Sprintf("%s:%d:%d: %s", loc.Filename, loc.Line, loc.Column, message)
}

// warningDiagnostics converts collected inference warnings into diagnostics.
func warningDiagnostics(warnings []dang.InferWarning, fallback string, pathOf func(filename string) (string, bool)) []Diagnostic {
	var diags []Diagnostic
	for _, w := range warnings {
		loc := w.Location
		if loc == nil {
			continue
		}
		path := fallback
		rng := locRange(loc)
		message := w.Message
		if loc.Filename != "" {
			var ok bool
			if path, ok = pathOf(loc.Filename); !ok {
				continue
			}
			if path == "" {
				// A file outside the Source, e.g. an imported Dang module's.
				rng = Range{Start: Pos{Line: 1, Column: 1}, End: Pos{Line: 1, Column: 2}}
				message = foreignMessage(loc, message)
			}
		}
		diags = append(diags, Diagnostic{
			Path:     path,
			Range:    rng,
			Severity: SeverityWarning,
			Code:     CodeWarning,
			Message:  message,
		})
	}
	return diags
}

// deprecatedBuiltin is what a deprecated builtin call is flagged with: the
// human-facing reason and, when available, the structured replacement callee.
type deprecatedBuiltin struct {
	reason      string
	replacement string
}

var (
	deprecatedBuiltinsOnce   sync.Once
	deprecatedBuiltinsByName map[string]deprecatedBuiltin
)

// deprecatedBuiltins returns the top-level builtin functions marked deprecated,
// keyed by name. The builtin registry is populated in dang's package init, so
// it is safe to read once and cache.
func deprecatedBuiltins() map[string]deprecatedBuiltin {
	deprecatedBuiltinsOnce.Do(func() {
		deprecatedBuiltinsByName = map[string]deprecatedBuiltin{}
		dang.ForEachFunction(func(d dang.BuiltinDef) {
			if d.Deprecated != "" {
				deprecatedBuiltinsByName[d.Name] = deprecatedBuiltin{
					reason:      d.Deprecated,
					replacement: d.Replacement,
				}
			}
		})
	})
	return deprecatedBuiltinsByName
}

// deprecationDiagnostics flags every call in root whose callee names a
// deprecated builtin. It matches by callee name: a local binding that shadows
// a builtin of the same name would be a false positive, but shadowing the
// deprecated conversion builtins is vanishingly rare, and the eval-time
// warning is the source of truth. It walks the parsed AST, so it works
// whether or not inference succeeded.
func deprecationDiagnostics(root dang.Node, path string) []Diagnostic {
	if root == nil {
		return nil
	}
	deprecated := deprecatedBuiltins()
	var diags []Diagnostic
	root.Walk(func(n dang.Node) bool {
		if n == nil {
			return false
		}
		call, ok := n.(*dang.FunCall)
		if !ok {
			return true
		}
		sym, ok := call.Fun.(*dang.Symbol)
		if !ok {
			return true
		}
		info, ok := deprecated[sym.Name]
		if !ok {
			return true
		}
		loc := sym.GetSourceLocation()
		if loc == nil {
			return true
		}
		// The message matches the eval-time warning's wording, so the two
		// agree.
		diags = append(diags, Diagnostic{
			Path:        path,
			Range:       nameRange(loc, sym.Name),
			Severity:    SeverityWarning,
			Code:        CodeDeprecated,
			Message:     fmt.Sprintf("%s is deprecated: %s", sym.Name, info.reason),
			Replacement: info.replacement,
		})
		return true
	})
	return diags
}
