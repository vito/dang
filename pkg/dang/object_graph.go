package dang

import (
	"fmt"
	"strings"
)

type objectSlotGraph struct {
	slots []*SlotDecl
	deps  map[int]map[int]struct{}
	rdeps map[int]map[int]struct{}
	names []string
}

func buildObjectSlotGraph(slots []*SlotDecl) (*objectSlotGraph, error) {
	localNames := make(map[string]int, len(slots))
	names := make([]string, len(slots))
	for i, slot := range slots {
		declared := slot.DeclaredSymbols()
		if len(declared) != 1 {
			return nil, fmt.Errorf("object slot must declare exactly one name")
		}
		name := declared[0]
		if prev, ok := localNames[name]; ok {
			return nil, fmt.Errorf("object literal has duplicate field %q (previous declaration at field %d)", name, prev+1)
		}
		localNames[name] = i
		names[i] = name
	}

	g := &objectSlotGraph{
		slots: slots,
		deps:  make(map[int]map[int]struct{}, len(slots)),
		rdeps: make(map[int]map[int]struct{}, len(slots)),
		names: names,
	}
	for i, slot := range slots {
		for _, ref := range slot.ReferencedSymbols() {
			dep, ok := localNames[ref]
			if !ok || dep == i {
				continue
			}
			if g.deps[i] == nil {
				g.deps[i] = map[int]struct{}{}
			}
			if g.rdeps[dep] == nil {
				g.rdeps[dep] = map[int]struct{}{}
			}
			g.deps[i][dep] = struct{}{}
			g.rdeps[dep][i] = struct{}{}
		}
	}
	return g, nil
}

func (g *objectSlotGraph) Layers() ([][]int, error) {
	remaining := make(map[int]map[int]struct{}, len(g.slots))
	ready := []int{}
	for i := range g.slots {
		remaining[i] = make(map[int]struct{}, len(g.deps[i]))
		for dep := range g.deps[i] {
			remaining[i][dep] = struct{}{}
		}
		if len(remaining[i]) == 0 {
			ready = append(ready, i)
		}
	}

	var layers [][]int
	done := make(map[int]struct{}, len(g.slots))
	for len(ready) > 0 {
		layer := append([]int(nil), ready...)
		layers = append(layers, layer)
		ready = nil
		for _, i := range layer {
			done[i] = struct{}{}
			for dependent := range g.rdeps[i] {
				delete(remaining[dependent], i)
				if len(remaining[dependent]) == 0 {
					if _, alreadyDone := done[dependent]; !alreadyDone {
						ready = append(ready, dependent)
					}
				}
			}
		}
	}

	if len(done) != len(g.slots) {
		return nil, fmt.Errorf("object literal has cyclic field dependencies: %s", g.cycleString(done))
	}
	return layers, nil
}

func (g *objectSlotGraph) cycleString(done map[int]struct{}) string {
	visiting := map[int]bool{}
	visited := map[int]bool{}
	var stack []int
	var cycle []int
	var dfs func(int) bool
	dfs = func(i int) bool {
		visiting[i] = true
		stack = append(stack, i)
		for dep := range g.deps[i] {
			if _, ok := done[dep]; ok {
				continue
			}
			if visiting[dep] {
				start := 0
				for start < len(stack) && stack[start] != dep {
					start++
				}
				cycle = append(append([]int(nil), stack[start:]...), dep)
				return true
			}
			if !visited[dep] && dfs(dep) {
				return true
			}
		}
		stack = stack[:len(stack)-1]
		visiting[i] = false
		visited[i] = true
		return false
	}
	for i := range g.slots {
		if _, ok := done[i]; ok || visited[i] {
			continue
		}
		if dfs(i) {
			break
		}
	}
	if len(cycle) == 0 {
		var names []string
		for i := range g.slots {
			if _, ok := done[i]; !ok {
				names = append(names, g.names[i])
			}
		}
		return strings.Join(names, ", ")
	}
	parts := make([]string, len(cycle))
	for i, idx := range cycle {
		parts[i] = g.names[idx]
	}
	return strings.Join(parts, " -> ")
}
