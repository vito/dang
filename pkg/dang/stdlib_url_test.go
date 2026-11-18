package dang

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vito/dang/v2/pkg/hm"
)

func TestURLComponents(t *testing.T) {
	for _, tc := range []struct {
		name     string
		raw      string
		scheme   string
		host     string
		hostname string
		port     string
		path     string
	}{
		{name: "absolute", raw: "https://foo:1234/bar", scheme: "https", host: "foo:1234", hostname: "foo", port: "1234", path: "/bar"},
		{name: "credentials and escapes", raw: "HTTPS://user:pass@Example.com:00443/a%2Fb%20c?q=a%20b#frag", scheme: "https", host: "Example.com:00443", hostname: "Example.com", port: "00443", path: "/a/b c"},
		{name: "IPv6", raw: "https://[2001:db8::1]:8443/", scheme: "https", host: "[2001:db8::1]:8443", hostname: "2001:db8::1", port: "8443", path: "/"},
		{name: "IPv6 zone", raw: "http://[fe80::1%25eth0]/", scheme: "http", host: "[fe80::1%eth0]", hostname: "fe80::1%eth0", path: "/"},
		{name: "no port or path", raw: "https://foo", scheme: "https", host: "foo", hostname: "foo"},
		{name: "empty port", raw: "https://foo:/", scheme: "https", host: "foo:", hostname: "foo", path: "/"},
		{name: "relative", raw: "../a%20b?x=1#f", path: "../a b"},
		{name: "network reference", raw: "//foo:80/a", host: "foo:80", hostname: "foo", port: "80", path: "/a"},
		{name: "absolute path", raw: "/a/b", path: "/a/b"},
		{name: "opaque", raw: "mailto:user@example.com", scheme: "mailto"},
		// net/url leaves query strings and opaque components uninterpreted.
		{name: "raw query", raw: "https://foo/?q=%zz", scheme: "https", host: "foo", hostname: "foo", path: "/"},
		{name: "raw opaque", raw: "mailto:user%zz@example.com", scheme: "mailto"},
		{name: "empty", raw: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Exercise registration, inference, construction and dispatch.
			nullable := func(s string) string {
				if s == "" {
					return "null"
				}
				return strconv.Quote(s)
			}
			port := "null"
			if tc.port != "" {
				n, err := strconv.Atoi(tc.port)
				require.NoError(t, err)
				port = strconv.Itoa(n)
			}
			script := fmt.Sprintf(`
let u = URL(raw: %q)
let scheme: String = u.scheme
let host: String = u.host
let hostname: String = u.hostname
let port: Int = u.port
let path: String! = u.path
assert { scheme == %s }
assert { host == %s }
assert { hostname == %s }
assert { port == %s }
assert { path == %q }
assert { u.string == %q }
assert { toString(u) == %q }
assert { u == %q }
assert { %q == u }
`, tc.raw, nullable(tc.scheme), nullable(tc.host), nullable(tc.hostname), port, tc.path, tc.raw, tc.raw, tc.raw, tc.raw)
			require.NoError(t, evalExample(script))
		})
	}
}

func TestURLValidation(t *testing.T) {
	for _, raw := range []string{
		"https://foo/%zz",
		"https://foo:abc/",
		"https://[::1/",
		"https://foo/#%zz",
		"https://foo/\nbar",
	} {
		t.Run(raw, func(t *testing.T) {
			for name, script := range map[string]string{
				"constructor": fmt.Sprintf(`URL(%q)`, raw),
				"literal":     fmt.Sprintf(`let u: URL! = %q`, raw),
				"cast":        fmt.Sprintf(`let s = %q; s :: URL!`, raw),
			} {
				t.Run(name, func(t *testing.T) {
					require.ErrorContains(t, evalExample(script), "invalid URL")
				})
			}
		})
	}
}

func TestURLScalarShadowing(t *testing.T) {
	require.NoError(t, evalExample(`
scalar URL {
  new(raw: String!) { raw.toUpper }
  host: String! { "custom" }
  string: String! { "custom-string" }
}
let u = URL("hello")
assert { (u :: String!) == "HELLO" }
assert { u.host == "custom" }
assert { u.string == "custom-string" }
let host = &u.host
assert { host() == "custom" }
`))
}

func TestURLQueriesAndFragments(t *testing.T) {
	require.NoError(t, evalExample(`
 let absent = URL("")
 let query: Map[[String!]!]! = absent.query
 assert { query == [:] }
 assert { absent.rawQuery == null }
 assert { absent.fragment == null }
 let empty = URL("?#")
 assert { empty.rawQuery == "" }
 assert { empty.query == [:] }
 assert { empty.fragment == "" }
 let u = URL("/?z=one&x=a+b&z=two&semi=a;b&empty&%78=last#hello%20world")
 assert { u.query == ["z": ["one", "two"], "x": ["a b", "last"], "semi": ["a;b"], "empty": [""]] }
 assert { u.fragment == "hello world" }
 assert { u.isAbsolute == false }
 assert { URL("mailto:hello").isAbsolute }
 assert { URL("/?x=%zz").rawQuery == "x=%zz" }
 assert { URL("https://foo:65536").port == 65536 }
 `))
	for _, raw := range []string{"?x=%zz", "?%zz=x", "?valid=ok&broken=%"} {
		require.ErrorContains(t, evalExample(fmt.Sprintf(`URL(%q).query`, raw)), "invalid URL query")
	}
	require.ErrorContains(t, evalExample(`URL("https://foo:99999999999999999999999999").port`), "invalid URL port")
	require.NoError(t, evalExample(`assert { URL("?z=1&x=2&z=3&y=4").query.keys == ["z", "x", "y"] }`))
}

func TestURLBuilders(t *testing.T) {
	require.NoError(t, evalExample(`
 let u = URL("https://user:pass@[::1]:00443/a%2fb//?keep=a%20b#")
 let v = u.withHostname("fe80::1%eth0").withPort(65536).withScheme("HTTP")
 assert { v.string == "http://user:pass@[fe80::1%25eth0]:65536/a%2fb//?keep=a%20b#" }
 assert { u.string == "https://user:pass@[::1]:00443/a%2fb//?keep=a%20b#" }
 assert { v.withPort(null).host == "[fe80::1%eth0]" }
 assert { v.withHostname(null).host == null }
 assert { v.withHostname(null).string == "http:/a%2fb//?keep=a%20b#" }
 assert { URL("").withScheme("https").withHostname("example.com").withPath("/a ?#%").string == "https://example.com/a%20%3F%23%25" }
 assert { URL("/api/").appendPath(["users", "a/b", "a?b#c%"]).string == "/api/users/a%2Fb/a%3Fb%23c%25" }
 assert { URL("/a%2fb//").appendPath([".", "..", ""]).string == "/a%2fb//%2E/%2E%2E/" }
 assert { URL("/a//").appendPath([]).string == "/a//" }
 assert { URL("").appendPath(["a:b"]).scheme == null }
 assert { URL("").withPath("a:b").path == "a:b" }
 assert { URL("").withPath("a:b").scheme == null }
 assert { URL("").withPath("//evil/path").host == null }
 assert { URL("").withPath("//evil/path").path == "//evil/path" }
 assert { URL("https:").withPath("//evil/path").host == null }
 assert { URL("https:").withPath("//evil/path").path == "//evil/path" }
 assert { URL("%2Ffoo").withScheme("https").path == "/foo" }
 assert { URL("%2Ffoo").withHostname("example.com").path == "/foo" }
 assert { URL("%2F%2Ffoo").withScheme("https").path == "//foo" }
 assert { URL("%2F%2Ffoo").withScheme("https").host == null }
 assert { URL("HTTPS://foo/a%2fb").appendPath([]).string == "HTTPS://foo/a%2fb" }
 assert { URL("/a").withFragment("").string == "/a#" }
 assert { URL("/a#old").withFragment(null).string == "/a" }
 assert { URL("/a").withFragment("a #?%").fragment == "a #?%" }
 assert { URL("/a?old=1#").withQuery(null).string == "/a#" }
 assert { URL("/a").withQuery([:]).string == "/a?" }
 assert { URL("/a").withQuery(["omit": [], "keep": ["x"]]).string == "/a?keep=x" }
 assert { URL("/a").withQuery(["omit": []]).string == "/a?" }
 assert { URL("/a").withQuery(["x": ["a;b", "a+b"]]).string == "/a?x=a%3Bb&x=a%2Bb" }
 assert { URL("?x=1&y=2&x=3").withQueryParam("x", "new").query == ["x": ["new"], "y": ["2"]] }
 assert { URL("?x=1&x=2").withAddedQueryParam("x", "3").query == ["x": ["1", "2", "3"]] }
 assert { URL("?x=1").withoutQueryParam("x").rawQuery == null }
 assert { URL("?x=1&y=2").withoutQueryParam("x").query == ["y": ["2"]] }
 assert { URL("mailto:a%zz").withQueryParam("x", "y").withFragment("").string == "mailto:a%zz?x=y#" }
 assert { URL("https://foo/a/b?old=1#old").resolve(URL("../c?")).string == "https://foo/c?" }
 assert { URL("https://foo/a?x=1#old").resolve(URL("#")).string == "https://foo/a?x=1#" }
 assert { URL("https://foo/a?x=1#").resolve(URL("")).string == "https://foo/a?x=1" }
 assert { URL("https://foo/a").resolve(URL("other").appendPath([".."]).withFragment("")).string == "https://foo/other/%2E%2E#" }
 `))
}

func TestURLReferenceResolution(t *testing.T) {
	for _, tc := range []struct{ base, ref, want string }{
		{"file://server/share/file", "///local/file", "file:///local/file"},
		{"https://user:pass@server/a?old=1#old", "///local/file", "https:///local/file"},
		{"file://server/share/file", "//", "file://"},
		{"file://server/share/file", "///a/../b?x=1#", "file:///b?x=1#"},
		{"file://server/share/file", "////local/file", "file:////local/file"},
		{"file://server/share/file", "file:///local/file", "file:///local/file"},
		{"file://server/share/file", "file:/local/file", "file:/local/file"},
		{"/base/path", "///local/file", "///local/file"},
		{"mailto:opaque?old=1#old", "///local/file", "mailto:///local/file"},
		{"https://server/a?", "", "https://server/a?"},
		{"https://server/a?#old", "", "https://server/a?"},
		{"https://server/a?", "#", "https://server/a?#"},
		{"https://server/a?", "#new", "https://server/a?#new"},
		{"https://server/a#old", "", "https://server/a"},
		{"https://server/a#", "", "https://server/a"},
		{"https://server/a?old=1#old", "?", "https://server/a?"},
		{"file:/etc/a", "#new", "file:/etc/a#new"},
		{"file:/etc/a", "b", "file:/etc/b"},
		{"file:/etc/a?", "#", "file:/etc/a?#"},
		{"file:/etc/a?", "", "file:/etc/a?"},
		{"file:///etc/a", "b", "file:///etc/b"},
		{"file:///etc/a", "#new", "file:///etc/a#new"},
	} {
		t.Run(tc.base+" -> "+tc.ref, func(t *testing.T) {
			require.NoError(t, evalExample(fmt.Sprintf(`assert { URL(%q).resolve(URL(%q)).string == %q }`, tc.base, tc.ref, tc.want)))
		})
	}
	require.NoError(t, evalExample(`
 assert { URL("///local/file").string == "///local/file" }
 assert { URL("///local/file").path == "/local/file" }
 assert { URL("///local/file").escapedPath == "/local/file" }
 assert { URL("///local/file").withFragment("new").string == "///local/file#new" }
 assert { URL("//").withQuery([:]).string == "//?" }
 assert { URL("//").appendPath(["a"]).string == "///a" }
 assert { URL("file://").withFragment("").string == "file://#" }
 assert { URL("/a").withScheme("file").string == "file:/a" }
 assert { URL("file:/a").withScheme(null).string == "/a" }
 assert { URL("file:///a").withScheme(null).string == "///a" }
 assert { URL("").withPath("//foo").path == "//foo" }
 assert { URL("").withPath("//foo").string == "/%2Ffoo" }
 assert { URL("file://server/a").resolve(URL("").withPath("//foo")).host == "server" }
 assert { URL("file://server/a").resolve(URL("").withPath("//foo")).path == "//foo" }
 `))
}

func TestURLRootlessResolution(t *testing.T) {
	for _, tc := range []struct{ base, ref, want string }{
		{"..", "image.png", "../image.png"},
		{"docs/..", "image.png", "image.png"},
		{"docs/../..", "image.png", "../image.png"},
		{".", "image.png", "image.png"},
		{"docs/.", "image.png", "docs/image.png"},
		{"docs//..", "image.png", "docs/image.png"},
		{"..", "../image.png", "../../image.png"},
		{"docs/page", "#section", "docs/page#section"},
		{"docs/page?old=1#old", "", "docs/page?old=1"},
		{"docs/page", "?q=x", "docs/page?q=x"},
		{"docs/page?", "#", "docs/page?#"},
		{"docs/./a/../page%2fdata", "#new", "docs/./a/../page%2fdata#new"},
		{"docs/page", "other", "docs/other"},
		{"docs/page", "../other", "other"},
		{"docs/page", "../../other", "../other"},
		{"../docs/page", "../../other", "../../other"},
		{"docs/page", ".", "docs/"},
		{"docs/page", "..", "./"},
		{"docs/page", "../..", "../"},
		{"docs/page", "../../..", "../../"},
		{"page", "other", "other"},
		{"", "other", "other"},
		{"", ".", "./"},
		{"", "..", "../"},
		{"docs//page", "other", "docs//other"},
		{"docs//page", "../other", "docs/other"},
		{"docs//page", "../../other", "other"},
		{"docs/page", "..//other", ".//other"},
		{"docs/page", "../a//", "a//"},
		{"docs/page", "../a/..//..", "./"},
		{"%2Fdocs/page", "other", "%2Fdocs/other"},
		{"docs/page", "%2Fother", "docs/%2Fother"},
		{"docs/page", "%2E%2E/other", "docs/%2E%2E/other"},
		{"docs%2Fdata/page", "../other", "other"},
	} {
		t.Run(tc.base+" -> "+tc.ref, func(t *testing.T) {
			require.NoError(t, evalExample(fmt.Sprintf(`
let base = URL(%q)
let ref = URL(%q)
let relative = base.resolve(ref)
assert { relative.string == %q }
let origin = URL("https://example.com/project/?origin=1#old")
assert { origin.resolve(relative) == origin.resolve(base).resolve(ref) }
`, tc.base, tc.ref, tc.want)))
		})
	}
}

func TestURLRootlessPathComposition(t *testing.T) {
	// Exercise combinations of dot, empty and escaped segments, not just
	// individual examples. Resolving before selecting an origin must agree
	// with selecting it first. Keep the origin deep enough not to saturate
	// at its root when the two references contain excess parent segments.
	origin, err := url.Parse("https://example.com/a/b/c/d/e/f/g/h/")
	require.NoError(t, err)
	paths := []string{""}
	level := []string{""}
	for range 3 {
		var next []string
		for _, prefix := range level {
			for _, segment := range []string{"a", "", ".", "..", "%2E", "%2F"} {
				path := prefix + segment
				if path != "" && !strings.HasPrefix(path, "/") {
					paths = append(paths, path)
				}
				next = append(next, path+"/")
			}
		}
		level = next
	}
	resolve := urlResolutionProbe(t)
	for _, base := range paths {
		baseURL, err := url.Parse(base)
		require.NoError(t, err)
		for _, ref := range paths {
			if ref == "" {
				continue // Empty-path references inherit verbatim, without merging.
			}
			refURL, err := url.Parse(ref)
			require.NoError(t, err)
			mergedText, err := resolve(base, ref)
			require.NoError(t, err, "base %q, reference %q", base, ref)
			merged, err := url.Parse(mergedText)
			require.NoError(t, err)
			want := origin.ResolveReference(baseURL).ResolveReference(refURL).String()
			got := origin.ResolveReference(merged).String()
			require.Equal(t, want, got, "base %q, reference %q", base, ref)
		}
	}
}

// Compile once, but exercise only the public URL API. This stays useful when
// URL moves between native builtins and the self-hosted prelude, and keeps the
// composition matrix from measuring the parser and type checker per case.
func urlResolutionProbe(t testing.TB) func(string, string) (string, error) {
	t.Helper()
	call := urlPublicProbe(t, "resolveURL", `
resolveURL(base: String!, reference: String!): String! {
  URL(base).resolve(URL(reference)).string
}
`)
	return func(base, reference string) (string, error) {
		result, err := call(map[string]Value{
			"base": StringValue{Val: base}, "reference": StringValue{Val: reference},
		})
		if err != nil {
			return "", err
		}
		return result.(StringValue).Val, nil
	}
}

func urlPublicProbe(t testing.TB, name, source string) func(map[string]Value) (Value, error) {
	t.Helper()
	parsed, err := ParseWithRecovery("url-probe", []byte(source))
	require.NoError(t, err)
	file := parsed.(*FileBlock)
	types, values := BuildScopesFromImports("", nil)
	ctx := context.Background()
	_, err = InferFormsWithPhases(ctx, file.Forms, types, hm.NewSimpleFresher())
	require.NoError(t, err)
	for _, node := range file.Forms {
		_, err = EvalNode(ctx, values, node)
		require.NoError(t, err)
	}
	fn, found, err := values.Lookup(ctx, name)
	require.NoError(t, err)
	require.True(t, found)
	call := fn.(Callable)
	return func(args map[string]Value) (Value, error) {
		return call.Call(ctx, values, args)
	}
}

func BenchmarkURLResolve(b *testing.B) {
	resolve := urlResolutionProbe(b)
	b.ResetTimer()
	for b.Loop() {
		if _, err := resolve("https://example.com/a/b?old=1#old", "../c?tag=a&tag=b#new"); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkURLByteScaling(b *testing.B) {
	for _, operation := range []struct{ name, body string }{
		{"decode", `URL(raw).path`},
		{"encode", `URL("").withPath(raw).string`},
	} {
		for _, size := range []int{1024, 4096, 16384} {
			b.Run(fmt.Sprintf("%s/%d", operation.name, size), func(b *testing.B) {
				probe := urlPublicProbe(b, "convert", `convert(raw: String!): String! { `+operation.body+` }`)
				raw := "/" + strings.Repeat("a", size)
				if operation.name == "decode" {
					raw += "%20"
				} else {
					raw += " "
				}
				args := map[string]Value{"raw": StringValue{Val: raw}}
				b.SetBytes(int64(len(raw)))
				b.ReportAllocs()
				b.ResetTimer()
				for b.Loop() {
					if _, err := probe(args); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

func urlQueryFixture(count int, distinct bool) string {
	var out strings.Builder
	out.WriteByte('?')
	for i := range count {
		if i > 0 {
			out.WriteByte('&')
		}
		key := "x"
		if distinct {
			key += strconv.Itoa(i)
		}
		fmt.Fprintf(&out, "%s=%d", key, i)
	}
	return out.String()
}

func TestURLLargeQueries(t *testing.T) {
	probe := urlPublicProbe(t, "checkQuery", `
checkQuery(raw: String!, count: Int!, keys: Int!): Boolean! {
  let q = URL(raw).query
  assert { q.length == keys }
  assert { q.values.map { values => values.length }.sum == count }
  assert { URL("").withQuery(q).query == q }
  if (keys == 1) {
    assert { q["x"][0] == "0" }
    assert { q["x"][count - 1] == toString(count - 1) }
  }
  true
}
`)
	for _, distinct := range []bool{false, true} {
		keys := 1
		if distinct {
			keys = 4096
		}
		_, err := probe(map[string]Value{
			"raw":   StringValue{Val: urlQueryFixture(4096, distinct)},
			"count": IntValue{Val: 4096}, "keys": IntValue{Val: keys},
		})
		require.NoError(t, err)
	}
}

func BenchmarkURLQueryScaling(b *testing.B) {
	for _, distinct := range []bool{false, true} {
		name := "duplicate"
		if distinct {
			name = "distinct"
		}
		for _, count := range []int{256, 1024, 4096} {
			b.Run(fmt.Sprintf("%s/%d", name, count), func(b *testing.B) {
				probe := urlPublicProbe(b, "readQuery", `readQuery(raw: String!): Int! { URL(raw).query.length }`)
				raw := urlQueryFixture(count, distinct)
				args := map[string]Value{"raw": StringValue{Val: raw}}
				b.SetBytes(int64(len(raw)))
				b.ReportAllocs()
				b.ResetTimer()
				for b.Loop() {
					if _, err := probe(args); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

func TestURLBuilderValidation(t *testing.T) {
	for _, script := range []string{
		`URL("").withScheme("https://evil")`,
		`URL("").withScheme("")`,
		`URL("abc").withScheme("https")`,
		`URL("abc").withHostname("example.com")`,
		`URL("a%3Ab").withScheme("https")`,
		`URL("https://foo").withPath("relative")`,
		`URL("https://foo").withPort(-1)`,
		`URL("").withPort(80)`,
		`URL("").withHostname("evil/path")`,
		`URL("").withHostname("evil?query")`,
		`URL("").withHostname("evil#frag")`,
		`URL("").withHostname("user@evil")`,
		`URL("").withHostname("foo:80")`,
		`URL("").withHostname("foo%20bar")`,
		`URL("").withHostname("foo bar")`,
		`URL("?x=%zz").withAddedQueryParam("x", "y")`,
		`URL("?x=%zz").withQueryParam("x", "y")`,
		`URL("?x=%zz").withoutQueryParam("x")`,
		`URL("mailto:a").withScheme(null)`,
		`URL("mailto:a").withHostname("foo")`,
		`URL("mailto:a").withPort(null)`,
		`URL("mailto:a").withPath("/a")`,
		`URL("mailto:a").appendPath(["a"])`,
	} {
		t.Run(script, func(t *testing.T) { require.Error(t, evalExample(script)) })
	}
}

func TestURLByteConformance(t *testing.T) {
	// Infer once and pass strings as values, so invalid UTF-8 never passes
	// through a source-code or JSON decoder. All URL operations are public.
	probe := urlPublicProbe(t, "checkBytes", `
checkBytes(raw: String!, segment: String!, path: String!, query: String!, fragment: String!, percent: String!): Boolean! {
  let data = String.fromBytes(raw.bytes)
  assert { data.bytes == raw.bytes }
  let appended = URL("/").appendPath([data])
  assert { appended.string == "/" + segment }
  assert { appended.path.bytes == ("/" + raw).bytes }
  let replaced = URL("https://example.com").withPath("/" + data)
  assert { replaced.escapedPath == path }
  assert { replaced.path.bytes == ("/" + raw).bytes }
  let queried = URL("").withQuery([data: [data]])
  assert { queried.rawQuery == query + "=" + query }
  assert { queried.query[data] == [raw] }
  let fragmented = URL("").withFragment(data)
  assert { fragmented.string == "#" + fragment }
  assert { fragmented.fragment == raw }
  assert { URL("/" + percent).path.bytes == ("/" + raw).bytes }
  assert { URL("?" + percent + "=" + percent).query[raw] == [raw] }
  assert { URL("#" + percent).fragment == raw }
  true
}
`)
	check := func(t *testing.T, raw string) {
		t.Helper()
		segment := url.PathEscape(raw)
		if raw == "." || raw == ".." {
			segment = strings.ReplaceAll(raw, ".", "%2E")
		}
		path := (&url.URL{Path: "/" + raw}).EscapedPath()
		fragment := (&url.URL{Fragment: raw}).EscapedFragment()
		var percent strings.Builder
		for _, b := range []byte(raw) {
			fmt.Fprintf(&percent, "%%%02X", b)
		}
		for _, escaped := range []string{percent.String(), strings.ToLower(percent.String())} {
			_, err := probe(map[string]Value{
				"raw": StringValue{Val: raw}, "segment": StringValue{Val: segment},
				"path": StringValue{Val: path}, "query": StringValue{Val: url.QueryEscape(raw)},
				"fragment": StringValue{Val: fragment}, "percent": StringValue{Val: escaped},
			})
			require.NoError(t, err)
		}
	}
	for i := range 256 {
		t.Run(fmt.Sprintf("byte-%02X", i), func(t *testing.T) { check(t, string([]byte{byte(i)})) })
	}
	for name, raw := range map[string]string{
		"empty": "", "dot-dot": "..", "Unicode": "café/東京/🙂 e\u0301",
		"invalid UTF-8": string([]byte{0xff, 0xc0, 0xaf, 0xed, 0xa0, 0x80, 0xe2, 0x82}),
		"delimiters":    "a/b?c#d%+ &;=:@!$'(),[]",
		"long ASCII":    strings.Repeat("a", 16384) + " /?#%+",
	} {
		t.Run(name, func(t *testing.T) { check(t, raw) })
	}
}

func TestURLAuthorityConformance(t *testing.T) {
	// net/url is an independent oracle for ordinary RFC authority examples,
	// not for its permissive acceptance of malformed IP address literals.
	for _, raw := range []string{
		"https://user%40name:p%3Ass@example.com:00443/a%2fb?x=a+b#c%20d",
		"https://user:@example.com/", "https://@example.com/",
		"https://[::ffff:192.0.2.128]:8080/a",
		"https://[2001:db8::192.0.2.1]/",
		"http://[fe80::1%25eth0]:80/",
		"http://[fe80::1%25en%30]/",
		"//user%3Aname:p%40ss@[::1]:0/",
	} {
		t.Run(raw, func(t *testing.T) {
			parsed, err := url.Parse(raw)
			require.NoError(t, err)
			changed := *parsed
			changed.Host = "example.net"
			require.NoError(t, evalExample(fmt.Sprintf(`
let u = URL(%q)
assert { u.host == %q }
assert { u.hostname == %q }
assert { u.path == %q }
assert { u.escapedPath == %q }
assert { u.string == %q }
assert { u.withPort(null).withHostname("example.net").string == %q }
`, raw, parsed.Host, parsed.Hostname(), parsed.Path, parsed.EscapedPath(), raw, changed.String())))
		})
	}
	require.NoError(t, evalExample(`
let u = URL("https://user:p%40ss@example.com:00443/a")
assert { u.withHostname("::ffff:192.0.2.128").string == "https://user:p%40ss@[::ffff:192.0.2.128]:00443/a" }
assert { u.withHostname("fe80::1%eth0").hostname == "fe80::1%eth0" }
assert { u.withHostname("fe80::1%eth0").withPort(null).string == "https://user:p%40ss@[fe80::1%25eth0]/a" }
assert { URL("//").host == null }
assert { URL("//").hostname == null }
assert { URL("//").port == null }
assert { URL("//").withFragment("").string == "//#" }
assert { URL("///a").withQuery([:]).string == "///a?" }
`))
	for _, host := range []string{"::ffff:192.0.2.999", "2001:db8:::1", "[::1]", "fe80::1%eth/0", "example.com%zone"} {
		t.Run(host, func(t *testing.T) {
			require.Error(t, evalExample(fmt.Sprintf(`URL("").withHostname(%q)`, host)))
		})
	}
}

func TestURLMalformedEscapes(t *testing.T) {
	for _, escaped := range []string{"%", "%0", "%GG", "%0g", "%g0", "ok%20bad%", "%é"} {
		t.Run(escaped, func(t *testing.T) {
			for _, raw := range []string{"/" + escaped, "#" + escaped, "http://user" + escaped + "@example.com/", "http://example.com" + escaped + "/"} {
				require.ErrorContains(t, evalExample(fmt.Sprintf(`URL(%q)`, raw)), "invalid URL")
			}
			// Query validation is deliberately lazy, unlike path and fragment.
			raw := "?valid=ok&broken=" + escaped
			require.NoError(t, evalExample(fmt.Sprintf(`assert { URL(%q).rawQuery == %q }`, raw, raw[1:])))
			for _, raw := range []string{raw, "?" + escaped + "=value"} {
				require.ErrorContains(t, evalExample(fmt.Sprintf(`URL(%q).query`, raw)), "invalid URL query")
			}
		})
	}
	for _, raw := range []string{"http://[::1", "http://[::1]:abc/", "http://[::1]:80:90/", "http://[::1]::/", "http://[::1]:abc:90/", "http://[fe80::1%eth0]/", "http://[fe80::1%25eth%GG]/", "http://user name@example.com/", "1http://example.com/", ":relative"} {
		t.Run(raw, func(t *testing.T) {
			require.ErrorContains(t, evalExample(fmt.Sprintf(`URL(%q)`, raw)), "invalid URL")
		})
	}
}

func TestURLScalarBoundaries(t *testing.T) {
	require.NoError(t, evalExample(`
let raw = "HTTPS://user:p%40ss@[::1]:00443/a%2fb?x=a+b#"
let constructed = URL(raw)
let literal: URL! = "HTTPS://user:p%40ss@[::1]:00443/a%2fb?x=a+b#"
let cast = raw :: URL!
let decoded: URL! = JSON.decode(JSON.encode(raw))
assert { constructed == literal }
assert { literal == cast }
assert { cast == decoded }
assert { JSON.encode(constructed) == JSON.encode(raw) }
let yaml: URL! = YAML.decode(YAML.encode(constructed))
assert { yaml == constructed }
let record: {{url: URL!}} = TOML.decode(TOML.encode({{url: constructed}}))
assert { record.url == constructed }
let absent: URL = JSON.decode("null")
assert { absent == null }
let values: [URL!]! = JSON.decode('["/a%2Fb", "?#", ""]')
assert { values[0].path == "/a/b" }
assert { values[1].rawQuery == "" }
assert { values[1].fragment == "" }
assert { values[2].string == "" }
`))
	for name, script := range map[string]string{
		"JSON invalid URL": `let u: URL! = JSON.decode('"https://example.com/%GG"')`,
		"YAML invalid URL": `let u: URL! = YAML.decode('https://example.com/%GG')`,
		"TOML invalid URL": `let u: {{url: URL!}} = TOML.decode('url = "https://example.com/%GG"')`,
		"JSON wrong shape": `let u: URL! = JSON.decode("42")`,
		"JSON non-null":    `let u: URL! = JSON.decode("null")`,
		"constructor null": `URL(null)`,
	} {
		t.Run(name, func(t *testing.T) {
			err := evalExample(script)
			if strings.Contains(name, "invalid URL") {
				require.ErrorContains(t, err, "invalid URL")
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestURLNullableDefaults(t *testing.T) {
	require.NoError(t, evalExample(`
let empty = URL("")
let scheme: String = empty.scheme
let host: String = empty.host
let hostname: String = empty.hostname
let port: Int = empty.port
let query: String = empty.rawQuery
let fragment: String = empty.fragment
assert { scheme == null }
assert { host == null }
assert { hostname == null }
assert { port == null }
assert { query == null }
assert { fragment == null }
assert { empty.path == "" }
assert { empty.escapedPath == "" }
assert { empty.query == [:] }
assert { !empty.isAbsolute }
assert { empty.withScheme(null).withHostname(null).withPort(null).withQuery(null).withFragment(null) == empty }
assert { empty.withQuery([:]).withFragment("").string == "?#" }
assert { URL("?#").withQuery(null).string == "#" }
assert { URL("?#").withFragment(null).string == "?" }
`))
}

func TestURLPreservesText(t *testing.T) {
	require.NoError(t, evalExample(`
let u: URL! = "HTTPS://foo/a%2fb"
let decoded: URL! = JSON.decode('"HTTPS://foo/a%2fb"')
assert { u == decoded }
assert { u != URL("https://foo/a/b") }
assert { u.path == "/a/b" }
assert { u.string == "HTTPS://foo/a%2fb" }
`))
}
