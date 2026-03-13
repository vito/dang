//go:build !cgo

package dang

// ParseWithRecovery parses source using the PEG parser. Without CGo,
// tree-sitter error enhancement is not available.
func ParseWithRecovery(filename string, source []byte, opts ...Option) (any, error) {
	return Parse(filename, source, opts...)
}

// ParseFileWithRecovery is like ParseFile but without tree-sitter error
// enhancement (CGo not available).
func ParseFileWithRecovery(filename string, opts ...Option) (any, error) {
	return ParseFile(filename, opts...)
}

// EnhanceParseError returns the original error as-is since tree-sitter is not
// available without CGo.
func EnhanceParseError(pegErr error, filename string, source []byte) error {
	return pegErr
}
