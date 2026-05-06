package dang

import (
	"sync"
	"testing"
)

func TestConcurrentLookupMethodFirstUse(t *testing.T) {
	methodRegistry = nil
	methodRegistryOnce = sync.Once{}

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
		t.Fatalf("missing list methods during concurrent first lookup: %d", len(misses))
	}
}
