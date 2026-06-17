package dang

import (
	"context"
	"sync"
	"testing"
	"testing/synctest"
)

func TestBuiltinRegistryClassifiesDefinitions(t *testing.T) {
	functionNames := map[string]bool{}
	functionDefs := map[string]BuiltinDef{}
	ForEachFunction(func(def BuiltinDef) {
		if def.IsMethod || def.IsStatic {
			t.Fatalf("function iterator returned non-function builtin: %+v", def)
		}
		functionNames[def.Name] = true
		functionDefs[def.Name] = def
	})
	for _, name := range []string{"print", "toString"} {
		if !functionNames[name] {
			t.Fatalf("function %q was not registered", name)
		}
	}
	// toJSON/fromJSON/fromYAML are restored as deprecated top-level aliases for
	// their JSON/YAML namespace members so old scripts keep working; each must
	// carry a deprecation reason so callers get a warning pointing at the new
	// namespace.
	for _, name := range []string{"toJSON", "fromJSON", "fromYAML"} {
		if !functionNames[name] {
			t.Fatalf("%q should be registered as a (deprecated) top-level function", name)
		}
		if functionDefs[name].Deprecated == "" {
			t.Fatalf("%q should be marked deprecated", name)
		}
	}
	// Non-deprecated builtins must not carry a deprecation reason.
	if functionDefs["print"].Deprecated != "" {
		t.Fatalf("print should not be marked deprecated")
	}
	for _, mod := range []*Type{JSONModule, YAMLModule, TOMLModule} {
		methods := map[string]bool{}
		ForEachStaticMethod(mod, func(def BuiltinDef) {
			methods[def.Name] = true
		})
		for _, name := range []string{"encode", "decode"} {
			if !methods[name] {
				t.Fatalf("%s.%s was not registered", mod.Named, name)
			}
		}
	}

	for _, name := range []string{"map", "filter", "find", "reduce", "uniq"} {
		if _, ok := LookupMethod(ListTypeModule, name); !ok {
			t.Fatalf("list method %q was not indexed", name)
		}
	}
	listReceiverFound := false
	for _, receiver := range MethodReceivers() {
		if receiver == ListTypeModule {
			listReceiverFound = true
		}
	}
	if !listReceiverFound {
		t.Fatalf("list method receiver was not registered")
	}

	randomMethods := map[string]bool{}
	ForEachStaticMethod(RandomModule, func(def BuiltinDef) {
		if !def.IsStatic || def.HostModule != RandomModule {
			t.Fatalf("random static iterator returned wrong builtin: %+v", def)
		}
		randomMethods[def.Name] = true
	})
	for _, name := range []string{"int", "float", "string"} {
		if !randomMethods[name] {
			t.Fatalf("Random.%s was not registered", name)
		}
		if functionNames[name] {
			t.Fatalf("Random.%s leaked into top-level functions", name)
		}
	}

	randomIndex, uuidIndex := -1, -1
	for i, module := range StaticModules() {
		switch module {
		case RandomModule:
			randomIndex = i
		case UUIDModule:
			uuidIndex = i
		}
	}
	if randomIndex == -1 || uuidIndex == -1 || randomIndex > uuidIndex {
		t.Fatalf("unexpected static module order: %v", StaticModules())
	}
}

func TestConcurrentNewValueScopeDoesNotMutateStaticModuleOrigins(t *testing.T) {
	savedBuiltins := builtins
	builtins = newBuiltinRegistry()
	t.Cleanup(func() {
		builtins = savedBuiltins
	})

	host := NewType("TestStatic", ObjectKind)
	StaticMethod(host, "value").
		Returns(NonNull(StringType)).
		Impl(func(context.Context, Args) (Value, error) {
			return StringValue{Val: "ok"}, nil
		})

	if _, found := host.LocalValueOrigin("value"); found {
		t.Fatalf("test static method unexpectedly has an origin before evaluation")
	}

	const goroutines = 128
	synctest.Test(t, func(t *testing.T) {
		start := make(chan struct{})
		misses := make(chan string, goroutines)

		for range goroutines {
			go func() {
				<-start

				env := NewValueScope(NewPreludeTypeScope("test"))
				hostVal, found, err := env.Lookup(context.Background(), "TestStatic")
				if err != nil {
					misses <- err.Error()
					return
				}
				if !found {
					misses <- "module"
					return
				}
				modVal, ok := hostVal.(*Object)
				if !ok {
					misses <- "module type"
					return
				}
				if _, found, err := modVal.Lookup(context.Background(), "value"); err != nil {
					misses <- err.Error()
				} else if !found {
					misses <- "method"
				}
			}()
		}

		close(start)
		synctest.Wait()
		close(misses)

		if len(misses) > 0 {
			t.Fatalf("missing static module runtime values during concurrent NewValueScope: %d", len(misses))
		}
	})

	if _, found := host.LocalValueOrigin("value"); found {
		t.Fatalf("concurrent NewValueScope mutated static module value origins")
	}
}

func TestConcurrentLookupMethod(t *testing.T) {
	const goroutines = 256
	start := make(chan struct{})
	misses := make(chan string, goroutines*3)

	var wg sync.WaitGroup
	for range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start

			for _, name := range []string{"map", "filter", "find", "reduce", "uniq"} {
				if _, ok := LookupMethod(ListTypeModule, name); !ok {
					misses <- name
				}
			}
		}()
	}

	close(start)
	wg.Wait()
	close(misses)

	if len(misses) > 0 {
		t.Fatalf("missing list methods during concurrent lookup: %d", len(misses))
	}
}
