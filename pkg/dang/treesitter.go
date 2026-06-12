package dang

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/vito/dang/v2/pkg/dang/treesitter"
)

// tsSkippedRules lists named rules whose bodies should not appear in the
// tree-sitter grammar because they are produced by the external scanner
// instead. References to these rules are redirected via tsRuleRefAliases.
var tsSkippedRules = map[string]bool{
	"MultiTemplateOpenToken":    true,
	"MultiTemplateCloseToken":   true,
	"MultiTemplateContentChar":  true,
	"SingleTemplateContentChar": true,
	"LangTagTerminator":         true,
	"CommentToken":              true,
}

func skipTS(name string) bool {
	if strings.HasPrefix(name, "_") {
		return true
	}
	return tsSkippedRules[name]
}

var tsExternalRuleNames = []treesitter.RuleName{
	"_automatic_newline",
	"_inline_space",
	// Backtick template fences. Variable-length matching can't be done in
	// tree-sitter's built-in lexer, so an external scanner tracks the open
	// fence count and refuses non-matching close runs as content.
	"multi_template_open_token",
	"multi_template_close_token",
	"_template_content_char",
	// Terminator for the optional language tag. External so the scanner can
	// clear its "just opened" flag at the right moment — once we've consumed
	// the newline after a lang tag, the next content letter is safe to treat
	// as content rather than the start of another lang tag attempt.
	"_lang_tag_terminator",
	// Line comment. External so the scanner can refuse it inside a backtick
	// template, where `#` is content (Markdown headers, `issue #5`, etc.)
	// rather than a comment start.
	"comment_token",
}

var tsRuleRefAliases = map[string]treesitter.RuleName{
	"_inlineSpace":              "_inline_space",
	"MultiTemplateOpenToken":    "multi_template_open_token",
	"MultiTemplateCloseToken":   "multi_template_close_token",
	"MultiTemplateContentChar":  "_template_content_char",
	"SingleTemplateContentChar": "_template_content_char",
	"LangTagTerminator":         "_lang_tag_terminator",
	"CommentToken":              "comment_token",
}

var tsRulePatches = map[treesitter.RuleName]func(treesitter.Rule) treesitter.Rule{
	treesitter.Name("Sep"):         patchSepAutomaticNewline,
	treesitter.Name("ArgType"):     patchArgTypeDocstringSep,
	treesitter.Name("Conditional"): patchPrecRight,
}

// patchPrecRight wraps a rule in right associativity. Used for the classic
// dangling-else ambiguity: `if (a) if (b) x else y` binds the `else` to the
// innermost `if`, matching the PEG's greedy parse.
func patchPrecRight(rule treesitter.Rule) treesitter.Rule {
	return treesitter.Rule{
		Type:    treesitter.RuleTypePrecRight,
		Value:   0,
		Content: &rule,
	}
}

func tsExternalSymbol(name treesitter.RuleName) treesitter.Rule {
	return treesitter.Rule{
		Type: treesitter.RuleTypeSymbol,
		Name: name,
	}
}

func tsExternalRules() []treesitter.Rule {
	rules := make([]treesitter.Rule, len(tsExternalRuleNames))
	for i, name := range tsExternalRuleNames {
		rules[i] = tsExternalSymbol(name)
	}
	return rules
}

func patchTSRule(name treesitter.RuleName, rule treesitter.Rule) treesitter.Rule {
	patch, ok := tsRulePatches[name]
	if !ok {
		return rule
	}
	return patch(rule)
}

// The PEG allows arbitrary whitespace — including newlines — between a
// parameter docstring and the parameter it documents. In tree-sitter that
// newline lexes as the external _automatic_newline (the call interpretation
// of `name("""...` keeps the sep token valid at that position), which the
// declaration's arg_type couldn't shift, so the GLR fork for the declaration
// died. Accept an optional sep between the docstring and the field.
func patchArgTypeDocstringSep(rule treesitter.Rule) treesitter.Rule {
	for i, member := range rule.Members {
		if member.Type != treesitter.RuleTypeSeq {
			continue
		}
		optSep := treesitter.Rule{
			Type: treesitter.RuleTypeChoice,
			Members: []treesitter.Rule{
				{Type: treesitter.RuleTypeSymbol, Name: "sep"},
				{Type: treesitter.RuleTypeBlank},
			},
		}
		member.Members = append(
			[]treesitter.Rule{member.Members[0], optSep},
			member.Members[1:]...,
		)
		rule.Members[i] = member
	}
	return rule
}

func patchSepAutomaticNewline(rule treesitter.Rule) treesitter.Rule {
	for i, member := range rule.Members {
		if member.Type == treesitter.RuleTypeString && member.Value == "\n" {
			rule.Members[i] = tsExternalSymbol("_automatic_newline")
		}
	}
	return rule
}

func TreesitterGrammar() treesitter.Grammar {
	ts := treesitter.NewGrammar("dang")

	ts.Word = "word_token"
	ts.Extras = []treesitter.Rule{
		{
			Type: treesitter.RuleTypeSymbol,
			Name: "comment_token",
		},
		{
			Type:  treesitter.RuleTypePattern,
			Value: `\s`,
		},
	}
	ts.Supertypes = []string{"expr", "form", "term"}
	ts.Conflicts = [][]treesitter.RuleName{
		{treesitter.Name("Float"), treesitter.Name("Int")},
		// With the `pub`/`let` keyword optional, a bare `name(arg: ...)` is
		// ambiguous between a method-with-args declaration (arg is an `Id` with
		// a type) and a function call with a named argument (`KeyValue`). GLR
		// explores both; the trailing `: Type` (declaration) vs its absence
		// (call) disambiguates.
		{treesitter.Name("Id"), treesitter.Name("KeyValue")},
		// A `"""..."""` after `(` is ambiguous between a parameter docstring
		// (declaration) and a triple-quoted string argument (call) — both are
		// built from the same tokens. GLR explores both; what follows the
		// closing paren disambiguates.
		{treesitter.Name("DocString"), treesitter.Name("TripleQuoteString")},
		// `{ x: Int!, ...` is ambiguous between a function-literal parameter
		// and a type-only field declaration in a block. GLR explores both;
		// the presence or absence of `=>` disambiguates.
		{treesitter.Name("TypeOnlyField"), treesitter.Name("BlockParam")},
	}
	ts.Precedences = [][]treesitter.Rule{
		{
			// Disambiguate block args:
			// 		{ x }         # x is body
			//    { x -> y }    # x is arg
			//    { x, y -> z } # x, y are args
			{
				Type: treesitter.RuleTypeSymbol,
				Name: "symbol",
			},
			{
				Type: treesitter.RuleTypeSymbol,
				Name: "auto_call_symbol",
			},
		},
	}

	// Register external scanner tokens. The scanner decides at lex time
	// whether a newline is a statement separator or just whitespace, and
	// whether PEG-only inline whitespace should be preserved for tree-sitter.
	ts.Externals = tsExternalRules()

	for i, rule := range g.rules {
		prec := len(g.rules) - i
		ruleName := treesitter.Name(rule.name)
		tsRule := treesitterRule(rule, prec)
		if tsRule == nil || skipTS(rule.name) {
			slog.Warn("skipping grammar rule", "rule", rule.name)
			continue
		} else {
			slog.Info("adding grammar rule", "rule", rule.name)
			ts.Rules.Add(ruleName, patchTSRule(ruleName, *tsRule))
		}
	}

	return ts
}

func treesitterRule(r *rule, prec int) *treesitter.Rule {
	ts := &treesitter.Rule{}

	switch t := r.expr.(type) {
	case *choiceExpr:
		ts.Type = treesitter.RuleTypeChoice
		for i, expr := range t.alternatives {
			sub := treesitterRule(&rule{
				expr:          expr,
				leftRecursive: r.leftRecursive,
			}, len(t.alternatives)-i)
			if sub == nil {
				continue
			}
			ts.Members = append(ts.Members, *sub)
		}
	case *actionExpr:
		ts = treesitterRule(&rule{
			name: r.name,
			expr: t.expr,
		}, prec)
	case *seqExpr:
		ts.Type = treesitter.RuleTypeSeq
		for _, expr := range t.exprs {
			sub := treesitterRule(&rule{
				expr: expr,
			}, prec)
			if sub == nil {
				continue
			}
			ts.Members = append(ts.Members, *sub)
		}
	case *labeledExpr:
		ts.Type = treesitter.RuleTypeField
		ts.Name = treesitter.Name(t.label)
		ts.Content = treesitterRule(&rule{
			expr: t.expr,
		}, prec)
	case *ruleRefExpr:
		if alias, ok := tsRuleRefAliases[t.name]; ok {
			rule := tsExternalSymbol(alias)
			return &rule
		}
		if skipTS(t.name) {
			// ignore whitespace; tree-sitter works differently
			return nil
		}
		ts.Type = treesitter.RuleTypeSymbol
		ts.Name = treesitter.Name(t.name)
	case *anyMatcher:
		ts.Type = treesitter.RuleTypePattern
		ts.Value = "."
	case *charClassMatcher:
		ts.Type = treesitter.RuleTypePattern
		ts.Value = string(t.val)
		if t.ignoreCase {
			ts.Flags = "i"
			ts.Value = strings.TrimSuffix(ts.Value.(string), "i")
		}
	case *litMatcher:
		ts.Type = treesitter.RuleTypeString
		ts.Value = string(t.val)
	case *andExpr:
		sub := treesitterRule(&rule{
			expr: t.expr,
		}, prec)
		if sub == nil {
			return nil
		}
		ts.Type = treesitter.RuleTypeRepeat
		ts.Content = sub
	case *oneOrMoreExpr:
		sub := treesitterRule(&rule{
			expr: t.expr,
		}, prec)
		if sub == nil {
			return nil
		}
		if sub.Type == treesitter.RuleTypePattern {
			// already a repeat-one
			sub.Value = sub.Value.(string) + "+"
			ts = sub
		} else {
			ts.Type = treesitter.RuleTypeRepeatOne
			ts.Content = sub
		}
	case *zeroOrMoreExpr:
		sub := treesitterRule(&rule{
			expr: t.expr,
		}, prec)
		if sub == nil {
			return nil
		}
		if sub.Type == treesitter.RuleTypePattern {
			// already a repeat-one
			sub.Value = sub.Value.(string) + "*"
			ts = sub
		} else {
			ts.Type = treesitter.RuleTypeRepeat
			ts.Content = sub
		}
	case *zeroOrOneExpr:
		sub := treesitterRule(&rule{
			expr: t.expr,
		}, prec)
		if sub == nil {
			return nil
		}
		ts.Type = treesitter.RuleTypeChoice
		ts.Members = []treesitter.Rule{
			*sub,
			{
				Type: treesitter.RuleTypeBlank,
			},
		}
	case *notExpr:
		// ignored
		return nil
	case *stateCodeExpr:
		// Pigeon state mutation has no tree-sitter equivalent; the runtime
		// constraint it enforces is dropped from the tree-sitter grammar.
		return nil
	case *andCodeExpr:
		// Same: pigeon code predicates don't translate.
		return nil
	// case *throwExpr:
	// 	// ignored
	// case *recoveryExpr:
	// 	// ignored
	// case *notCodeExpr:
	// 	// ignored
	default:
		panic(fmt.Sprintf("unhandled rule type: %T", t))
	}

	// Simplify, since some things are seq'd with _ which doesn't get added
	if len(ts.Members) == 1 {
		// TODO only for Seq or Choice?
		*ts = ts.Members[0]
	}

	if strings.HasSuffix(string(r.name), "Token") {
		ts = &treesitter.Rule{
			Type:    treesitter.RuleTypeToken,
			Content: ts,
		}
	}

	if r.leftRecursive {
		ts = &treesitter.Rule{
			Type:    treesitter.RuleTypePrecLeft,
			Value:   prec,
			Content: ts,
		}
	}

	if strings.HasPrefix(string(r.name), "Immediate") {
		ts = &treesitter.Rule{
			Type:    treesitter.RuleTypeImmediateToken,
			Content: ts,
		}
	}

	if strings.HasPrefix(string(r.name), "PrecRight") {
		ts = &treesitter.Rule{
			Type:    treesitter.RuleTypePrecRight,
			Content: ts,
			Value:   2,
		}
	}

	return ts
}
