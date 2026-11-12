//go:build cgo

package tsgo_test

import (
	"os"
	"strings"
	"testing"
	"time"

	gts "github.com/odvcencio/gotreesitter"
	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	"github.com/vito/dang/v2/pkg/dang/danglang"
	"github.com/vito/dang/v2/pkg/dang/tsgo"
)

// TestRecoveryFlags measures how gotreesitter's per-language recovery
// "certification" flags move error recovery parity with C. Opt-in: set
// TSGO_FLAGS=1.
func TestRecoveryFlags(t *testing.T) {
	if os.Getenv("TSGO_FLAGS") == "" {
		t.Skip("TSGO_FLAGS not set")
	}
	inputs := parityInputs(t)
	cParser := tree_sitter.NewParser()
	defer cParser.Close()
	if err := cParser.SetLanguage(danglang.Language()); err != nil {
		t.Fatal(err)
	}
	var errInputs []parityInput
	var wants []string
	for _, in := range inputs {
		tr := cParser.Parse(in.src, nil)
		if tr.RootNode().HasError() {
			var w strings.Builder
			dumpC(&w, tr.RootNode(), 0)
			errInputs = append(errInputs, in)
			wants = append(wants, w.String())
		}
		tr.Close()
	}
	type flag struct {
		name string
		set  func(*gts.Language)
	}
	flags := []flag{
		{"certified C recovery only", func(*gts.Language) {}},
		{"RecoveryStackVersionOrderEnabled", func(l *gts.Language) { l.RecoveryStackVersionOrderEnabled = true }},
		{"RecoveryMissingVersionTurnsCertified", func(l *gts.Language) { l.RecoveryMissingVersionTurnsCertified = true }},
		{"FullParseGSSConvergenceEnabled", func(l *gts.Language) { l.FullParseGSSConvergenceEnabled = true }},
		{"ExactStackNodeEquivalenceCertified", func(l *gts.Language) { l.ExactStackNodeEquivalenceCertified = true }},
		{"ConflictActionVersionOrderCertified", func(l *gts.Language) { l.ConflictActionVersionOrderCertified = true }},
		{"compact S3+S5 missing insertion", func(l *gts.Language) {
			l.CompactStrategy2ErrorRegionCertified = true
			l.CompactMissingTokenInsertionCertified = true
			l.CompactS5EOFMissingInsertionCertified = true
		}},
		{"compact S3+S5+stack summary+faithful S5", func(l *gts.Language) {
			l.CompactStrategy2ErrorRegionCertified = true
			l.CompactMissingTokenInsertionCertified = true
			l.CompactS5EOFMissingInsertionCertified = true
			l.CompactStackSummaryRecoveryCertified = true
			l.CompactFaithfulS5RecoveryCertified = true
		}},
		{"compact bundle + MissingVersionTurns", func(l *gts.Language) {
			l.CompactStrategy2ErrorRegionCertified = true
			l.CompactMissingTokenInsertionCertified = true
			l.CompactS5EOFMissingInsertionCertified = true
			l.CompactStackSummaryRecoveryCertified = true
			l.CompactFaithfulS5RecoveryCertified = true
			l.RecoveryMissingVersionTurnsCertified = true
		}},
	}
	for _, f := range flags {
		lang, err := tsgo.Load()
		if err != nil {
			t.Fatal(err)
		}
		f.set(lang)
		p := gts.NewParser(lang)
		p.SetTimeoutMicros(2_000_000)
		same, failed := 0, 0
		start := time.Now()
		for i, in := range errInputs {
			tr, err := p.Parse(in.src)
			if err != nil || tr == nil {
				failed++
				continue
			}
			var g strings.Builder
			dumpGo(&g, tr.RootNode(), lang, 0)
			tr.Release()
			if g.String() == wants[i] {
				same++
			}
		}
		t.Logf("FLAG %s: %d/%d error inputs identical, %d failed, %s", f.name, same, len(errInputs), failed, time.Since(start))
	}
}
