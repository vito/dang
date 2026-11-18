package dang

import (
	"fmt"
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
			// Exercise registration, inference, scalar construction and method
			// dispatch as well as parsing. Every component is a non-null string.
			script := fmt.Sprintf(`
let u = URL(raw: %q)
let scheme: String! = u.scheme
let host: String! = u.host
let hostname: String! = u.hostname
let port: String! = u.port
let path: String! = u.path
assert { scheme == %q }
assert { host == %q }
assert { hostname == %q }
assert { port == %q }
assert { path == %q }
assert { u.string == %q }
assert { toString(u) == %q }
assert { u == %q }
assert { %q == u }
`, tc.raw, tc.scheme, tc.host, tc.hostname, tc.port, tc.path, tc.raw, tc.raw, tc.raw, tc.raw)
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
