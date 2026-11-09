package dang

import "context"

// Local declarations shadow unqualified imports.
//
// An unqualified import installs its names into the frame the import is in,
// which, in a single-file module, is the same frame the module's own
// declarations go in. Hoisting can register a declaration before the import is
// inferred, while evaluation binds the import before any declaration, so
// letting whichever came first win would differ between the checker and the
// runtime. Instead, phased inference and evaluation tell the import which
// names the forms beside it declare, and it leaves them alone: the local
// declaration always wins, in both. (A declaration after an import in a plain
// block, inferred and evaluated in source order, replaces it; see
// FieldDecl.rebind.)

type localDeclsKey struct{}

// withLocalDecls records the names forms declare, for imports among them.
// Forms without an import leave ctx alone: nothing there consults it.
func withLocalDecls(ctx context.Context, forms []Node) context.Context {
	hasImport := false
	for _, form := range forms {
		if _, isImport := form.(*ImportDecl); isImport {
			hasImport = true
			break
		}
	}
	if !hasImport {
		return ctx
	}
	names := map[string]bool{}
	for _, form := range forms {
		if _, isImport := form.(*ImportDecl); isImport {
			continue
		}
		for _, name := range form.DeclaredSymbols() {
			names[name] = true
		}
	}
	return context.WithValue(ctx, localDeclsKey{}, names)
}

// locallyDeclared returns a predicate for the names the forms beside an
// import declare (none outside phased inference/evaluation).
func locallyDeclared(ctx context.Context) func(string) bool {
	names, _ := ctx.Value(localDeclsKey{}).(map[string]bool)
	return func(name string) bool { return names[name] }
}
