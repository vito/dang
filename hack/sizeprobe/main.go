// Command sizeprobe approximates what a downstream importer of Dang (such as
// the Dagger engine, which embeds pkg/dang and pkg/dang/analysis) links. It
// exists only to measure binary size: build it with and without cgo, and
// with and without the pure-Go tree-sitter, and compare.
package main

import (
	"context"
	"fmt"
	"os"
	"testing/fstest"

	"github.com/vito/dang/v2/pkg/dang"
	"github.com/vito/dang/v2/pkg/dang/analysis"
)

func main() {
	src := []byte("pub x = \n")
	if len(os.Args) > 1 {
		src = []byte(os.Args[1])
	}
	_, err := dang.ParseWithRecovery("probe.dang", src)
	fmt.Println(err)

	fsys := fstest.MapFS{"probe.dang": {Data: src}}
	a, err := analysis.Analyze(context.Background(), analysis.Overlay(fsys, nil), ".", analysis.Options{})
	fmt.Println(a != nil, err)
	if a != nil && a.Scope != nil {
		fmt.Println(len(analysis.Complete(context.Background(), a.Scope, string(src), analysis.Pos{}).Items))
	}
}
