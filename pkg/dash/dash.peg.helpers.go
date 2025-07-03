package dash

func (c current) Loc() *SourceLocation {
	fn, _ := c.globalStore["filePath"].(string)
	return &SourceLocation{
		Filename: fn,
		Line:     c.pos.line,
		Column:   c.pos.col,
		Length:   len(string(c.text)),
	}
}
