package dang

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vito/dang/pkg/hm"
	"github.com/vito/dang/pkg/introspection"
)

func requireEvalGet(t *testing.T, env EvalEnv, name string) (Value, bool) {
	t.Helper()
	val, found, err := env.Lookup(context.Background(), name)
	require.NoError(t, err)
	return val, found
}

func TestNewEnvSchemaTypeShadowsPreludeType(t *testing.T) {
	_, found := ErrorType.LocalSchemeOf("id")
	require.False(t, found)

	env := NewEnv("Dagger", schemaWithErrorObject())

	schemaString, found := env.NamedType("String")
	require.True(t, found)
	require.Same(t, StringType, schemaString)

	schemaError, found := env.NamedType("Error")
	require.True(t, found)
	require.NotSame(t, ErrorType, schemaError)

	schemaErrorMod, ok := schemaError.(*Module)
	require.True(t, ok)
	require.Equal(t, ObjectKind, schemaErrorMod.Kind)

	_, found = schemaError.LocalSchemeOf("id")
	require.True(t, found)
	_, found = schemaError.LocalSchemeOf("message")
	require.True(t, found)

	_, found = ErrorType.LocalSchemeOf("id")
	require.False(t, found)
}

func TestConcurrentNewEnvWithPreludeTypeCollision(t *testing.T) {
	schema := schemaWithErrorObject()

	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make(chan bool, 32)
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			env := NewEnv("Dagger", schema)
			schemaError, found := env.NamedType("Error")
			errs <- found && schemaError != ErrorType
		}()
	}
	close(start)
	wg.Wait()
	close(errs)

	for ok := range errs {
		require.True(t, ok)
	}
	_, found := ErrorType.LocalSchemeOf("id")
	require.False(t, found)
}

func TestModuleValueSetDoesNotMutateTypeEnvOrigins(t *testing.T) {
	mod := NewModule("runtime", ObjectKind)
	val := NewModuleValue(mod)

	val.Set("plain", StringValue{Val: "a"})
	val.SetWithVisibility("visible", StringValue{Val: "b"}, PublicVisibility)
	val.Update("reassigned", StringValue{Val: "c"})

	for _, name := range []string{"plain", "visible", "reassigned"} {
		_, found := val.LookupLocal(name)
		require.True(t, found)
		_, found = mod.LocalValueOrigin(name)
		require.False(t, found, "runtime set for %q created a type-environment origin", name)
	}

	importedOrigin := ImportedBindingOrigin("Upstream", false)
	for _, name := range []string{"existingPlain", "existingVisible", "existingReassigned"} {
		mod.Add(name, hm.NewScheme(nil, StringType))
		mod.SetValueOrigin(name, importedOrigin)
	}

	val.Set("existingPlain", StringValue{Val: "d"})
	val.SetWithVisibility("existingVisible", StringValue{Val: "e"}, PublicVisibility)
	val.Update("existingReassigned", StringValue{Val: "f"})

	for _, name := range []string{"existingPlain", "existingVisible", "existingReassigned"} {
		origin, found := mod.LocalValueOrigin(name)
		require.True(t, found)
		require.Equal(t, importedOrigin, origin, "runtime set for %q clobbered type-environment origin", name)
	}
}

func TestRunDirDeclarationsShadowPreludeTypes(t *testing.T) {
	env := runDangSnippet(t, `
type Error {
  pub id: String! = "x"
}
assert { Error.id == "x" }
`)
	classVal, found := requireEvalGet(t, env, "Error")
	require.True(t, found)
	classFn, ok := classVal.(*ConstructorFunction)
	require.True(t, ok)
	require.NotSame(t, ErrorType, classFn.ClassType)
	_, found = ErrorType.LocalSchemeOf("id")
	require.False(t, found)

	env = runDangSnippet(t, `
enum Error { FOO }
assert { Error.FOO == Error.FOO }
`)
	enumVal, found := requireEvalGet(t, env, "Error")
	require.True(t, found)
	enumMod, ok := enumVal.(*ModuleValue)
	require.True(t, ok)
	require.NotSame(t, ErrorType, enumMod.Mod)
	_, found = ErrorType.LocalSchemeOf("FOO")
	require.False(t, found)

	env = runDangSnippet(t, `
scalar Error
`)
	scalarVal, found := requireEvalGet(t, env, "Error")
	require.True(t, found)
	scalarMod, ok := scalarVal.(*ModuleValue)
	require.True(t, ok)
	require.NotSame(t, ErrorType, scalarMod.Mod)
	require.Equal(t, ScalarKind, scalarMod.Mod.(*Module).Kind)
}

func TestImportedTypeDisplayNamesAreQualified(t *testing.T) {
	env := NewEnv("Dagger", schemaWithCoreShadowTypes())

	container, found := env.NamedType("Container")
	require.True(t, found)
	require.Equal(t, "Container", container.Name())
	require.Equal(t, "Dagger.Container", container.String())
	require.Equal(t, "Dagger.Container", container.Clone().(Env).String())

	nonNullContainer := hm.NonNullType{Type: container}
	require.Equal(t, "Dagger.Container!", nonNullContainer.String())
	require.Equal(t, "Dagger.Container!", nonNullContainer.Name())

	containerList := ListType{nonNullContainer}
	require.Equal(t, "[Dagger.Container!]", containerList.String())
	require.Equal(t, "[Dagger.Container!]", containerList.Name())

	fn := hm.NewFnType(
		NewRecordType("", Keyed[*hm.Scheme]{Key: "input", Value: hm.NewScheme(nil, nonNullContainer)}),
		nonNullContainer,
	)
	require.Equal(t, "(input: Dagger.Container!): Dagger.Container!", fn.String())
}

func TestBuildEnvFromImportsTracksImportedTypeOrigins(t *testing.T) {
	typeEnv, _ := BuildEnvFromImports("", []ImportConfig{{
		Name:   "Dagger",
		Schema: schemaWithCoreShadowTypes(),
	}})

	importedContainer, found := typeEnv.NamedType("Container")
	require.True(t, found)

	localContainer, err := declareLocalType(typeEnv, "Container", ObjectKind)
	require.NoError(t, err)
	require.NotSame(t, importedContainer, localContainer)

	daggerType, found := typeEnv.NamedType("Dagger")
	require.True(t, found)
	qualifiedContainer, found := daggerType.NamedType("Container")
	require.True(t, found)
	require.Same(t, importedContainer, qualifiedContainer)
}

func TestBuildEnvFromImportsKeepsImportedBindingsPrivate(t *testing.T) {
	typeEnv, evalEnv := BuildEnvFromImports("", []ImportConfig{{
		Name:   "Dagger",
		Schema: schemaWithCoreShadowTypes(),
	}})

	publicTypeBindings := map[string]bool{}
	for name := range typeEnv.Bindings(PublicVisibility) {
		publicTypeBindings[name] = true
	}
	require.NotContains(t, publicTypeBindings, "Dagger")
	require.NotContains(t, publicTypeBindings, "container")

	_, found := typeEnv.SchemeOf("Dagger")
	require.True(t, found)
	_, found = typeEnv.SchemeOf("container")
	require.True(t, found)

	publicEvalBindings := map[string]bool{}
	for _, binding := range evalEnv.Bindings(PublicVisibility) {
		publicEvalBindings[binding.Key] = true
	}
	require.NotContains(t, publicEvalBindings, "Dagger")
	require.NotContains(t, publicEvalBindings, "container")

	_, found = requireEvalGet(t, evalEnv, "Dagger")
	require.True(t, found)
	_, found = requireEvalGet(t, evalEnv, "container")
	require.True(t, found)
}

func TestDeclareLocalTypeRejectsQualifiedImportAlias(t *testing.T) {
	typeEnv, _ := BuildEnvFromImports("", []ImportConfig{{
		Name:   "Dagger",
		Schema: schemaWithCoreShadowTypes(),
	}})

	importAlias, found := typeEnv.NamedType("Dagger")
	require.True(t, found)
	importedContainer, found := importAlias.NamedType("Container")
	require.True(t, found)

	localDagger, err := declareLocalType(typeEnv, "Dagger", ObjectKind)
	require.ErrorContains(t, err, `type "Dagger" conflicts with import alias`)
	require.Nil(t, localDagger)

	aliasAfter, found := typeEnv.NamedType("Dagger")
	require.True(t, found)
	require.Same(t, importAlias, aliasAfter)
	qualifiedContainer, found := aliasAfter.NamedType("Container")
	require.True(t, found)
	require.Same(t, importedContainer, qualifiedContainer)
}

func TestRunDirDeclarationCannotShadowImportAlias(t *testing.T) {
	ctx := ContextWithImportConfigs(context.Background(), ImportConfig{
		Name:   "Dagger",
		Schema: schemaWithCoreShadowTypes(),
	})
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.dang"), []byte(`
import Dagger

type Dagger {
  pub value: String!
}

pub core: Dagger.Container! = Dagger.container
`), 0o600))

	_, err := RunDir(ctx, dir, false)
	require.ErrorContains(t, err, `type "Dagger" conflicts with import alias`)
}

func TestRunDirDeclarationsShadowImportedTypes(t *testing.T) {
	ctx := ContextWithImportConfigs(context.Background(), ImportConfig{
		Name:       "Dagger",
		Schema:     schemaWithCoreShadowTypes(),
		AutoImport: true,
	})
	env := runDangSnippetContext(t, ctx, `
pub maybe: Container = null

type TestShadowing {
  pub makeLocal: Container! {
    Container("local")
  }

  pub makeCore: Dagger.Container! {
    Dagger.container
  }
}

type Container {
  pub value: String!
}

type Directory {
  pub value: String!
}
`)

	daggerVal, found := requireEvalGet(t, env, "Dagger")
	require.True(t, found)
	daggerMod, ok := daggerVal.(*ModuleValue)
	require.True(t, ok)
	importedContainer, found := daggerMod.Mod.NamedType("Container")
	require.True(t, found)
	importedDirectory, found := daggerMod.Mod.NamedType("Directory")
	require.True(t, found)

	containerVal, found := requireEvalGet(t, env, "Container")
	require.True(t, found)
	containerCtor, ok := containerVal.(*ConstructorFunction)
	require.True(t, ok)
	require.NotSame(t, importedContainer, containerCtor.ClassType)

	moduleVal, ok := env.(*ModuleValue)
	require.True(t, ok)
	maybeScheme, found := moduleVal.Mod.SchemeOf("maybe")
	require.True(t, found)
	maybeType, mono := maybeScheme.Type()
	require.True(t, mono)
	require.Same(t, containerCtor.ClassType, maybeType)
	require.NotSame(t, importedContainer, maybeType)

	directoryVal, found := requireEvalGet(t, env, "Directory")
	require.True(t, found)
	directoryCtor, ok := directoryVal.(*ConstructorFunction)
	require.True(t, ok)
	require.NotSame(t, importedDirectory, directoryCtor.ClassType)

	testVal, found := requireEvalGet(t, env, "TestShadowing")
	require.True(t, found)
	testCtor, ok := testVal.(*ConstructorFunction)
	require.True(t, ok)

	localScheme, found := testCtor.ClassType.LocalSchemeOf("makeLocal")
	require.True(t, found)
	require.Same(t, containerCtor.ClassType, functionReturnType(t, localScheme))

	coreScheme, found := testCtor.ClassType.LocalSchemeOf("makeCore")
	require.True(t, found)
	require.Same(t, importedContainer, functionReturnType(t, coreScheme))
}

func TestDeclareDirSkipsBodiesAndKeepsDaggerTypes(t *testing.T) {
	ctx := ContextWithImportConfigs(context.Background(), ImportConfig{
		Name:       "Dagger",
		Schema:     schemaWithCoreShadowTypes(),
		AutoImport: true,
	})
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.dang"), []byte(`
type Test {
  pub containerEcho(stringArg: String! = missing.default): Container! {
    Dagger.container
  }

  pub print(stringArg: String!): String! {
    test.containerEcho(stringArg: stringArg).stdout
  }
}
`), 0o600))

	_, err := RunDir(ctx, dir, false)
	require.Error(t, err)

	env, err := DeclareDir(ctx, dir, false)
	require.NoError(t, err)

	daggerVal, found := requireEvalGet(t, env, "Dagger")
	require.True(t, found)
	daggerMod, ok := daggerVal.(*ModuleValue)
	require.True(t, ok)
	importedContainer, found := daggerMod.Mod.NamedType("Container")
	require.True(t, found)

	testVal, found := requireEvalGet(t, env, "Test")
	require.True(t, found)
	testCtor, ok := testVal.(*ConstructorFunction)
	require.True(t, ok)

	containerEcho, found := testCtor.ClassType.LocalSchemeOf("containerEcho")
	require.True(t, found)
	require.Same(t, importedContainer, functionReturnType(t, containerEcho))

	print, found := testCtor.ClassType.LocalSchemeOf("print")
	require.True(t, found)
	require.Same(t, StringType, functionReturnType(t, print))
}

func TestRunDirImplementingPreludeInterfaceDoesNotMutatePrelude(t *testing.T) {
	before := len(ErrorType.GetImplementers())
	runDangSnippet(t, `
type MyError implements Error {
  pub message: String! = "x"
}
assert { MyError.message == "x" }
`)
	require.Len(t, ErrorType.GetImplementers(), before)
}

func TestRunDirUnionWithPreludeMemberDoesNotMutatePrelude(t *testing.T) {
	before := len(BasicErrorType.GetUnions())
	runDangSnippet(t, `
union MyUnion = BasicError
assert { MyUnion != null }
`)
	require.Len(t, BasicErrorType.GetUnions(), before)
}

func runDangSnippet(t *testing.T, source string) EvalEnv {
	t.Helper()
	return runDangSnippetContext(t, context.Background(), source)
}

func runDangSnippetContext(t *testing.T, ctx context.Context, source string) EvalEnv {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.dang"), []byte(source), 0o600))
	env, err := RunDir(ctx, dir, false)
	require.NoError(t, err)
	return env
}

func functionReturnType(t *testing.T, scheme *hm.Scheme) hm.Type {
	t.Helper()
	typ, mono := scheme.Type()
	require.True(t, mono)
	fn, ok := typ.(*hm.FunctionType)
	require.True(t, ok)
	ret := fn.Ret(false)
	if nn, ok := ret.(hm.NonNullType); ok {
		ret = nn.Type
	}
	return ret
}

func schemaWithCoreShadowTypes() *introspection.Schema {
	schema := &introspection.Schema{
		Types: introspection.Types{
			{
				Kind: introspection.TypeKindScalar,
				Name: "ID",
			},
			{
				Kind: introspection.TypeKindScalar,
				Name: "String",
			},
			{
				Kind: introspection.TypeKindObject,
				Name: "Container",
				Fields: []*introspection.Field{
					{
						Name: "id",
						TypeRef: &introspection.TypeRef{
							Kind: introspection.TypeKindNonNull,
							OfType: &introspection.TypeRef{
								Kind: introspection.TypeKindScalar,
								Name: "ID",
							},
						},
					},
				},
			},
			{
				Kind: introspection.TypeKindObject,
				Name: "Directory",
				Fields: []*introspection.Field{
					{
						Name: "id",
						TypeRef: &introspection.TypeRef{
							Kind: introspection.TypeKindNonNull,
							OfType: &introspection.TypeRef{
								Kind: introspection.TypeKindScalar,
								Name: "ID",
							},
						},
					},
				},
			},
			{
				Kind: introspection.TypeKindObject,
				Name: "Query",
				Fields: []*introspection.Field{
					{
						Name: "container",
						TypeRef: &introspection.TypeRef{
							Kind: introspection.TypeKindNonNull,
							OfType: &introspection.TypeRef{
								Kind: introspection.TypeKindObject,
								Name: "Container",
							},
						},
					},
				},
			},
		},
	}
	schema.QueryType.Name = "Query"
	return schema
}

func schemaWithErrorObject() *introspection.Schema {
	schema := &introspection.Schema{
		Types: introspection.Types{
			{
				Kind: introspection.TypeKindScalar,
				Name: "ID",
			},
			{
				Kind: introspection.TypeKindScalar,
				Name: "String",
			},
			{
				Kind: introspection.TypeKindObject,
				Name: "Error",
				Fields: []*introspection.Field{
					{
						Name: "id",
						TypeRef: &introspection.TypeRef{
							Kind: introspection.TypeKindNonNull,
							OfType: &introspection.TypeRef{
								Kind: introspection.TypeKindScalar,
								Name: "ID",
							},
						},
					},
					{
						Name: "message",
						TypeRef: &introspection.TypeRef{
							Kind: introspection.TypeKindNonNull,
							OfType: &introspection.TypeRef{
								Kind: introspection.TypeKindScalar,
								Name: "String",
							},
						},
					},
				},
			},
			{
				Kind: introspection.TypeKindObject,
				Name: "Query",
			},
		},
	}
	schema.QueryType.Name = "Query"
	return schema
}
