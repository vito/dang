package dang

import (
	"sync"
	"testing"
)

// The Dang-source prelude is evaluated once and shared process-wide; its
// declarations must behave like the Go-built Prelude: frozen after load and
// safe under concurrent use from independent programs. Each goroutine runs a
// full parse→infer→eval of a snippet that constructs Paths, dispatches
// methods (including bare sibling calls through the shared closures), and
// exercises the new() hook at several materialization boundaries.
func TestPreludeConcurrentUse(t *testing.T) {
	const goroutines = 64

	var wg sync.WaitGroup
	errs := make(chan error, goroutines)
	for range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := evalExample(`
				let p = Path("src//app/./main.go")
				assert { p.parent.join("lib") == Path("src/app/lib") }
				assert { p.stem == "main" }
				assert { p.extension == "go" }
				assert { p.relativeTo("src") == Path("app/main.go") }
				assert { Path("main.dang").matches("*.dang") }
				let s: String! = p
				assert { s == "src/app/main.go" }
			`)
			if err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		t.Fatalf("concurrent prelude use failed: %v", err)
	}
}

// Every public member declared by the Dang-source prelude must carry a
// runnable example — the first fenced code block of its docstring — and
// every example must parse, type-check, and evaluate. This is the prelude's
// counterpart of TestStdlibExamplesEvaluate for Go builtins.
func TestPreludeDocExamples(t *testing.T) {
	mod := PreludeModule()
	checked := 0

	check := func(label, doc string) {
		_, example := SplitDocExample(doc)
		if example == "" {
			t.Errorf("prelude %s docstring has no runnable example fence", label)
			return
		}
		if err := evalExample(example); err != nil {
			t.Errorf("prelude %s example: %v", label, err)
		}
		checked++
	}

	for typeName, sub := range mod.NamedTypes() {
		typ, ok := sub.(*Type)
		if !ok {
			continue
		}
		for member := range typ.Bindings(PublicVisibility) {
			doc, _ := typ.GetDocString(member)
			check(typeName+"."+member, doc)
		}
	}

	for name := range mod.Bindings(PublicVisibility) {
		doc, _ := mod.GetDocString(name)
		check(name, doc)
	}

	if checked == 0 {
		t.Fatalf("no prelude members found")
	}
}

// A user program declaring its own `scalar Path` must shadow the prelude's,
// not mutate it: the prelude type keeps its methods and hook afterwards.
func TestPreludeShadowingDoesNotMutate(t *testing.T) {
	loadPrelude()

	pathType, found := preludeChain.NamedType("Path")
	if !found {
		t.Fatalf("prelude Path type not found")
	}
	mod := pathType.(*Type)
	methodsBefore := mod.ScalarMethods()
	_, _, hookBefore := mod.ScalarHook()

	if err := evalExample(`
		scalar Path

		let x = "plain" :: Path!
		assert { toString(x) == "plain" }
	`); err != nil {
		t.Fatalf("shadowing program failed: %v", err)
	}

	if mod.ScalarMethods() != methodsBefore {
		t.Fatalf("user program mutated the prelude Path's methods")
	}
	if _, _, hookAfter := mod.ScalarHook(); hookAfter != hookBefore {
		t.Fatalf("user program mutated the prelude Path's hook")
	}
}
