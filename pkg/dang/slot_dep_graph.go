package dang

import "fmt"

// slotDepGraph models the data-dependency graph between nodes that declare
// and reference named symbols. An edge i -> dep means nodes[i] references a
// symbol declared by nodes[dep]. Used by module-variable inference ordering
// and by object-literal field scheduling.
type slotDepGraph struct {
	n     int
	names []string      // names[i] is one declared symbol per node, for diagnostics
	deps  map[int][]int // i -> indices it depends on, source order, deduplicated
	rdeps map[int][]int // i -> indices that depend on i, source order, deduplicated
}

// newSlotDepGraph builds a dependency graph from nodes' DeclaredSymbols and
// ReferencedSymbols. References to symbols declared by the same node are
// ignored. If multiple nodes declare the same symbol, the first declaration
// wins.
func newSlotDepGraph(nodes []Node) *slotDepGraph {
	declared := make(map[string]int, len(nodes))
	names := make([]string, len(nodes))
	for i, node := range nodes {
		decls := node.DeclaredSymbols()
		if len(decls) > 0 {
			names[i] = decls[0]
		}
		for _, name := range decls {
			if _, dup := declared[name]; !dup {
				declared[name] = i
			}
		}
	}

	g := &slotDepGraph{
		n:     len(nodes),
		names: names,
		deps:  make(map[int][]int, len(nodes)),
		rdeps: make(map[int][]int, len(nodes)),
	}
	seen := make(map[[2]int]struct{}, len(nodes))
	for i, node := range nodes {
		for _, ref := range node.ReferencedSymbols() {
			dep, ok := declared[ref]
			if !ok || dep == i {
				continue
			}
			key := [2]int{i, dep}
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			g.deps[i] = append(g.deps[i], dep)
			g.rdeps[dep] = append(g.rdeps[dep], i)
		}
	}
	return g
}

// LinearOrder returns a DFS post-order topological order (dependencies
// before dependents). If a cycle exists, returns nil and a cycle path with
// the repeated entry node at both ends, e.g. [a, b, a].
func (g *slotDepGraph) LinearOrder() (order []int, cycle []int) {
	const (
		unvisited = iota
		visiting
		done
	)

	state := make([]int, g.n)
	var stack []int
	positions := make(map[int]int)

	var visit func(int) []int
	visit = func(i int) []int {
		state[i] = visiting
		positions[i] = len(stack)
		stack = append(stack, i)

		for _, dep := range g.deps[i] {
			switch state[dep] {
			case unvisited:
				if c := visit(dep); c != nil {
					return c
				}
			case visiting:
				c := append([]int(nil), stack[positions[dep]:]...)
				return append(c, dep)
			}
		}

		stack = stack[:len(stack)-1]
		delete(positions, i)
		state[i] = done
		order = append(order, i)
		return nil
	}

	for i := range g.n {
		if state[i] == unvisited {
			if c := visit(i); c != nil {
				return nil, c
			}
		}
	}

	return order, nil
}

// Layers returns a Kahn-style layered topological order: each layer is a
// set of indices that can be evaluated independently after all prior layers
// have completed. If a cycle exists, returns nil and a cycle path.
func (g *slotDepGraph) Layers() (layers [][]int, cycle []int) {
	inDegree := make([]int, g.n)
	for i := range g.n {
		inDegree[i] = len(g.deps[i])
	}
	var ready []int
	for i := range g.n {
		if inDegree[i] == 0 {
			ready = append(ready, i)
		}
	}
	scheduled := 0
	for len(ready) > 0 {
		layer := append([]int(nil), ready...)
		layers = append(layers, layer)
		ready = nil
		for _, i := range layer {
			scheduled++
			for _, dependent := range g.rdeps[i] {
				inDegree[dependent]--
				if inDegree[dependent] == 0 {
					ready = append(ready, dependent)
				}
			}
		}
	}
	if scheduled == g.n {
		return layers, nil
	}
	// Cycle present: derive a path via DFS for diagnostics.
	_, cycle = g.LinearOrder()
	return nil, cycle
}

// CycleNames maps a cycle path (indices) to declared names for diagnostics.
// Indices for nodes with no declared symbol get a placeholder.
func (g *slotDepGraph) CycleNames(cycle []int) []string {
	names := make([]string, len(cycle))
	for i, idx := range cycle {
		if g.names[idx] == "" {
			names[i] = fmt.Sprintf("<node %d>", idx)
		} else {
			names[i] = g.names[idx]
		}
	}
	return names
}
