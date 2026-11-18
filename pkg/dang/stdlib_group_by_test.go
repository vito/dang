package dang

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestListGroupBy(t *testing.T) {
	require.NoError(t, evalExample(`
let input = ["pear", "apple", "plum", "apricot", "", "pear"]
let grouped: Map[[String!]!]! = input.groupBy { item => item.truncate(1) }
assert { grouped.keys == ["p", "a", ""] }
assert { grouped == ["p": ["pear", "plum", "pear"], "a": ["apple", "apricot"], "": [""]] }
assert { input == ["pear", "apple", "plum", "apricot", "", "pear"] }
let indexed = [10, 20, 30, 40].groupBy { item, index => toString(index % 2) }
assert { indexed == ["0": [10, 30], "1": [20, 40]] }
let records = [{{key: "b", value: 1}}, {{key: "a", value: 2}}, {{key: "b", value: 3}}]
assert { records.groupBy { item => item.key }.map { key, items => items.map { item => item.value } } == ["b": [1, 3], "a": [2]] }
`))
}

func TestListGroupByInference(t *testing.T) {
	require.NoError(t, evalExample(`
let inferred = [1, 2].groupBy { item => "all" }
let annotated: Map[[Int!]!]! = inferred
assert { annotated == ["all": [1, 2]] }
let empty: Map[[Int!]!]! = [].groupBy { item => "unused" }
assert { empty == [:] }
assert { empty.keys == [] }
let untypedEmpty = [].groupBy { item => "unused" }
let resolved: Map[[String!]!]! = untypedEmpty
assert { resolved == [:] }
let typedEmpty: [Float!]! = []
let floats: Map[[Float!]!]! = typedEmpty.groupBy { item => toString(item) }
assert { floats == [:] }
group[a](items: [a]!): Map[[a]!]! { items.groupBy { item => "all" } }
assert { group([1, 2]) == ["all": [1, 2]] }
assert { group(["a", "b"]) == ["all": ["a", "b"]] }
`))
}

func TestListGroupByNulls(t *testing.T) {
	require.NoError(t, evalExample(`
let nullable: Map[[Int]!]! = [1, null, 2, null].groupBy { item => if (item == null) { "null" } else { "value" } }
assert { nullable.keys == ["value", "null"] }
assert { nullable == ["value": [1, 2], "null": [null, null]] }
let absent: [Int!] = null
let result: Map[[Int!]!] = absent.groupBy { item => raise "must not run" }
assert { result == null }
`))
}

func TestListGroupByScalarKeys(t *testing.T) {
	// Blocks require their declared String! result, so degrade custom scalars
	// at an explicit typed boundary (as for other String-returning blocks).
	require.NoError(t, evalExample(`
assert { [1, 2].groupBy { item => Path("key").string } == ["key": [1, 2]] }
assert { [1, 2].groupBy { item => Path("key") :: String! } == ["key": [1, 2]] }
assert { [1, 2].groupBy { item => let key: String! = Path("key"); key } == ["key": [1, 2]] }
`))
}

func TestListGroupByTypeErrors(t *testing.T) {
	for name, script := range map[string]string{
		"integer key":       `[1].groupBy { item => item }`,
		"boolean key":       `[1].groupBy { item => true }`,
		"list key":          `[1].groupBy { item => ["a"] }`,
		"null key":          `[1].groupBy { item => null }`,
		"nullable key":      `["a", null].groupBy { item => item }`,
		"scalar key":        `[1].groupBy { item => Path("key") }`,
		"missing block":     `[1].groupBy`,
		"wrong result":      `let groups: Map[[String!]!]! = [1].groupBy { item => "key" }`,
		"nullable elements": `let groups: Map[[Int!]!]! = [1, null].groupBy { item => "key" }`,
		"bound wrong key":   `let group = &[1].groupBy; group { item => item }`,
	} {
		t.Run(name, func(t *testing.T) {
			require.ErrorContains(t, evalExample(script), "type:")
		})
	}
}

func TestListGroupByIsolation(t *testing.T) {
	require.NoError(t, evalExample(`
let input = [1, 2]
let bound = &input.groupBy
input += [3]
let original = bound { item => "key" }
assert { original == ["key": [1, 2]] }
let fresh = input.groupBy { item => "key" }
assert { fresh == ["key": [1, 2, 3]] }
let group = original["key"] ?? []
group += [4]
let changed = original.with("key", group).with("other", [5])
assert { changed == ["key": [1, 2, 4], "other": [5]] }
assert { original == ["key": [1, 2]] }
assert { fresh == ["key": [1, 2, 3]] }
assert { input == [1, 2, 3] }
type Box {
  value: Int!
  incr: Box! { value += 1; self }
}
let boxes = [Box(1), Box(2)]
let byKey = boxes.groupBy { box => "key" }
let modified = (byKey["key"] ?? []).map { box => box.incr }
assert { modified.map { box => box.value } == [2, 3] }
assert { boxes.map { box => box.value } == [1, 2] }
assert { (byKey["key"] ?? []).map { box => box.value } == [1, 2] }
`))
}

func TestListGroupByEffects(t *testing.T) {
	require.NoError(t, evalExample(`
let seen: [Int!]! = []
let groups = [4, 5, 6].groupBy { item, index => seen += [index]; toString(item) }
assert { seen == [0, 1, 2] }
[].groupBy { item => raise "empty input must not call selector" }
let stopped = [1, 2, 3].groupBy { item => if (item == 2) { break "stopped" }; "key" }
assert { stopped == "stopped" }
assert { ([1].groupBy { item => raise "selector failed" } rescue [:]) == [:] }
`))
}

func TestListGroupByLargeGroup(t *testing.T) {
	// One large group is the case where repeated immutable append/map updates
	// cost quadratic memory. groupBy builds the group with private appends.
	require.NoError(t, evalExample(fmt.Sprintf(`
let input = %q.split(",")
let groups = input.groupBy { item => item }
assert { groups.keys == ["a"] }
assert { groups["a"] == input }
assert { groups["a"].length == 4096 }
`, strings.TrimSuffix(strings.Repeat("a,", 4096), ","))))
}
