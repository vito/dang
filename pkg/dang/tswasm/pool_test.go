//go:build !js

package tswasm

import (
	"context"
	"strings"
	"testing"
)

// TestPoolDropsGrownInstances checks that an instance whose memory grew past
// maxIdleMemory to parse a large source is closed rather than pooled: its
// memory would never shrink.
func TestPoolDropsGrownInstances(t *testing.T) {
	big := []byte(strings.Repeat("let x = [1, 2, (3 + 4)]\n", 20_000) + "let y = (1\n")
	if _, err := Parse(context.Background(), big, PruneErrorFree); err != nil {
		t.Fatal(err)
	}
	e, err := loadEngine()
	if err != nil {
		t.Fatal(err)
	}
	for {
		select {
		case inst := <-e.idle:
			if size := inst.mod.Memory().Size(); size > maxIdleMemory {
				t.Errorf("pooled an instance with %d bytes of memory", size)
			}
			inst.mod.Close(context.Background())
		default:
			return
		}
	}
}
