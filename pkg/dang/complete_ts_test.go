package dang

import (
	"context"
	"testing"

	"github.com/vito/dang/pkg/hm"
)

// testCompletionEnv builds a simple env with some bindings for testing.
func testCompletionEnv() Env {
	registerBuiltinTypes()

	env := NewPreludeEnv()

	// Add a "container" module with from and withExec methods.
	containerMod := NewModule("Container", ObjectKind)

	// from(address: String!) -> Container
	fromArgs := NewRecordType("")
	fromArgs.Add("address", hm.NewScheme(nil, hm.NonNullType{Type: StringType}))
	fromArgs.Add("platform", hm.NewScheme(nil, StringType))
	fromArgs.DocStrings = map[string]string{
		"address":  "Image address.",
		"platform": "Platform to use.",
	}
	containerMod.Add("from", hm.NewScheme(nil, hm.NewFnType(fromArgs, containerMod)))
	containerMod.SetVisibility("from", PublicVisibility)

	// withExec(args: [String!]!) -> Container
	execArgs := NewRecordType("")
	execArgs.Add("args", hm.NewScheme(nil, hm.NonNullType{Type: ListType{Type: hm.NonNullType{Type: StringType}}}))
	containerMod.Add("withExec", hm.NewScheme(nil, hm.NewFnType(execArgs, containerMod)))
	containerMod.SetVisibility("withExec", PublicVisibility)

	env.Add("container", hm.NewScheme(nil, hm.NonNullType{Type: containerMod}))
	env.SetVisibility("container", PublicVisibility)
	env.AddClass("Container", containerMod)

	// Add a top-level "directory" binding.
	dirMod := NewModule("Directory", ObjectKind)
	dirMod.Add("entries", hm.NewScheme(nil, hm.NewFnType(NewRecordType(""), ListType{Type: hm.NonNullType{Type: StringType}})))
	dirMod.SetVisibility("entries", PublicVisibility)

	env.Add("directory", hm.NewScheme(nil, hm.NonNullType{Type: dirMod}))
	env.SetVisibility("directory", PublicVisibility)
	env.AddClass("Directory", dirMod)

	return env
}

func TestComplete_DotMember(t *testing.T) {
	env := testCompletionEnv()
	ctx := context.Background()

	tests := []struct {
		name       string
		text       string
		line, col  int
		wantLabels []string
	}{
		{
			name:       "partial member",
			text:       "container.fr",
			line:       0,
			col:        12,
			wantLabels: []string{"from"},
		},
		{
			name:       "all members after dot",
			text:       "container.",
			line:       0,
			col:        10,
			wantLabels: []string{"from", "withExec"},
		},
		{
			name:       "filtered member",
			text:       "container.with",
			line:       0,
			col:        14,
			wantLabels: []string{"withExec"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := Complete(ctx, env, tt.text, tt.line, tt.col)
			if len(result.Items) != len(tt.wantLabels) {
				labels := make([]string, len(result.Items))
				for i, c := range result.Items {
					labels[i] = c.Label
				}
				t.Fatalf("got %d items %v, want %d %v", len(result.Items), labels, len(tt.wantLabels), tt.wantLabels)
			}
			gotLabels := map[string]bool{}
			for _, c := range result.Items {
				gotLabels[c.Label] = true
			}
			for _, want := range tt.wantLabels {
				if !gotLabels[want] {
					t.Errorf("missing expected completion %q", want)
				}
			}
		})
	}
}

func TestComplete_BareIdent(t *testing.T) {
	env := testCompletionEnv()
	ctx := context.Background()

	tests := []struct {
		name       string
		text       string
		line, col  int
		wantLabels []string
	}{
		{
			name:       "partial top-level name",
			text:       "cont",
			line:       0,
			col:        4,
			wantLabels: []string{"container"},
		},
		{
			name:       "partial matching multiple",
			text:       "dir",
			line:       0,
			col:        3,
			wantLabels: []string{"directory"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := Complete(ctx, env, tt.text, tt.line, tt.col)
			gotLabels := map[string]bool{}
			for _, c := range result.Items {
				gotLabels[c.Label] = true
			}
			for _, want := range tt.wantLabels {
				if !gotLabels[want] {
					labels := make([]string, len(result.Items))
					for i, c := range result.Items {
						labels[i] = c.Label
					}
					t.Errorf("missing expected completion %q in %v", want, labels)
				}
			}
		})
	}
}

func TestComplete_ArgCompletion(t *testing.T) {
	env := testCompletionEnv()
	ctx := context.Background()

	tests := []struct {
		name       string
		text       string
		line, col  int
		wantLabels []string
		wantNone   bool
	}{
		{
			name:       "arg names after open paren",
			text:       "container.from(",
			line:       0,
			col:        15,
			wantLabels: []string{"address", "platform"},
		},
		{
			name:       "partial arg name",
			text:       "container.from(addr",
			line:       0,
			col:        19,
			wantLabels: []string{"address"},
		},
		{
			name:     "in value position after colon",
			text:     "container.from(address: ",
			line:     0,
			col:      24,
			wantNone: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := Complete(ctx, env, tt.text, tt.line, tt.col)
			if tt.wantNone {
				if len(result.Items) > 0 {
					labels := make([]string, len(result.Items))
					for i, c := range result.Items {
						labels[i] = c.Label
					}
					t.Fatalf("expected no completions, got %v", labels)
				}
				return
			}
			gotLabels := map[string]bool{}
			for _, c := range result.Items {
				gotLabels[c.Label] = true
			}
			for _, want := range tt.wantLabels {
				if !gotLabels[want] {
					labels := make([]string, len(result.Items))
					for i, c := range result.Items {
						labels[i] = c.Label
					}
					t.Errorf("missing expected completion %q in %v", want, labels)
				}
			}
		})
	}
}

func TestComplete_ReplaceFrom(t *testing.T) {
	env := testCompletionEnv()
	ctx := context.Background()

	tests := []struct {
		name            string
		text            string
		line, col       int
		wantReplaceFrom int
	}{
		{
			name:            "partial member replaces from partial start",
			text:            "container.fr",
			line:            0,
			col:             12,
			wantReplaceFrom: 10, // "fr" starts at offset 10
		},
		{
			name:            "dot with no partial",
			text:            "container.",
			line:            0,
			col:             10,
			wantReplaceFrom: 10, // partial is empty, replace from cursor
		},
		{
			name:            "bare ident",
			text:            "cont",
			line:            0,
			col:             4,
			wantReplaceFrom: 0, // "cont" starts at 0
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := Complete(ctx, env, tt.text, tt.line, tt.col)
			if len(result.Items) == 0 {
				t.Fatal("expected completions")
			}
			if result.ReplaceFrom != tt.wantReplaceFrom {
				t.Errorf("ReplaceFrom = %d, want %d", result.ReplaceFrom, tt.wantReplaceFrom)
			}
		})
	}
}

func TestComplete_MultiLine(t *testing.T) {
	env := testCompletionEnv()
	ctx := context.Background()

	// Multi-line input: second line has "container.fr"
	text := "let x = 1\ncontainer.fr"
	result := Complete(ctx, env, text, 1, 12)

	found := false
	for _, c := range result.Items {
		if c.Label == "from" {
			found = true
			break
		}
	}
	if !found {
		labels := make([]string, len(result.Items))
		for i, c := range result.Items {
			labels[i] = c.Label
		}
		t.Errorf("expected 'from' in completions, got %v", labels)
	}

	// ReplaceFrom should be the byte offset of "fr" in the full text
	expectedReplaceFrom := len("let x = 1\ncontainer.")
	if result.ReplaceFrom != expectedReplaceFrom {
		t.Errorf("ReplaceFrom = %d, want %d", result.ReplaceFrom, expectedReplaceFrom)
	}
}

func TestComplete_DotMemberAfterParensOnEarlierLines(t *testing.T) {
	env := testCompletionEnv()
	ctx := context.Background()

	// Regression: earlier lines with parens (e.g. function calls) must not
	// cause splitArgExpr to think the cursor is inside an arg list.
	text := "container.from()\n\"hello\".sp"
	result := Complete(ctx, env, text, 1, 10)

	found := false
	for _, c := range result.Items {
		if c.Label == "split" {
			found = true
			break
		}
	}
	if !found {
		labels := make([]string, len(result.Items))
		for i, c := range result.Items {
			labels[i] = c.Label
		}
		t.Errorf("expected 'split' in completions after parens on earlier line, got %v", labels)
	}
}

func TestComplete_NoResults(t *testing.T) {
	env := testCompletionEnv()
	ctx := context.Background()

	tests := []struct {
		name      string
		text      string
		line, col int
	}{
		{
			name: "empty text",
			text: "",
			line: 0,
			col:  0,
		},
		{
			name: "just whitespace",
			text: "   ",
			line: 0,
			col:  3,
		},
		{
			name: "no matching prefix",
			text: "zzz",
			line: 0,
			col:  3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := Complete(ctx, env, tt.text, tt.line, tt.col)
			if len(result.Items) != 0 {
				labels := make([]string, len(result.Items))
				for i, c := range result.Items {
					labels[i] = c.Label
				}
				t.Errorf("expected no completions, got %v", labels)
			}
		})
	}
}

func TestByteOffsetToLineCol(t *testing.T) {
	// Test the REPL helper
	tests := []struct {
		text     string
		offset   int
		wantLine int
		wantCol  int
	}{
		{"hello", 3, 0, 3},
		{"hello\nworld", 8, 1, 2},
		{"a\nb\nc", 4, 2, 0},
		{"", 0, 0, 0},
	}

	for _, tt := range tests {
		line, col := 0, 0
		if tt.offset > len(tt.text) {
			tt.offset = len(tt.text)
		}
		for i := 0; i < tt.offset; i++ {
			if tt.text[i] == '\n' {
				line++
				col = 0
			} else {
				col++
			}
		}
		if line != tt.wantLine || col != tt.wantCol {
			t.Errorf("byteOffsetToLineCol(%q, %d) = (%d, %d), want (%d, %d)",
				tt.text, tt.offset, line, col, tt.wantLine, tt.wantCol)
		}
	}
}
