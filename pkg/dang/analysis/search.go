package analysis

import (
	"sort"
	"strings"

	"github.com/vito/dang/v2/pkg/dang"
	"github.com/vito/dang/v2/pkg/hm"
)

// Search ranks what scope can reach by how well its name and docs match
// query: the scope's own members (declarations, imports, builtin
// functions), and the members of every named type it can name (builtin
// types, the List and Map methods, the types of its imports) along with
// their static members. Every word of query must appear in a result's name
// or docs; matches in names rank first. At most limit results are returned
// (20 if limit is not positive).
func Search(scope dang.TypeScope, query string, limit int) []Member {
	if limit <= 0 {
		limit = 20
	}
	words := strings.Fields(strings.ToLower(query))
	if len(words) == 0 || scope == nil {
		return nil
	}

	type result struct {
		member Member
		score  int
	}
	var results []result
	seenNames := map[string]bool{}
	seenBindings := map[*hm.Scheme]bool{}
	// Each member is considered once, under the first (shortest) name it's
	// reached by: an import's field is reachable unqualified, through its
	// Query type, and through its namespace.
	consider := func(m Member) {
		if m.scheme != nil {
			if seenBindings[m.scheme] {
				return
			}
			seenBindings[m.scheme] = true
		}
		key := m.Qualified()
		if seenNames[key] {
			return
		}
		seenNames[key] = true
		if score := searchScore(m, query, words); score > 0 {
			results = append(results, result{m, score})
		}
	}

	for _, m := range scopeMembers(scope, dang.PrivateVisibility, false) {
		consider(m)
	}
	// Walk the named types breadth-first, so each is qualified by its
	// shortest name: Test's types see String through the prelude too.
	type namespace struct {
		prefix string
		scope  dang.TypeScope
	}
	visited := map[dang.TypeScope]bool{}
	queue := []namespace{{"", scope}}
	for depth := 0; len(queue) > 0 && depth < 4; depth++ {
		var next []namespace
		for _, ns := range queue {
			var types []string
			byName := map[string]dang.TypeScope{}
			for name, named := range ns.scope.NamedTypes() {
				types = append(types, name)
				byName[name] = named
			}
			sort.Strings(types)
			for _, name := range types {
				named := byName[name]
				if visited[named] {
					continue
				}
				visited[named] = true
				qualified := ns.prefix + name
				for _, m := range scopeMembers(named, dang.PublicVisibility, true) {
					m.Receiver = qualified
					consider(m)
				}
				// Its statics, e.g. JSON.encode.
				for _, m := range staticMembers(named) {
					m.Receiver = qualified
					consider(m)
				}
				next = append(next, namespace{qualified + ".", named})
			}
		}
		queue = next
	}
	if !visited[dang.MapTypeModule] {
		for _, m := range scopeMembers(dang.MapTypeModule, dang.PublicVisibility, true) {
			m.Receiver = "Map"
			consider(m)
		}
	}

	sort.SliceStable(results, func(i, j int) bool {
		a, b := results[i], results[j]
		if a.score != b.score {
			return a.score > b.score
		}
		// Prefer what's in scope, then shallower receivers.
		if da, db := strings.Count(a.member.Qualified(), "."), strings.Count(b.member.Qualified(), "."); da != db {
			return da < db
		}
		return a.member.Qualified() < b.member.Qualified()
	})
	if len(results) > limit {
		results = results[:limit]
	}
	members := make([]Member, len(results))
	for i, r := range results {
		members[i] = r.member
	}
	return members
}

// searchScore scores how well m matches a query, 0 for no match: every word
// must appear in its name or docs.
func searchScore(m Member, query string, words []string) int {
	name := strings.ToLower(m.Name)
	doc := strings.ToLower(m.Doc)
	score := 0
	for _, w := range words {
		switch {
		case strings.Contains(name, w):
			score += 10
		case strings.Contains(doc, w):
			score += 3
		default:
			return 0
		}
	}
	q := strings.ToLower(strings.TrimSpace(query))
	switch {
	case name == q:
		score += 100
	case strings.HasPrefix(name, q):
		score += 50
	case strings.Contains(doc, q):
		score += 5
	}
	if m.Deprecated != "" {
		score--
	}
	return score
}
