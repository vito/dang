package dang

import (
	"context"
	"fmt"
	stdpath "path"
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
