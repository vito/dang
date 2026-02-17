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

	// Check if we're inside a function call (argument position)
	if funcExpr, partial, alreadyProvided, ok := splitArgExpr(text); ok {
		return completeArgs(ctx, typeEnv, funcExpr, partial, alreadyProvided)
	}

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
	receiverType := InferReceiverType(ctx, typeEnv, receiverText)
	if receiverType == nil {
		return nil
	}

	return MembersOf(receiverType, partial)
}

// splitArgExpr detects when the cursor is inside a function call's argument
// list. Given text like "container.from(addr" it returns:
//   - funcExpr: "container.from" (the function expression)
//   - partial: "addr" (the partial argument name being typed)
//   - alreadyProvided: names of arguments already present in the call
//   - ok: true if we're in an argument position
//
// It handles nested calls by finding the innermost unmatched '('.
func splitArgExpr(text string) (funcExpr, partial string, alreadyProvided []string, ok bool) {
	// Find the innermost unmatched '(' by scanning from the end
	parenDepth := 0
	bracketDepth := 0
	openIdx := -1
	for i := len(text) - 1; i >= 0; i-- {
		switch text[i] {
		case ')':
			parenDepth++
		case '(':
			if parenDepth > 0 {
				parenDepth--
			} else {
				openIdx = i
			}
		case ']':
			bracketDepth++
		case '[':
			if bracketDepth > 0 {
				bracketDepth--
			}
		}
		if openIdx >= 0 {
			break
		}
	}
	if openIdx < 0 {
		return "", "", nil, false
	}

	beforeParen := text[:openIdx]
	if beforeParen == "" {
		return "", "", nil, false
	}

	// The function expression must end with an identifier (the function name).
	lastChar := beforeParen[len(beforeParen)-1]
	if !isIdentByte(lastChar) {
		return "", "", nil, false
	}

	// Walk backwards from the open paren to extract the function expression.
	// Handle dotted chains like "container.from" and chained calls like
	// "a.b(1).from" by consuming identifiers, dots, and balanced groups.
	j := len(beforeParen) - 1
	for j >= 0 {
		c := beforeParen[j]
		if isIdentByte(c) || c == '.' {
			j--
		} else if c == ')' || c == ']' {
			open := matchingOpen(c)
			depth := 1
			j--
			for j >= 0 && depth > 0 {
				switch beforeParen[j] {
				case c:
					depth++
				case open:
					depth--
				}
				j--
			}
		} else {
			break
		}
	}
	funcExpr = beforeParen[j+1:]
	if funcExpr == "" {
		return "", "", nil, false
	}

	argsText := text[openIdx+1:]

	// Extract partial: the identifier being typed at the end (if any).
	partial = lastIdent(argsText)

	// Extract already-provided named argument keys from the args text.
	// Look for patterns like "name:" that indicate named arguments.
	alreadyProvided = extractProvidedArgNames(argsText)

	return funcExpr, partial, alreadyProvided, true
}

// extractProvidedArgNames scans argument text for named argument patterns
// ("name:") and returns the names found.
func extractProvidedArgNames(argsText string) []string {
	var names []string
	i := 0
	for i < len(argsText) {
		// Skip whitespace
		for i < len(argsText) && (argsText[i] == ' ' || argsText[i] == '\t' || argsText[i] == '\n') {
			i++
		}
		// Try to read an identifier
		start := i
		for i < len(argsText) && isIdentByte(argsText[i]) {
			i++
		}
		if i > start && i < len(argsText) && argsText[i] == ':' {
			names = append(names, argsText[start:i])
			i++ // skip ':'
		}
		// Skip to next comma or end, handling nested parens/brackets
		depth := 0
		for i < len(argsText) {
			switch argsText[i] {
			case '(', '[':
				depth++
			case ')', ']':
				depth--
			case ',':
				if depth == 0 {
					i++
					goto next
				}
			}
			i++
		}
	next:
	}
	return names
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
