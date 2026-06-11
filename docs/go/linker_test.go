//go:build cgo

package dangdocs

import (
	"testing"
)

// linkTags renders the resolved links of source as text→tag pairs.
func linkTags(t *testing.T, source string) map[string]string {
	t.Helper()
	tags := map[string]string{}
	for _, span := range stdlibLinks(source) {
		tags[source[span.start:span.end]] = span.tag
	}
	return tags
}

func assertLink(t *testing.T, tags map[string]string, text, tag string) {
	t.Helper()
	if got := tags[text]; got != tag {
		t.Errorf("link for %q is %q, want %q", text, got, tag)
	}
}

func assertNoLink(t *testing.T, tags map[string]string, text string) {
	t.Helper()
	if got, ok := tags[text]; ok {
		t.Errorf("unexpected link for %q: %q", text, got)
	}
}

// Method calls link to their receiver's stdlib page, resolved by
// typechecking — including through intermediate bindings and chains.
func TestStdlibLinksMethods(t *testing.T) {
	tags := linkTags(t, `let xs = [1, 2, 3]
let doubled = xs.map { x => x * 2 }
"a,b".split(",")`)
	assertLink(t, tags, "map", "stdlib-List-map")
	assertLink(t, tags, "split", "stdlib-String-split")
}

// Free builtin functions link to the functions page; static module methods
// link via their host module.
func TestStdlibLinksFunctionsAndStatics(t *testing.T) {
	tags := linkTags(t, `print(toJSON([1, 2]))
let f = Random.float`)
	assertLink(t, tags, "print", "stdlib-fn-print")
	assertLink(t, tags, "toJSON", "stdlib-fn-toJSON")
	assertLink(t, tags, "float", "stdlib-Random-float")
}

// assert with a block is a call position too.
func TestStdlibLinksBlockCall(t *testing.T) {
	tags := linkTags(t, "assert { 1 + 1 == 2 }")
	assertLink(t, tags, "assert", "stdlib-fn-assert")
}

// Names the snippet declares itself never link, and methods on user-defined
// types never link even when they shadow stdlib names.
func TestStdlibLinksShadowing(t *testing.T) {
	tags := linkTags(t, `pub print(x: Int!): Int! { x }
print(2)`)
	assertNoLink(t, tags, "print")

	tags = linkTags(t, `type Box {
  pub map: Int! { 1 }
}
Box.map`)
	assertNoLink(t, tags, "map")
}

// Unresolvable receivers (undefined symbols, fragments) yield no links and
// no errors; resolvable calls elsewhere in the same snippet still link.
func TestStdlibLinksPartialResolution(t *testing.T) {
	tags := linkTags(t, `foo.fizz(arg: 1).buzz
print("still works")`)
	assertNoLink(t, tags, "fizz")
	assertNoLink(t, tags, "buzz")
	assertLink(t, tags, "print", "stdlib-fn-print")
}

// Snippets that don't parse at all yield no links.
func TestStdlibLinksUnparseable(t *testing.T) {
	if links := stdlibLinks("withExec(args: [String!]!): Container!"); len(links) != 0 {
		t.Errorf("expected no links for a declaration fragment, got %v", links)
	}
}
