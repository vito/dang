//go:build !cgo

package dang

import "os"

// ParseWithRecovery parses source using the PEG parser. Without CGo,
// tree-sitter error enhancement is not available; parse failures degrade to
// a SourceError at the PEG error's position (pegSourceError).
func ParseWithRecovery(filename string, source []byte, opts ...Option) (any, error) {
	result, err := Parse(filename, source, opts...)
	if err != nil {
		return nil, pegSourceError(err, filename, source)
	}
	return result, nil
}

// ParseFileWithRecovery is like ParseFile but without tree-sitter error
// enhancement (CGo not available).
func ParseFileWithRecovery(filename string, opts ...Option) (any, error) {
	result, err := ParseFile(filename, opts...)
	if err != nil {
		source, readErr := os.ReadFile(filename)
		if readErr != nil {
			return nil, err // can't enhance, return original
		}
		return nil, pegSourceError(err, filename, source)
	}
	return result, nil
}

// EnhanceParseError returns a position-annotated version of the PEG error;
// the tree-sitter description is not available without CGo.
func EnhanceParseError(pegErr error, filename string, source []byte) error {
	return pegSourceError(pegErr, filename, source)
}
