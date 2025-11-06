package dang

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/vito/dang/pkg/dang/treesitter"
)

func skipTS(name string) bool {
	return strings.HasPrefix(name, "_")
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
		{
			{
				Type: treesitter.RuleTypeSymbol,
				Name: "doc_string",
			},
			{
				Type: treesitter.RuleTypeSymbol,
				Name: "triple_quote_string",
			},
		},
	}

	for i, rule := range g.rules {
		prec := len(g.rules) - i
		tsRule := treesitterRule(rule, prec)
		if tsRule == nil || skipTS(rule.name) {
			slog.Warn("skipping grammar rule", "rule", rule.name)
			continue
		} else {
			slog.Info("adding grammar rule", "rule", rule.name)
			ts.Rules.Add(treesitter.Name(rule.name), *tsRule)
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
	// case *throwExpr:
	// 	// ignored
	// case *recoveryExpr:
	// 	// ignored
	// case *stateCodeExpr:
	// 	// ignored
	// case *andCodeExpr:
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
