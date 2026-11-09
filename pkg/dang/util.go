package dang

import "strings"

// visOrPublic returns the parsed Visibility, defaulting to public when the
// optional `pub`/`let` keyword was omitted (val is nil). Inlined into the
// field-declaration rules rather than wrapped in a named grammar rule, because
// tree-sitter forbids a named rule that can match the empty string.
func visOrPublic(val any) Visibility {
	if val == nil {
		return PublicVisibility
	}
	return val.(Visibility)
}

func sliceOf[T any](val any) []T {
	if val == nil {
		return nil
	}
	if ts, ok := val.([]T); ok {
		return ts
	}
	anys := val.([]any)
	ts := make([]T, len(anys))
	for i, node := range anys {
		ts[i] = node.(T)
	}
	return ts
}

func sliceOfAppend[T any](val any, last any) []T {
	anys := val.([]any)
	ts := make([]T, len(anys))
	for i, node := range anys {
		ts[i] = node.(T)
	}
	if last != nil {
		ts = append(ts, last.(T))
	}
	return ts
}

func sliceOfPrepend[T any](first any, val any) []T {
	anys := val.([]any)
	ts := make([]T, len(anys))
	for i, node := range anys {
		ts[i] = node.(T)
	}
	if first != nil {
		ts = append([]T{first.(T)}, ts...)
	}
	return ts
}

// qualifierString joins the parsed qualifier segments of a qualified type
// reference (e.g. the ["Dagger"] in `... on Dagger.Editor`) into a
// dot-separated string. Returns "" when there is no qualifier.
func qualifierString(val any) string {
	anys, ok := val.([]any)
	if !ok || len(anys) == 0 {
		return ""
	}
	parts := make([]string, len(anys))
	for i, p := range anys {
		parts[i] = p.(string)
	}
	return strings.Join(parts, ".")
}
