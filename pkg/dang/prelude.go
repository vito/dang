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

// SplitDocExample splits a docstring into its description and its runnable
// example, per the stdlib convention that a docstring's first fenced code
// block (``` ... ```) is a self-contained snippet demonstrating the member.
// Docstrings without a fence return the whole text and "".
func SplitDocExample(doc string) (desc, example string) {
	idx := strings.Index(doc, "```")
	if idx < 0 {
		return strings.TrimSpace(doc), ""
	}
	rest := doc[idx+3:]
	if nl := strings.Index(rest, "\n"); nl >= 0 {
		rest = rest[nl+1:] // drop an optional language tag
	}
	end := strings.Index(rest, "```")
	if end < 0 {
		return strings.TrimSpace(doc), ""
	}
	return strings.TrimSpace(doc[:idx]), strings.TrimSpace(rest[:end])
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
