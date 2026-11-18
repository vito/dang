package dang

import (
	"fmt"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
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
	m, err := readURLQuery("z=1&x=2&z=3&y=4")
	require.NoError(t, err)
	require.Equal(t, []string{"z", "x", "y"}, m.Keys)
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
 assert { URL("https://foo/a?x=1#").resolve(URL("")).string == "https://foo/a?x=1#" }
 assert { URL("https://foo/a").resolve(URL("other").appendPath([".."]).withFragment("")).string == "https://foo/other/%2E%2E#" }
 `))
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
