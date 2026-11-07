package dang

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// stdlibSkillDoc is the stdlib reference of the dang-language agent skill:
// what agents writing Dang read to learn the builtins. It is hand-written,
// so TestStdlibSkillCoversBuiltins keeps it from silently falling behind.
var stdlibSkillDoc = filepath.Join("..", "..", ".agents", "skills", "dang-language", "reference", "stdlib.md")

// TestStdlibSkillCoversBuiltins fails when a builtin — a Go-registered
// function, method, or static, a Regexp static, or a public member of the
// Dang-source prelude — isn't mentioned in the skill's stdlib reference.
// Deprecated builtins are exempt: the skill should steer away from them.
//
// The check is by spelling only: a method must appear as `.name`, a static
// as `Module.name`, a free function as `name(` or `name {`. That's enough to
// catch a builtin nobody wrote down, which is the drift that matters.
func TestStdlibSkillCoversBuiltins(t *testing.T) {
	doc, err := os.ReadFile(stdlibSkillDoc)
	if err != nil {
		t.Fatalf("reading the skill's stdlib reference: %v", err)
	}

	mentions := func(pattern string) bool {
		return regexp.MustCompile(pattern).Match(doc)
	}
	member := func(name string) string { return `\.` + regexp.QuoteMeta(name) + `\b` }
	function := func(name string) string { return `\b` + regexp.QuoteMeta(name) + `\s*[({]` }

	for _, d := range allBuiltins() {
		if d.Deprecated != "" {
			continue
		}
		var pattern string
		switch {
		case d.IsStatic:
			pattern = regexp.QuoteMeta(d.HostModule.Named) + member(d.Name)
		case d.IsMethod:
			pattern = member(d.Name)
		default:
			pattern = function(d.Name)
		}
		if !mentions(pattern) {
			t.Errorf("builtin %s is not documented in %s", builtinLabel(d), stdlibSkillDoc)
		}
	}

	// Regexp's statics are wired onto its meta type directly, outside the
	// builtin registry.
	for name := range RegexpType.MetaType().Bindings(PublicVisibility) {
		if !mentions(`Regexp` + member(name)) {
			t.Errorf("builtin Regexp.%s is not documented in %s", name, stdlibSkillDoc)
		}
	}

	prelude := PreludeModule()
	for name := range prelude.Bindings(PublicVisibility) {
		if !mentions(function(name)) {
			t.Errorf("prelude function %s is not documented in %s", name, stdlibSkillDoc)
		}
	}
	for typeName, scope := range prelude.NamedTypes() {
		mod, ok := scope.(*Type)
		if !ok {
			continue
		}
		for name := range mod.Bindings(PublicVisibility) {
			if !mentions(member(name)) {
				t.Errorf("prelude member %s.%s is not documented in %s", typeName, name, stdlibSkillDoc)
			}
		}
	}
}
