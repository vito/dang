//go:build wasmprobe

// Command wasm-probe runs the C tree-sitter runtime and the Dang grammar,
// compiled to WebAssembly, through wazero: the alternative to gotreesitter
// for builds without cgo. It reports the first syntax error of each file it
// is given, and the total parse time.
package main

import (
	"context"
	_ "embed"
	"encoding/binary"
	"fmt"
	"os"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

//go:embed dang.wasm
var wasm []byte

func main() {
	ctx := context.Background()
	cfg := wazero.NewRuntimeConfig()
	if os.Getenv("INTERP") != "" {
		cfg = wazero.NewRuntimeConfigInterpreter()
	}
	start := time.Now()
	r := wazero.NewRuntimeWithConfig(ctx, cfg)
	wasi_snapshot_preview1.MustInstantiate(ctx, r)
	mod, err := r.InstantiateWithConfig(ctx, wasm, wazero.NewModuleConfig().WithStartFunctions("_initialize"))
	if err != nil {
		panic(err)
	}
	inst := time.Since(start)

	alloc := mod.ExportedFunction("dang_alloc")
	free := mod.ExportedFunction("dang_free")
	first := mod.ExportedFunction("dang_first_error")
	var total time.Duration
	var bytes int
	for _, path := range os.Args[1:] {
		src, err := os.ReadFile(path)
		if err != nil {
			panic(err)
		}
		ptr, err := alloc.Call(ctx, uint64(len(src)))
		if err != nil {
			panic(err)
		}
		mod.Memory().Write(uint32(ptr[0]), src)
		t := time.Now()
		res, err := first.Call(ctx, ptr[0], uint64(len(src)))
		if err != nil {
			panic(err)
		}
		total += time.Since(t)
		bytes += len(src)
		if _, err := free.Call(ctx, ptr[0]); err != nil {
			panic(err)
		}
		if res[0] != 0 && os.Getenv("QUIET") == "" {
			buf, _ := mod.Memory().Read(uint32(res[0]), 28)
			v := make([]uint32, 7)
			for i := range v {
				v[i] = binary.LittleEndian.Uint32(buf[i*4:])
			}
			kind := "ERROR"
			if v[0] != 0 {
				kind = "MISSING"
			}
			fmt.Printf("%s: %s symbol %d at %d:%d-%d:%d\n", path, kind, v[5], v[1], v[2], v[3], v[4])
		}
	}
	fmt.Printf("instantiate %s; parsed %d files, %d bytes in %s\n", inst, len(os.Args)-1, bytes, total)
}
