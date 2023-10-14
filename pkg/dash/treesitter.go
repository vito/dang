package dash

import (
	"fmt"
	"log"
	"strings"

	"github.com/vito/dash/pkg/dash/treesitter"
)

func TreesitterGrammar() treesitter.Grammar {
	ts := treesitter.NewGrammar("dash")

	ts.Word = "word_token"
	ts.Extras = []treesitter.Rule{
		{
			Type: treesitter.RuleTypeSymbol,
			Name: "comment_token",
		},
		{
			Type:  treesitter.RuleTypePattern,
			Value: `[\s]`,
		},
	}
	ts.Supertypes = []string{"expr"}

	for i, rule := range g.rules {
		prec := len(g.rules) - i
		tsRule := treesitterRule(rule, prec)
		if tsRule == nil || rule.name == "_" {
			log.Println("skipping rule", rule.name)
			continue
		} else {
			log.Println("adding rule", rule.name)
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
		if t.name == "_" {
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

	return ts
}
