package dangdocs

import (
	"context"
	"sort"
	"unicode/utf8"

	"github.com/vito/dang/v2/pkg/dang"
	"github.com/vito/dang/v2/pkg/hm"
)

// linkSpan marks a byte range of a snippet that should link to a stdlib tag.
type linkSpan struct {
	start, end int
	tag        string
}

// stdlibLinks typechecks source against the prelude (stdlib builtins only, no
// external imports) and returns spans for call-position names that resolve to
// stdlib builtins: method calls whose inferred receiver is a stdlib module
// (List, Map, String, Random, ...) and calls of free builtin functions. Names
// the snippet declares itself are skipped, as is anything whose receiver
// doesn't resolve — a user-defined type's `map` field or a GraphQL `from`
// never links. Snippets that don't parse yield no links; snippets that only
// partially typecheck yield links for the parts that did.
func stdlibLinks(source string) []linkSpan {
	parsed, err := dang.Parse("snippet.dang", []byte(source))
	if err != nil {
		return nil
	}
	block, ok := parsed.(*dang.FileBlock)
	if !ok {
		return nil
	}

	// Inference errors are expected (undefined example symbols, fragments):
	// failed nodes get fallback type variables and simply don't resolve.
	scope := dang.NewPreludeTypeScope("snippet")
	_ = dang.InferDirectoryFiles(context.Background(), []*dang.FileBlock{block}, scope, hm.NewSimpleFresher())

	offsets := lineByteOffsets(source)
	declared := declaredNames(block)
	statics := staticMethodNames()
	freeFns := map[string]bool{}
	dang.ForEachFunction(func(d dang.BuiltinDef) { freeFns[d.Name] = true })

	var spans []linkSpan
	addSpan := func(sym *dang.Symbol, tag string) {
		if declared[sym.Name] {
			return
		}
		start, ok := byteOffset(source, offsets, sym.Loc)
		if !ok {
			return
		}
		end := start + len(sym.Name)
		// The PEG and byte worlds must agree before we splice a link in.
		if end > len(source) || source[start:end] != sym.Name {
			return
		}
		spans = append(spans, linkSpan{start: start, end: end, tag: tag})
	}

	block.Walk(func(n dang.Node) bool {
		switch node := n.(type) {
		case *dang.Select:
			if node.Receiver == nil || node.Field == nil {
				return true
			}
			if key, ok := receiverKey(node.Receiver.GetInferredType(), node.Field.Name, statics); ok {
				addSpan(node.Field, stdlibTag(key, node.Field.Name))
			}
		case *dang.FunCall:
			if sym, ok := node.Fun.(*dang.Symbol); ok && freeFns[sym.Name] {
				addSpan(sym, stdlibTag("fn", sym.Name))
			}
		}
		return true
	})

	sort.Slice(spans, func(i, j int) bool { return spans[i].start < spans[j].start })
	return spans
}

// receiverKey resolves an inferred receiver type to the stdlib anchor key
// (the registry type's name), mirroring the normalization Select.Infer
// performs: unwrap non-null, special-case List/Map, then match registry
// receivers and static modules by identity. Returns false for anything else —
// user types, GraphQL objects, unresolved type variables.
func receiverKey(receiverType hm.Type, method string, statics map[*dang.Type]map[string]bool) (string, bool) {
	if receiverType == nil {
		return "", false
	}
	t := receiverType
	if nn, ok := t.(hm.NonNullType); ok {
		t = nn.Type
	}

	switch t.(type) {
	case dang.ListType:
		if _, found := dang.LookupMethod(dang.ListTypeModule, method); found {
			return dang.ListTypeModule.Named, true
		}
		return "", false
	case dang.MapType:
		if _, found := dang.LookupMethod(dang.MapTypeModule, method); found {
			return dang.MapTypeModule.Named, true
		}
		return "", false
	}

	mod, ok := t.(*dang.Type)
	if !ok {
		return "", false
	}
	for _, recv := range dang.MethodReceivers() {
		if recv == mod {
			if _, found := dang.LookupMethod(mod, method); found {
				return mod.Named, true
			}
			return "", false
		}
	}
	if names, ok := statics[mod]; ok && names[method] {
		return mod.Named, true
	}
	return "", false
}

// staticMethodNames indexes the registry's static methods by host module.
func staticMethodNames() map[*dang.Type]map[string]bool {
	statics := map[*dang.Type]map[string]bool{}
	for _, mod := range dang.StaticModules() {
		names := map[string]bool{}
		dang.ForEachStaticMethod(mod, func(d dang.BuiltinDef) { names[d.Name] = true })
		statics[mod] = names
	}
	return statics
}

// declaredNames collects every name the snippet declares anywhere, so a
// snippet defining its own `print` or `map` doesn't link those names to the
// stdlib.
func declaredNames(block *dang.FileBlock) map[string]bool {
	declared := map[string]bool{}
	block.Walk(func(n dang.Node) bool {
		for _, name := range n.DeclaredSymbols() {
			declared[name] = true
		}
		return true
	})
	return declared
}

// lineByteOffsets returns the byte offset of the start of each 1-based line.
func lineByteOffsets(source string) []int {
	offsets := []int{0}
	for i := 0; i < len(source); i++ {
		if source[i] == '\n' {
			offsets = append(offsets, i+1)
		}
	}
	return offsets
}

// byteOffset converts a PEG SourceLocation (1-based line, 1-based rune
// column) to a byte offset.
func byteOffset(source string, offsets []int, loc *dang.SourceLocation) (int, bool) {
	if loc == nil || loc.Line < 1 || loc.Line > len(offsets) {
		return 0, false
	}
	off := offsets[loc.Line-1]
	lineEnd := len(source)
	if loc.Line < len(offsets) {
		lineEnd = offsets[loc.Line] - 1
	}
	line := source[off:lineEnd]
	for col := 1; col < loc.Column; col++ {
		_, size := utf8.DecodeRuneInString(line)
		if size == 0 {
			return 0, false
		}
		line = line[size:]
		off += size
	}
	return off, true
}
