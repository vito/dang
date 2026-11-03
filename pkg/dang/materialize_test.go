package dang

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestParameterTypesWithoutConstructorSignature(t *testing.T) {
	// Type returns an hm.Type interface containing a nil *hm.FunctionType.
	constructor := &ConstructorFunction{}
	require.Empty(t, parameterTypes(constructor))
}

// An imported module decoding into its own type from its top-level code finds
// the constructor while its evaluation is still running, rather than waiting
// on that evaluation to finish (see typeItemFor); its functions, called by
// the importer after, decode the same way.
func TestImportedModuleDecodesOwnTypeAtTopLevel(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "dang.toml"), []byte("[imports.Lib]\npath = \"./lib\"\n"), 0o600))
	require.NoError(t, os.Mkdir(filepath.Join(dir, "lib"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "lib", "main.dang"), []byte(`type Greeter {
  name: String!
}
pub made: Greeter! = JSON.decode("{\"name\": \"top\"}")
let hidden: Greeter! = JSON.decode("{\"name\": \"private\"}")
pub hiddenName: String! = hidden.name
pub decodeLater(data: String!): String! {
  let g: Greeter! = JSON.decode(data)
  g.name
}
`), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.dang"), []byte(`import Lib
assert { Lib.made.name == "top" }
assert { Lib.hiddenName == "private" }
assert { Lib.decodeLater("{\"name\": \"later\"}") == "later" }
`), 0o600))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- RunFile(ctx, filepath.Join(dir, "main.dang"), false) }()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-ctx.Done():
		t.Fatal("decoding into the module's own type at its top level hung")
	}
}

// A scalar's constructor carries its new() hook, so it works wherever it is
// called from, such as another module's code holding a reference to it.
func TestScalarConstructorCalledFromElsewhere(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.dang"), []byte(`scalar Tag {
  new(raw: String!) { raw.toUpper }
}
`), 0o600))
	ctx := context.Background()
	values, err := RunDir(ctx, dir, false)
	require.NoError(t, err)
	tag, found, err := values.Lookup(ctx, "Tag")
	require.NoError(t, err)
	require.True(t, found)
	elsewhere := NewValueScope(NewPreludeTypeScope(""))
	made, err := tag.(Callable).Call(ctx, elsewhere, map[string]Value{"raw": StringValue{Val: "abc"}})
	require.NoError(t, err)
	require.Equal(t, "ABC", made.(ScalarValue).Val)
}
