package lsp

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/creachadair/jrpc2"
	"github.com/vito/dang/pkg/dang"
	"github.com/vito/dang/pkg/hm"
)

func (h *langHandler) handleTextDocumentHover(ctx context.Context, req *jrpc2.Request) (any, error) {
	if !req.HasParams() {
		return nil, jrpc2.Errorf(jrpc2.InvalidParams, "missing parameters")
	}

	var params HoverParams
	if err := req.UnmarshalParams(&params); err != nil {
		return nil, err
	}

	f := h.waitForFile(params.TextDocument.URI)
	if f == nil {
		return nil, nil
	}

	if f.AST == nil {
		return nil, nil
	}

	// Find the node at this position to get its inferred type
	node := h.findNodeAtPosition(f.AST, params.Position)

	// Check if we're hovering over a directive application
	if directiveApp, ok := node.(*dang.DirectiveApplication); ok {
		return h.hoverDirectiveApplication(ctx, f, directiveApp, params.Position)
	}

	// Find the symbol at the cursor position
	symbolName := h.symbolAtPosition(f, params.Position)
	if symbolName == "" {
		return nil, nil
	}

	slog.InfoContext(ctx, "hover request", "uri", params.TextDocument.URI, "position", params.Position, "symbol", symbolName)

	if node == nil {
		slog.InfoContext(ctx, "no node found at position")
		return nil, nil
	}

	// Skip hover for literal nodes — the word under cursor is just content, not a symbol.
	switch node.(type) {
	case *dang.String, *dang.Int, *dang.Float, *dang.Boolean, *dang.Null:
		return nil, nil
	}

	// For type/interface definitions, show the full declaration body rather than
	// just the inferred type name.
	if codeBlock, docString := formatTypeDefinitionForHover(f, node, symbolName); codeBlock != "" {
		return h.hoverResultWithDocBelow(docString, codeBlock)
	}

	// For named type references (including GraphQL-loaded types with no local AST
	// definition), show the full type body rather than "Foo: Foo".
	if codeBlock, docString := formatNamedTypeForHover(f, params.Position, symbolName); codeBlock != "" {
		return h.hoverResultWithDocBelow(docString, codeBlock)
	}

	// Check if we're hovering over a field access (Select node)
	if selectNode, ok := node.(*dang.Select); ok {
		receiverType := selectNode.Receiver.GetInferredType()
		if receiverType != nil {
			if nn, ok := receiverType.(hm.NonNullType); ok {
				receiverType = nn.Type
			}
			if env, ok := receiverType.(dang.TypeScope); ok {
				var docString string
				if doc, found := env.GetDocString(selectNode.Field.Name); found {
					docString = doc
				}
				if scheme, found := env.LocalSchemeOf(selectNode.Field.Name); found {
					fieldType, _ := scheme.Type()
					codeBlock := fmt.Sprintf("%s: %s", symbolName, fieldType)
					if extra := typeDetailSuffix(fieldType); extra != "" {
						codeBlock += extra
					}
					return h.hoverResultWithDoc(docString, codeBlock)
				}
			}
		}
	}

	// Try the node's inferred type
	if inferredType := node.GetInferredType(); inferredType != nil {
		codeBlock := fmt.Sprintf("%s: %s", symbolName, inferredType)

		// Enrich hover for union and enum types with member details
		if extra := typeDetailSuffix(inferredType); extra != "" {
			codeBlock += extra
		}

		return h.hoverResult(f, params.Position, symbolName, codeBlock)
	}

	// Try to format the declaring node's signature from the symbol table
	if f.Symbols != nil {
		if def, ok := f.Symbols.Definitions[symbolName]; ok {
			if sig := formatDeclSignature(def.Node); sig != "" {
				return h.hoverResult(f, params.Position, symbolName, sig)
			}
		}
	}

	return nil, nil
}

// findNodeAtPosition finds the most specific AST node at the given position
func (h *langHandler) findNodeAtPosition(root dang.Node, pos Position) dang.Node {
	var result dang.Node

	root.Walk(func(n dang.Node) bool {
		if n == nil {
			return false
		}

		// Check if position is within this node's range
		if positionWithinNode(n, pos) {
			// This node contains the position, keep it as a candidate
			// Continue walking to find more specific children
			result = n
			return true
		}

		return true
	})

	return result
}

// hoverResult builds a hover response, looking up doc strings from the environment.
func (h *langHandler) hoverResult(f *File, pos Position, symbolName string, codeBlock string) (any, error) {
	var docString string

	// Try the file's type environment
	if f.TypeScope != nil {
		if doc, ok := f.TypeScope.GetDocString(symbolName); ok {
			docString = doc
		}
	}

	// If not found at file level, try to find in lexical scopes
	if docString == "" && f.AST != nil {
		environments := findEnclosingEnvironments(f.AST, pos)
		for i := len(environments) - 1; i >= 0; i-- {
			if doc, ok := environments[i].GetDocString(symbolName); ok {
				docString = doc
				break
			}
		}
	}

	return h.hoverResultWithDoc(docString, codeBlock)
}

// hoverResultWithDoc builds a hover response with an explicit doc string.
func (h *langHandler) hoverResultWithDoc(docString string, codeBlock string) (any, error) {
	var content string
	if docString != "" {
		content = fmt.Sprintf("%s\n\n```dang\n%s\n```", docString, codeBlock)
	} else {
		content = fmt.Sprintf("```dang\n%s\n```", codeBlock)
	}

	return hoverWithMarkdown(content), nil
}

// hoverResultWithDocBelow builds a hover response with code first and docs below
// a separator. This is useful for definition hovers where the declaration is the
// primary content.
func (h *langHandler) hoverResultWithDocBelow(docString string, codeBlock string) (any, error) {
	content := fmt.Sprintf("```dang\n%s\n```", codeBlock)
	if docString != "" {
		content = fmt.Sprintf("%s\n\n---\n\n%s", content, docString)
	}

	return hoverWithMarkdown(content), nil
}

func hoverWithMarkdown(content string) *Hover {
	return &Hover{
		Contents: MarkupContent{
			Kind:  Markdown,
			Value: content,
		},
	}
}

// typeDetailSuffix returns additional hover text for union and enum types,
// showing their members or values.
func typeDetailSuffix(t hm.Type) string {
	// Unwrap NonNull
	if nn, ok := t.(hm.NonNullType); ok {
		t = nn.Type
	}

	mod, ok := t.(*dang.Type)
	if !ok {
		return ""
	}

	switch mod.Kind {
	case dang.UnionKind:
		members := mod.GetMembers()
		if len(members) == 0 {
			return ""
		}
		var result strings.Builder
		result.WriteString("\n\n// Members:\n")
		for i, member := range members {
			if i > 0 {
				result.WriteString("\n")
			}
			if m, ok := member.(*dang.Type); ok {
				result.WriteString("//   " + m.Name())
			}
		}
		return result.String()

	case dang.EnumKind:
		bindings := mod.Bindings(dang.PublicVisibility)
		// Filter out "values" method
		var names []string
		for name := range bindings {
			if name == "values" {
				continue
			}
			names = append(names, name)
		}
		if len(names) == 0 {
			return ""
		}
		// Sort for deterministic output
		sortStrings(names)
		var result strings.Builder
		result.WriteString("\n\n// Values:\n")
		for i, name := range names {
			if i > 0 {
				result.WriteString("\n")
			}
			result.WriteString("//   " + name)
		}
		return result.String()
	}

	return ""
}

// sortStrings sorts a slice of strings in place.
func sortStrings(s []string) {
	sort.Strings(s)
}

// formatTypeDefinitionForHover formats full type/interface declarations for
// hover. The top-level doc string is returned separately so it can be displayed
// below the declaration instead of duplicated inside the code block.
func formatTypeDefinitionForHover(_ *File, node dang.Node, symbolName string) (codeBlock string, docString string) {
	switch n := node.(type) {
	case *dang.ObjectDecl:
		if n.Name == nil || n.Name.Name != symbolName {
			return "", ""
		}
		return formatModuleForHover(n.Inferred, n.DocString)
	case *dang.InterfaceDecl:
		if n.Name == nil || n.Name.Name != symbolName {
			return "", ""
		}
		return formatModuleForHover(n.Inferred, n.DocString)
	default:
		return "", ""
	}
}

func formatNamedTypeForHover(f *File, pos Position, symbolName string) (codeBlock string, docString string) {
	if f == nil || symbolName == "" {
		return "", ""
	}

	mod, ok := resolveNamedTypeForHover(f, pos, symbolName).(*dang.Type)
	if !ok {
		return "", ""
	}

	codeBlock, docString = formatModuleForHover(mod, "")
	if codeBlock == "" {
		return "", ""
	}
	if docString == "" && len(qualifiedNameAtPosition(f, pos)) == 0 {
		docString = docStringForHoverSymbol(f, pos, symbolName)
	}
	return codeBlock, docString
}

func resolveNamedTypeForHover(f *File, pos Position, symbolName string) dang.TypeScope {
	qualifiedName := qualifiedNameAtPosition(f, pos)
	if len(qualifiedName) > 1 && qualifiedName[len(qualifiedName)-1] == symbolName {
		if typ := resolveQualifiedNamedTypeForHover(f, pos, qualifiedName); typ != nil {
			return typ
		}
	}

	if f.AST != nil {
		environments := findEnclosingEnvironments(f.AST, pos)
		for i := len(environments) - 1; i >= 0; i-- {
			if typ, found := environments[i].NamedType(symbolName); found {
				return typ
			}
		}
	}
	if f.TypeScope != nil {
		if typ, found := f.TypeScope.NamedType(symbolName); found {
			return typ
		}
	}
	return nil
}

func resolveQualifiedNamedTypeForHover(f *File, pos Position, parts []string) dang.TypeScope {
	if len(parts) == 0 {
		return nil
	}

	if f.AST != nil {
		environments := findEnclosingEnvironments(f.AST, pos)
		for i := len(environments) - 1; i >= 0; i-- {
			if typ := resolveQualifiedNamedType(environments[i], parts); typ != nil {
				return typ
			}
		}
	}
	if f.TypeScope != nil {
		return resolveQualifiedNamedType(f.TypeScope, parts)
	}
	return nil
}

func resolveQualifiedNamedType(env dang.TypeScope, parts []string) dang.TypeScope {
	if env == nil || len(parts) == 0 {
		return nil
	}

	current := env
	for _, part := range parts[:len(parts)-1] {
		next, found := current.NamedType(part)
		if !found {
			return nil
		}
		current = next
	}

	last, found := current.NamedType(parts[len(parts)-1])
	if !found {
		return nil
	}
	return last
}

func qualifiedNameAtPosition(f *File, pos Position) []string {
	if f == nil {
		return nil
	}

	lines := strings.Split(f.Text, "\n")
	if pos.Line >= len(lines) {
		return nil
	}

	line := lines[pos.Line]
	if pos.Character >= len(line) {
		return nil
	}

	start := pos.Character
	for start > 0 && isIdentifierChar(rune(line[start-1])) {
		start--
	}

	end := pos.Character
	for end < len(line) && isIdentifierChar(rune(line[end])) {
		end++
	}

	if start == end || (end < len(line) && line[end] == '.') {
		return nil
	}

	parts := []string{line[start:end]}
	left := start
	for left > 0 && line[left-1] == '.' {
		prevEnd := left - 1
		prevStart := prevEnd
		for prevStart > 0 && isIdentifierChar(rune(line[prevStart-1])) {
			prevStart--
		}
		if prevStart == prevEnd {
			return nil
		}
		parts = append([]string{line[prevStart:prevEnd]}, parts...)
		left = prevStart
	}

	if len(parts) < 2 {
		return nil
	}
	return parts
}

func formatModuleForHover(mod *dang.Type, fallbackDoc string) (codeBlock string, docString string) {
	if mod == nil {
		return "", ""
	}
	if mod.Canonical != nil {
		mod = mod.Canonical
	}

	codeBlock = dang.FormatPublicTypeShape(mod)
	if codeBlock == "" {
		return "", ""
	}

	docString = mod.GetTypeDocString()
	if docString == "" {
		docString = fallbackDoc
	}
	return codeBlock, docString
}

func docStringForHoverSymbol(f *File, pos Position, symbolName string) string {
	if f == nil || symbolName == "" {
		return ""
	}

	if f.TypeScope != nil {
		if doc, ok := f.TypeScope.GetDocString(symbolName); ok {
			return doc
		}
	}

	if f.AST != nil {
		environments := findEnclosingEnvironments(f.AST, pos)
		for i := len(environments) - 1; i >= 0; i-- {
			if doc, ok := environments[i].GetDocString(symbolName); ok {
				return doc
			}
		}
	}

	return ""
}

// formatDeclSignature formats a declaring node's signature without the body.
func formatDeclSignature(node dang.Node) string {
	switch n := node.(type) {
	case *dang.FieldDecl:
		// Format a shallow copy so concurrent hover/completion requests don't
		// mutate the shared, inferred AST.
		fieldCopy := *n
		fieldCopy.DocString = ""

		if funDecl, ok := n.Value.(*dang.FunDecl); ok {
			// For functions, keep the FunDecl but strip its body.
			funCopy := *funDecl
			baseCopy := funDecl.FunctionBase
			baseCopy.Body = nil
			funCopy.FunctionBase = baseCopy
			fieldCopy.Value = &funCopy
			return dang.Format(&fieldCopy)
		}

		// For non-function fields, strip the value so we get just the type annotation.
		fieldCopy.Value = nil
		return dang.Format(&fieldCopy)

	case *dang.ObjectDecl:
		return fmt.Sprintf("type %s", n.Name.Name)

	default:
		return ""
	}
}

// hoverDirectiveApplication returns hover info for a directive application by looking up its declaration.
func (h *langHandler) hoverDirectiveApplication(ctx context.Context, f *File, app *dang.DirectiveApplication, pos Position) (any, error) {
	// Find the directive declaration from enclosing environments
	var decl *dang.DirectiveDecl
	environments := findEnclosingEnvironments(f.AST, pos)
	for i := len(environments) - 1; i >= 0; i-- {
		if d, ok := environments[i].GetDirective(app.Name); ok {
			decl = d
			break
		}
	}
	// Also check the file-level type env
	if decl == nil && f.TypeScope != nil {
		if d, ok := f.TypeScope.GetDirective(app.Name); ok {
			decl = d
		}
	}

	if decl == nil {
		return nil, nil
	}

	// Format without the doc string so we can show it separately as markdown.
	declCopy := *decl
	declCopy.DocString = ""
	schema := dang.Format(&declCopy)

	var content string
	if decl.DocString != "" {
		content = fmt.Sprintf("%s\n\n```dang\n%s\n```", decl.DocString, schema)
	} else {
		content = fmt.Sprintf("```dang\n%s\n```", schema)
	}

	return &Hover{
		Contents: MarkupContent{
			Kind:  Markdown,
			Value: content,
		},
	}, nil
}
