package dang

import (
	"context"
	"sync"
	"testing"
	"testing/synctest"
)

func TestBuiltinRegistryClassifiesDefinitions(t *testing.T) {
	functionNames := map[string]bool{}
	ForEachFunction(func(def BuiltinDef) {
		if def.IsMethod || def.IsStatic {
			t.Fatalf("function iterator returned non-function builtin: %+v", def)
		}
		functionNames[def.Name] = true
	})
	for _, name := range []string{"print", "toJSON", "toString"} {
		if !functionNames[name] {
			t.Fatalf("function %q was not registered", name)
		}
	}

	for _, name := range []string{"map", "filter", "reduce"} {
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

func TestConcurrentNewEvalEnvDoesNotMutateStaticModuleOrigins(t *testing.T) {
	savedBuiltins := builtins
	builtins = newBuiltinRegistry()
	t.Cleanup(func() {
		builtins = savedBuiltins
	})

	host := NewModule("TestStatic", ObjectKind)
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

				env := NewEvalEnv(NewPreludeEnv("test"))
				hostVal, found, err := env.Get(context.Background(), "TestStatic")
				if err != nil {
					misses <- err.Error()
					return
				}
				if !found {
					misses <- "module"
					return
				}
				modVal, ok := hostVal.(*ModuleValue)
				if !ok {
					misses <- "module type"
					return
				}
				if _, found, err := modVal.Get(context.Background(), "value"); err != nil {
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
			t.Fatalf("missing static module runtime values during concurrent NewEvalEnv: %d", len(misses))
		}
	})

	if _, found := host.LocalValueOrigin("value"); found {
		t.Fatalf("concurrent NewEvalEnv mutated static module value origins")
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

			for _, name := range []string{"map", "filter", "reduce"} {
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
