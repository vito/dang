package dang

import (
	"context"
	"fmt"
	"net/url"

	"github.com/vito/dang/v2/pkg/hm"
)

// URLType is a URL reference validated by net/url.Parse. Like other string
// scalars, it retains its source text rather than a separately encoded value.
var URLType = NewType("URL", ScalarKind)

const urlConstructorArg = "raw"

// Shared by the Prelude scheme and the runtime ScalarConstructor.
func urlConstructorType() *hm.FunctionType {
	args := NewRecordType("", Keyed[*hm.Scheme]{
		Key:   urlConstructorArg,
		Value: hm.NewScheme(nil, NonNull(StringType)),
	})
	return hm.NewFnType(args, NonNull(URLType))
}

func parseURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}
	return parsed, nil
}

func registerURL() {
	URLType.SetTypeDocString("a URL reference parsed with Go net/url.Parse; accepts absolute, relative, empty, and opaque references, preserving the original text without normalization")
	URLType.SetGoScalarHook(func(raw string) (string, error) {
		if _, err := parseURL(raw); err != nil {
			return "", err
		}
		return raw, nil
	})

	// Parsing is cheap and each result is private to the call; the scalar
	// remains a plain ScalarValue with no shared mutable parsed representation.
	for _, component := range []struct {
		name    string
		doc     string
		example string
		get     func(*url.URL) string
	}{
		{
			name:    "scheme",
			doc:     "the lower-case URL scheme without its colon, or an empty string when absent",
			example: `URL("https://foo:1234/bar").scheme`,
			get:     func(u *url.URL) string { return u.Scheme },
		},
		{
			name:    "host",
			doc:     "the host with any explicit port and IPv6 brackets, excluding user information; empty when absent",
			example: `URL("https://foo:1234/bar").host`,
			get:     func(u *url.URL) string { return u.Host },
		},
		{
			name:    "hostname",
			doc:     "the host without its port or IPv6 brackets, or an empty string when absent",
			example: `URL("https://foo:1234/bar").hostname`,
			get:     (*url.URL).Hostname,
		},
		{
			name:    "port",
			doc:     "the explicit numeric port as a string, or an empty string when absent; no default port is inferred",
			example: `URL("https://foo:1234/bar").port`,
			get:     (*url.URL).Port,
		},
		{
			name:    "path",
			doc:     "the percent-decoded URL path, excluding query and fragment; empty when absent or the URL is opaque",
			example: `URL("https://foo/a%20b?x=1#section").path`,
			get:     func(u *url.URL) string { return u.Path },
		},
	} {
		Method(URLType, component.name).
			Doc(component.doc).
			Example(component.example).
			Returns(NonNull(StringType)).
			Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
				parsed, err := parseURL(self.(ScalarValue).Val)
				if err != nil {
					return nil, err
				}
				return ToValue(component.get(parsed))
			})
	}

	Method(URLType, "string").
		Doc("the original URL text, with its spelling and escapes preserved").
		Example(`URL("https://foo/a%20b").string`).
		Returns(NonNull(StringType)).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			return ToValue(self.(ScalarValue).Val)
		})
}
