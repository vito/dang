package dang

import (
	"context"
	"fmt"
	stdpath "path"
	"strings"
)

// PathType is the "Path" scalar: a slash-separated path that is normalized on
// construction. Strings materialize into it through the scalar coercion path
// (string literals at value-handoff boundaries, `:: Path!` casts, and the
// Path(...) constructor), and materialization always path.Clean's the string,
// so an un-normalized Path value cannot exist and Path equality is semantic.
var PathType = NewType("Path", ScalarKind)

// newPathValue builds the runtime value for a Path: a generic ScalarValue
// holding the path.Clean'ed string. Cleaning here — the only place Path
// values are born — is what guarantees the normalization invariant.
func newPathValue(s string) ScalarValue {
	return ScalarValue{Val: stdpath.Clean(s), ScalarType: PathType}
}

// pathSelf returns the receiver's path string inside a Path method impl.
func pathSelf(self Value) string {
	return self.(ScalarValue).Val
}

// pathArg returns a Path!-typed argument's path string.
func pathArg(args Args, name string) string {
	return args.Values[name].(ScalarValue).Val
}

// pathExtension returns the extension of a path's final element, without the
// dot. A leading dot alone (".bashrc") does not begin an extension.
func pathExtension(name string) (string, bool) {
	idx := strings.LastIndex(name, ".")
	if idx <= 0 {
		return "", false
	}
	return name[idx+1:], true
}

func registerPath() {
	PathType.SetTypeDocString("a slash-separated path, normalized on construction")

	// Path(path: String!) -> Path!
	Builtin("Path").
		Doc("constructs a Path from a string, normalizing away `.` and `..` segments and redundant slashes").
		Example(`Path("a//b/../c")`).
		Params("path", NonNull(StringType)).
		Returns(NonNull(PathType)).
		Impl(func(ctx context.Context, args Args) (Value, error) {
			return newPathValue(args.GetString("path")), nil
		})

	Method(PathType, "name").
		Doc("the last element of the path").
		Example(`Path("/usr/local/bin").name`).
		Returns(NonNull(StringType)).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			return ToValue(stdpath.Base(pathSelf(self)))
		})

	Method(PathType, "stem").
		Doc("the last element of the path with its extension removed").
		Example(`Path("src/app.tar.gz").stem`).
		Returns(NonNull(StringType)).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			base := stdpath.Base(pathSelf(self))
			if idx := strings.LastIndex(base, "."); idx > 0 {
				base = base[:idx]
			}
			return ToValue(base)
		})

	Method(PathType, "extension").
		Doc("the extension of the last path element without its dot, or null when there is none; a leading dot alone (\".bashrc\") does not begin an extension").
		Example(`Path("src/app.tar.gz").extension`).
		Returns(StringType).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			ext, ok := pathExtension(stdpath.Base(pathSelf(self)))
			if !ok {
				return NullValue{}, nil
			}
			return ToValue(ext)
		})

	Method(PathType, "parent").
		Doc("the path with its last element removed; the parent of the root is the root, and the parent of a bare name is `.`").
		Example(`Path("/usr/local/bin").parent`).
		Returns(NonNull(PathType)).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			return newPathValue(stdpath.Dir(pathSelf(self))), nil
		})

	Method(PathType, "parts").
		Doc("the path's segments, excluding the root; the empty path `.` has no segments").
		Example(`Path("/usr/local/bin").parts`).
		Returns(NonNull(ListOf(NonNull(StringType)))).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			p := strings.TrimPrefix(pathSelf(self), "/")
			if p == "" || p == "." {
				return ToValue([]string{})
			}
			return ToValue(strings.Split(p, "/"))
		})

	Method(PathType, "isAbsolute").
		Doc("whether the path is absolute").
		Example(`Path("/etc").isAbsolute`).
		Returns(NonNull(BooleanType)).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			return ToValue(stdpath.IsAbs(pathSelf(self)))
		})

	Method(PathType, "join").
		Doc("the path extended with another path, normalized").
		Example(`Path("a").join("b/c")`).
		Params("other", NonNull(PathType)).
		Returns(NonNull(PathType)).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			return newPathValue(stdpath.Join(pathSelf(self), pathArg(args, "other"))), nil
		})

	Method(PathType, "relativeTo").
		Doc("the path expressed relative to base, or null when that is not expressible (mixing absolute and relative paths, or base escaping upward through `..`)").
		Example(`Path("/usr/local/bin").relativeTo("/usr")`).
		Params("base", NonNull(PathType)).
		Returns(PathType).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			rel, err := pathRel(pathArg(args, "base"), pathSelf(self))
			if err != nil {
				return NullValue{}, nil
			}
			return newPathValue(rel), nil
		})

	Method(PathType, "contains").
		Doc("whether other is the same path or lexically contained within it; mixing absolute and relative paths is never containment").
		Example(`Path("/usr/local").contains("/usr/local/bin")`).
		Params("other", NonNull(PathType)).
		Returns(NonNull(BooleanType)).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			return ToValue(pathContains(pathSelf(self), pathArg(args, "other")))
		})

	Method(PathType, "matches").
		Doc("whether the path matches the shell-style glob pattern; raises when the pattern is invalid").
		Example(`Path("main.dang").matches("*.dang")`).
		Params("pattern", NonNull(StringType)).
		Returns(NonNull(BooleanType)).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			pattern := args.GetString("pattern")
			matched, err := stdpath.Match(pattern, pathSelf(self))
			if err != nil {
				return nil, fmt.Errorf("matches: invalid pattern %q", pattern)
			}
			return ToValue(matched)
		})

	Method(PathType, "string").
		Doc("the path as a plain String").
		Example(`Path("a//b/./c").string`).
		Returns(NonNull(StringType)).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			return ToValue(pathSelf(self))
		})
}

// pathContains reports whether target is equal to base or contained within
// it, comparing cleaned paths lexically.
func pathContains(base, target string) bool {
	rel, err := pathRel(base, target)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, "../"))
}

// pathRel is filepath.Rel adapted to Go's slash-only path package semantics.
func pathRel(basepath, targpath string) (string, error) {
	base := stdpath.Clean(basepath)
	targ := stdpath.Clean(targpath)
	if targ == base {
		return ".", nil
	}
	if stdpath.IsAbs(base) != stdpath.IsAbs(targ) {
		return "", fmt.Errorf("can't make %q relative to %q", targpath, basepath)
	}
	if base == "." {
		base = ""
	}

	bl := len(base)
	tl := len(targ)
	var b0, bi, t0, ti int
	for {
		for bi < bl && base[bi] != '/' {
			bi++
		}
		for ti < tl && targ[ti] != '/' {
			ti++
		}
		if targ[t0:ti] != base[b0:bi] {
			break
		}
		if bi < bl {
			bi++
		}
		if ti < tl {
			ti++
		}
		b0 = bi
		t0 = ti
	}
	if base[b0:bi] == ".." {
		return "", fmt.Errorf("can't make %q relative to %q", targpath, basepath)
	}
	if b0 != bl {
		seps := strings.Count(base[b0:bl], "/")
		size := 2 + seps*3
		if tl != t0 {
			size += 1 + tl - t0
		}
		buf := make([]byte, size)
		n := copy(buf, "..")
		for range seps {
			buf[n] = '/'
			copy(buf[n+1:], "..")
			n += 3
		}
		if t0 != tl {
			buf[n] = '/'
			copy(buf[n+1:], targ[t0:])
		}
		return string(buf), nil
	}
	return targ[t0:], nil
}
