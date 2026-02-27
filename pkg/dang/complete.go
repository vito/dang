package dang

import (
	"context"
	"strings"

	"github.com/vito/dang/pkg/hm"
)

// Completion represents a single completion suggestion.
type Completion struct {
	Label         string // The text to insert
	Detail        string // Type signature or short description
	Documentation string // Longer doc string
	IsFunction    bool   // Whether this is a function/method
	IsArg         bool   // Whether this is a function argument
}

func isIdentByte(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_'
}

// completeMember tries to parse and type-check the receiver expression, then
// returns completions for its members filtered by the partial name.
func completeMember(ctx context.Context, typeEnv Env, receiverText, partial string) []Completion {
	receiverType := InferReceiverType(ctx, typeEnv, receiverText)
	if receiverType == nil {
		return nil
	}

	return MembersOf(receiverType, partial)
}

// completeArgs returns completions for function arguments given the function
// expression text, the partial argument name being typed, and the set of
// argument names already provided in the call.
func completeArgs(ctx context.Context, typeEnv Env, funcExpr, partial string, alreadyProvided []string) []Completion {
	t := InferReceiverType(ctx, typeEnv, funcExpr)
	if t == nil {
		return nil
	}
	return ArgsOf(t, partial, alreadyProvided)
}

// ArgsOf returns completions for the arguments of a function type, filtered
// by the partial prefix and excluding already-provided argument names. This
// is the shared logic used by both the LSP and REPL for argument completion.
// The type is unwrapped from NonNull and must be a *hm.FunctionType with a
// RecordType argument.
func ArgsOf(t hm.Type, partial string, alreadyProvided []string) []Completion {
	// Unwrap NonNull
	if nn, ok := t.(hm.NonNullType); ok {
		t = nn.Type
	}

	ft, ok := t.(*hm.FunctionType)
	if !ok {
		return nil
	}

	argType := ft.Arg()
	if argType == nil {
		return nil
	}

	// Unwrap NonNull on arg type
	if nn, ok := argType.(hm.NonNullType); ok {
		argType = nn.Type
	}

	record, ok := argType.(*RecordType)
	if !ok {
		return nil
	}

	provided := make(map[string]bool, len(alreadyProvided))
	for _, name := range alreadyProvided {
		provided[name] = true
	}

	partialLower := strings.ToLower(partial)
	var completions []Completion

	for _, field := range record.Fields {
		if provided[field.Key] {
			continue
		}
		if partial != "" && !strings.HasPrefix(strings.ToLower(field.Key), partialLower) {
			continue
		}

		fieldType, _ := field.Value.Type()
		typeStr := fieldType.String()

		var doc string
		if record.DocStrings != nil {
			doc = record.DocStrings[field.Key]
		}

		completions = append(completions, Completion{
			Label:         field.Key,
			Detail:        typeStr,
			Documentation: doc,
			IsArg:         true,
		})
	}

	return completions
}

// InferReceiverType parses and type-checks a receiver expression, returning
// its inferred type. Returns nil if parsing or inference fails.
func InferReceiverType(ctx context.Context, typeEnv Env, expr string) hm.Type {
	parsed, err := Parse("completion", []byte(expr))
	if err != nil {
		return nil
	}

	block, ok := parsed.(*ModuleBlock)
	if !ok || len(block.Forms) == 0 {
		return nil
	}

	// Type-check the expression in a cloned env to avoid mutating the original
	fresh := hm.NewSimpleFresher()
	_, err = InferFormsWithPhases(ctx, block.Forms, typeEnv.Clone(), fresh)
	if err != nil {
		return nil
	}

	// Get the inferred type of the last form
	lastForm := block.Forms[len(block.Forms)-1]
	t := lastForm.GetInferredType()
	if t == nil {
		return nil
	}

	return t
}

// MembersOf returns completions for the public members of a type, filtered
// by the partial prefix. This is the shared logic used by both the LSP and
// REPL for dot-completion.
func MembersOf(t hm.Type, partial string) []Completion {
	// Unwrap NonNull
	if nn, ok := t.(hm.NonNullType); ok {
		t = nn.Type
	}

	// Map built-in scalar and list types to their method module so that
	// completions show e.g. split for strings and reduce for lists.
	module, ok := t.(Env)
	if !ok {
		if mod := builtinModuleFor(t); mod != nil {
			module = mod
		} else {
			return nil
		}
	}

	partialLower := strings.ToLower(partial)
	var completions []Completion

	for name, scheme := range module.Bindings(PublicVisibility) {
		if partial != "" && !strings.HasPrefix(strings.ToLower(name), partialLower) {
			continue
		}

		memberType, _ := scheme.Type()
		_, isFn := memberType.(*hm.FunctionType)

		var doc string
		if d, found := module.GetDocString(name); found {
			doc = d
		}

		completions = append(completions, Completion{
			Label:         name,
			Detail:        memberType.String(),
			Documentation: doc,
			IsFunction:    isFn,
		})
	}

	return completions
}

// builtinModuleFor returns the builtin method module for a primitive or list
// type, or nil if the type has no builtin methods.
func builtinModuleFor(t hm.Type) *Module {
	switch t.(type) {
	case ListType:
		return ListTypeModule
	}
	if mod, ok := t.(*Module); ok {
		switch mod {
		case StringType, IntType, FloatType, BooleanType:
			return mod
		}
	}
	return nil
}

// completeLexical returns completions from the type environment matching a prefix.
// It filters out type definitions (scalars, enums, etc.) and ID types, which
// are not useful as standalone expressions.
func completeLexical(typeEnv Env, prefix string) []Completion {
	if prefix == "" {
		return nil
	}

	prefixLower := strings.ToLower(prefix)
	var completions []Completion

	for name, scheme := range typeEnv.Bindings(PublicVisibility) {
		if !strings.HasPrefix(strings.ToLower(name), prefixLower) {
			continue
		}
		if IsTypeDefBinding(scheme) || IsIDTypeName(name) {
			continue
		}

		memberType, _ := scheme.Type()
		_, isFn := memberType.(*hm.FunctionType)

		var doc string
		if d, found := typeEnv.GetDocString(name); found {
			doc = d
		}

		completions = append(completions, Completion{
			Label:         name,
			Detail:        memberType.String(),
			Documentation: doc,
			IsFunction:    isFn,
		})
	}

	return completions
}

// IsTypeDefBinding returns true if the scheme represents a type definition
// (scalar, enum, input, interface, union) rather than a callable field.
func IsTypeDefBinding(scheme *hm.Scheme) bool {
	t, _ := scheme.Type()
	if nn, ok := t.(hm.NonNullType); ok {
		t = nn.Type
	}
	mod, ok := t.(*Module)
	if !ok {
		return false
	}
	switch mod.Kind {
	case ScalarKind, EnumKind, InputKind, InterfaceKind, UnionKind:
		return true
	}
	return false
}

// IsIDTypeName returns true for GraphQL ID scalar type names (e.g.
// "ContainerID", "DirectoryID") which are internal plumbing.
func IsIDTypeName(name string) bool {
	return len(name) > 2 && strings.HasSuffix(name, "ID")
}
