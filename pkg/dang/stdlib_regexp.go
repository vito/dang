package dang

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/vito/dang/v2/pkg/hm"
)

// RegexpType is the "Regexp" scalar — backtick template strings auto-coerce
// to it via the scalar coercion path in materializeStringValue, where its
// Go-native new() hook compile-checks the pattern. At runtime a Regexp is a
// plain ScalarValue holding the pattern source; consumers reach the compiled
// form through compileRegexp's cache (see regexpArg).
var RegexpType = NewType("Regexp", ScalarKind)

// MatchType is the "Match" object returned by String regex methods. A Match
// is a plain object (see newMatch): its members are materialized as ordinary
// fields, so it needs no bespoke runtime value — field access resolves the
// bound value directly. The Method(MatchType, ...) registrations exist only to
// type-check those members and list them in the stdlib reference.
var MatchType = NewType("Match", ObjectKind)

// regexpConstructorArg is the parameter name of the Regexp(...) constructor.
const regexpConstructorArg = "pattern"

// regexpConstructorType is the type of the Regexp(...) constructor:
// String! -> Regexp!. Shared between the Prelude type-level scheme and the
// runtime ScalarConstructor binding so the two never drift.
func regexpConstructorType() *hm.FunctionType {
	args := NewRecordType("", Keyed[*hm.Scheme]{
		Key:   regexpConstructorArg,
		Value: hm.NewScheme(nil, NonNull(StringType)),
	})
	return hm.NewFnType(args, NonNull(RegexpType))
}

// regexpArg returns the compiled form of a Regexp-typed argument. The value
// is a plain ScalarValue whose string is the pattern source; materialization
// already compile-checked it, so this is a cache hit (or a cheap re-compile
// of a known-good pattern after a cache reset).
func regexpArg(args Args, name string) (*regexp.Regexp, error) {
	val, ok := args.Values[name].(ScalarValue)
	if !ok {
		return nil, fmt.Errorf("%s: expected Regexp, got %T", name, args.Values[name])
	}
	return compileRegexp(val.Val)
}

// newMatch builds the plain object returned for a successful regex match.
// indices comes straight from regexp.FindSubmatchIndex (pairs of byte
// offsets). A Match carries no behavior of its own: every member is computed
// here and bound as an ordinary field, so the value flows through normal
// object field access with no dedicated value type or dispatch special-case.
func newMatch(re *regexp.Regexp, src string, indices []int) *Object {
	m := NewObject(MatchType)

	m.Bind("string", StringValue{Val: src[indices[0]:indices[1]]}, PublicVisibility)
	m.Bind("start", IntValue{Val: indices[0]}, PublicVisibility)
	m.Bind("end", IntValue{Val: indices[1]}, PublicVisibility)

	// Positional captures: indices = [match_start, match_end, g1s, g1e, ...].
	// Skip the whole-match pair; emit "" for any -1 (unmatched) group.
	nCaps := (len(indices) - 2) / 2
	caps := make([]Value, nCaps)
	for i := range nCaps {
		start := indices[2+2*i]
		end := indices[2+2*i+1]
		if start < 0 {
			caps[i] = StringValue{Val: ""}
		} else {
			caps[i] = StringValue{Val: src[start:end]}
		}
	}
	m.Bind("captures", ListValue{Elements: caps, ElemType: NonNull(StringType)}, PublicVisibility)

	// Named captures: group name -> matched substring, or null for a named
	// group that did not participate in the match. Unnamed groups are omitted
	// (use captures for those). The value type is the nullable String, so an
	// unmatched group reads back as null — the same as indexing a missing key,
	// preserving the behavior of the old capture(name) lookup.
	names := re.SubexpNames()
	namedKeys := make([]string, 0, len(names))
	namedEntries := make(map[string]Value, len(names))
	for i, name := range names {
		if i == 0 || name == "" {
			continue // whole match, or an unnamed group
		}
		start := indices[2*i]
		end := indices[2*i+1]
		var v Value = NullValue{}
		if start >= 0 {
			v = StringValue{Val: src[start:end]}
		}
		if _, exists := namedEntries[name]; !exists {
			namedKeys = append(namedKeys, name)
		}
		namedEntries[name] = v
	}
	m.Bind("named", MapValue{Keys: namedKeys, Entries: namedEntries, ValType: StringType}, PublicVisibility)

	return m
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

	// The materialization hook: every string entering a Regexp slot must
	// compile. The pattern source is its own canonical form.
	RegexpType.SetGoScalarHook(func(raw string) (string, error) {
		if _, err := compileRegexp(raw); err != nil {
			return "", err
		}
		return raw, nil
	})

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
			re, err := regexpArg(args, "pattern")
			if err != nil {
				return nil, err
			}
			return BoolValue{Val: re.MatchString(s)}, nil
		})

	Method(StringType, "match").
		Doc("returns the first match for the pattern, or null").
		Example("\"x42y\".match(`\\d+`)").
		Params("pattern", NonNull(RegexpType)).
		Returns(MatchType).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			s := self.(StringValue).Val
			re, err := regexpArg(args, "pattern")
			if err != nil {
				return nil, err
			}
			idx := re.FindStringSubmatchIndex(s)
			if idx == nil {
				return NullValue{}, nil
			}
			return newMatch(re, s, idx), nil
		})

	Method(StringType, "matchAll").
		Doc("returns all non-overlapping matches for the pattern").
		Example("\"a1 b22 c333\".matchAll(`\\d+`)").
		Params("pattern", NonNull(RegexpType)).
		Returns(NonNull(ListOf(NonNull(MatchType)))).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			s := self.(StringValue).Val
			re, err := regexpArg(args, "pattern")
			if err != nil {
				return nil, err
			}
			all := re.FindAllStringSubmatchIndex(s, -1)
			out := make([]Value, len(all))
			for i, idx := range all {
				out[i] = newMatch(re, s, idx)
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
			re, err := regexpArg(args, "pattern")
			if err != nil {
				return nil, err
			}
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
			re, err := regexpArg(args, "pattern")
			if err != nil {
				return nil, err
			}
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
				res, err := callFunc(ctx, fn, newMatch(re, s, idx))
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
			re, err := regexpArg(args, "pattern")
			if err != nil {
				return nil, err
			}
			n := args.GetInt("limit")
			if n <= 0 {
				n = -1
			}
			parts := re.Split(s, n)
			return ToValue(parts)
		})
}

// registerRegexpMatchMethods declares Match's members for the type checker and
// the stdlib reference. The behavior lives in newMatch, which binds each member
// as a field on the match object; field access resolves those bindings directly
// and never reaches these impls. They are kept coherent — returning the bound
// member — so the registration stays a faithful description of the type.
func registerRegexpMatchMethods() {
	Method(MatchType, "string").
		Doc("the whole matched substring").
		Example("\"x42y\".match(`\\d+`).string").
		Returns(NonNull(StringType)).
		Impl(matchMember("string"))

	Method(MatchType, "start").
		Doc("byte offset of the match start in the source string").
		Example("\"x42y\".match(`\\d+`).start").
		Returns(NonNull(IntType)).
		Impl(matchMember("start"))

	Method(MatchType, "end").
		Doc("byte offset just past the end of the match").
		Example("\"x42y\".match(`\\d+`).end").
		Returns(NonNull(IntType)).
		Impl(matchMember("end"))

	Method(MatchType, "captures").
		Doc("positional captures, with `captures[0]` corresponding to $1").
		Example("\"42-99\".match(`(\\d+)-(\\d+)`).captures").
		Returns(NonNull(ListOf(NonNull(StringType)))).
		Impl(matchMember("captures"))

	Method(MatchType, "named").
		Doc("named captures keyed by group name; a key reads as null if that group did not match, and is absent for an unknown name").
		Example("\"555-1212\".match(`(?P<area>\\d{3})-(\\d{4})`).named[\"area\"]").
		Returns(NonNull(MapOf(StringType))).
		Impl(matchMember("named"))
}

// matchMember returns a builtin impl that yields a match object's already-bound
// member. See registerRegexpMatchMethods for why these are declaration-only.
func matchMember(name string) func(context.Context, Value, Args) (Value, error) {
	return func(ctx context.Context, self Value, _ Args) (Value, error) {
		v, _, err := self.(ValueScope).Lookup(ctx, name)
		return v, err
	}
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
