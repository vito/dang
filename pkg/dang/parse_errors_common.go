package dang

import "fmt"

// pegSourceError converts a pigeon parse failure into a SourceError at the
// first error's position, keeping only the bare "no match found, expected
// …" message — the position prefix pigeon bakes into its Error() string
// ("filename:line:col (offset):") reads poorly in output that renders the
// location itself, and leaks synthetic unit filenames (the playground's
// per-entry names) into the message. Used where tree-sitter enhancement is
// unavailable (no cgo — notably the wasm playground) or found nothing to
// improve; the enhanced and fallback messages word the failure differently,
// but both carry a location and source, so every frontend still annotates.
func pegSourceError(pegErr error, filename string, source []byte) error {
	first := pegErr
	if el, ok := pegErr.(errList); ok && len(el) > 0 {
		first = el[0]
	}
	pe, ok := first.(*parserError)
	if !ok {
		return pegErr
	}

	loc := &SourceLocation{
		Filename: filename,
		Line:     pe.pos.line,
		Column:   pe.pos.col,
		Length:   1,
	}
	return NewSourceError(fmt.Errorf("syntax error: %s", pe.Inner.Error()), loc, string(source))
}
