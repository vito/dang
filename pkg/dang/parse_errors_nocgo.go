//go:build !cgo

package dang

import "os"

// ParseWithRecovery parses source using the PEG parser. Without CGo,
// tree-sitter error enhancement is not available.
func ParseWithRecovery(filename string, source []byte, opts ...Option) (any, error) {
	result, err := Parse(filename, source, opts...)
	if err != nil {
		return nil, enhanceParseError(err, filename, source, optionsVersion(opts))
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
		return nil, enhanceParseError(err, filename, source, optionsVersion(opts))
	}
	return result, nil
}

// EnhanceParseError reports syntax a grammar action rejected, or a pitfall
// (see Pitfall), with its source location. Otherwise it returns the original
// error as-is, since tree-sitter is not available without CGo.
func EnhanceParseError(pegErr error, filename string, source []byte) error {
	return enhanceParseError(pegErr, filename, source, CurrentVersion)
}

// enhanceParseError is EnhanceParseError for source parsed as Dang v.
func enhanceParseError(pegErr error, filename string, source []byte, v Version) error {
	if rejected, ok := rejectedSourceError(pegErr, filename, source); ok {
		return rejected
	}
	if pitfall, ok := syntaxPitfallError(pegErr, filename, source, v); ok {
		return pitfall
	}
	return pegErr
}
