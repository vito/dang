package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"github.com/dave/jennifer/jen"
	sitter "github.com/smacker/go-tree-sitter"
	dash "github.com/vito/dash/grammar/bindings/go"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintf(os.Stderr, "usage: %s <grammar.json> <output.go>\n", os.Args[0])
		os.Exit(1)
	}

	grammarFile, err := os.Open(os.Args[1])
	if err != nil {
		panic(err)
	}

	defer grammarFile.Close()

	generatedFile, err := os.Create(os.Args[2])
	if err != nil {
		panic(err)
	}

	defer generatedFile.Close()

	var grammar Grammar
	err = json.NewDecoder(grammarFile).Decode(&grammar)
	if err != nil {
		panic(err)
	}

	type namedRule struct {
		Name string
		Rule Rule
	}

	rules := []namedRule{}
	for name, rule := range grammar.Rules {
		rules = append(rules, namedRule{
			Name: name,
			Rule: rule,
		})
	}
	sort.Slice(rules, func(i, j int) bool {
		return rules[i].Name < rules[j].Name
	})

	f := jen.NewFile("main")

	gen := NewCodegen()

	for _, nr := range rules {
		name, rule := nr.Name, nr.Rule

		fields := []jen.Code{jen.Id("unimplementedNode")}
		fields = append(fields, ruleFields(grammar.Rules, rule)...)

		gen.AddType(
			name,
			jen.Type().Id(name).Struct(fields...),
		)
	}

	gen.Generate(f)

	f.Render(generatedFile)
}

// Grammar is a tree-sitter grammar.json file.
type Grammar struct {
	Name  string          `json:"name"`
	Word  string          `json:"word"`
	Rules map[string]Rule `json:"rules"`
}

type RuleType string

const (
	RuleTypeBlank          RuleType = "BLANK"
	RuleTypeChoice         RuleType = "CHOICE"
	RuleTypeField          RuleType = "FIELD"
	RuleTypeImmediateToken RuleType = "IMMEDIATE_TOKEN"
	RuleTypePattern        RuleType = "PATTERN"
	RuleTypePrec           RuleType = "PREC"
	RuleTypePrecLeft       RuleType = "PREC_LEFT"
	RuleTypePrecRight      RuleType = "PREC_RIGHT"
	RuleTypeRepeat         RuleType = "REPEAT"
	RuleTypeSeq            RuleType = "SEQ"
	RuleTypeString         RuleType = "STRING"
	RuleTypeSymbol         RuleType = "SYMBOL"
	RuleTypeToken          RuleType = "TOKEN"
)

type Rule struct {
	Type    RuleType `json:"type"`
	Name    string   `json:"name,omitempty"`
	Value   any      `json:"value"`
	Members []Rule   `json:"members,omitempty"`
	Content *Rule    `json:"content,omitempty"`
}

type Codegen struct {
	NodeTypes       []jen.Code
	NodeTypesByName map[string]jen.Code
}

func NewCodegen() *Codegen {
	return &Codegen{
		NodeTypes:       []jen.Code{},
		NodeTypesByName: map[string]jen.Code{},
	}
}

func (c *Codegen) AddType(name string, def jen.Code) {
	if _, ok := c.NodeTypesByName[name]; ok {
		return
	}
	c.NodeTypes = append(c.NodeTypes, def)
	c.NodeTypesByName[name] = def
}

func (c *Codegen) Generate(f *jen.File) {
	c.WriteConstructors(f)
	c.WriteTypes(f)
}

func (c *Codegen) WriteTypes(f *jen.File) {
	f.Type().Id("blank").Struct(jen.Id("unimplementedNode"))
	f.Line()

	f.Type().Id("Node").Interface(
		// TODO
		jen.Id("UnmarshalTS").
			Params(
				jen.Op("*").Qual("github.com/smacker/go-tree-sitter", "Node"),
				jen.Index().Byte(),
			).
			Error(),
	)

	for _, t := range c.NodeTypes {
		f.Add(t)
		f.Line()
	}
}

func (c *Codegen) WriteConstructors(f *jen.File) {
	lang := dash.Language()

	f.Type().Id("unimplementedNode").Struct()

	f.Func().
		Params(jen.Id("unimplementedNode")).
		Id("UnmarshalTS").
		Params(
			jen.Id("node").Op("*").Qual("github.com/smacker/go-tree-sitter", "Node"),
			jen.Id("input").Index().Byte(),
		).
		Error().
		Block(
			jen.Return(jen.Qual("fmt", "Errorf").Call(jen.Lit("unimplemented node type"))),
		)

	symbols := lang.SymbolCount()

	types := f.Var().Id("Nodes").Op("=").Map(
		jen.Qual("github.com/smacker/go-tree-sitter", "Symbol"),
	).Func().Params().Id("Node")

	constructors := []jen.Code{}
	for i := uint32(0); i < symbols; i++ {
		name := lang.SymbolName(sitter.Symbol(i))
		symType := lang.SymbolType(sitter.Symbol(i))
		if symType != sitter.SymbolTypeRegular {
			continue
		}
		constructors = append(constructors,
			jen.Line().Lit(int(i)).Op(":").Func().
				Params().
				Id("Node").
				Values(jen.Return().Op("&").Id(name).Values()))
	}

	types.Values(constructors...).Line()
}

func ruleType(rules map[string]Rule, s *jen.Statement, rule Rule) jen.Code {
	switch rule.Type {
	case "BLANK":
		return s.Id("blank")
	case "CHOICE":
		return s.Struct(ruleFields(rules, rule)...)
	case "FIELD":
		return ruleType(rules, s, *rule.Content)
	case "IMMEDIATE_TOKEN", "PREC_LEFT", "PREC_RIGHT", "PREC":
		// these hints have no bearing on the AST types
		return ruleType(rules, s, *rule.Content)
	case "PATTERN", "STRING", "TOKEN":
		// records the matched content
		return s.String()
	case "REPEAT":
		return ruleType(rules, s.Index(), *rule.Content)
	case "SEQ":
		fields := []jen.Code{}
		for _, member := range rule.Members {
			fields = append(fields, ruleFields(rules, member)...)
		}
		return s.Struct(fields...)
	case "SYMBOL":
		return s.Op("*").Id(rule.Name)
	default:
		panic("unknown rule type: " + rule.Type)
	}
}

func ruleFields(rules map[string]Rule, rule Rule) []jen.Code {
	switch rule.Type {
	case "BLANK":
		return nil
	case "CHOICE":
		fields := []jen.Code{}
		for _, member := range rule.Members {
			fields = append(fields, ruleFields(rules, member)...)
		}
		return fields
	case "FIELD":
		return []jen.Code{ruleType(rules, jen.Id(rule.Name), *rule.Content)}
	case "IMMEDIATE_TOKEN", "PREC_LEFT", "PREC_RIGHT", "PREC":
		return ruleFields(rules, *rule.Content)
	case "PATTERN", "TOKEN":
		// record the matched content
		return []jen.Code{ruleType(rules, jen.Id("Token"), rule)}
	case "REPEAT":
		return nil // TODO?
	case "SEQ":
		fields := []jen.Code{}
		for _, member := range rule.Members {
			fields = append(fields, ruleFields(rules, member)...)
		}
		return fields
	case "STRING", "SYMBOL":
		// symbols act as field boundaries
		return nil
	default:
		panic("unknown rule type: " + rule.Type)
	}
}

// func ruleSelector(rules map[string]Rule, rule Rule) []jen.Code {
// 	switch rule.Type {
// 	case "BLANK":
// 		return nil
// 	case "CHOICE":
// 		return nil
// 	case "FIELD":
// 		return []jen.Code{ruleType(rules, jen.Id(rule.Name), *rule.Content)}
// 	case "IMMEDIATE_TOKEN", "PREC_LEFT", "PREC_RIGHT", "PREC":
// 		return ruleFields(rules, *rule.Content)
// 	case "PATTERN", "TOKEN":
// 		// record the matched content
// 		return []jen.Code{ruleType(rules, jen.Id("Token"), rule)}
// 	case "REPEAT":
// 		return nil // TODO?
// 	case "SEQ":
// 		fields := []jen.Code{}
// 		for _, member := range rule.Members {
// 			fields = append(fields, ruleFields(rules, member)...)
// 		}
// 		return fields
// 	case "STRING", "SYMBOL":
// 		// symbols act as field boundaries
// 		return nil
// 	default:
// 		panic("unknown rule type: " + rule.Type)
// 	}
// }

// func ruleConstructor(rules map[string]Rule, rule Rule) []jen.Code {
// 	switch rule.Type {
// 	case "BLANK":
// 		return nil
// 	case "CHOICE":
// 		return nil
// 	case "FIELD":
// 		return []jen.Code{ruleType(rules, jen.Id(rule.Name), *rule.Content)}
// 	case "IMMEDIATE_TOKEN", "PREC_LEFT", "PREC_RIGHT", "PREC":
// 		return ruleFields(rules, *rule.Content)
// 	case "PATTERN", "TOKEN":
// 		// record the matched content
// 		return []jen.Code{ruleType(rules, jen.Id("Token"), rule)}
// 	case "REPEAT":
// 		return nil // TODO?
// 	case "SEQ":
// 		fields := []jen.Code{}
// 		for _, member := range rule.Members {
// 			fields = append(fields, ruleFields(rules, member)...)
// 		}
// 		return fields
// 	case "STRING", "SYMBOL":
// 		// symbols act as field boundaries
// 		return nil
// 	default:
// 		panic("unknown rule type: " + rule.Type)
// 	}
// }
