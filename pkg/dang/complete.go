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
}

// CompleteInput returns completions for the given REPL input text and cursor
// position. It handles both lexical completions (top-level names) and member
// completions (e.g. "container.fr" -> members of Container).
//
// typeEnv is the current type environment (with all bindings in scope).
func CompleteInput(ctx context.Context, typeEnv Env, input string, cursorPos int) []Completion {
	if cursorPos > len(input) {
		cursorPos = len(input)
	}
	text := input[:cursorPos]

	// Check if we're in a dotted expression (member access)
	dotIdx, receiver, partial := splitDotExpr(text)
	if dotIdx >= 0 {
		return completeMember(ctx, typeEnv, receiver, partial)
	}

	// Otherwise, return lexical completions
	return completeLexical(typeEnv, lastIdent(text))
}

// splitDotExpr checks if text ends with a dotted expression like "foo.bar.ba".
// Returns the index of the last dot, the receiver text ("foo.bar"), and the
// partial member name ("ba"). Returns dotIdx=-1 if there's no dot expression.
//
// It handles chained method calls with arguments, e.g.
// "apko.wolfi(["go"]).std" -> receiver="apko.wolfi(["go"])", partial="std".
func splitDotExpr(text string) (dotIdx int, receiver, partial string) {
	// Find the last dot that's part of an identifier chain
	i := len(text) - 1

	// Walk back over the partial member name
	for i >= 0 && isIdentByte(text[i]) {
		i--
	}

	if i < 0 || text[i] != '.' {
		return -1, "", ""
	}

	dotIdx = i
	partial = text[dotIdx+1:]
	receiver = text[:dotIdx]

	// Walk back further to find the start of the receiver expression.
	// Handle identifier chars, dots, and balanced parens/brackets for
	// chained calls like a.b(args).c or a.b([x, y]).c.
	j := dotIdx - 1
	for j >= 0 {
		c := text[j]
		if isIdentByte(c) || c == '.' {
			j--
		} else if c == ')' || c == ']' {
			// Walk back over the balanced group
			open := matchingOpen(c)
			depth := 1
			j--
			for j >= 0 && depth > 0 {
				switch text[j] {
				case c:
					depth++
				case open:
					depth--
				}
				j--
			}
			// After the loop j is one before the opening bracket, which
			// is correct for the next iteration.
		} else {
			break
		}
	}
	receiver = text[j+1 : dotIdx]

	if receiver == "" {
		return -1, "", ""
	}

	return dotIdx, receiver, partial
}

// matchingOpen returns the opening bracket for a closing bracket.
func matchingOpen(close byte) byte {
	switch close {
	case ')':
		return '('
	case ']':
		return '['
	default:
		return 0
	}
}

func isIdentByte(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_'
}

// lastIdent extracts the last identifier fragment from text for lexical completion.
func lastIdent(text string) string {
	i := len(text) - 1
	for i >= 0 && isIdentByte(text[i]) {
		i--
	}
	return text[i+1:]
}

// completeMember tries to parse and type-check the receiver expression, then
// returns completions for its members filtered by the partial name.
func completeMember(ctx context.Context, typeEnv Env, receiverText, partial string) []Completion {
	receiverType := inferReceiverType(ctx, typeEnv, receiverText)
	if receiverType == nil {
		return nil
	}

	return MembersOf(receiverType, partial)
}

// inferReceiverType parses and type-checks a receiver expression, returning
// its inferred type. Returns nil if parsing or inference fails.
func inferReceiverType(ctx context.Context, typeEnv Env, expr string) hm.Type {
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

	module, ok := t.(Env)
	if !ok {
		return nil
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
