package lsp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vito/dang/pkg/dang"
	"github.com/vito/dang/pkg/hm"
)

func TestFormatTypeDefinitionForHover(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "type",
			src: `"""
A person type.
"""
type Person {
  pub name: String!
}
`,
			want: "type Person {\n  pub name: String!\n}",
		},
		{
			name: "interface",
			src: `"""
A thing interface.
"""
interface Thing {
  pub id: String!
}
`,
			want: "interface Thing {\n  pub id: String!\n}",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed, err := dang.Parse("hover.dang", []byte(tt.src))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}

			mod := parsed.(*dang.ModuleBlock)
			if _, err := mod.Infer(context.Background(), dang.NewPreludeEnv(""), hm.NewSimpleFresher()); err != nil {
				t.Fatalf("infer: %v", err)
			}
			codeBlock, docString := formatTypeDefinitionForHover(nil, mod.Forms[0], strings.Fields(tt.want)[1])

			if codeBlock != tt.want {
				t.Fatalf("code block:\n%s\nwant:\n%s", codeBlock, tt.want)
			}
			if docString == "" {
				t.Fatalf("expected doc string")
			}
			if strings.Contains(codeBlock, docString) {
				t.Fatalf("code block should not contain top-level doc string: %q", codeBlock)
			}
		})
	}
}

func TestFormatPublicTypeShape(t *testing.T) {
	iface := dang.NewModule("Thing", dang.InterfaceKind)
	iface.SetModuleDocString("A GraphQL-loaded interface.")
	iface.Add("id", hm.NewScheme(nil, hm.NewFnType(
		dang.NewRecordType(""),
		hm.NonNullType{Type: dang.StringType},
	)))
	iface.SetVisibility("id", dang.PublicVisibility)
	iface.SetDocString("id", "The stable ID.")

	argRecord := dang.NewRecordType("", dang.Keyed[*hm.Scheme]{
		Key:   "limit",
		Value: hm.NewScheme(nil, dang.IntType),
	})
	iface.Add("items", hm.NewScheme(nil, hm.NewFnType(
		argRecord,
		hm.NonNullType{Type: dang.GraphQLListType{Type: hm.NonNullType{Type: dang.StringType}}},
	)))
	iface.SetVisibility("items", dang.PublicVisibility)

	codeBlock := dang.FormatPublicTypeShape(iface)
	docString := iface.GetModuleDocString()
	want := `interface Thing {
  """
  The stable ID.
  """
  pub id: String!
  pub items(limit: Int): [String!]!
}`
	if codeBlock != want {
		t.Fatalf("code block:\n%s\nwant:\n%s", codeBlock, want)
	}
	if docString != "A GraphQL-loaded interface." {
		t.Fatalf("doc string = %q", docString)
	}
}

func TestFormatNamedTypeForHoverUsesTypeEnv(t *testing.T) {
	iface := dang.NewModule("Thing", dang.InterfaceKind)
	iface.SetModuleDocString("Loaded from GraphQL.")
	iface.Add("id", hm.NewScheme(nil, hm.NewFnType(
		dang.NewRecordType(""),
		hm.NonNullType{Type: dang.StringType},
	)))
	iface.SetVisibility("id", dang.PublicVisibility)

	env := dang.NewPreludeEnv("")
	env.AddObject("Thing", iface)
	f := &File{TypeEnv: env}

	codeBlock, docString := formatNamedTypeForHover(f, Position{}, "Thing")
	if !strings.Contains(codeBlock, "interface Thing {") || !strings.Contains(codeBlock, "pub id: String!") {
		t.Fatalf("unexpected code block: %q", codeBlock)
	}
	if docString != "Loaded from GraphQL." {
		t.Fatalf("doc string = %q", docString)
	}
}

func TestFormatNamedTypeForHoverResolvesQualifiedType(t *testing.T) {
	imported := dang.NewModule("ServerInfo", dang.ObjectKind)
	imported.SetModuleDocString("Imported GraphQL type.")
	imported.Add("version", hm.NewScheme(nil, hm.NewFnType(
		dang.NewRecordType(""),
		hm.NonNullType{Type: dang.StringType},
	)))
	imported.SetVisibility("version", dang.PublicVisibility)

	local := dang.NewModule("ServerInfo", dang.ObjectKind)
	local.Add("localOnly", hm.NewScheme(nil, hm.NonNullType{Type: dang.IntType}))
	local.SetVisibility("localOnly", dang.PublicVisibility)

	schemaEnv := dang.NewPreludeEnv("Test")
	schemaEnv.AddObject("ServerInfo", imported)

	env := dang.NewPreludeEnv("")
	env.AddObject("Test", schemaEnv)
	env.AddObject("ServerInfo", local)

	text := "let info: Test.ServerInfo! = null"
	pos := Position{Line: 0, Character: strings.Index(text, "ServerInfo")}
	f := &File{Text: text, TypeEnv: env}

	codeBlock, docString := formatNamedTypeForHover(f, pos, "ServerInfo")
	if !strings.Contains(codeBlock, "pub version: String!") {
		t.Fatalf("expected imported type, got: %q", codeBlock)
	}
	if strings.Contains(codeBlock, "localOnly") {
		t.Fatalf("qualified hover resolved local shadow: %q", codeBlock)
	}
	if docString != "Imported GraphQL type." {
		t.Fatalf("doc string = %q", docString)
	}
}

func TestFormatNamedTypeForHoverFromGraphQLImport(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join("testdata", "hover.dang")
	absPath, err := filepath.Abs(path)
	if err != nil {
		t.Fatalf("abs path: %v", err)
	}
	textBytes, err := os.ReadFile(absPath)
	if err != nil {
		t.Fatalf("read hover fixture: %v", err)
	}
	text := string(textBytes)

	h := NewHandler(ctx)
	uri := toURI(absPath)
	if err := h.openFile(uri, "dang", 1); err != nil {
		t.Fatalf("open file: %v", err)
	}
	version := 1
	if err := h.updateFile(ctx, uri, text, &version); err != nil {
		t.Fatalf("update file: %v", err)
	}

	f := h.waitForFile(uri)
	lineIdx := -1
	for i, line := range strings.Split(text, "\n") {
		if strings.Contains(line, "pub info: ServerInfo!") {
			lineIdx = i
			break
		}
	}
	if lineIdx < 0 {
		t.Fatalf("fixture missing ServerInfo annotation")
	}
	line := strings.Split(text, "\n")[lineIdx]
	pos := Position{Line: lineIdx, Character: strings.Index(line, "ServerInfo")}

	codeBlock, _ := formatNamedTypeForHover(f, pos, "ServerInfo")
	if !strings.Contains(codeBlock, "type ServerInfo {") || !strings.Contains(codeBlock, "pub version: String!") {
		t.Fatalf("unexpected code block: %q", codeBlock)
	}
}

func TestHoverResultWithDocBelow(t *testing.T) {
	h := &langHandler{}
	res, err := h.hoverResultWithDocBelow("A thing interface.", "interface Thing {\n  pub id: String!\n}")
	if err != nil {
		t.Fatalf("hover result: %v", err)
	}

	hover := res.(*Hover)
	contents := hover.Contents.(MarkupContent)
	want := "```dang\ninterface Thing {\n  pub id: String!\n}\n```\n\n---\n\nA thing interface."
	if contents.Value != want {
		t.Fatalf("hover markdown:\n%s\nwant:\n%s", contents.Value, want)
	}
}
