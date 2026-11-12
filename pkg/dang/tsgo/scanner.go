package tsgo

import (
	gts "github.com/odvcencio/gotreesitter"
)

// External token indexes, in the order of the grammar's "externals" array
// (and of the enum in treesitter/src/scanner.c, which this file ports).
const (
	automaticNewline = iota
	inlineSpace
	templateMultiOpen
	templateMultiClose
	templateContentChar
	langTagTerminator
	commentToken
	externalCount
)

// templateMaxDepth bounds backtick template nesting; each level stores the
// open fence length so the matching close fence can be enforced.
const templateMaxDepth = 32

// scanner is a line-for-line Go port of treesitter/src/scanner.c. Keep the
// two in step: the C scanner is what the editors and the cgo build run.
//
// Unlike C, gotreesitter indexes validSymbols by external token index but
// takes a language symbol ID as the result, so the scanner carries the
// language's external symbol table.
type scanner struct {
	syms [externalCount]gts.Symbol
}

type scannerState struct {
	depth uint8
	// After an open fence is consumed, the very next position might start a
	// language tag. This flag tells the content scanner to refuse a leading
	// letter once, so the internal lexer gets a chance to match the optional
	// lang_tag_part.
	justOpened bool
	fenceStack [templateMaxDepth]uint8
}

func newScanner(lang *gts.Language) *scanner {
	s := &scanner{}
	copy(s.syms[:], lang.ExternalSymbols)
	return s
}

func (*scanner) Create() any { return &scannerState{} }
func (*scanner) Destroy(any) {}
func (s *scanner) result(lx *gts.ExternalLexer, tok int) bool {
	lx.SetResultSymbol(s.syms[tok])
	return true
}

func (*scanner) Serialize(payload any, buf []byte) int {
	st := payload.(*scannerState)
	n := 0
	buf[n] = st.depth
	n++
	if st.justOpened {
		buf[n] = 1
	} else {
		buf[n] = 0
	}
	n++
	for i := 0; i < int(st.depth) && i < templateMaxDepth; i++ {
		buf[n] = st.fenceStack[i]
		n++
	}
	return n
}

func (*scanner) Deserialize(payload any, buf []byte) {
	st := payload.(*scannerState)
	*st = scannerState{}
	if len(buf) == 0 {
		return
	}
	d := int(buf[0])
	if d > templateMaxDepth {
		d = templateMaxDepth
	}
	st.depth = uint8(d)
	if len(buf) >= 2 {
		st.justOpened = buf[1] != 0
	}
	for i := 0; i < d && i+2 < len(buf); i++ {
		st.fenceStack[i] = buf[i+2]
	}
}

func isWordChar(c rune) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
		(c >= '0' && c <= '9') || c == '_'
}

func isLetter(c rune) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// eof reports whether the lexer is at the end of input. gotreesitter has no
// separate eof() call: Lookahead returns 0 there.
func eof(lx *gts.ExternalLexer) bool { return lx.Lookahead() == 0 }

// isLeadingLogicalOp checks for a leading `and`/`or` keyword that continues
// an expression on a new line. It advances the lexer, so the caller must
// MarkEnd the token first.
func isLeadingLogicalOp(lx *gts.ExternalLexer) bool {
	switch lx.Lookahead() {
	case 'a':
		lx.Advance(false)
		if lx.Lookahead() == 'n' {
			lx.Advance(false)
			if lx.Lookahead() == 'd' {
				lx.Advance(false)
				return !isWordChar(lx.Lookahead())
			}
		}
	case 'o':
		lx.Advance(false)
		if lx.Lookahead() == 'r' {
			lx.Advance(false)
			return !isWordChar(lx.Lookahead())
		}
	}
	return false
}

// scanTemplateOpen scans a multi-line template opening fence: 3 or more
// backticks. See scan_template_open in scanner.c for atNewline.
func (s *scanner) scanTemplateOpen(st *scannerState, lx *gts.ExternalLexer, atNewline bool) bool {
	if st.depth >= templateMaxDepth {
		return false
	}
	for {
		c := lx.Lookahead()
		if c == ' ' || c == '\t' || (atNewline && (c == '\n' || c == '\r')) {
			lx.Advance(true)
			continue
		}
		break
	}
	if lx.Lookahead() != '`' {
		return false
	}
	count := 0
	for lx.Lookahead() == '`' {
		count++
		lx.Advance(false)
	}
	if count < 3 {
		return false
	}
	st.fenceStack[st.depth] = uint8(min(count, 255))
	st.depth++
	st.justOpened = true
	return s.result(lx, templateMultiOpen)
}

// scanTemplateClose scans a closing fence matching the open fence's length.
func (s *scanner) scanTemplateClose(st *scannerState, lx *gts.ExternalLexer) bool {
	if st.depth == 0 || lx.Lookahead() != '`' {
		return false
	}
	expected := int(st.fenceStack[st.depth-1])
	for i := 0; i < expected; i++ {
		if lx.Lookahead() != '`' {
			return false
		}
		lx.Advance(false)
	}
	st.depth--
	return s.result(lx, templateMultiClose)
}

// scanLangTagTerminator scans the newline that ends a language tag.
func (s *scanner) scanLangTagTerminator(st *scannerState, lx *gts.ExternalLexer) bool {
	if !st.justOpened || lx.Lookahead() != '\n' {
		return false
	}
	lx.Advance(false)
	st.justOpened = false
	return s.result(lx, langTagTerminator)
}

// scanTemplateContentChar scans one piece of template content.
func (s *scanner) scanTemplateContentChar(st *scannerState, lx *gts.ExternalLexer) bool {
	if eof(lx) {
		return false
	}
	if st.depth == 0 && lx.Lookahead() == '\n' {
		return false
	}
	if st.depth > 0 {
		if st.justOpened {
			st.justOpened = false
			if isLetter(lx.Lookahead()) {
				return false
			}
		}
		if lx.Lookahead() == '`' {
			expected := int(st.fenceStack[st.depth-1])
			count := 0
			for lx.Lookahead() == '`' {
				count++
				lx.Advance(false)
			}
			if count >= expected {
				return false
			}
			return s.result(lx, templateContentChar)
		}
	} else if lx.Lookahead() == '`' {
		return false
	}
	if lx.Lookahead() == '\\' {
		lx.Advance(false)
		if lx.Lookahead() == '$' {
			lx.Advance(false)
			if lx.Lookahead() == '{' {
				lx.Advance(false)
			}
		}
		return s.result(lx, templateContentChar)
	}
	if lx.Lookahead() == '$' {
		lx.Advance(false)
		if lx.Lookahead() == '{' {
			return false
		}
		return s.result(lx, templateContentChar)
	}
	lx.Advance(false)
	return s.result(lx, templateContentChar)
}

// scanCommentToken scans a `# ...` comment, refusing inside templates.
func (s *scanner) scanCommentToken(lx *gts.ExternalLexer, valid []bool) bool {
	if valid[templateContentChar] {
		return false
	}
	if lx.Lookahead() != '#' {
		return false
	}
	lx.Advance(false)
	for !eof(lx) && lx.Lookahead() != '\n' {
		lx.Advance(false)
	}
	return s.result(lx, commentToken)
}

// Scan mirrors tree_sitter_dang_external_scanner_scan; see scanner.c for the
// reasoning behind each step.
func (s *scanner) Scan(payload any, lx *gts.ExternalLexer, valid []bool) bool {
	st := payload.(*scannerState)

	if valid[langTagTerminator] && s.scanLangTagTerminator(st, lx) {
		return true
	}
	if valid[templateMultiClose] && s.scanTemplateClose(st, lx) {
		return true
	}
	if valid[templateMultiOpen] && s.scanTemplateOpen(st, lx, !valid[automaticNewline]) {
		return true
	}
	if valid[templateContentChar] && s.scanTemplateContentChar(st, lx) {
		return true
	}

	sawInlineSpace := false
	for lx.Lookahead() == ' ' || lx.Lookahead() == '\t' {
		lx.Advance(true)
		sawInlineSpace = true
	}

	if valid[commentToken] && s.scanCommentToken(lx, valid) {
		return true
	}

	if valid[inlineSpace] && sawInlineSpace {
		switch c := lx.Lookahead(); c {
		case '\n', '\r', '#', '}', ')', ']', ',':
		default:
			if !eof(lx) {
				return s.result(lx, inlineSpace)
			}
		}
	}

	if !valid[automaticNewline] {
		return false
	}
	if lx.Lookahead() != '\n' {
		return false
	}
	lx.Advance(false)
	for {
		c := lx.Lookahead()
		if c == ' ' || c == '\t' || c == '\r' || c == '\n' {
			lx.Advance(false)
			continue
		}
		break
	}
	lx.MarkEnd()

	switch lx.Lookahead() {
	case '.':
		lx.Advance(false)
		if lx.Lookahead() != '.' {
			return false
		}
		return s.result(lx, automaticNewline)
	case '|':
		return false
	case '{':
		lx.Advance(false)
		if lx.Lookahead() != '{' {
			return false
		}
		return s.result(lx, automaticNewline)
	case '}', ')', ']':
		return false
	}
	if isLeadingLogicalOp(lx) {
		return false
	}
	if eof(lx) {
		return false
	}
	return s.result(lx, automaticNewline)
}
