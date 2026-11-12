// Package tsgo loads the Dang tree-sitter grammar into gotreesitter, a
// tree-sitter runtime written in pure Go, so that builds without cgo get the
// same concrete syntax tree (and so the same parse error recovery) as builds
// that link the C runtime through danglang.
//
// dang.bin holds the parse tables of treesitter/src/parser.c, extracted by
// gotreesitter's ts2go; scanner.go ports treesitter/src/scanner.c.
package tsgo

import (
	_ "embed"
	"fmt"
	"sync"

	gts "github.com/odvcencio/gotreesitter"
)

//go:generate sh -c "go tool ts2go -input ../../../treesitter/src/parser.c -output .ts2go/dang.go -package tsgo -name dang >/dev/null && cp .ts2go/grammar_blobs/dang.bin dang.bin && rm -r .ts2go"

//go:embed dang.bin
var blob []byte

var language = sync.OnceValues(load)

// load decodes a fresh copy of the grammar.
func load() (*gts.Language, error) {
	lang, err := gts.LoadLanguage(blob)
	if err != nil {
		return nil, err
	}
	lang.ExternalScanner = newScanner(lang)
	// Opt into gotreesitter's port of C tree-sitter's error recovery (it is
	// off by default for grammars outside its registry); without it,
	// recovered trees bear little resemblance to the C runtime's.
	if diag := gts.CertifyCRecoveryCostCompetition(lang); !diag.Supported {
		return nil, fmt.Errorf("C error recovery unsupported: %s", diag.Reason)
	}
	return lang, nil
}

// Language returns the Dang grammar, decoding the embedded tables on first
// use.
func Language() *gts.Language {
	lang, err := language()
	if err != nil {
		panic("tsgo: load Dang grammar: " + err.Error())
	}
	return lang
}
