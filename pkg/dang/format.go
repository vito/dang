package dang

import (
	"bytes"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

const (
	maxLineLength = 80
	indentString  = "  "
)

// Comment represents a comment with its location
type Comment struct {
	Line       int    // 1-indexed line number
	Text       string // comment text including the # prefix
	IsTrailing bool   // true if comment is after code on the same line
}

// Formatter formats Dang AST nodes into canonical source code
type Formatter struct {
	buf      bytes.Buffer
	indent   int
	col      int // current column (approximate, for line length decisions)
	comments []Comment
	lastLine int    // last source line we've processed (for comment emission)
	source   []byte // original source (for #nofmt regions)

	// Line-based comment map from parser (line number -> comment text).
	// This is the primary comment data source when available.
	commentMap map[int]string
	// Set of line numbers whose comments have already been emitted.
	emittedComments map[int]bool
}

// Format formats a node and returns the formatted source code
func Format(node Node) string {
	f := &Formatter{emittedComments: make(map[int]bool)}
	f.formatNode(node)
	return f.buf.String()
}

// FormatFile parses and formats a Dang source file
func FormatFile(source []byte) (string, error) {
	result, commentMap, err := ParseWithComments("format", source)
	if err != nil {
		return "", err
	}

	// Also extract structured comments for nofmt/blank-line logic
	comments := extractComments(source)

	f := &Formatter{
		comments:        comments,
		lastLine:        0,
		source:          source,
		commentMap:      commentMap,
		emittedComments: make(map[int]bool),
	}
	f.formatNode(result.(*ModuleBlock))

	// Emit any trailing comments
	f.emitRemainingComments()

	return trimTrailingWhitespace(f.buf.String()), nil
}

// trimTrailingWhitespace removes trailing whitespace from each line
func trimTrailingWhitespace(s string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " \t")
	}
	return strings.Join(lines, "\n")
}

// isNoFmtText returns true if a comment's text is a #nofmt directive.
func isNoFmtText(commentText string) bool {
	text := strings.TrimSpace(strings.TrimPrefix(commentText, "#"))
	return text == "nofmt" || strings.HasPrefix(text, "nofmt ")
}

// hasNoFmtComment checks if a node has a #nofmt comment attached to it
// (either as a preceding standalone comment or a trailing comment on the same line)
func (f *Formatter) hasNoFmtComment(node Node) bool {
	// Don't check container nodes - the comment should apply to their contents
	switch node.(type) {
	case *ModuleBlock, *Block:
		return false
	}

	loc := node.GetSourceLocation()
	if loc == nil {
		return false
	}

	// Check for preceding #nofmt comment (standalone comment on line before)
	for _, c := range f.comments {
		if !c.IsTrailing && c.Line == loc.Line-1 && isNoFmtText(c.Text) {
			return true
		}
	}

	// Check for trailing #nofmt comment on the same line
	for _, c := range f.comments {
		if c.IsTrailing && c.Line == loc.Line && isNoFmtText(c.Text) {
			return true
		}
	}

	return false
}

// getOriginalSource extracts the original source text for a node's span
// If the node has a trailing #nofmt comment, include it in the span
func (f *Formatter) getOriginalSource(node Node) string {
	loc := node.GetSourceLocation()
	if loc == nil || loc.End == nil {
		return ""
	}

	startOffset := f.lineColumnToOffset(loc.Line, loc.Column)
	endOffset := f.lineColumnToOffset(loc.End.Line, loc.End.Column)
	if startOffset < 0 || endOffset < 0 || endOffset > len(f.source) {
		return ""
	}

	// Check if there's a trailing #nofmt comment on the end line
	// If so, extend the span to include the entire line
	for _, c := range f.comments {
		if c.Line == loc.End.Line && c.IsTrailing && isNoFmtText(c.Text) {
			// Extend to end of line
			for endOffset < len(f.source) && f.source[endOffset] != '\n' {
				endOffset++
			}
			break
		}
	}

	return string(f.source[startOffset:endOffset])
}

// lineColumnToOffset converts a 1-indexed line and column to a byte offset
func (f *Formatter) lineColumnToOffset(line, col int) int {
	if f.source == nil || line < 1 || col < 1 {
		return -1
	}

	offset := 0
	currentLine := 1

	for offset < len(f.source) && currentLine < line {
		if f.source[offset] == '\n' {
			currentLine++
		}
		offset++
	}

	// Add column offset (1-indexed)
	offset += col - 1

	if offset > len(f.source) {
		return len(f.source)
	}
	return offset
}

// extractComments extracts all comments from source with their line numbers
func extractComments(source []byte) []Comment {
	var comments []Comment

	// Track if we're inside a triple-quoted string across lines
	inTripleQuote := false
	lines := bytes.Split(source, []byte("\n"))

	for i, line := range lines {
		lineNum := i + 1 // 1-indexed

		// Process the line character by character to handle strings and comments
		j := 0
		lineStr := string(line)
		inString := false

		for j < len(lineStr) {
			// Check for triple quotes
			if j+2 < len(lineStr) && lineStr[j:j+3] == `"""` {
				inTripleQuote = !inTripleQuote
				j += 3
				continue
			}

			// Skip everything inside triple-quoted strings
			if inTripleQuote {
				j++
				continue
			}

			// Check for regular string start/end
			if lineStr[j] == '"' {
				inString = !inString
				j++
				continue
			}

			// Skip everything inside regular strings
			if inString {
				// Handle escape sequences
				if lineStr[j] == '\\' && j+1 < len(lineStr) {
					j += 2
					continue
				}
				j++
				continue
			}

			// Found a comment outside of strings
			if lineStr[j] == '#' {
				commentText := lineStr[j:]
				prefix := lineStr[:j]
				hasCodeBefore := len(strings.TrimSpace(prefix)) > 0
				comments = append(comments, Comment{
					Line:       lineNum,
					Text:       commentText,
					IsTrailing: hasCodeBefore,
				})
				break
			}

			j++
		}
	}

	return comments
}

// emitTrailingComment emits a trailing comment for the given line if one exists
func (f *Formatter) emitTrailingComment(line int) {
	// Skip if already emitted via nl()
	if f.emittedComments[line] {
		return
	}
	for i, comment := range f.comments {
		if comment.Line == line && comment.IsTrailing {
			f.write(" ")
			f.write(comment.Text)
			f.emittedComments[line] = true
			// Remove this comment from the list
			f.comments = append(f.comments[:i], f.comments[i+1:]...)
			return
		}
		if comment.Line > line {
			break
		}
	}
}

// removeTrailingNoFmtComment removes a trailing #nofmt comment on the given line
// (because it's already included in the preserved original source)
func (f *Formatter) removeTrailingNoFmtComment(line int) {
	for i, c := range f.comments {
		if c.Line == line && c.IsTrailing && isNoFmtText(c.Text) {
			f.comments = append(f.comments[:i], f.comments[i+1:]...)
			return
		}
	}
}

// emitCommentsForNode emits comments that "hug" this node (appear on lines before it)
func (f *Formatter) emitCommentsForNode(node Node) {
	line := nodeEffectiveStartLine(node)
	if line > 0 {
		f.emitCommentsBeforeNode(line, nodeHasDocString(node))
		f.lastLine = line
	}
}

// emitCommentsBeforeNode emits comments before a node, optionally suppressing
// the blank line that would normally be added for a gap (used when node has docstring)
func (f *Formatter) emitCommentsBeforeNode(line int, hasDocString bool) {
	for len(f.comments) > 0 && f.comments[0].Line < line {
		// Skip over trailing comments - they're emitted by emitTrailingComment.
		if f.comments[0].IsTrailing || f.emittedComments[f.comments[0].Line] {
			f.comments = f.comments[1:]
			continue
		}

		comment := f.comments[0]
		f.comments = f.comments[1:]

		// Add blank lines if there's a gap from the last processed line
		// (preserves spacing between comment groups)
		if f.lastLine > 0 && comment.Line > f.lastLine+1 {
			f.newline()
		}

		f.writeIndent()
		f.write(comment.Text)
		f.emittedComments[comment.Line] = true
		f.newline()
		f.lastLine = comment.Line
	}

	// If there's a blank line between the last comment and the node, preserve it
	// BUT not if the node has a docstring (which fills the gap)
	if !hasDocString && f.lastLine > 0 && line > f.lastLine+1 {
		f.newline()
	}
}

// nodeEffectiveStartLine returns the first line where a node's content would appear,
// accounting for docstrings that precede the actual code
func nodeEffectiveStartLine(node Node) int {
	// For slots with prefix directives, the effective start is the first directive
	if s, ok := node.(*SlotDecl); ok {
		for _, d := range s.Directives {
			if d.IsPrefix && d.Loc != nil {
				return d.Loc.Line
			}
		}
	}
	if loc := node.GetSourceLocation(); loc != nil {
		return loc.Line
	}
	return 0
}

// nodeHasDocString returns true if the node has a docstring that would be formatted before it
func nodeHasDocString(node Node) bool {
	switch n := node.(type) {
	case *SlotDecl:
		return n.DocString != ""
	case *ClassDecl:
		return n.DocString != ""
	case *InterfaceDecl:
		return n.DocString != ""
	case *UnionDecl:
		return n.DocString != ""
	case *EnumDecl:
		return n.DocString != ""
	default:
		return false
	}
}

// emitChainCommentsBefore emits standalone comments before a given source line
// within a method chain. Comments are emitted on their own lines at the current
// indentation level.
func (f *Formatter) emitChainCommentsBefore(line int) {
	for len(f.comments) > 0 && f.comments[0].Line < line {
		if f.comments[0].IsTrailing || f.emittedComments[f.comments[0].Line] {
			f.comments = f.comments[1:]
			continue
		}

		comment := f.comments[0]
		f.comments = f.comments[1:]

		f.newline()
		f.writeIndent()
		f.write(comment.Text)
		f.emittedComments[comment.Line] = true
		f.lastLine = comment.Line
	}
}

// emitRemainingComments emits any comments at the end of the file
func (f *Formatter) emitRemainingComments() {
	for _, comment := range f.comments {
		if f.emittedComments[comment.Line] {
			continue
		}
		if f.lastLine > 0 && comment.Line > f.lastLine+1 {
			f.newline()
		}
		f.writeIndent()
		f.write(comment.Text)
		f.newline()
		f.lastLine = comment.Line
	}
	f.comments = nil
}

// typeNodeLine extracts the source line from a TypeNode if available.
func typeNodeLine(t TypeNode) int {
	switch n := t.(type) {
	case *NamedTypeNode:
		if n.Loc != nil {
			return n.Loc.Line
		}
	case NonNullTypeNode:
		return typeNodeLine(n.Elem)
	case *ListTypeNode:
		return typeNodeLine(n.Elem)
	}
	return 0
}

func (f *Formatter) write(s string) {
	f.buf.WriteString(s)
	// Update column tracking (approximate)
	if idx := strings.LastIndex(s, "\n"); idx >= 0 {
		f.col = len(s) - idx - 1
	} else {
		f.col += len(s)
	}
}

func (f *Formatter) newline() {
	f.write("\n")
	f.col = 0
}

// nl emits any not-yet-printed trailing comment on the given source line,
// then writes a newline. This is the preferred way to end a line when the
// source line number is known.
func (f *Formatter) nl(line int) {
	if line > 0 && f.commentMap != nil {
		if text, ok := f.commentMap[line]; ok && !f.emittedComments[line] {
			// Only emit as trailing if there's code on this line (i.e., we've
			// already written something on this output line).
			if f.col > 0 {
				f.write(" ")
				f.write(text)
			}
			f.emittedComments[line] = true
		}
	}
	f.write("\n")
	f.col = 0
}

func (f *Formatter) writeIndent() {
	for i := 0; i < f.indent; i++ {
		f.write(indentString)
	}
}

func (f *Formatter) indented(fn func()) {
	f.indent++
	fn()
	f.indent--
}

// resetLastLineForForms resets lastLine to just before the first form,
// preventing a spurious blank line at the start of a body.
func (f *Formatter) resetLastLineForForms(forms []Node) {
	if len(forms) > 0 {
		if loc := forms[0].GetSourceLocation(); loc != nil && loc.Line > 0 {
			f.lastLine = loc.Line - 1
		}
	}
}

// finishForm emits a trailing comment for the form and updates lastLine.
func (f *Formatter) finishForm(form Node) {
	if loc := form.GetSourceLocation(); loc != nil {
		f.emitTrailingComment(loc.Line)
	}
	if endLine := nodeEndLine(form); endLine > 0 {
		f.lastLine = endLine
	}
}

// handleNoFmtForm checks if a form has a #nofmt directive and, if so, emits its
// original source text (preserving formatting). Returns true when the form was
// handled; the caller is responsible for any trailing newline.
func (f *Formatter) handleNoFmtForm(form Node) bool {
	if !f.hasNoFmtComment(form) {
		return false
	}
	orig := f.getOriginalSource(form)
	if orig == "" {
		return false
	}
	f.emitCommentsForNode(form)
	f.writeIndent()
	f.write(orig)
	fullLoc := form.GetSourceLocation()
	if fullLoc != nil && fullLoc.End != nil {
		f.removeTrailingNoFmtComment(fullLoc.End.Line)
		f.lastLine = fullLoc.End.Line
	}
	return true
}

// formatDeclForms formats a list of declaration forms with blank-line
// separation and comment handling. It is used for module, class, and
// interface bodies.
func (f *Formatter) formatDeclForms(forms []Node) {
	f.resetLastLineForForms(forms)
	for i, form := range forms {
		if i > 0 && f.needsBlankLineBetween(forms[i-1], form) {
			f.newline()
			if loc := form.GetSourceLocation(); loc != nil && loc.Line > 0 {
				f.lastLine = loc.Line - 1
			}
		}
		if f.handleNoFmtForm(form) {
			f.newline()
			continue
		}
		f.emitCommentsForNode(form)
		f.writeIndent()
		f.formatNode(form)
		f.finishForm(form)
		f.newline()
	}
}

// estimateLength estimates how long a node would be if rendered on one line
func (f *Formatter) estimateLength(node Node) int {
	temp := &Formatter{}
	temp.formatNodeInline(node)
	return len(temp.buf.String())
}

func (f *Formatter) formatNode(node Node) {
	// Check for #nofmt comment - if present, emit original source
	if f.hasNoFmtComment(node) {
		orig := f.getOriginalSource(node)
		if orig != "" {
			f.write(orig)
			return
		}
	}

	switch n := node.(type) {
	case *ModuleBlock:
		f.formatModuleBlock(n)
	case *ClassDecl:
		f.formatClassDecl(n)
	case *InterfaceDecl:
		f.formatInterfaceDecl(n)
	case *UnionDecl:
		f.formatUnionDecl(n)
	case *EnumDecl:
		f.formatEnumDecl(n)
	case *ScalarDecl:
		f.formatScalarDecl(n)
	case *SlotDecl:
		f.formatSlotDecl(n)
	case *FunDecl:
		f.formatFunDecl(n)
	case *DirectiveDecl:
		f.formatDirectiveDecl(n)
	case *NewConstructorDecl:
		f.formatNewConstructorDecl(n)
	case *ImportDecl:
		f.formatImportDecl(n)
	case *Block:
		f.formatBlock(n)
	case *FunCall:
		f.formatFunCall(n, false)
	case *Select:
		f.formatSelect(n, false)
	case *Symbol:
		f.formatSymbol(n)
	case *String:
		f.formatString(n)
	case *Int:
		f.formatInt(n)
	case *Float:
		f.formatFloat(n)
	case *Boolean:
		f.formatBoolean(n)
	case *Null:
		f.write("null")
	case *SelfKeyword:
		f.write("self")
	case *List:
		f.formatList(n)
	case *Object:
		f.formatObject(n)
	case *TryCatch:
		f.formatTryCatch(n)
	case *Raise:
		f.formatRaise(n)
	case *Conditional:
		f.formatConditional(n)
	case *ForLoop:
		f.formatForLoop(n)
	case *Case:
		f.formatCase(n)
	case *Assert:
		f.formatAssert(n)
	case *Break:
		f.write("break")
	case *Continue:
		f.write("continue")
	case *Reassignment:
		f.formatReassignment(n)
	case *TypeHint:
		f.formatTypeHint(n)
	case *Index:
		f.formatIndex(n)
	case *ObjectSelection:
		f.formatObjectSelection(n)
	case *BlockArg:
		f.formatBlockArg(n)
	case *Default:
		f.formatBinaryOp(n.Left, n.Right, "??")
	case *LogicalOr:
		f.formatBinaryOp(n.Left, n.Right, "or")
	case *LogicalAnd:
		f.formatBinaryOp(n.Left, n.Right, "and")
	case *Equality:
		f.formatBinaryOp(n.Left, n.Right, "==")
	case *UnaryNegation:
		f.formatUnaryNegation(n)
	case *Addition:
		f.formatBinaryOp(n.Left, n.Right, "+")
	case *Subtraction:
		f.formatBinaryOp(n.Left, n.Right, "-")
	case *Multiplication:
		f.formatBinaryOp(n.Left, n.Right, "*")
	case *Division:
		f.formatBinaryOp(n.Left, n.Right, "/")
	case *Modulo:
		f.formatBinaryOp(n.Left, n.Right, "%")
	case *LessThan:
		f.formatBinaryOp(n.Left, n.Right, "<")
	case *LessThanEqual:
		f.formatBinaryOp(n.Left, n.Right, "<=")
	case *GreaterThan:
		f.formatBinaryOp(n.Left, n.Right, ">")
	case *GreaterThanEqual:
		f.formatBinaryOp(n.Left, n.Right, ">=")
	case *Inequality:
		f.formatBinaryOp(n.Left, n.Right, "!=")
	case *Grouped:
		f.formatGrouped(n)
	default:
		// Fallback for unknown node types
		f.write(fmt.Sprintf("/* unknown node type: %T */", node))
	}
}

// formatNodeInline formats a node without any newlines (for length estimation)
func (f *Formatter) formatNodeInline(node Node) {
	switch n := node.(type) {
	case *FunCall:
		f.formatFunCallInline(n)
	case *Select:
		f.formatSelectInline(n)
	case *List:
		f.formatListInline(n)
	case *Object:
		f.formatObjectInline(n)
	default:
		f.formatNode(node)
	}
}

// sortImports sorts import declarations in-place within a module block.
// Imports are sorted alphabetically by their source/name while preserving
// their position relative to non-import forms.
func (f *Formatter) sortImports(m *ModuleBlock) {
	// Find the range of consecutive imports at the start (after any leading non-imports)
	// Actually, collect all imports and their indices, then sort and put back
	var importIndices []int
	var imports []*ImportDecl
	for i, form := range m.Forms {
		if imp, ok := form.(*ImportDecl); ok {
			importIndices = append(importIndices, i)
			imports = append(imports, imp)
		}
	}
	if len(imports) <= 1 {
		return
	}

	sort.SliceStable(imports, func(i, j int) bool {
		return importSortKey(imports[i]) < importSortKey(imports[j])
	})

	for i, idx := range importIndices {
		m.Forms[idx] = imports[i]
	}
}

// importSortKey returns the string to sort an import by
func importSortKey(imp *ImportDecl) string {
	if imp.Name != nil {
		return imp.Name.Name
	}
	return ""
}

func (f *Formatter) formatModuleBlock(m *ModuleBlock) {
	// Sort imports: collect them, sort, and reorder in the forms slice
	f.sortImports(m)
	f.formatDeclForms(m.Forms)
}

// needsBlankLineBetween determines if a blank line should separate two forms
func (f *Formatter) needsBlankLineBetween(prev, next Node) bool {
	// Preserve blank lines from original source
	if f.hadBlankLineBetween(prev, next) {
		return true
	}

	// Always add blank line after imports when followed by non-import
	if isImport(prev) && !isImport(next) {
		return true
	}

	// No blank line between consecutive imports
	if isImport(prev) && isImport(next) {
		return false
	}

	// Always add blank line before/after type declarations
	if isTypeDecl(prev) || isTypeDecl(next) {
		return true
	}

	// Always add blank line before/after interface declarations
	if isInterfaceDecl(prev) || isInterfaceDecl(next) {
		return true
	}

	// Always add blank line before/after union declarations
	if isUnionDecl(prev) || isUnionDecl(next) {
		return true
	}

	// Always add blank line before/after directive declarations
	if isDirectiveDecl(prev) || isDirectiveDecl(next) {
		return true
	}

	// Always add blank line before/after function definitions
	if isFunctionDef(prev) || isFunctionDef(next) {
		return true
	}

	// No blank line between consecutive asserts
	if isAssert(prev) && isAssert(next) {
		return false
	}

	// No blank line between simple field assignments
	if isSimpleAssignment(prev) && isSimpleAssignment(next) {
		return false
	}

	// Default: no blank line
	return false
}

// hadBlankLineBetween checks if there was a blank line between two nodes in the original source
// (not counting comment lines - those are handled by comment emission)
func (f *Formatter) hadBlankLineBetween(prev, next Node) bool {
	prevEnd := nodeEndLine(prev)
	nextLoc := next.GetSourceLocation()
	if prevEnd <= 0 || nextLoc == nil || nextLoc.Line <= 0 {
		return false
	}

	// Count how many lines between prev end and next start are comments
	commentLines := 0
	for _, c := range f.comments {
		if c.Line > prevEnd && c.Line < nextLoc.Line && !c.IsTrailing {
			commentLines++
		}
	}

	// There's a blank line if there's more than 1 line gap after accounting for comments
	gap := nextLoc.Line - prevEnd - 1 // lines between prev and next
	return gap > commentLines
}

// nodeEndLine returns the last line of a node (using End if available, otherwise start line)
func nodeEndLine(node Node) int {
	// Use nodeFullLocation first to get the full span (e.g., SlotDecl.Loc includes the
	// value block, whereas nodeLocation returns just the name location). This prevents
	// hadBlankLineBetween from seeing a false gap when a form expands to multiline.
	loc := node.GetSourceLocation()
	if loc == nil {
		return 0
	}
	if loc.End != nil {
		return loc.End.Line
	}
	return loc.Line
}

func isTypeDecl(node Node) bool {
	_, ok := node.(*ClassDecl)
	return ok
}

func isInterfaceDecl(node Node) bool {
	_, ok := node.(*InterfaceDecl)
	return ok
}

func isUnionDecl(node Node) bool {
	_, ok := node.(*UnionDecl)
	return ok
}

func isDirectiveDecl(node Node) bool {
	_, ok := node.(*DirectiveDecl)
	return ok
}

func isImport(node Node) bool {
	_, ok := node.(*ImportDecl)
	return ok
}

func isAssert(node Node) bool {
	_, ok := node.(*Assert)
	return ok
}

func isFunctionDef(node Node) bool {
	if _, ok := node.(*NewConstructorDecl); ok {
		return true
	}
	slot, ok := node.(*SlotDecl)
	if !ok {
		return false
	}
	// Check if the value is a FunDecl (function with args/body)
	if _, ok := slot.Value.(*FunDecl); ok {
		return true
	}
	// Check if it's a slot with a block body and type annotation (parameterless function)
	if _, ok := slot.Value.(*Block); ok {
		return slot.Type_ != nil
	}
	return false
}

func isSimpleAssignment(node Node) bool {
	slot, ok := node.(*SlotDecl)
	if !ok {
		return false
	}
	// Simple assignment: has a value that's not a block, or no type annotation with a block
	if slot.Value == nil {
		return false
	}
	if _, ok := slot.Value.(*Block); ok {
		// Block with type annotation is a function, not a simple assignment
		return slot.Type_ == nil
	}
	// Non-block value is a simple assignment
	return true
}

func (f *Formatter) formatClassDecl(c *ClassDecl) {
	// Doc string
	if c.DocString != "" {
		f.formatDocString(c.DocString)
	}

	// Prefix directives
	for _, d := range c.Directives {
		if d.Loc != nil && c.Name.Loc != nil && d.Loc.Line < c.Name.Loc.Line {
			f.formatDirectiveApplication(d)
			f.emitTrailingComment(d.Loc.Line)
			f.newline()
			f.writeIndent()
		}
	}

	f.write("type ")
	f.write(c.Name.Name)

	// Implements clause
	if len(c.Implements) > 0 {
		f.write(" implements ")
		for i, impl := range c.Implements {
			if i > 0 {
				f.write(" & ")
			}
			f.write(impl.Name)
		}
	}

	// Suffix directives (on same line)
	for _, d := range c.Directives {
		if d.Loc == nil || c.Name.Loc == nil || d.Loc.Line >= c.Name.Loc.Line {
			f.write(" ")
			f.formatDirectiveApplication(d)
		}
	}

	f.write(" {")
	// Emit trailing comment on the "type Foo {" line before the newline
	if c.Name.Loc != nil {
		f.nl(c.Name.Loc.Line)
	} else {
		f.newline()
	}

	// Format block contents with blank lines between function definitions
	f.indented(func() {
		f.formatDeclForms(c.Value.Forms)

		// Emit any remaining comments inside the body (before closing brace)
		if c.Value.Loc != nil && c.Value.Loc.End != nil {
			f.emitCommentsBeforeNode(c.Value.Loc.End.Line, false)
		}
	})

	f.writeIndent()
	f.write("}")
	// Emit trailing comment on closing brace line
	if c.Loc != nil && c.Loc.End != nil {
		f.emitTrailingComment(c.Loc.End.Line)
	}
}

func (f *Formatter) formatNewConstructorDecl(n *NewConstructorDecl) {
	if n.DocString != "" {
		f.formatDocString(n.DocString)
	}

	if len(n.Args) > 0 {
		f.write("new(")
		f.formatFunctionArgs(n.Args, nil, n.Loc)
		f.write(") ")
	} else {
		f.write("new ")
	}
	f.formatBlock(n.BodyBlock)
}

func (f *Formatter) formatInterfaceDecl(i *InterfaceDecl) {
	if i.DocString != "" {
		f.formatDocString(i.DocString)
	}

	f.write("interface ")
	f.write(i.Name.Name)
	f.write(" {")
	if i.Name.Loc != nil {
		f.nl(i.Name.Loc.Line)
	} else {
		f.newline()
	}

	f.indented(func() {
		f.formatDeclForms(i.Value.Forms)
	})

	f.write("}")
	// Emit trailing comment on closing brace line
	if i.Loc != nil && i.Loc.End != nil {
		f.emitTrailingComment(i.Loc.End.Line)
	}
}

func (f *Formatter) formatUnionDecl(u *UnionDecl) {
	if u.DocString != "" {
		f.formatDocString(u.DocString)
	}

	f.write("union ")
	f.write(u.Name.Name)
	f.write(" = ")
	for i, member := range u.Members {
		if i > 0 {
			f.write(" | ")
		}
		f.write(member.Name)
	}
}

func (f *Formatter) formatEnumDecl(e *EnumDecl) {
	if e.DocString != "" {
		f.formatDocString(e.DocString)
	}

	f.write("enum ")
	f.write(e.Name.Name)

	// Check if enum was originally on a single line
	singleLine := e.Loc != nil && (e.Loc.End == nil || e.Loc.End.Line == e.Loc.Line)

	if singleLine {
		f.write(" { ")
		for i, val := range e.Values {
			if i > 0 {
				f.write(" ")
			}
			f.write(val.Name)
		}
		f.write(" }")
	} else {
		f.write(" {")
		if e.Loc != nil {
			f.nl(e.Loc.Line)
		} else {
			f.newline()
		}

		f.indented(func() {
			for _, val := range e.Values {
				// Emit comments for this enum value
				if val.Loc != nil {
					f.emitCommentsBeforeNode(val.Loc.Line, false)
					f.lastLine = val.Loc.Line
				}
				f.writeIndent()
				f.write(val.Name)
				f.newline()
			}
		})

		f.write("}")
		// Emit trailing comment on closing brace line
		if e.Loc != nil && e.Loc.End != nil {
			f.emitTrailingComment(e.Loc.End.Line)
		}
	}
}

func (f *Formatter) formatScalarDecl(s *ScalarDecl) {
	if s.DocString != "" {
		f.formatDocString(s.DocString)
	}

	f.write("scalar ")
	f.write(s.Name.Name)
}

// wereSuffixDirectivesMultiline checks if suffix directives were originally on
// separate lines from the preceding content (identified by precedingLine).
func wereSuffixDirectivesMultiline(directives []*DirectiveApplication, precedingLine int) bool {
	for _, d := range directives {
		if d.IsPrefix {
			continue
		}
		if d.Loc != nil && d.Loc.Line > precedingLine {
			return true
		}
	}
	return false
}

// formatSuffixDirectives emits suffix directives, preserving multiline layout
// when the directives were originally on separate lines. precedingLine is the
// source line of the content that appeared before the directives.
func (f *Formatter) formatSuffixDirectives(directives []*DirectiveApplication, precedingLine int) {
	multiline := wereSuffixDirectivesMultiline(directives, precedingLine)
	if multiline {
		f.indent++
		defer func() { f.indent-- }()
	}
	prevLine := precedingLine
	for _, d := range directives {
		if d.IsPrefix {
			continue
		}
		if multiline && d.Loc != nil && d.Loc.Line > prevLine {
			f.newline()
			f.writeIndent()
		} else {
			f.write(" ")
		}
		f.formatDirectiveApplication(d)
		if d.Loc != nil {
			prevLine = d.Loc.Line
		}
	}
}

func (f *Formatter) formatSlotDecl(s *SlotDecl) {
	// Doc string
	if s.DocString != "" {
		f.formatDocString(s.DocString)
		f.writeIndent()
	}

	// Prefix directives
	prevDirectiveLine := 0
	for _, d := range s.Directives {
		if d.IsPrefix {
			if prevDirectiveLine > 0 {
				if d.Loc != nil && d.Loc.Line > prevDirectiveLine {
					// Directive on a new line
					f.newline()
					f.writeIndent()
				} else {
					// Directive on same line as previous
					f.write(" ")
				}
			}
			f.formatDirectiveApplication(d)
			if d.Loc != nil {
				f.emitTrailingComment(d.Loc.Line)
				prevDirectiveLine = d.Loc.Line
			}
		}
	}
	// If last prefix directive was on a different line than the name, newline; otherwise space
	if prevDirectiveLine > 0 {
		if s.Name.Loc != nil && s.Name.Loc.Line > prevDirectiveLine {
			f.newline()
			f.writeIndent()
		} else {
			f.write(" ")
		}
	}

	// Visibility
	switch s.Visibility {
	case PublicVisibility:
		f.write("pub ")
	case PrivateVisibility:
		f.write("let ")
	}

	f.write(s.Name.Name)

	// Determine the source line of the name for directive multiline detection
	nameLine := 0
	if s.Name.Loc != nil {
		nameLine = s.Name.Loc.Line
	}

	// Check if this is a function declaration
	if funDecl, ok := s.Value.(*FunDecl); ok {
		f.formatFunDeclSignature(funDecl, nameLine)
		return
	}

	// Type annotation
	if s.Type_ != nil {
		// For FunTypeNode with args, output as method signature: name(args): ret
		// For other types or FunTypeNode without args, output as: name: type
		if ft, ok := s.Type_.(FunTypeNode); ok && len(ft.Args) > 0 {
			f.write("(")
			for i, arg := range ft.Args {
				if i > 0 {
					f.write(", ")
				}
				f.write(arg.Name.Name)
				f.write(": ")
				f.formatTypeNode(arg.Type_)
			}
			f.write("): ")
			f.formatTypeNode(ft.Ret)
		} else {
			f.write(": ")
			f.formatTypeNode(s.Type_)
		}
	}

	// Value and suffix directives - placement depends on value type
	if s.Value != nil {
		if block, ok := s.Value.(*Block); ok {
			// Block value: suffix directives come before block
			// pub foo: Type @directive { ... } OR pub foo @directive = { ... }
			f.formatSuffixDirectives(s.Directives, nameLine)
			if s.Type_ != nil {
				f.write(" {")
				f.formatBlockContents(block)
				f.write("}")
			} else {
				f.write(" = {")
				f.formatBlockContents(block)
				f.write("}")
			}
		} else {
			// Non-block value: suffix directives come after value
			// pub foo: Type = value @directive OR pub foo = value @directive
			f.write(" = ")
			f.formatNode(s.Value)
			f.formatSuffixDirectives(s.Directives, nameLine)
		}
	} else {
		// No value - just type with suffix directives
		// pub foo: Type @directive
		f.formatSuffixDirectives(s.Directives, nameLine)
	}
}

func (f *Formatter) formatFunDecl(fn *FunDecl) {
	// This is typically called via SlotDecl, but handle standalone case
	nameLine := 0
	if fn.Loc != nil {
		nameLine = fn.Loc.Line
	}
	f.formatFunDeclSignature(fn, nameLine)
}

func (f *Formatter) formatFunDeclSignature(fn *FunDecl, slotNameLine int) {
	// Arguments
	if len(fn.Args) > 0 || fn.BlockParam != nil {
		f.write("(")
		f.formatFunctionArgs(fn.Args, fn.BlockParam, fn.Loc)
		f.write(")")
	}

	// Return type
	if fn.Ret != nil {
		f.write(": ")
		f.formatTypeNode(fn.Ret)
	}

	// Directives (only suffix; prefix directives are emitted by formatSlotDecl)
	// When args are multiline, the closing "): Type @directive" is on a later
	// line than slotNameLine. Compute the line of the closing paren / return
	// type so that directives on the same line aren't forced onto a new line.
	directivePrecedingLine := slotNameLine
	if fn.Ret != nil {
		if retLine := typeNodeLine(fn.Ret); retLine > directivePrecedingLine {
			directivePrecedingLine = retLine
		}
	}
	if directivePrecedingLine == slotNameLine {
		// No return type or return type on same line as name — check last arg
		lastArg := fn.BlockParam
		if lastArg == nil && len(fn.Args) > 0 {
			lastArg = fn.Args[len(fn.Args)-1]
		}
		if lastArg != nil {
			if argEnd := nodeEndLine(lastArg); argEnd > directivePrecedingLine {
				// The closing paren is on or after the last arg's line
				directivePrecedingLine = argEnd
			}
		}
	}
	f.formatSuffixDirectives(fn.Directives, directivePrecedingLine)

	// Body
	if fn.FunctionBase.Body != nil {
		if block, ok := fn.FunctionBase.Body.(*Block); ok {
			f.write(" {")
			f.formatBlockContents(block)
			f.write("}")
		} else {
			f.write(" {")
			if fn.Loc != nil {
				f.nl(fn.Loc.Line)
			} else {
				f.newline()
			}
			f.indented(func() {
				f.writeIndent()
				f.formatNode(fn.FunctionBase.Body)
				f.newline()
			})
			f.writeIndent()
			f.write("}")
		}
	}
}

func (f *Formatter) formatFunctionArgs(args []*SlotDecl, blockParam *SlotDecl, parentLoc *SourceLocation) {
	// Check if any arg has a docstring - if so, force multiline
	hasDocString := false
	for _, arg := range args {
		if arg.DocString != "" {
			hasDocString = true
			break
		}
	}
	if blockParam != nil && blockParam.DocString != "" {
		hasDocString = true
	}

	// Check if args were originally on multiple lines
	wasMultiline := false
	// If the first arg is on a different line than the parent declaration, it's multiline
	if parentLoc != nil && len(args) > 0 {
		firstArgLoc := args[0].GetSourceLocation()
		if firstArgLoc != nil && firstArgLoc.Line > parentLoc.Line {
			wasMultiline = true
		}
	}
	// Also check if any arg starts on a different line than a previous arg
	for i := 1; i < len(args); i++ {
		prevLoc := args[i-1].GetSourceLocation()
		currLoc := args[i].GetSourceLocation()
		if prevLoc != nil && currLoc != nil && currLoc.Line > prevLoc.Line {
			wasMultiline = true
			break
		}
	}
	// Also check block param
	if !wasMultiline && blockParam != nil && len(args) > 0 {
		lastArgLoc := args[len(args)-1].GetSourceLocation()
		blockParamLoc := blockParam.GetSourceLocation()
		if lastArgLoc != nil && blockParamLoc != nil && blockParamLoc.Line > lastArgLoc.Line {
			wasMultiline = true
		}
	}

	// Estimate total length
	totalLen := 0
	for i, arg := range args {
		if i > 0 {
			totalLen += 2 // ", "
		}
		totalLen += f.estimateArgLength(arg)
	}
	if blockParam != nil {
		if len(args) > 0 {
			totalLen += 2
		}
		totalLen += f.estimateArgLength(blockParam) + 1 // +1 for &
	}

	multiline := hasDocString || wasMultiline || f.col+totalLen > maxLineLength

	if multiline {
		f.newline()
		f.indented(func() {
			for _, arg := range args {
				f.emitCommentsForNode(arg)
				f.writeIndent()
				f.formatArgDecl(arg)
				f.write(",")
				f.newline()
			}
			if blockParam != nil {
				f.emitCommentsForNode(blockParam)
				f.writeIndent()
				f.write("&")
				f.formatArgDecl(blockParam)
				f.write(",")
				f.newline()
			}
		})
		f.writeIndent()
	} else {
		for i, arg := range args {
			if i > 0 {
				f.write(", ")
			}
			f.formatArgDecl(arg)
		}
		if blockParam != nil {
			if len(args) > 0 {
				f.write(", ")
			}
			f.write("&")
			f.formatArgDecl(blockParam)
		}
	}
}

func (f *Formatter) estimateArgLength(arg *SlotDecl) int {
	length := len(arg.Name.Name)
	if arg.Type_ != nil {
		length += 2 + f.estimateTypeLength(arg.Type_)
	}
	if arg.Value != nil {
		length += 3 + f.estimateLength(arg.Value)
	}
	return length
}

func (f *Formatter) formatArgDecl(arg *SlotDecl) {
	// Doc string for arg (only in multiline mode)
	if arg.DocString != "" {
		f.formatDocString(arg.DocString)
		f.writeIndent()
	}

	// Prefix directives (those that appeared before the name)
	for _, d := range arg.Directives {
		if d.IsPrefix {
			f.formatDirectiveApplication(d)
			f.write(" ")
		}
	}

	f.write(arg.Name.Name)

	if arg.Type_ != nil {
		// Block params have special syntax: &block(args): ret instead of &block: (args): ret
		if arg.IsBlockParam {
			if funType, ok := arg.Type_.(FunTypeNode); ok {
				f.formatBlockParamType(funType)
			} else {
				f.write(": ")
				f.formatTypeNode(arg.Type_)
			}
		} else {
			f.write(": ")
			f.formatTypeNode(arg.Type_)
		}
	}

	// Suffix directives (those that appeared after the type)
	for _, d := range arg.Directives {
		if !d.IsPrefix {
			f.write(" ")
			f.formatDirectiveApplication(d)
		}
	}

	if arg.Value != nil {
		f.write(" = ")
		f.formatNode(arg.Value)
	}
}

func (f *Formatter) formatBlockParamType(funType FunTypeNode) {
	// Format as (args): retType or just : retType if no args
	if len(funType.Args) > 0 {
		f.write("(")
		for i, arg := range funType.Args {
			if i > 0 {
				f.write(", ")
			}
			f.write(arg.Name.Name)
			f.write(": ")
			f.formatTypeNode(arg.Type_)
		}
		f.write(")")
	}
	f.write(": ")
	f.formatTypeNode(funType.Ret)
}

func (f *Formatter) formatDirectiveDecl(d *DirectiveDecl) {
	if d.DocString != "" {
		f.formatDocString(d.DocString)
	}

	f.write("directive @")
	f.write(d.Name)

	if len(d.Args) > 0 {
		f.write("(")
		f.formatFunctionArgs(d.Args, nil, nil)
		f.write(")")
	}

	f.write(" on ")
	for i, loc := range d.Locations {
		if i > 0 {
			f.write(" | ")
		}
		f.write(loc.Name)
	}
}

func (f *Formatter) formatImportDecl(i *ImportDecl) {
	f.write("import ")
	f.write(i.Name.Name)
}

func (f *Formatter) formatDirectiveApplication(d *DirectiveApplication) {
	f.write("@")
	if d.Scope != nil {
		f.write(d.Scope.Name)
		f.write(".")
	}
	f.write(d.Name)

	if len(d.Args) > 0 {
		f.write("(")
		f.formatCallArgs(d.Args, false)
		f.write(")")
	}
}

func (f *Formatter) formatBlock(b *Block) {
	f.write("{")
	f.formatBlockContents(b)
	f.write("}")
}

func (f *Formatter) formatBlockContents(b *Block) {
	if len(b.Forms) == 0 {
		// Even with no forms, there may be comments inside the block
		if b.Loc != nil && b.Loc.End != nil && f.hasCommentsInBlock(b) {
			f.nl(b.Loc.Line)
			f.indented(func() {
				f.lastLine = b.Loc.Line
				f.emitCommentsBeforeNode(b.Loc.End.Line, false)
			})
			f.writeIndent()
		}
		return
	}

	// Check if block can be single line (only if no comments and wasn't originally multiline)
	if !wasMultiline(b) && f.canFormatBlockInline(b) {
		// Trial-render all forms to check none produce multiline output
		allInline := true
		for _, form := range b.Forms {
			trial := &Formatter{comments: f.comments, source: f.source}
			trial.formatNode(form)
			if strings.Contains(trial.buf.String(), "\n") {
				allInline = false
				break
			}
		}
		if allInline {
			f.write(" ")
			for i, form := range b.Forms {
				if i > 0 {
					f.write(", ")
				}
				f.formatNode(form)
			}
			f.write(" ")
			return
		}
	}

	// Use nl() to emit any trailing comment on the opening { line
	if b.Loc != nil {
		f.nl(b.Loc.Line)
	} else {
		f.newline()
	}
	f.indented(func() {
		f.resetLastLineForForms(b.Forms)
		for _, form := range b.Forms {
			if f.handleNoFmtForm(form) {
				f.newline()
				continue
			}
			f.emitCommentsForNode(form)
			f.writeIndent()
			f.formatNode(form)
			f.finishForm(form)
			f.newline()
		}

		// Emit comments between last form and closing }
		if b.Loc != nil && b.Loc.End != nil {
			f.emitCommentsBeforeNode(b.Loc.End.Line, false)
		}
	})
	f.writeIndent()
}

// canFormatBlockInline checks if a block's forms can all be rendered inline
func (f *Formatter) canFormatBlockInline(b *Block) bool {
	for _, form := range b.Forms {
		if f.isMultilineNode(form) || f.hasCommentsBeforeNode(form) {
			return false
		}
	}
	// Also reject inlining if there are comments after the last form
	if f.hasCommentsInBlock(b) {
		lastForm := b.Forms[len(b.Forms)-1]
		lastLine := nodeEndLine(lastForm)
		if lastLine == 0 {
			if loc := lastForm.GetSourceLocation(); loc != nil {
				lastLine = loc.Line
			}
		}
		if b.Loc != nil && b.Loc.End != nil && lastLine > 0 {
			for _, c := range f.comments {
				if c.Line > lastLine && c.Line < b.Loc.End.Line && !c.IsTrailing {
					return false
				}
			}
		}
	}
	return true
}

// hasCommentsInBlock checks if there are standalone comments inside a block's line range
func (f *Formatter) hasCommentsInBlock(b *Block) bool {
	if b.Loc == nil || b.Loc.End == nil {
		return false
	}
	for _, c := range f.comments {
		if c.Line > b.Loc.Line && c.Line < b.Loc.End.Line && !c.IsTrailing {
			return true
		}
	}
	return false
}

// hasCommentsBeforeNode checks if there are standalone comments that would precede this node
func (f *Formatter) hasCommentsBeforeNode(node Node) bool {
	if loc := node.GetSourceLocation(); loc != nil && loc.Line > 0 {
		for _, c := range f.comments {
			if c.Line < loc.Line && !c.IsTrailing {
				return true
			}
		}
	}
	return false
}

// wasMultiline checks if a node was originally written across multiple lines
func wasMultiline(node Node) bool {
	loc := node.GetSourceLocation()
	if loc == nil {
		return false
	}

	// Check if node has an explicit end position on a different line
	if loc.End != nil && loc.End.Line > loc.Line {
		return true
	}

	// For blocks, check if any form is on a different line than the block start
	if block, ok := node.(*Block); ok {
		for _, form := range block.Forms {
			if formLoc := form.GetSourceLocation(); formLoc != nil && formLoc.Line != loc.Line {
				return true
			}
		}
	}

	// For function calls, check if args span multiple lines
	if call, ok := node.(*FunCall); ok {
		if len(call.Args) > 0 {
			firstArg := call.Args[0]
			lastArg := call.Args[len(call.Args)-1]
			firstLoc := firstArg.Value.GetSourceLocation()
			lastLoc := lastArg.Value.GetSourceLocation()
			if firstLoc != nil && lastLoc != nil && lastLoc.Line > firstLoc.Line {
				return true
			}
		}
		// Check if block arg is on different line
		if call.BlockArg != nil {
			if argLoc := call.BlockArg.BodyNode.GetSourceLocation(); argLoc != nil && argLoc.Line != loc.Line {
				return true
			}
		}
	}

	// For method chains (Select), check if receiver is on different line
	if sel, ok := node.(*Select); ok && sel.Receiver != nil {
		if recvLoc := sel.Receiver.GetSourceLocation(); recvLoc != nil && recvLoc.Line != loc.Line {
			return true
		}
	}

	return false
}

func (f *Formatter) isMultilineNode(node Node) bool {
	switch n := node.(type) {
	case *Block:
		return true
	case *Conditional:
		return wasMultiline(n)
	case *ForLoop:
		return true
	case *Case:
		return true
	case *FunCall:
		// Check if it's a chain that should be split
		return f.isChainedCall(n) && f.estimateLength(n) > maxLineLength-f.col
	default:
		return false
	}
}

func (f *Formatter) isChainedCall(node Node) bool {
	switch n := node.(type) {
	case *FunCall:
		if _, ok := n.Fun.(*Select); ok {
			return true
		}
		return f.isChainedCall(n.Fun)
	case *Select:
		return true
	default:
		return false
	}
}

func (f *Formatter) formatFunCall(c *FunCall, forceMultiline bool) {
	// Check if this is a method chain that should be split
	if f.shouldSplitChain(c) {
		f.formatChainedCall(c)
		return
	}

	f.formatNode(c.Fun)

	// Format arguments
	if len(c.Args) > 0 {
		f.write("(")
		// Use the opening paren's line for arg splitting decisions. For method
		// calls (Select), this is the field name's line rather than the overall
		// expression start, so that e.g. a multiline list receiver doesn't
		// force the method's args onto separate lines.
		parenLoc := c.Loc
		if sel, ok := c.Fun.(*Select); ok && sel.Field.Loc != nil {
			parenLoc = sel.Field.Loc
		}
		multiline := forceMultiline || f.shouldSplitArgs(c.Args, parenLoc)
		f.formatCallArgs(c.Args, multiline)
		f.write(")")
	}

	// Block arg
	if c.BlockArg != nil {
		f.write(" ")
		f.formatBlockArg(c.BlockArg)
	}
}

func (f *Formatter) formatFunCallInline(c *FunCall) {
	f.formatNodeInline(c.Fun)
	if len(c.Args) > 0 {
		f.write("(")
		f.formatCallArgs(c.Args, false)
		f.write(")")
	}
	if c.BlockArg != nil {
		f.write(" ")
		f.formatBlockArg(c.BlockArg)
	}
}

// nodeEndLineForChain returns the end line of a node using its direct Loc field,
// which includes the full span (args, block args, etc.)
func (f *Formatter) nodeEndLineForChain(node Node) int {
	switch n := node.(type) {
	case *FunCall:
		if n.Loc != nil && n.Loc.End != nil {
			return n.Loc.End.Line
		}
		if n.Loc != nil {
			return n.Loc.Line
		}
	case *Select:
		if n.Loc != nil && n.Loc.End != nil {
			return n.Loc.End.Line
		}
		if n.Loc != nil {
			return n.Loc.Line
		}
	}
	// Fall back to nodeEndLine for other types
	return nodeEndLine(node)
}

func (f *Formatter) shouldSplitChain(c *FunCall) bool {
	// Only split if the chain itself was originally on multiple lines
	return f.wasChainMultiline(c)
}

// wasChainMultiline checks if any part of a method chain was on a different line
func (f *Formatter) wasChainMultiline(node Node) bool {
	switch n := node.(type) {
	case *FunCall:
		if sel, ok := n.Fun.(*Select); ok {
			// Use Field.Loc to precisely check if the field name is on a
			// different line than the receiver ends. This avoids false
			// positives from dots elsewhere on the same source line.
			if sel.Field.Loc != nil {
				recvEndLine := f.nodeEndLineForChain(sel.Receiver)
				if recvEndLine > 0 && sel.Field.Loc.Line > recvEndLine {
					return true
				}
			}
			return f.wasChainMultiline(sel.Receiver)
		}
		return false
	case *Select:
		if n.Field.Loc != nil && n.Receiver != nil {
			recvEndLine := f.nodeEndLineForChain(n.Receiver)
			if recvEndLine > 0 && n.Field.Loc.Line > recvEndLine {
				return true
			}
		}
		if n.Receiver != nil {
			return f.wasChainMultiline(n.Receiver)
		}
		return false
	default:
		return false
	}
}

func (f *Formatter) formatChainedCall(c *FunCall) {
	// Collect the chain
	var chain []Node
	var root Node
	f.collectChain(c, &chain, &root)

	// Format root
	f.formatNode(root)

	// Emit trailing comment on the root's line before breaking to chain
	if rootEndLine := nodeEndLine(root); rootEndLine > 0 {
		f.emitTrailingComment(rootEndLine)
	}

	// Format chain elements with leading dots
	f.indented(func() {
		for _, elem := range chain {
			// Emit any standalone comments that precede this chain element
			var elemLine int
			switch e := elem.(type) {
			case *FunCall:
				if sel, ok := e.Fun.(*Select); ok && sel.Field.Loc != nil {
					elemLine = sel.Field.Loc.Line
				}
			case *Select:
				if e.Field.Loc != nil {
					elemLine = e.Field.Loc.Line
				}
			}
			if elemLine > 0 {
				f.emitChainCommentsBefore(elemLine)
			}

			f.newline()
			f.writeIndent()
			f.write(".")
			switch e := elem.(type) {
			case *FunCall:
				if sel, ok := e.Fun.(*Select); ok {
					f.write(sel.Field.Name)
				}
				if len(e.Args) > 0 {
					f.write("(")
					// In chains, don't use single-arg line check since we've already
					// reformatted the chain structure. Only split if multiple args
					// were on different lines.
					multiline := f.shouldSplitArgs(e.Args, nil)
					f.formatCallArgs(e.Args, multiline)
					f.write(")")
				}
				if e.BlockArg != nil {
					f.write(" ")
					f.formatBlockArg(e.BlockArg)
				}
				// Emit trailing comment for this chain element
				// Use the FunCall's Loc directly to get the actual span
				if e.Loc != nil && e.Loc.End != nil {
					f.emitTrailingComment(e.Loc.End.Line)
				}
			case *Select:
				f.write(e.Field.Name)
				// Use the Select's Loc directly to get the actual span
				if e.Loc != nil && e.Loc.End != nil {
					f.emitTrailingComment(e.Loc.End.Line)
				}
			}
		}
	})
}

func (f *Formatter) collectChain(node Node, chain *[]Node, root *Node) {
	switch n := node.(type) {
	case *FunCall:
		if sel, ok := n.Fun.(*Select); ok {
			f.collectChain(sel.Receiver, chain, root)
			*chain = append(*chain, n)
		} else {
			*root = node
		}
	case *Select:
		f.collectChain(n.Receiver, chain, root)
		*chain = append(*chain, n)
	default:
		*root = node
	}
}

func (f *Formatter) shouldSplitArgs(args Record, callLoc *SourceLocation) bool {
	if len(args) == 0 {
		return false
	}

	// Check if any arg STARTS on a different line than the previous arg STARTS
	// This respects user's intent to keep args on the same line
	for i := 1; i < len(args); i++ {
		prevLoc := args[i-1].Value.GetSourceLocation()
		currLoc := args[i].Value.GetSourceLocation()
		if prevLoc != nil && currLoc != nil && currLoc.Line > prevLoc.Line {
			return true
		}
	}

	// For a single arg, check if it's on a different line than the opening paren
	if len(args) == 1 && callLoc != nil {
		argLoc := args[0].Value.GetSourceLocation()
		if argLoc != nil && argLoc.Line > callLoc.Line {
			return true
		}
	}

	// Check if any arg spans multiple lines (e.g., a multiline chain)
	for _, arg := range args {
		if f.isMultilineNode(arg.Value) {
			return true
		}
	}

	// When there are multiple args and any arg would start past
	// maxLineLength, force splitting even if the original was one line.
	if len(args) > 1 {
		col := f.col
		for i, arg := range args {
			if i > 0 {
				col += 2 // ", "
			}
			if col >= maxLineLength {
				return true
			}
			// Skip past this arg's rendered length
			if !arg.Positional && arg.Key != "" {
				col += len(arg.Key) + 2 // "key: "
			}
			col += f.estimateLength(arg.Value)
		}
	}

	// All args were on the same line - keep them that way
	return false
}

func (f *Formatter) formatCallArgs(args []Keyed[Node], multiline bool) {
	if multiline {
		f.newline()
		f.indented(func() {
			for _, arg := range args {
				f.writeIndent()
				if !arg.Positional && arg.Key != "" {
					f.write(arg.Key)
					f.write(": ")
				}
				f.formatNode(arg.Value)
				f.write(",")
				f.newline()
			}
		})
		f.writeIndent()
	} else {
		for i, arg := range args {
			if i > 0 {
				f.write(", ")
			}
			if !arg.Positional && arg.Key != "" {
				f.write(arg.Key)
				f.write(": ")
			}
			f.formatNode(arg.Value)
		}
	}
}

func (f *Formatter) formatSelect(s *Select, forceMultiline bool) {
	// Check if this is a multiline chain that should be formatted with leading dots
	if f.wasChainMultiline(s) || forceMultiline {
		f.formatSelectChain(s)
		return
	}
	f.formatNode(s.Receiver)
	f.write(".")
	f.write(s.Field.Name)
}

// formatSelectChain formats a select chain with leading dots on new lines
func (f *Formatter) formatSelectChain(s *Select) {
	// Collect the chain - keep the Select nodes for trailing comments
	var selects []*Select
	var root Node
	current := Node(s)
	for {
		if sel, ok := current.(*Select); ok {
			selects = append([]*Select{sel}, selects...)
			current = sel.Receiver
		} else {
			root = current
			break
		}
	}

	// Format the root
	f.formatNode(root)

	// Emit trailing comment on the root's line before breaking to chain
	if rootEndLine := nodeEndLine(root); rootEndLine > 0 {
		f.emitTrailingComment(rootEndLine)
	}

	// Format each field on its own line with a leading dot, indented one level
	f.indented(func() {
		for _, sel := range selects {
			// Emit any standalone comments that precede this chain element
			if sel.Field.Loc != nil && sel.Field.Loc.Line > 0 {
				f.emitChainCommentsBefore(sel.Field.Loc.Line)
			}

			f.newline()
			f.writeIndent()
			f.write(".")
			f.write(sel.Field.Name)
			// Emit trailing comment for this select
			if sel.Loc != nil && sel.Loc.End != nil {
				f.emitTrailingComment(sel.Loc.End.Line)
			}
		}
	})
}

func (f *Formatter) formatSelectInline(s *Select) {
	f.formatNodeInline(s.Receiver)
	f.write(".")
	f.write(s.Field.Name)
}

func (f *Formatter) formatSymbol(s *Symbol) {
	f.write(s.Name)
}

func (f *Formatter) formatString(s *String) {
	// Preserve triple-quoted strings as triple-quoted
	if s.TripleQuoted {
		// If the original was on a single line, keep it inline: """value"""
		wasInline := s.Loc != nil && (s.Loc.End == nil || s.Loc.End.Line == s.Loc.Line)
		if wasInline {
			f.write(`"""`)
			f.write(s.Value)
			f.write(`"""`)
			return
		}
		f.write(`"""`)
		f.newline()
		lines := strings.Split(s.Value, "\n")
		for _, line := range lines {
			if line != "" {
				f.writeIndent()
				f.write(line)
			}
			f.newline()
		}
		f.writeIndent()
		f.write(`"""`)
	} else {
		// Use regular quoted string
		f.write(strconv.Quote(s.Value))
	}
}

func (f *Formatter) formatInt(i *Int) {
	f.write(strconv.FormatInt(i.Value, 10))
}

func (f *Formatter) formatFloat(fl *Float) {
	// Use original text if available
	if fl.Text != "" {
		f.write(fl.Text)
		return
	}

	// Fallback: format the value
	s := strconv.FormatFloat(fl.Value, 'f', -1, 64)
	if !strings.Contains(s, ".") {
		s += ".0"
	}
	f.write(s)
}

func (f *Formatter) formatBoolean(b *Boolean) {
	if b.Value {
		f.write("true")
	} else {
		f.write("false")
	}
}

func (f *Formatter) formatList(l *List) {
	if len(l.Elements) == 0 {
		f.write("[]")
		return
	}

	// Check if list was originally multiline (elements on different lines)
	if f.wasListMultiline(l) {
		f.formatListMultiline(l)
		return
	}

	// List was originally on one line - keep it that way
	// (respect user's intent over line length)
	f.formatListInline(l)
}

func (f *Formatter) wasListMultiline(l *List) bool {
	if len(l.Elements) == 0 {
		return false
	}

	// Check if any element STARTS on a different line than the previous element STARTS
	// This distinguishes between:
	//   ["a", "b", """multiline"""] - elements start on same line, not multiline
	//   ["a",
	//    "b"] - elements start on different lines, is multiline
	for i := 1; i < len(l.Elements); i++ {
		prevLoc := l.Elements[i-1].GetSourceLocation()
		currLoc := l.Elements[i].GetSourceLocation()
		if prevLoc != nil && currLoc != nil && currLoc.Line > prevLoc.Line {
			return true
		}
	}

	// For single-element lists, check if the element starts on a different line
	// than the opening bracket (the list's start line)
	if len(l.Elements) == 1 && l.Loc != nil {
		elemLoc := l.Elements[0].GetSourceLocation()
		if elemLoc != nil && elemLoc.Line > l.Loc.Line {
			return true
		}
	}

	return false
}

func (f *Formatter) formatListInline(l *List) {
	f.write("[")
	for i, elem := range l.Elements {
		if i > 0 {
			f.write(", ")
		}
		f.formatNodeInline(elem)
	}
	f.write("]")
}

func (f *Formatter) formatListMultiline(l *List) {
	f.write("[")
	if l.Loc != nil {
		f.nl(l.Loc.Line)
	} else {
		f.newline()
	}
	f.indented(func() {
		// Reset lastLine to prevent spurious blank line at start of list
		if len(l.Elements) > 0 {
			if loc := l.Elements[0].GetSourceLocation(); loc != nil && loc.Line > 0 {
				f.lastLine = loc.Line - 1
			}
		}

		// Group elements by their original line - elements on the same line stay together
		i := 0
		for i < len(l.Elements) {
			elem := l.Elements[i]
			elemLoc := elem.GetSourceLocation()

			f.emitCommentsForNode(elem)
			f.writeIndent()
			f.formatNode(elem)

			// Check if next elements are on the same line - if so, keep them together
			for i+1 < len(l.Elements) {
				nextElem := l.Elements[i+1]
				nextLoc := nextElem.GetSourceLocation()
				if elemLoc != nil && nextLoc != nil && nextLoc.Line == elemLoc.Line {
					f.write(", ")
					f.formatNode(nextElem)
					i++
				} else {
					break
				}
			}

			// Always add trailing comma
			f.write(",")

			if elemLoc != nil {
				f.emitTrailingComment(elemLoc.Line)
			}
			f.newline()
			i++
		}
	})
	f.writeIndent()
	f.write("]")
}

func (f *Formatter) formatObject(o *Object) {
	if len(o.Slots) == 0 {
		f.write("{{}}")
		return
	}

	// Check if object was originally multiline
	wasMultiline := o.Loc != nil && o.Loc.End != nil && o.Loc.End.Line > o.Loc.Line

	// Estimate inline length
	length := 4 // {{}}
	for i, slot := range o.Slots {
		if i > 0 {
			length += 2
		}
		length += len(slot.Name.Name) + 2 + f.estimateLength(slot.Value)
	}

	if !wasMultiline && f.col+length <= maxLineLength {
		f.formatObjectInline(o)
	} else {
		f.formatObjectMultiline(o)
	}
}

func (f *Formatter) formatObjectInline(o *Object) {
	f.write("{{")
	for i, slot := range o.Slots {
		if i > 0 {
			f.write(", ")
		}
		f.write(slot.Name.Name)
		f.write(": ")
		f.formatNodeInline(slot.Value)
	}
	f.write("}}")
}

func (f *Formatter) formatObjectMultiline(o *Object) {
	f.write("{{")
	// Emit trailing comment on the opening {{ line
	if o.Loc != nil {
		f.emitTrailingComment(o.Loc.Line)
	}
	f.newline()
	f.indented(func() {
		// Reset lastLine to prevent spurious blank line at start
		if len(o.Slots) > 0 {
			if loc := o.Slots[0].GetSourceLocation(); loc != nil && loc.Line > 0 {
				f.lastLine = loc.Line - 1
			}
		}
		for i, slot := range o.Slots {
			f.emitCommentsForNode(slot)
			f.writeIndent()
			f.write(slot.Name.Name)
			f.write(": ")
			f.formatNode(slot.Value)
			// Add trailing comma (except for last slot)
			if i < len(o.Slots)-1 {
				f.write(",")
			}
			if loc := slot.GetSourceLocation(); loc != nil {
				f.emitTrailingComment(loc.Line)
			}
			f.newline()
		}
	})
	f.writeIndent()
	f.write("}}")
}

func (f *Formatter) formatConditional(c *Conditional) {
	f.write("if (")
	f.formatNode(c.Condition)
	f.write(") {")
	f.formatBlockContents(c.Then)
	f.write("}")

	if c.Else != nil {
		f.write(" else ")
		// Check if else contains another conditional (else if)
		if elseBlock, ok := c.Else.(*Block); ok {
			if len(elseBlock.Forms) == 1 {
				if cond, ok := elseBlock.Forms[0].(*Conditional); ok {
					f.formatConditional(cond)
					return
				}
			}
			f.write("{")
			f.formatBlockContents(elseBlock)
			f.write("}")
		}
	}
}

func (f *Formatter) formatForLoop(l *ForLoop) {
	f.write("for ")

	if l.KeyVariable != "" {
		// Two-variable iteration: for (key, value in iterable)
		f.write("(")
		f.write(l.KeyVariable)
		f.write(", ")
		f.write(l.ValueVariable)
		f.write(" in ")
		f.formatNode(l.Iterable)
		f.write(") ")
	} else if l.Variable != "" {
		// Single-variable iteration: for (var in iterable)
		f.write("(")
		f.write(l.Variable)
		if l.Type != nil {
			f.write(": ")
			f.formatTypeNode(l.Type)
		}
		f.write(" in ")
		f.formatNode(l.Iterable)
		f.write(") ")
	} else if l.Condition != nil {
		// Condition loop: for (condition)
		f.write("(")
		f.formatNode(l.Condition)
		f.write(") ")
	}
	// else: infinite loop - just "for { ... }"

	f.write("{")
	f.formatBlockContents(l.LoopBody)
	f.write("}")
}

func (f *Formatter) formatCase(c *Case) {
	if c.NoOperand {
		f.write("case {")
	} else {
		f.write("case (")
		f.formatNode(c.Expr)
		f.write(") {")
	}
	if c.Loc != nil {
		f.nl(c.Loc.Line)
	} else {
		f.newline()
	}
	f.indented(func() {
		// Reset lastLine to prevent spurious blank line at start of case body
		if len(c.Clauses) > 0 && c.Clauses[0].Loc != nil {
			f.lastLine = c.Clauses[0].Loc.Line - 1
		}
		for _, clause := range c.Clauses {
			// Emit standalone comments before this clause
			if clause.Loc != nil {
				f.emitCommentsBeforeNode(clause.Loc.Line, false)
				f.lastLine = clause.Loc.Line
			}
			f.writeIndent()
			if clause.IsElse {
				f.write("else")
			} else if clause.IsTypePattern() {
				f.write(clause.Binding)
				f.write(": ")
				f.write(clause.TypePattern.Name)
			} else {
				f.formatNode(clause.Value)
			}
			f.write(" => ")
			f.formatNode(clause.Expr)
			// Emit trailing comment on the clause line
			if clause.Loc != nil {
				f.emitTrailingComment(clause.Loc.Line)
			}
			f.newline()
		}
	})
	f.writeIndent()
	f.write("}")
}

func (f *Formatter) formatAssert(a *Assert) {
	f.write("assert")
	if a.Message != nil {
		f.write("(")
		f.formatNode(a.Message)
		f.write(")")
	}
	f.write(" {")
	f.formatBlockContents(a.Block)
	f.write("}")
}

func (f *Formatter) formatTryCatch(t *TryCatch) {
	f.write("try {")
	f.formatBlockContents(t.TryBody)
	f.write("} catch {")
	if t.Loc != nil {
		f.nl(t.Loc.Line)
	} else {
		f.newline()
	}
	f.indented(func() {
		if len(t.Clauses) > 0 && t.Clauses[0].Loc != nil {
			f.lastLine = t.Clauses[0].Loc.Line - 1
		}
		for _, clause := range t.Clauses {
			if clause.Loc != nil {
				f.emitCommentsBeforeNode(clause.Loc.Line, false)
				f.lastLine = clause.Loc.Line
			}
			f.writeIndent()
			if clause.IsTypePattern() {
				f.write(clause.Binding)
				f.write(": ")
				f.write(clause.TypePattern.Name)
			} else if clause.Binding != "" {
				f.write(clause.Binding)
			}
			f.write(" => ")
			f.formatNode(clause.Expr)
			if clause.Loc != nil {
				f.emitTrailingComment(clause.Loc.Line)
			}
			f.newline()
		}
	})
	f.writeIndent()
	f.write("}")
}

func (f *Formatter) formatRaise(r *Raise) {
	f.write("raise ")
	f.formatNode(r.Value)
}

func (f *Formatter) formatReassignment(r *Reassignment) {
	f.formatNode(r.Target)
	switch r.Modifier {
	case "+":
		f.write(" += ")
	default:
		f.write(" = ")
	}
	f.formatNode(r.Value)
}

func (f *Formatter) formatTypeHint(t *TypeHint) {
	// Check if expr needs parens (binary ops, etc.)
	needsParens := exprNeedsParensForTypeHint(t.Expr)
	if needsParens {
		f.write("(")
	}
	f.formatNode(t.Expr)
	if needsParens {
		f.write(")")
	}
	f.write(" :: ")
	f.formatTypeNode(t.Type)
}

func exprNeedsParensForTypeHint(node Node) bool {
	switch node.(type) {
	case *Addition, *Subtraction, *Multiplication, *Division, *Modulo:
		return true
	case *Equality, *Inequality:
		return true
	case *LessThan, *LessThanEqual, *GreaterThan, *GreaterThanEqual:
		return true
	case *LogicalAnd, *LogicalOr:
		return true
	case *Default:
		return true
	default:
		return false
	}
}

func (f *Formatter) formatIndex(i *Index) {
	f.formatNode(i.Receiver)
	f.write("[")
	f.formatNode(i.Index)
	f.write("]")
}

func (f *Formatter) formatObjectSelection(o *ObjectSelection) {
	// Receiver may be nil for nested selections (e.g., the inner {c, d} in a.{b.{c, d}})
	if o.Receiver != nil {
		f.formatNode(o.Receiver)
		f.write(".")
	}

	// Check if the selection was originally multiline
	multiline := o.Loc != nil && o.Loc.End != nil && o.Loc.End.Line > o.Loc.Line

	// Handle inline fragments
	if len(o.InlineFragments) > 0 {
		f.write("{")
		if multiline {
			f.newline()
			f.indented(func() {
				// Reset lastLine to prevent spurious blank line at start
				if len(o.InlineFragments) > 0 && o.InlineFragments[0].Loc != nil {
					f.lastLine = o.InlineFragments[0].Loc.Line - 1
				}

				for _, frag := range o.InlineFragments {
					if frag.Loc != nil {
						f.emitCommentsBeforeNode(frag.Loc.Line, false)
						f.lastLine = frag.Loc.Line
					}
					f.writeIndent()
					f.formatInlineFragment(frag)
					if frag.Loc != nil {
						f.emitTrailingComment(frag.Loc.Line)
						f.lastLine = frag.Loc.Line
					}
					f.newline()
				}

				// Emit comments between last fragment and closing }
				if o.Loc != nil && o.Loc.End != nil {
					f.emitCommentsBeforeNode(o.Loc.End.Line, false)
				}
			})
			f.writeIndent()
		} else {
			for i, frag := range o.InlineFragments {
				if i > 0 {
					f.write(", ")
				}
				f.formatInlineFragment(frag)
			}
		}
		f.write("}")
		return
	}

	f.write("{")
	if multiline {
		f.newline()
		f.indented(func() {
			// Reset lastLine to prevent spurious blank line at start
			if len(o.Fields) > 0 && o.Fields[0].Loc != nil {
				f.lastLine = o.Fields[0].Loc.Line - 1
			}

			for i, field := range o.Fields {
				if field.Loc != nil {
					f.emitCommentsBeforeNode(field.Loc.Line, false)
					f.lastLine = field.Loc.Line
				}
				f.writeIndent()
				f.formatFieldSelection(field)
				if i < len(o.Fields)-1 {
					f.write(",")
				}
				if field.Loc != nil {
					f.emitTrailingComment(field.Loc.Line)
					endLine := field.Loc.Line
					if field.Selection != nil && field.Selection.Loc != nil && field.Selection.Loc.End != nil {
						endLine = field.Selection.Loc.End.Line
					}
					f.lastLine = endLine
				}
				f.newline()
			}

			// Emit comments between last field and closing }
			if o.Loc != nil && o.Loc.End != nil {
				f.emitCommentsBeforeNode(o.Loc.End.Line, false)
			}
		})
		f.writeIndent()
	} else {
		for i, field := range o.Fields {
			if i > 0 {
				f.write(", ")
			}
			f.formatFieldSelection(field)
		}
	}
	f.write("}")
}

func (f *Formatter) formatInlineFragment(frag *InlineFragment) {
	f.write("... on ")
	f.write(frag.TypeName.Name)
	f.write(" {")
	for i, field := range frag.Fields {
		if i > 0 {
			f.write(",")
		}
		f.write(" ")
		f.formatFieldSelection(field)
	}
	f.write(" }")
}

func (f *Formatter) formatFieldSelection(field *FieldSelection) {
	f.write(field.Name)
	if len(field.Args) > 0 {
		f.write("(")
		f.formatCallArgs(field.Args, false)
		f.write(")")
	}
	if field.Selection != nil {
		f.write(".")
		f.formatObjectSelection(field.Selection)
	}
}

func (f *Formatter) formatBlockArg(b *BlockArg) {
	f.write("{")

	block, isBlock := b.BodyNode.(*Block)
	singleLineBody := isBlock && len(block.Forms) == 1 && !wasMultiline(block)

	if len(b.Args) > 0 {
		f.write(" ")
		for i, arg := range b.Args {
			if i > 0 {
				f.write(", ")
			}
			f.write(arg.Name.Name)
		}
		if singleLineBody {
			f.write(" => ")
		} else {
			f.write(" =>")
		}
	} else {
		f.write(" ")
	}

	if singleLineBody {
		// Single expression block arg that was originally on one line
		f.formatNode(block.Forms[0])
		f.write(" }")
	} else if isBlock {
		// Multi-line block arg
		f.newline()
		f.indented(func() {
			for _, form := range block.Forms {
				f.writeIndent()
				f.formatNode(form)
				f.newline()
			}
		})
		f.writeIndent()
		f.write("}")
	}
}

func (f *Formatter) formatUnaryNegation(u *UnaryNegation) {
	f.write("!")
	f.formatNode(u.Expr)
}

func (f *Formatter) formatGrouped(g *Grouped) {
	// Check if this grouped expression spans multiple lines
	multiline := g.Loc != nil && g.Loc.End != nil && g.Loc.End.Line > g.Loc.Line

	f.write("(")
	if multiline {
		f.newline()
		f.indented(func() {
			f.writeIndent()
			f.formatNode(g.Expr)
			f.newline()
		})
		f.writeIndent()
	} else {
		f.formatNode(g.Expr)
	}
	f.write(")")
}

func (f *Formatter) formatBinaryOp(left, right Node, op string) {
	// Check if left operand needs parentheses
	leftNeedsParens := needsParensForPrecedence(left, op, true)
	if leftNeedsParens {
		f.write("(")
	}
	f.formatNode(left)
	if leftNeedsParens {
		f.write(")")
	}

	f.write(" ")
	f.write(op)
	f.write(" ")

	// Check if right operand needs parentheses (for right-associativity issues)
	rightNeedsParens := needsParensForPrecedence(right, op, false)
	if rightNeedsParens {
		f.write("(")
	}
	f.formatNode(right)
	if rightNeedsParens {
		f.write(")")
	}
}

// needsParensForPrecedence checks if an expression needs parentheses in a binary operation
func needsParensForPrecedence(node Node, parentOp string, isLeft bool) bool {
	childOp := nodeOperator(node)
	if childOp == "" {
		return false
	}

	parentPrec := operatorPrecedence(parentOp)
	childPrec := operatorPrecedence(childOp)

	// If child has lower precedence, needs parens
	if childPrec < parentPrec {
		return true
	}

	// For same precedence on right side, needs parens for left-associative ops
	if childPrec == parentPrec && !isLeft && isLeftAssociative(parentOp) {
		return true
	}

	return false
}

func nodeOperator(node Node) string {
	switch node.(type) {
	case *Addition:
		return "+"
	case *Subtraction:
		return "-"
	case *Multiplication:
		return "*"
	case *Division:
		return "/"
	case *Modulo:
		return "%"
	case *LessThan:
		return "<"
	case *LessThanEqual:
		return "<="
	case *GreaterThan:
		return ">"
	case *GreaterThanEqual:
		return ">="
	case *Equality:
		return "=="
	case *Inequality:
		return "!="
	case *LogicalAnd:
		return "and"
	case *LogicalOr:
		return "or"
	case *Default:
		return "??"
	default:
		return ""
	}
}

func operatorPrecedence(op string) int {
	switch op {
	case "??":
		return 0 // Lowest precedence
	case "or":
		return 1
	case "and":
		return 2
	case "==", "!=":
		return 3
	case "<", "<=", ">", ">=":
		return 4
	case "+", "-":
		return 5
	case "*", "/", "%":
		return 6
	default:
		return -1
	}
}

func isLeftAssociative(op string) bool {
	// ?? is right-associative; all other operators are left-associative
	return op != "??"
}

func (f *Formatter) formatDocString(doc string) {
	f.write(`"""`)
	f.newline()
	lines := strings.Split(strings.TrimSpace(doc), "\n")
	for _, line := range lines {
		if line != "" {
			f.writeIndent()
			f.write(line)
		}
		f.newline()
	}
	f.writeIndent()
	f.write(`"""`)
	f.newline()
}

func (f *Formatter) formatTypeNode(t TypeNode) {
	switch tn := t.(type) {
	case *NamedTypeNode:
		if tn.Base != nil {
			f.write(tn.Base.Name)
			f.write(".")
		}
		f.write(tn.Name)
	case NonNullTypeNode:
		f.formatTypeNode(tn.Elem)
		f.write("!")
	case ListTypeNode:
		f.write("[")
		f.formatTypeNode(tn.Elem)
		f.write("]")
	case ObjectTypeNode:
		f.write("{{")
		for i, field := range tn.Fields {
			if i > 0 {
				f.write(", ")
			}
			f.write(field.Key)
			f.write(": ")
			f.formatTypeNode(field.Type)
		}
		f.write("}}")
	case FunTypeNode:
		// Function type: (args): returnType
		// If no args, just output the return type directly (for interface slot types)
		if len(tn.Args) == 0 {
			f.formatTypeNode(tn.Ret)
		} else {
			f.write("(")
			for i, arg := range tn.Args {
				if i > 0 {
					f.write(", ")
				}
				f.write(arg.Name.Name)
				f.write(": ")
				f.formatTypeNode(arg.Type_)
			}
			f.write("): ")
			f.formatTypeNode(tn.Ret)
		}
	case VariableTypeNode:
		f.write(string(tn.Name))
	default:
		f.write(fmt.Sprintf("%v", t))
	}
}

func (f *Formatter) estimateTypeLength(t TypeNode) int {
	temp := &Formatter{}
	temp.formatTypeNode(t)
	return len(temp.buf.String())
}
