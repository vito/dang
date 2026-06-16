package dang

import "fmt"

// slotDepGraph models the data-dependency graph between nodes that declare
// and reference named symbols. An edge i -> dep means nodes[i] references a
// symbol declared by nodes[dep]. Used to order module-variable and
// object-literal field inference (dependencies before dependents) and to
// reject cyclic dependencies.
type slotDepGraph struct {
	n     int
	names []string      // names[i] is one declared symbol per node, for diagnostics
	deps  map[int][]int // i -> indices it depends on, source order, deduplicated
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
