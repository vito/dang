package bind

import "bytes"

func (c current) Loc() *SourceLocation {
	fn, _ := c.globalStore["filePath"].(string)
	textEnd := len(c.text)
	if ln := bytes.IndexByte(c.text, '\n'); ln != -1 {
		textEnd = ln
	}
	return &SourceLocation{
		Filename: fn,
		Line:     c.pos.line,
		Column:   c.pos.col,
		Length:   len(string(c.text[:textEnd])),
	}
}
