package dangdocs

import (
	"testing"

	"github.com/vito/dang/v2/pkg/dang"
)

// The \stdlib-toc reference index is generated from tocGroups. If a new method
// receiver or static module is added to the builtin registry without a matching
// group, the ToC silently stops being exhaustive. StdlibToc fails the docs
// build in that case; this catches it faster, in `go test`.
func TestStdlibTocCoversEveryGroup(t *testing.T) {
	covered := map[string]bool{}
	for _, g := range tocGroups {
		covered[g.key] = true
	}
	for _, r := range dang.MethodReceivers() {
		if !covered[r.Named] && !tocExternalReceivers[r.Named] {
			t.Errorf("method receiver %q has no entry in tocGroups; \\stdlib-toc would omit it", r.Named)
		}
	}
	for _, m := range dang.StaticModules() {
		if !covered[m.Named] {
			t.Errorf("static module %q has no entry in tocGroups; \\stdlib-toc would omit it", m.Named)
		}
	}
}
