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

// Ordinary URL behavior lives in tests/test_url*.dang. Go retains independent
// net/url differential oracles and benchmarks, whose compile-once harness keeps
// parser/type-checker costs and fixture generation outside the measured work.

// Go net/url independently checks the 46k-case rootless composition matrix.
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

// Compile once to measure the public API, not parsing and inference per case.
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

// Compile-once harness shared by differential oracles and benchmarks.
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

// Generate benchmark input outside the timed public-API operation.
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

// Go net/url supplies independent escaping expectations, including invalid UTF-8.
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
}
