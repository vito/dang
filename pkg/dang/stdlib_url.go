package dang

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/vito/dang/v2/pkg/hm"
)

// URLType retains the original spelling of a validated URL reference.
var URLType = NewType("URL", ScalarKind)

const urlConstructorArg = "raw"

func urlConstructorType() *hm.FunctionType {
	args := NewRecordType("", Keyed[*hm.Scheme]{Key: urlConstructorArg, Value: hm.NewScheme(nil, NonNull(StringType))})
	return hm.NewFnType(args, NonNull(URLType))
}

func parseURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}
	return parsed, nil
}

// net/url does not track explicitly empty authorities or fragments. Keep
// their presence beside each private parsed value, never in scalar state.
type urlReference struct {
	*url.URL
	fragmentPresent  bool
	authorityPresent bool
}

func readURL(raw string) (*urlReference, error) {
	u, err := parseURL(raw)
	if err != nil {
		return nil, err
	}
	rest := raw
	if u.Scheme != "" {
		rest = raw[len(u.Scheme)+1:]
	}
	authority := strings.HasPrefix(rest, "//")
	if authority && u.Scheme == "" {
		// Go treats ///path as a path-only reference, unlike RFC 3986's
		// empty authority plus /path. A temporary scheme selects its
		// authority parser without changing the original scalar spelling.
		u, err = parseURL("url:" + raw)
		if err != nil {
			return nil, err
		}
		u.Scheme = ""
	}
	u.OmitHost = !authority
	return &urlReference{URL: u, fragmentPresent: strings.Contains(raw, "#"), authorityPresent: authority}, nil
}

func (u *urlReference) value() (Value, error) {
	// URL.String protects a first-segment colon using ./, which changes Path.
	// Escape delimiters instead, including // without an authority.
	if u.Opaque == "" && (u.Scheme != "" || u.authorityPresent) {
		if u.Path != "" && !strings.HasPrefix(u.Path, "/") {
			return nil, fmt.Errorf("URL with a scheme or authority requires an absolute path; set an absolute path first")
		}
		// A percent-encoded leading slash is path data in a relative reference,
		// but String would make it opaque (scheme:) or insert an extra slash
		// (authority). Expose that slash without changing the decoded path.
		path := u.EscapedPath()
		if strings.HasPrefix(u.Path, "/") && !strings.HasPrefix(path, "/") {
			u.RawPath = "/" + path[3:]
		}
	}
	if u.Opaque == "" && !u.authorityPresent {
		path := u.EscapedPath()
		if strings.HasPrefix(path, "//") {
			path = "/%2F" + path[2:]
		}
		if u.Scheme == "" {
			first, rest, found := strings.Cut(path, "/")
			first = strings.ReplaceAll(first, ":", "%3A")
			path = first
			if found {
				path += "/" + rest
			}
		}
		u.RawPath = path
	}
	u.OmitHost = !u.authorityPresent
	raw := u.URL.String()
	if u.authorityPresent && u.Host == "" && u.User == nil {
		// URL.String omits // with an empty authority when the scheme or
		// path is empty. Serialize that explicit authority ourselves.
		tail := *u.URL
		tail.Scheme, tail.Path, tail.RawPath = "", "", ""
		raw = "//" + u.EscapedPath() + tail.String()
		if u.Scheme != "" {
			raw = u.Scheme + ":" + raw
		}
	}
	if u.fragmentPresent && u.Fragment == "" {
		raw += "#"
	}
	if _, err := parseURL(raw); err != nil {
		return nil, err
	}
	return ScalarValue{Val: raw, ScalarType: URLType}, nil
}

func (u *urlReference) hierarchical() error {
	if u.Opaque != "" {
		return fmt.Errorf("cannot edit the path or authority of an opaque URL")
	}
	return nil
}

func urlQueryType() hm.Type { return NonNull(ListOf(NonNull(StringType))) }

// Unlike url.ParseQuery, semicolons are data, and errors are never discarded.
func readURLQuery(raw string) (MapValue, error) {
	m := MapValue{Entries: map[string]Value{}, ValType: urlQueryType()}
	for _, pair := range strings.Split(raw, "&") {
		if pair == "" {
			continue
		}
		key, val, _ := strings.Cut(pair, "=")
		key, err := url.QueryUnescape(key)
		if err != nil {
			return m, fmt.Errorf("invalid URL query: %w", err)
		}
		val, err = url.QueryUnescape(val)
		if err != nil {
			return m, fmt.Errorf("invalid URL query: %w", err)
		}
		values := ListValue{ElemType: NonNull(StringType)}
		if prev, ok := m.Entries[key]; ok {
			values = prev.(ListValue)
		} else {
			m.Keys = append(m.Keys, key)
		}
		values.Elements = append(values.Elements, StringValue{Val: val})
		m.Entries[key] = values
	}
	return m, nil
}

func writeURLQuery(m MapValue) string {
	var pairs []string
	for _, key := range m.Keys {
		values := m.Entries[key].(ListValue).Elements
		for _, value := range values {
			pairs = append(pairs, url.QueryEscape(key)+"="+url.QueryEscape(value.(StringValue).Val))
		}
	}
	return strings.Join(pairs, "&")
}

func urlNullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func registerURL() {
	URLType.SetTypeDocString("a URL reference; preserves original text until edited, accepts relative and opaque references; query escapes are validated only when decoded query data is accessed or edited")
	URLType.SetGoScalarHook(func(raw string) (string, error) { _, err := parseURL(raw); return raw, err })
	for _, c := range []struct {
		name, doc, example string
		typ                hm.Type
		get                func(*urlReference) (any, error)
	}{
		{"scheme", "the lower-case scheme without its colon, or null when absent", `URL("https://foo").scheme`, StringType, func(u *urlReference) (any, error) { return urlNullableString(u.Scheme), nil }},
		{"host", "the host with explicit port and IPv6 brackets, excluding user information; null when empty", `URL("https://foo:1234").host`, StringType, func(u *urlReference) (any, error) { return urlNullableString(u.Host), nil }},
		{"hostname", "the hostname without port or IPv6 brackets; null when empty", `URL("https://foo:1234").hostname`, StringType, func(u *urlReference) (any, error) { return urlNullableString(u.Hostname()), nil }},
		{"port", "the explicit numeric port, or null when absent or empty; fails on Int overflow, with no network port range restriction", `URL("https://foo:1234").port`, IntType, func(u *urlReference) (any, error) {
			if u.Port() == "" {
				return nil, nil
			}
			n, err := strconv.Atoi(u.Port())
			if err != nil {
				return nil, fmt.Errorf("invalid URL port: %w", err)
			}
			return n, nil
		}},
		{"path", "the decoded path; empty when absent or opaque", `URL("/a%20b").path`, NonNull(StringType), func(u *urlReference) (any, error) { return u.Path, nil }},
		{"escapedPath", "the escaped path, preserving valid original escapes", `URL("/a%2Fb").escapedPath`, NonNull(StringType), func(u *urlReference) (any, error) { return u.EscapedPath(), nil }},
		{"rawQuery", "the undecoded query without ?, null when absent and empty for an explicit ?; malformed query escapes are retained", `URL("/?q=%zz").rawQuery`, StringType, func(u *urlReference) (any, error) {
			if u.RawQuery == "" && !u.ForceQuery {
				return nil, nil
			}
			return u.RawQuery, nil
		}},
		{"query", "the decoded query map, empty when absent; duplicate values and first-seen key order are preserved, + means space and semicolons are data; invalid percent escapes raise an error", `URL("/?q=one&q=two").query`, NonNull(MapOf(urlQueryType())), func(u *urlReference) (any, error) {
			return readURLQuery(u.RawQuery)
		}},
		{"fragment", "the decoded fragment without #, null when absent and empty for an explicit #", `URL("/#a%20b").fragment`, StringType, func(u *urlReference) (any, error) {
			if !u.fragmentPresent {
				return nil, nil
			}
			return u.Fragment, nil
		}},
		{"isAbsolute", "whether a scheme is present, including opaque URLs", `URL("mailto:a@example.com").isAbsolute`, NonNull(BooleanType), func(u *urlReference) (any, error) { return u.IsAbs(), nil }},
	} {
		Method(URLType, c.name).Doc(c.doc).Example(c.example).Returns(c.typ).Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			u, err := readURL(self.(ScalarValue).Val)
			if err != nil {
				return nil, err
			}
			v, err := c.get(u)
			if err != nil {
				return nil, err
			}
			return ToValue(v)
		})
	}
	Method(URLType, "string").Doc("the exact original text, or the serialization of the latest edit; edits need not preserve all original spelling").Example(`URL("https://foo/a%20b").string`).Returns(NonNull(StringType)).Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
		return ToValue(self.(ScalarValue).Val)
	})
	registerURLBuilders()
}

func registerURLBuilders() {
	for _, b := range []struct {
		name, doc, example string
		params             []any
		edit               func(*urlReference, Args) error
	}{
		{"withScheme", "return a URL with a valid scheme, or null to clear it; rejects opaque URLs and nonempty relative paths without an authority", `URL("https://foo/a").withScheme("http")`, []any{"scheme", StringType}, func(u *urlReference, a Args) error {
			if err := u.hierarchical(); err != nil {
				return err
			}
			scheme := a.GetString("scheme")
			if !urlArgNull(a, "scheme") {
				p, err := url.Parse(scheme + ":")
				if err != nil || scheme == "" || p.Scheme == "" || p.Scheme != strings.ToLower(scheme) {
					return fmt.Errorf("invalid URL scheme %q", scheme)
				}
			}
			u.Scheme = strings.ToLower(scheme)
			return nil
		}},
		{"withHostname", "return a URL with a hostname (IPv6 accepted without brackets), preserving user information and port; null clears host, port and user information; rejects opaque URLs and nonempty relative paths", `URL("https://foo:80/a").withHostname("example.com")`, []any{"hostname", StringType}, func(u *urlReference, a Args) error {
			if err := u.hierarchical(); err != nil {
				return err
			}
			if urlArgNull(a, "hostname") {
				u.Host = ""
				u.User = nil
				u.authorityPresent = false
				return nil
			}
			host := a.GetString("hostname")
			if host == "" || strings.ContainsAny(host, "/?#@[]\\") {
				return fmt.Errorf("invalid URL hostname %q", host)
			}
			if strings.Contains(host, ":") {
				ip, _, _ := strings.Cut(host, "%")
				if net.ParseIP(ip) == nil {
					return fmt.Errorf("invalid IPv6 hostname %q", host)
				}
				host = "[" + host + "]"
			} else if strings.Contains(host, "%") {
				return fmt.Errorf("invalid URL hostname %q", host)
			}
			if port := u.Port(); port != "" {
				host += ":" + port
			}
			u.Host = host
			u.authorityPresent = true
			return nil
		}},
		{"withPort", "return a URL with an explicit nonnegative Int port, or null to clear it; requires a hostname when adding a port and rejects opaque URLs", `URL("https://foo").withPort(8443)`, []any{"port", IntType}, func(u *urlReference, a Args) error {
			if err := u.hierarchical(); err != nil {
				return err
			}
			host := u.Hostname()
			if strings.Contains(host, ":") {
				host = "[" + host + "]"
			}
			if !urlArgNull(a, "port") {
				port := a.GetInt("port")
				if port < 0 {
					return fmt.Errorf("URL port must be nonnegative")
				}
				if host == "" {
					return fmt.Errorf("URL port requires a hostname")
				}
				host += ":" + strconv.Itoa(port)
			}
			u.Host = host
			return nil
		}},
		{"withPath", "return a URL with a decoded path, escaping delimiters; rejects opaque URLs; authority paths must be empty or start with /", `URL("https://foo").withPath("/a b")`, []any{"path", NonNull(StringType)}, func(u *urlReference, a Args) error {
			if err := u.hierarchical(); err != nil {
				return err
			}
			u.Path = a.GetString("path")
			u.RawPath = ""
			if u.authorityPresent && u.Path != "" && !strings.HasPrefix(u.Path, "/") {
				return fmt.Errorf("URL authority path must start with /")
			}
			return nil
		}},
		{"appendPath", "append decoded segments as data, escaping slash, percent, query/fragment delimiters and dot segments; preserves existing escaped path and repeated slashes; rejects opaque URLs", `URL("/api/").appendPath(["users", "a/b"])`, []any{"segments", NonNull(ListOf(NonNull(StringType)))}, func(u *urlReference, a Args) error {
			if err := u.hierarchical(); err != nil {
				return err
			}
			path := u.EscapedPath()
			for i, v := range a.GetList("segments") {
				if (i > 0 || path != "" || u.authorityPresent) && (i > 0 || !strings.HasSuffix(path, "/")) {
					path += "/"
				}
				s := v.(StringValue).Val
				escaped := url.PathEscape(s)
				if s == "." || s == ".." {
					escaped = strings.ReplaceAll(s, ".", "%2E")
				}
				path += escaped
			}
			decoded, err := url.PathUnescape(path)
			if err != nil {
				return err
			}
			u.Path = decoded
			u.RawPath = path
			return nil
		}},
		{"withQuery", "replace the query map; null removes ?, an empty map adds ?, and empty value lists emit no pairs; preserves map iteration order", `URL("/search").withQuery(["q": ["hello world"]])`, []any{"query", MapOf(urlQueryType())}, func(u *urlReference, a Args) error {
			u.RawQuery = ""
			u.ForceQuery = !urlArgNull(a, "query")
			if !u.ForceQuery {
				return nil
			}
			u.RawQuery = writeURLQuery(a.Require("query").(MapValue))
			return nil
		}},
		{"withQueryParam", "replace all values of a decoded query key with one value; validates existing query escapes", `URL("/?q=old").withQueryParam("q", "new")`, []any{"name", NonNull(StringType), "value", NonNull(StringType)}, func(u *urlReference, a Args) error { return editURLQuery(u, a, false, false) }},
		{"withAddedQueryParam", "append a value to a decoded query key, preserving its previous values; validates existing query escapes", `URL("/?q=one").withAddedQueryParam("q", "two")`, []any{"name", NonNull(StringType), "value", NonNull(StringType)}, func(u *urlReference, a Args) error { return editURLQuery(u, a, true, false) }},
		{"withoutQueryParam", "remove a decoded query key and all its values; removes ? when no keys remain; validates existing query escapes", `URL("/?q=one").withoutQueryParam("q")`, []any{"name", NonNull(StringType)}, func(u *urlReference, a Args) error { return editURLQuery(u, a, false, true) }},
		{"withFragment", "replace the decoded fragment; null removes #, while an empty string adds #", `URL("/a").withFragment("section one")`, []any{"fragment", StringType}, func(u *urlReference, a Args) error {
			u.fragmentPresent = !urlArgNull(a, "fragment")
			u.Fragment = a.GetString("fragment")
			u.RawFragment = ""
			return nil
		}},
		{"resolve", "resolve a reference using RFC 3986 rules; rootless relative bases stay rootless and retain unresolved parent segments; an empty reference path preserves the base path", `URL("https://foo/a/b").resolve(URL("../c"))`, []any{"reference", NonNull(URLType)}, func(u *urlReference, a Args) error {
			ref, err := readURL(a.Require("reference").(ScalarValue).Val)
			if err != nil {
				return err
			}
			inheritQuery := ref.Scheme == "" && !ref.authorityPresent && ref.Path == "" && ref.Opaque == "" && !ref.ForceQuery && ref.RawQuery == ""
			authority := ref.authorityPresent
			if ref.Scheme == "" && !ref.authorityPresent {
				authority = u.authorityPresent
			}
			var result *url.URL
			if ref.authorityPresent && ref.Scheme == "" {
				// Force net/url's absolute-reference branch even when both the
				// hostname and user information are empty, so no base authority,
				// path, query or opaque payload can leak into a network reference.
				absolute := *ref.URL
				absolute.Scheme = "url"
				result = u.ResolveReference(&absolute)
				result.Scheme = u.Scheme
			} else {
				result = u.ResolveReference(ref.URL)
			}
			if ref.Scheme == "" && !ref.authorityPresent && ref.Opaque == "" && u.Opaque == "" {
				if ref.Path == "" {
					// An empty-path reference inherits the base path verbatim;
					// net/url otherwise roots and normalizes relative base paths.
					result.Path, result.RawPath = u.Path, u.RawPath
				} else if u.Scheme == "" && !u.authorityPresent && !strings.HasPrefix(u.EscapedPath(), "/") && !strings.HasPrefix(ref.EscapedPath(), "/") {
					path := resolveRootlessURLPath(u.EscapedPath(), ref.EscapedPath())
					result.Path, err = url.PathUnescape(path)
					if err != nil {
						return err
					}
					result.RawPath = path
				}
			}
			if inheritQuery {
				result.ForceQuery = u.ForceQuery
			}
			// RFC 3986 always takes the reference fragment, including absence.
			result.Fragment, result.RawFragment = ref.Fragment, ref.RawFragment
			u.fragmentPresent = ref.fragmentPresent
			u.authorityPresent = authority
			result.OmitHost = !authority
			u.URL = result
			return nil
		}},
	} {
		Method(URLType, b.name).Doc(b.doc).Example(b.example).Params(b.params...).Returns(NonNull(URLType)).Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			u, err := readURL(self.(ScalarValue).Val)
			if err != nil {
				return nil, err
			}
			if err = b.edit(u, args); err != nil {
				return nil, err
			}
			if b.name == "appendPath" && len(args.GetList("segments")) == 0 {
				return self, nil
			}
			return u.value()
		})
	}
}

// resolveRootlessURLPath extends reference resolution to relative bases.
// RFC 3986 assumes an absolute base: net/url inserts a root and consequently
// loses excess parent traversals. Keep those traversals so the result can
// later be resolved against an absolute base without changing its meaning.
// Work on escaped segments: %2E and %2F are data, not path operators.
func resolveRootlessURLPath(base, ref string) string {
	// A terminal dot segment denotes a directory, not a filename. Normalize
	// before selecting the base directory so ".." retains its parent step.
	base = normalizeRootlessURLPath(base)
	return normalizeRootlessURLPath(base[:strings.LastIndex(base, "/")+1] + ref)
}

func normalizeRootlessURLPath(path string) string {
	parts := strings.Split(path, "/")
	var stack []string
	for i, part := range parts {
		switch part {
		case ".":
			if i == len(parts)-1 {
				stack = append(stack, "")
			}
		case "..":
			if len(stack) > 0 && stack[len(stack)-1] != ".." {
				stack = stack[:len(stack)-1]
			} else {
				stack = append(stack, "..")
			}
			if i == len(parts)-1 {
				stack = append(stack, "")
			}
		default:
			stack = append(stack, part)
		}
	}
	path = strings.Join(stack, "/")
	if path == "" {
		return "./"
	}
	// Dot removal can expose an empty segment. Retain its slash without
	// turning a rootless result into an absolute path or network reference.
	if strings.HasPrefix(path, "/") {
		return "./" + path
	}
	return path
}

func urlArgNull(a Args, name string) bool {
	v, ok := a.Get(name)
	if !ok {
		return true
	}
	_, null := v.(NullValue)
	return null
}

func editURLQuery(u *urlReference, a Args, add, remove bool) error {
	m, err := readURLQuery(u.RawQuery)
	if err != nil {
		return err
	}
	key := a.GetString("name")
	if remove {
		m = m.Without(key)
	} else {
		values := ListValue{ElemType: NonNull(StringType)}
		if add {
			if prev, ok := m.Entries[key]; ok {
				values = prev.(ListValue)
			}
		}
		values.Elements = append(values.Elements, StringValue{Val: a.GetString("value")})
		m = m.With(key, values)
	}
	u.RawQuery = writeURLQuery(m)
	u.ForceQuery = len(m.Keys) > 0
	return nil
}
