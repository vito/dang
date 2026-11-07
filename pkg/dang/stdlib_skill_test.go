package dang

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// stdlibSkillDoc is the stdlib reference of the dang-language agent skill:
// what agents writing Dang read to learn the builtins. It is hand-written,
// so TestStdlibSkillCoversBuiltins keeps it from silently falling behind.
var stdlibSkillDoc = filepath.Join("..", "..", ".agents", "skills", "dang-language", "reference", "stdlib.md")

// stdlibSkillSections maps each builtin receiver type to the heading of its
// section in stdlib.md. A method must be mentioned in its receiver's section,
// so a Map method doesn't count as documented because List has one by the
// same name. Free functions belong under the "" key.
var stdlibSkillSections = map[string]string{
	"":       "## Top-level functions",
	"String": "## `String!` methods",
	"Match":  "### `Match` object",
	"Path":   "## `Path!` scalar",
	"List":   "## `[T]!` methods",
	"Map":    "## `Map[T]!` methods",
}

// TestStdlibSkillCoversBuiltins fails when a builtin — a Go-registered
// function, method, or static, a Regexp static, or a public member of the
// Dang-source prelude — isn't mentioned in the skill's stdlib reference.
// Deprecated builtins are exempt: the skill should steer away from them.
//
// The check is by spelling only: a method must appear as `.name` in its
// receiver's section, a free function as `name(` or `name {` among the
// top-level functions, and a static as `Module.name` anywhere. That's enough
// to catch a builtin nobody wrote down, which is the drift that matters.
func TestStdlibSkillCoversBuiltins(t *testing.T) {
	raw, err := os.ReadFile(stdlibSkillDoc)
	if err != nil {
		t.Fatalf("reading the skill's stdlib reference: %v", err)
	}
	doc := string(raw)

	member := func(name string) string { return `\.` + regexp.QuoteMeta(name) + `\b` }
	function := func(name string) string { return `\b` + regexp.QuoteMeta(name) + `\s*[({]` }
	mentions := func(text, pattern string) bool {
		return regexp.MustCompile(pattern).MatchString(text)
	}
	section := func(receiver string) string {
		heading, ok := stdlibSkillSections[receiver]
		if !ok {
			t.Errorf("no stdlib.md section for builtin receiver %q: document it and add it to stdlibSkillSections", receiver)
			return ""
		}
		text, ok := markdownSection(doc, heading)
		if !ok {
			t.Errorf("stdlib.md has no heading starting with %q", heading)
		}
		return text
	}
	check := func(label, text, pattern string) {
		t.Helper()
		if !mentions(text, pattern) {
			t.Errorf("builtin %s is not documented in %s", label, stdlibSkillDoc)
		}
	}

	for _, d := range allBuiltins() {
		if d.Deprecated != "" {
			continue
		}
		switch {
		case d.IsStatic:
			check(builtinLabel(d), doc, regexp.QuoteMeta(d.HostModule.Named)+member(d.Name))
		case d.IsMethod:
			check(builtinLabel(d), section(d.ReceiverType.Named), member(d.Name))
		default:
			check(builtinLabel(d), section(""), function(d.Name))
		}
	}

	// Regexp's statics are wired onto its meta type directly, outside the
	// builtin registry.
	for name := range RegexpType.MetaType().Bindings(PublicVisibility) {
		check("Regexp."+name, doc, `Regexp`+member(name))
	}

	prelude := PreludeModule()
	for name := range prelude.Bindings(PublicVisibility) {
		check(name, section(""), function(name))
	}
	for typeName, scope := range prelude.NamedTypes() {
		mod, ok := scope.(*Type)
		if !ok {
			continue
		}
		text := section(typeName)
		for name := range mod.Bindings(PublicVisibility) {
			check(typeName+"."+name, text, member(name))
		}
	}
}

// markdownSection returns the text under the first heading line starting with
// heading, up to the next heading of the same or a higher level.
func markdownSection(doc, heading string) (string, bool) {
	level := strings.IndexFunc(heading, func(r rune) bool { return r != '#' })
	var b strings.Builder
	in := false
	for line := range strings.SplitSeq(doc, "\n") {
		if in {
			if l := strings.IndexFunc(line, func(r rune) bool { return r != '#' }); l > 0 && l <= level && strings.HasPrefix(line[l:], " ") {
				break
			}
			b.WriteString(line)
			b.WriteString("\n")
			continue
		}
		in = strings.HasPrefix(line, heading)
	}
	return b.String(), b.Len() > 0 || in
}
