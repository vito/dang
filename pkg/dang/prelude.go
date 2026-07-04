package dang

import (
	"context"
	"embed"
	"fmt"
	"strings"
	"sync"
)

// The Dang-source prelude: stdlib pieces written in Dang itself (currently
// the Path scalar). The files are parsed, inferred, and evaluated exactly
// once per process, and the resulting declarations are layered between the
// Go-built Prelude and every program's own scope.
//
//go:embed prelude/*.dang
var preludeFS embed.FS

var (
	preludeOnce sync.Once
	// preludeChain is Prelude with the Dang-source prelude layered on top;
	// NewPreludeTypeScope builds user scopes over it.
	preludeChain TypeScope
	// preludeModule holds the prelude's own top-level declarations.
	preludeModule *Type
	// preludeBindings are the prelude's public value bindings (constructor
	// functions and the like), bound builtin-style into every fresh value
	// scope by NewValueScope.
	preludeBindings []Keyed[Value]
)

// PreludeModule returns the module holding the Dang-source prelude's
// top-level declarations (types via NamedTypes, value schemes via Bindings,
// docstrings via GetDocString). Read-only: the prelude is frozen after load.
func PreludeModule() *Type {
	loadPrelude()
	return preludeModule
}

// ExampleDirective returns the runnable example attached to a declaration
// via its @example directive (declared in prelude/docs.dang), per the stdlib
// convention that every public member of the Dang-source prelude documents
// itself with one. The code argument is written as a ```dang fenced template
// literal; any literal string works, but interpolation does not (the example
// must be self-contained), so non-literal arguments report ok=false.
func ExampleDirective(directives []*DirectiveApplication) (code string, ok bool) {
	for _, d := range directives {
		if d.Scope != nil || d.Name != "example" || len(d.Args) == 0 {
			continue
		}
		return literalString(d.Args[0].Value)
	}
	return "", false
}

// literalString extracts the compile-time content of a literal string node:
// a plain string or an interpolation-free template.
func literalString(node Node) (string, bool) {
	switch n := node.(type) {
	case *String:
		return strings.TrimSpace(n.Value), true
	case *Template:
		if !n.IsLiteralOnly() {
			return "", false
		}
		var b strings.Builder
		for _, p := range n.Parts {
			b.WriteString(p.Lit)
		}
		return strings.TrimSpace(b.String()), true
	}
	return "", false
}

// preludeSource returns the embedded source of a prelude file for error
// rendering, matching the "prelude/<name>.dang" filenames that prelude
// source locations carry.
func preludeSource(filename string) (string, bool) {
	if !strings.HasPrefix(filename, "prelude/") {
		return "", false
	}
	src, err := preludeFS.ReadFile(filename)
	if err != nil {
		return "", false
	}
	return string(src), true
}

// loadPrelude evaluates the embedded Dang-source prelude once. Everything it
// produces — the layered type scope, the value bindings, and any state hung
// off the declared *Types (scalar methods, new() hooks) — is frozen after
// this returns and shared process-wide, under the same never-mutate
// discipline as Prelude itself.
//
// Failures panic: the prelude is a build artifact, so a broken prelude is a
// broken interpreter, not a user error.
func loadPrelude() {
	preludeOnce.Do(func() {
		mod := NewType("prelude", ObjectKind)
		chain := &OverlayTypeScope{primary: mod, lexical: Prelude}

		entries, err := preludeFS.ReadDir("prelude")
		if err != nil {
			panic(fmt.Errorf("prelude: %w", err))
		}

		var forms []Node
		for _, entry := range entries { // ReadDir sorts by name
			src, err := preludeFS.ReadFile("prelude/" + entry.Name())
			if err != nil {
				panic(fmt.Errorf("prelude: %w", err))
			}
			name := "prelude/" + entry.Name()
			parsed, err := ParseWithRecovery(name, src, GlobalStore("filePath", name))
			if err != nil {
				panic(fmt.Errorf("prelude: parsing %s: %w", name, err))
			}
			forms = append(forms, parsed.(*FileBlock).Forms...)
		}

		// Inline: declarations must land in the chain scope itself, not a
		// clone — the whole point is that user scopes layer over them.
		fileBlock := &FileBlock{Forms: forms, Inline: true}

		ctx := context.Background()
		if _, err := Infer(ctx, chain, fileBlock, true); err != nil {
			panic(fmt.Errorf("prelude: inference: %w", err))
		}

		// Build the value scope directly rather than via NewValueScope,
		// which would re-enter this loader.
		scope := NewObject(chain)
		addBuiltinFunctions(scope)
		if _, err := EvalNode(ctx, scope, fileBlock); err != nil {
			panic(fmt.Errorf("prelude: evaluation: %w", err))
		}

		// Snapshot the prelude's public declarations, forcing each lazy
		// binding so nothing evaluates after the freeze.
		for name := range mod.Bindings(PublicVisibility) {
			val, found, err := scope.Lookup(ctx, name)
			if err != nil {
				panic(fmt.Errorf("prelude: %s: %w", name, err))
			}
			if !found {
				continue
			}
			preludeBindings = append(preludeBindings, Keyed[Value]{Key: name, Value: val})
		}

		preludeModule = mod
		preludeChain = chain
	})
}
