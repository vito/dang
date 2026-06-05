package dang

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/vito/dang/pkg/hm"
)

// RegexpType is the "Regexp" scalar — backtick template strings auto-coerce
// to it via the scalar coercion path in materializeStringValue.
var RegexpType = NewType("Regexp", ScalarKind)

// MatchType is the "Match" object returned by String regex methods. Fields
// like .string and .captures are zero-arg methods that auto-call when
// accessed via dot notation.
var MatchType = NewType("Match", ObjectKind)

// RegexpValue is the runtime value for a compiled Regexp.
type RegexpValue struct {
	Re     *regexp.Regexp
	Source string
}

var _ Value = RegexpValue{}

func (r RegexpValue) Type() hm.Type  { return NonNull(RegexpType) }
func (r RegexpValue) String() string { return r.Source }
func (r RegexpValue) MarshalJSON() ([]byte, error) {
	return fmt.Appendf(nil, "%q", r.Source), nil
}

// MatchValue is the runtime value for a single regex match. Indices comes
// straight from regexp.FindSubmatchIndex (pairs of byte offsets); fields are
// derived lazily so we keep the cost of an unused match minimal.
type MatchValue struct {
	Re      *regexp.Regexp
	Src     string
	Indices []int
}

var _ Value = MatchValue{}

func (m MatchValue) Type() hm.Type { return NonNull(MatchType) }

func (m MatchValue) String() string {
	if len(m.Indices) < 2 || m.Indices[0] < 0 {
		return ""
	}
	return m.Src[m.Indices[0]:m.Indices[1]]
}

func (m MatchValue) MarshalJSON() ([]byte, error) {
	return fmt.Appendf(nil, "%q", m.String()), nil
}

// regexpCacheCap bounds the compiled-pattern cache. On overflow we reset
// instead of evicting one-at-a-time; the cost of a re-compile is small and
// any realistic program stays well under the bound.
const regexpCacheCap = 1024

var (
	regexpCacheMu sync.Mutex
	regexpCache   = make(map[string]*regexp.Regexp, 16)
)

func compileRegexp(pat string) (*regexp.Regexp, error) {
	regexpCacheMu.Lock()
	if re, ok := regexpCache[pat]; ok {
		regexpCacheMu.Unlock()
		return re, nil
	}
	regexpCacheMu.Unlock()

	re, err := regexp.Compile(pat)
	if err != nil {
		return nil, fmt.Errorf("invalid regexp %q: %w", pat, err)
	}

	regexpCacheMu.Lock()
	if len(regexpCache) >= regexpCacheCap {
		regexpCache = make(map[string]*regexp.Regexp, 16)
	}
	regexpCache[pat] = re
	regexpCacheMu.Unlock()
	return re, nil
}

func registerRegexp() {
	RegexpType.SetTypeDocString("a compiled regular expression (Go regexp/syntax)")
	MatchType.SetTypeDocString("the result of a successful regex match")

	registerRegexpStringMethods()
	registerRegexpMatchMethods()
}

func registerRegexpStringMethods() {
	Method(StringType, "containsMatch").
		Doc("reports whether the string contains a match for the regexp").
		Example("\"abc123\".containsMatch(`\\d+`)").
		Params("pattern", NonNull(RegexpType)).
		Returns(NonNull(BooleanType)).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			s := self.(StringValue).Val
			re := args.Values["pattern"].(RegexpValue).Re
			return BoolValue{Val: re.MatchString(s)}, nil
		})

	Method(StringType, "match").
		Doc("returns the first match for the pattern, or null").
		Example("\"x42y\".match(`\\d+`)").
		Params("pattern", NonNull(RegexpType)).
		Returns(MatchType).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			s := self.(StringValue).Val
			re := args.Values["pattern"].(RegexpValue).Re
			idx := re.FindStringSubmatchIndex(s)
			if idx == nil {
				return NullValue{}, nil
			}
			return MatchValue{Re: re, Src: s, Indices: idx}, nil
		})

	Method(StringType, "matchAll").
		Doc("returns all non-overlapping matches for the pattern").
		Example("\"a1 b22 c333\".matchAll(`\\d+`)").
		Params("pattern", NonNull(RegexpType)).
		Returns(NonNull(ListOf(NonNull(MatchType)))).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			s := self.(StringValue).Val
			re := args.Values["pattern"].(RegexpValue).Re
			all := re.FindAllStringSubmatchIndex(s, -1)
			out := make([]Value, len(all))
			for i, idx := range all {
				out[i] = MatchValue{Re: re, Src: s, Indices: idx}
			}
			return ListValue{Elements: out, ElemType: NonNull(MatchType)}, nil
		})

	Method(StringType, "replaceMatches").
		Doc("replaces matches of pattern with `with`; supports $0/$1/$name backref expansion").
		Example("\"a1b2\".replaceMatches(`\\d`, \"#\")").
		Params(
			"pattern", NonNull(RegexpType),
			"with", NonNull(StringType),
			"count", IntType, IntValue{Val: -1},
		).
		Returns(NonNull(StringType)).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			s := self.(StringValue).Val
			re := args.Values["pattern"].(RegexpValue).Re
			tpl := args.GetString("with")
			n := args.GetInt("count")

			if n < 0 {
				return StringValue{Val: re.ReplaceAllString(s, tpl)}, nil
			}
			return StringValue{Val: replaceFirstN(re, s, tpl, n)}, nil
		})

	Method(StringType, "rewriteMatches").
		Doc("replaces matches of pattern using the block to compute each replacement").
		Example("\"hello world\".rewriteMatches(`\\w+`) { m => m.string.toUpper }").
		Params(
			"pattern", NonNull(RegexpType),
			"count", IntType, IntValue{Val: -1},
		).
		Block(hm.NewFnType(
			NewRecordType("", Keyed[*hm.Scheme]{
				Key:   "match",
				Value: hm.NewScheme(nil, NonNull(MatchType)),
			}),
			NonNull(StringType),
		)).
		Returns(NonNull(StringType)).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			s := self.(StringValue).Val
			re := args.Values["pattern"].(RegexpValue).Re
			n := args.GetInt("count")
			if args.Block == nil {
				return nil, fmt.Errorf("rewriteMatches requires a block argument")
			}
			fn := *args.Block

			all := re.FindAllStringSubmatchIndex(s, -1)
			var out strings.Builder
			last := 0
			for i, idx := range all {
				if n >= 0 && i >= n {
					break
				}
				out.WriteString(s[last:idx[0]])
				res, err := callFunc(ctx, fn, MatchValue{Re: re, Src: s, Indices: idx})
				if err != nil {
					return nil, fmt.Errorf("rewriteMatches block: %w", err)
				}
				strVal, ok := res.(StringValue)
				if !ok {
					return nil, fmt.Errorf("rewriteMatches block must return String!, got %T", res)
				}
				out.WriteString(strVal.Val)
				last = idx[1]
			}
			out.WriteString(s[last:])
			return StringValue{Val: out.String()}, nil
		})

	Method(StringType, "splitMatches").
		Doc("splits the string by matches of pattern").
		Example("\"a1b22c\".splitMatches(`\\d+`)").
		Params(
			"pattern", NonNull(RegexpType),
			"limit", IntType, IntValue{Val: 0},
		).
		Returns(NonNull(ListOf(NonNull(StringType)))).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			s := self.(StringValue).Val
			re := args.Values["pattern"].(RegexpValue).Re
			n := args.GetInt("limit")
			if n <= 0 {
				n = -1
			}
			parts := re.Split(s, n)
			return ToValue(parts)
		})
}

func registerRegexpMatchMethods() {
	Method(MatchType, "string").
		Doc("the whole matched substring").
		Example("\"x42y\".match(`\\d+`).string").
		Returns(NonNull(StringType)).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			m := self.(MatchValue)
			return StringValue{Val: m.Src[m.Indices[0]:m.Indices[1]]}, nil
		})

	Method(MatchType, "start").
		Doc("byte offset of the match start in the source string").
		Example("\"x42y\".match(`\\d+`).start").
		Returns(NonNull(IntType)).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			return IntValue{Val: self.(MatchValue).Indices[0]}, nil
		})

	Method(MatchType, "end").
		Doc("byte offset just past the end of the match").
		Example("\"x42y\".match(`\\d+`).end").
		Returns(NonNull(IntType)).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			return IntValue{Val: self.(MatchValue).Indices[1]}, nil
		})

	Method(MatchType, "captures").
		Doc("positional captures, with `captures[0]` corresponding to $1").
		Example("\"42-99\".match(`(\\d+)-(\\d+)`).captures").
		Returns(NonNull(ListOf(NonNull(StringType)))).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			m := self.(MatchValue)
			// Indices = [match_start, match_end, g1_start, g1_end, g2_start, ...].
			// Skip the whole-match pair; emit "" for any -1 (unmatched) group.
			n := (len(m.Indices) - 2) / 2
			out := make([]Value, n)
			for i := range n {
				start := m.Indices[2+2*i]
				end := m.Indices[2+2*i+1]
				if start < 0 {
					out[i] = StringValue{Val: ""}
				} else {
					out[i] = StringValue{Val: m.Src[start:end]}
				}
			}
			return ListValue{Elements: out, ElemType: NonNull(StringType)}, nil
		})

	Method(MatchType, "capture").
		Doc("named capture; null if no such group or the group did not match").
		Example("\"555-1212\".match(`(?P<area>\\d{3})-(\\d{4})`).capture(\"area\")").
		Params("name", NonNull(StringType)).
		Returns(StringType).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			m := self.(MatchValue)
			name := args.GetString("name")
			for i, n := range m.Re.SubexpNames() {
				if n != name {
					continue
				}
				start := m.Indices[2*i]
				end := m.Indices[2*i+1]
				if start < 0 {
					return NullValue{}, nil
				}
				return StringValue{Val: m.Src[start:end]}, nil
			}
			return NullValue{}, nil
		})
}

// replaceFirstN replaces up to the first n matches of re in s, expanding
// Go-style backrefs ($0, $1, ${name}) using re.Expand.
func replaceFirstN(re *regexp.Regexp, s, tpl string, n int) string {
	if n == 0 {
		return s
	}
	all := re.FindAllStringSubmatchIndex(s, n)
	if len(all) == 0 {
		return s
	}
	var out []byte
	last := 0
	for _, idx := range all {
		out = append(out, s[last:idx[0]]...)
		out = re.ExpandString(out, tpl, s, idx)
		last = idx[1]
	}
	out = append(out, s[last:]...)
	return string(out)
}
