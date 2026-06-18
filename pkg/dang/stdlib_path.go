package dang

import (
	"context"
	"fmt"
	stdpath "path"
	"strings"
)

// PathModule is the "Path" namespace for slash-separated path manipulation.
var PathModule = NewType("Path", ObjectKind)

// PathSplitType is the object returned by Path.split.
var PathSplitType = NewType("Split", ObjectKind)

func registerPath() {
	PathModule.SetTypeDocString("functions for manipulating slash-separated paths")
	PathSplitType.SetTypeDocString("the directory and file parts returned by Path.split")
	PathModule.AddObject("Split", PathSplitType)
	registerPathSplitMethods()

	// Path.base(path: String!) -> String!
	StaticMethod(PathModule, "base").
		Doc("returns the last element of path, after removing trailing slashes").
		Example(`Path.base("/usr/local/bin")`).
		Params("path", NonNull(StringType)).
		Returns(NonNull(StringType)).
		Impl(func(ctx context.Context, args Args) (Value, error) {
			return ToValue(stdpath.Base(args.GetString("path")))
		})

	// Path.clean(path: String!) -> String!
	StaticMethod(PathModule, "clean").
		Doc("returns the shortest slash-separated path name equivalent to path").
		Example(`Path.clean("/a/b/../c//")`).
		Params("path", NonNull(StringType)).
		Returns(NonNull(StringType)).
		Impl(func(ctx context.Context, args Args) (Value, error) {
			return ToValue(stdpath.Clean(args.GetString("path")))
		})

	// Path.contains(base: String!, path: String!) -> Boolean!
	StaticMethod(PathModule, "contains").
		Doc("reports whether path is equal to base or contained within base after cleaning both paths").
		Example(`Path.contains("/usr/local", "/usr/local/bin")`).
		Params("base", NonNull(StringType), "path", NonNull(StringType)).
		Returns(NonNull(BooleanType)).
		Impl(func(ctx context.Context, args Args) (Value, error) {
			contained := pathContains(args.GetString("base"), args.GetString("path"))
			return ToValue(contained)
		})

	// Path.dir(path: String!) -> String!
	StaticMethod(PathModule, "dir").
		Doc("returns all but the last element of path, typically after cleaning").
		Example(`Path.dir("/usr/local/bin")`).
		Params("path", NonNull(StringType)).
		Returns(NonNull(StringType)).
		Impl(func(ctx context.Context, args Args) (Value, error) {
			return ToValue(stdpath.Dir(args.GetString("path")))
		})

	// Path.ext(path: String!) -> String!
	StaticMethod(PathModule, "ext").
		Doc("returns the file name extension used by path").
		Example(`Path.ext("archive.tar.gz")`).
		Params("path", NonNull(StringType)).
		Returns(NonNull(StringType)).
		Impl(func(ctx context.Context, args Args) (Value, error) {
			return ToValue(stdpath.Ext(args.GetString("path")))
		})

	// Path.isAbs(path: String!) -> Boolean!
	StaticMethod(PathModule, "isAbs").
		Doc("reports whether path is absolute").
		Example(`Path.isAbs("/usr/local/bin")`).
		Params("path", NonNull(StringType)).
		Returns(NonNull(BooleanType)).
		Impl(func(ctx context.Context, args Args) (Value, error) {
			return ToValue(stdpath.IsAbs(args.GetString("path")))
		})

	// Path.join(elements: [String!]!) -> String!
	StaticMethod(PathModule, "join").
		Doc("joins any number of path elements into a single clean slash-separated path").
		Example(`Path.join(["a", "b", "c.txt"])`).
		Params("elements", NonNull(ListOf(NonNull(StringType)))).
		Returns(NonNull(StringType)).
		Impl(func(ctx context.Context, args Args) (Value, error) {
			elements := args.GetList("elements")
			parts := make([]string, len(elements))
			for i, elem := range elements {
				str, ok := elem.(StringValue)
				if !ok {
					return nil, fmt.Errorf("Path.join: element %d must be String!, got %T", i, elem)
				}
				parts[i] = str.Val
			}
			return ToValue(stdpath.Join(parts...))
		})

	// Path.match(pattern: String!, name: String!) -> Boolean!
	StaticMethod(PathModule, "match").
		Doc("reports whether name matches the shell-style path pattern").
		Example(`Path.match("*.dang", "main.dang")`).
		Params("pattern", NonNull(StringType), "name", NonNull(StringType)).
		Returns(NonNull(BooleanType)).
		Impl(func(ctx context.Context, args Args) (Value, error) {
			matched, err := stdpath.Match(args.GetString("pattern"), args.GetString("name"))
			if err != nil {
				return nil, fmt.Errorf("Path.match: %w", err)
			}
			return ToValue(matched)
		})

	// Path.rel(base: String!, path: String!) -> String!
	StaticMethod(PathModule, "rel").
		Doc("returns a relative path lexically equivalent to path when joined to base, using slash-separated paths").
		Example(`Path.rel("/usr/local", "/usr/local/bin")`).
		Params("base", NonNull(StringType), "path", NonNull(StringType)).
		Returns(NonNull(StringType)).
		Impl(func(ctx context.Context, args Args) (Value, error) {
			rel, err := pathRel(args.GetString("base"), args.GetString("path"))
			if err != nil {
				return nil, fmt.Errorf("Path.rel: %w", err)
			}
			return ToValue(rel)
		})

	// Path.split(path: String!) -> Path.Split!
	StaticMethod(PathModule, "split").
		Doc("splits path immediately after the final slash, separating it into dir and file fields").
		Example(`Path.split("a/b/c.txt").file`).
		Params("path", NonNull(StringType)).
		Returns(NonNull(PathSplitType)).
		Impl(func(ctx context.Context, args Args) (Value, error) {
			dir, file := stdpath.Split(args.GetString("path"))
			result := NewObject(PathSplitType)
			result.Bind("dir", StringValue{Val: dir}, PublicVisibility)
			result.Bind("file", StringValue{Val: file}, PublicVisibility)
			return result, nil
		})
}

// registerPathSplitMethods declares Split's members for the type checker and
// the stdlib reference. Path.split binds these as ordinary fields on its result;
// these registrations keep docs/examples coherent with that shape.
func registerPathSplitMethods() {
	Method(PathSplitType, "dir").
		Doc("the path prefix ending immediately after the final slash, or empty if there is no slash").
		Example(`Path.split("a/b/c.txt").dir`).
		Returns(NonNull(StringType)).
		Impl(pathSplitMember("dir"))

	Method(PathSplitType, "file").
		Doc("the path suffix after the final slash").
		Example(`Path.split("a/b/c.txt").file`).
		Returns(NonNull(StringType)).
		Impl(pathSplitMember("file"))
}

func pathSplitMember(name string) func(context.Context, Value, Args) (Value, error) {
	return func(ctx context.Context, self Value, _ Args) (Value, error) {
		v, _, err := self.(ValueScope).Lookup(ctx, name)
		return v, err
	}
}

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
