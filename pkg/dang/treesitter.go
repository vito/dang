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
// InvalidEscape only exists to report an error, so tree-sitter leaves it out.
var tsSkippedRules = map[string]bool{
	"MultiTemplateOpenToken":            true,
	"MultiTemplateCloseToken":           true,
	"MultiTemplateContentChar":          true,
	"SingleTemplateContentChar":         true,
	"LangTagName":                       true,
	"LangTagTerminator":                 true,
	"CommentToken":                      true,
	"InvalidEscape":                     true,
	"ImmediateQuoteContent":             true,
	"ImmediateTripleQuoteContent":       true,
	"ImmediateSingleQuoteContent":       true,
	"ImmediateTripleSingleQuoteContent": true,
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
	// Newline ending a language tag.
	"_lang_tag_terminator",
	// Line comment. External so the scanner can refuse it inside a string or
	// template, where `#` is content (Markdown headers, `issue #5`, etc.)
	// rather than a comment start.
	"comment_token",
	// Language tag of a multi-line string (```sql, """sql, '''sql). External
	// so it is only taken right after an opening fence, and only for a lone
	// word ending its line: other text there is content.
	"lang_tag_name",
	// String content, one token per kind of delimiter, so the scanner knows
	// from the valid symbols which delimiters, escapes and interpolations end
	// the content, and refuses comments within it. Each is aliased to
	// string_content (see tsExternalNodeAliases).
	"string_content",
	"triple_quote_content",
	"single_quote_content",
	"triple_single_quote_content",
}

var tsRuleRefAliases = map[string]treesitter.RuleName{
	"_inlineSpace":                      "_inline_space",
	"MultiTemplateOpenToken":            "multi_template_open_token",
	"MultiTemplateCloseToken":           "multi_template_close_token",
	"MultiTemplateContentChar":          "_template_content_char",
	"SingleTemplateContentChar":         "_template_content_char",
	"LangTagName":                       "lang_tag_name",
	"LangTagTerminator":                 "_lang_tag_terminator",
	"CommentToken":                      "comment_token",
	"ImmediateQuoteContent":             "string_content",
	"ImmediateTripleQuoteContent":       "triple_quote_content",
	"ImmediateSingleQuoteContent":       "single_quote_content",
	"ImmediateTripleSingleQuoteContent": "triple_single_quote_content",
}

// tsExternalNodeAliases names the nodes of external tokens that share a node
// name with others: every kind of string content is a string_content node,
// so queries color the text of any string alike.
var tsExternalNodeAliases = map[treesitter.RuleName]treesitter.RuleName{
	"triple_quote_content":        "string_content",
	"single_quote_content":        "string_content",
	"triple_single_quote_content": "string_content",
}

// tsVersion is the Dang version whose syntax the tree-sitter grammar
// describes. Tree-sitter has no equivalent of the parser's version option, so
// the version-dependent rules are resolved statically for this one version.
var tsVersion = CurrentVersion

// tsPredicates maps the PEG's version predicates (rules whose body is a pigeon
// code predicate) to their value in tsVersion. A reference to a predicate
// matches nothing; a sequence the predicate rules out (`P x` where P is false,
// or `!P x` where P is true) can never match, so it is pruned from its choice.
var tsPredicates = map[string]bool{
	"SingleBraceSelection": tsVersion.singleBraceSelection(),
	"SingleBraceFragment":  tsVersion.singleBraceFragments(),
	"ForLoops":             tsVersion.forLoops(),
	"CommaFormSep":         tsVersion.commaFormSep(),
	"RescueKeyword":        tsVersion.postfixRescue(),
	"LiteralQuotes":        tsVersion.literalQuotes(),
}

// tsInlinedRules are PEG rules that only exist to switch syntax by version,
// or to give the parser a value to act on (TypePattern locates a pattern,
// QuotePart turns a piece of a string into a TemplatePart).
// Their bodies are inlined at each reference instead of becoming named nodes,
// so the tree-sitter grammar keeps the shape it has for tsVersion alone.
var tsInlinedRules = map[string]bool{
	"SelectionOpen":           true,
	"SelectionClose":          true,
	"FragmentOpen":            true,
	"FragmentClose":           true,
	"DotBlockArg":             true,
	"ForLoop":                 true,
	"ForToken":                true,
	"TypePattern":             true,
	"LegacyTripleQuoteString": true,
	"CookedTripleQuoteString": true,
	"LegacyString":            true,
	"CookedString":            true,
	"ImmediateStringContent":  true,
	"TripleQuotePart":         true,
	"QuotePart":               true,
}

func tsGrammarRule(name string) *rule {
	for _, r := range g.rules {
		if r.name == name {
			return r
		}
	}
	panic(fmt.Sprintf("unknown grammar rule: %s", name))
}

// tsVersionOnly reports whether a rule is resolved away by tsPredicates or
// tsInlinedRules rather than emitted into the tree-sitter grammar.
func tsVersionOnly(name string) bool {
	_, isPredicate := tsPredicates[name]
	return isPredicate || tsInlinedRules[name]
}

// tsNeverMatches reports whether expr cannot match in tsVersion, given the
// static values of tsPredicates.
func tsNeverMatches(expr any) bool {
	switch t := expr.(type) {
	case *ruleRefExpr:
		if v, ok := tsPredicates[t.name]; ok {
			return !v
		}
		if tsInlinedRules[t.name] {
			return tsNeverMatches(tsGrammarRule(t.name).expr)
		}
	case *notExpr:
		if ref, ok := t.expr.(*ruleRefExpr); ok {
			if v, ok := tsPredicates[ref.name]; ok {
				return v
			}
		}
	case *seqExpr:
		for _, e := range t.exprs {
			if tsNeverMatches(e) {
				return true
			}
		}
	case *choiceExpr:
		for _, alt := range t.alternatives {
			if !tsNeverMatches(alt) {
				return false
			}
		}
		return true
	case *actionExpr:
		return tsNeverMatches(t.expr)
	case *labeledExpr:
		return tsNeverMatches(t.expr)
	}
	return false
}

// tsInlineRule translates a version-only rule's body for use in place of a
// reference to it. A label at the top of the body only names the value for
// the rule's action, so it is dropped rather than becoming a field.
func tsInlineRule(name string, prec int) *treesitter.Rule {
	ts := treesitterRule(&rule{expr: tsGrammarRule(name).expr}, prec)
	if ts != nil && ts.Type == treesitter.RuleTypeField {
		ts = ts.Content
	}
	return ts
}

var tsRulePatches = map[treesitter.RuleName]func(treesitter.Rule) treesitter.Rule{
	treesitter.Name("Sep"):                  patchSepAutomaticNewline,
	treesitter.Name("FormSep"):              patchSepAutomaticNewline,
	treesitter.Name("ArgType"):              patchArgTypeDocstringSep,
	treesitter.Name("Conditional"):          patchPrecRight,
	treesitter.Name("DirectiveApplication"): patchDirectiveScopeAsId,
	treesitter.Name("TypeAndBlockField"):        patchTypeParamsName,
	treesitter.Name("TypeAndArgsAndBlockField"): patchTypeParamsName,
	treesitter.Name("DocString"):                patchDocStringShape,
	treesitter.Name("SingleQuoteString"):        patchRepeatContent,
}

// patchRepeatContent lets a `'...'` string's optional content repeat. The
// scanner takes all of it in one token anyway, but the repetition keeps the
// content token valid after it, so at a newline the scanner can tell it's in
// an unterminated string (see the scanner's `unterminated`).
func patchRepeatContent(rule treesitter.Rule) treesitter.Rule {
	for i, member := range rule.Members {
		if member.Type != treesitter.RuleTypeField || member.Name != "content" ||
			member.Content == nil || member.Content.Type != treesitter.RuleTypeChoice {
			continue
		}
		content := member.Content.Members[0]
		rule.Members[i].Content = &treesitter.Rule{
			Type:    treesitter.RuleTypeRepeat,
			Content: &content,
		}
	}
	return rule
}

// patchDocStringShape gives a docstring the shape of a triple-quoted string.
// The two share an opening `"""`, and a `"""` after `(` or at the start of a
// statement may be either (see the conflict in TreesitterGrammar): GLR can
// only explore both while they are made of the same tokens, so tree-sitter
// lexes a docstring's verbatim text like a string's (escapes, interpolations
// and all). The PEG keeps docstrings verbatim. The scanner takes a backslash
// starting no escape as text, so most docstrings still parse, but one whose
// `${` starts no expression (`${HOME:-/root}`), or that ends in a backslash,
// is an error to tree-sitter alone.
func patchDocStringShape(treesitter.Rule) treesitter.Rule {
	return *tsInlineRule("CookedTripleQuoteString", 0)
}

// patchDirectiveScopeAsId rewrites a directive's optional qualifier so it lexes
// as a plain identifier (word_token) rather than a NamedType (upper_token). In
// `@JSON.field` the qualifier `JSON` is all-caps, so it matches both word_token
// and upper_token; tree-sitter's lexer prefers word_token — the directive
// name's own token — which leaves the upper_token NamedType qualifier
// unreachable and `.field` an error. Lexing the qualifier as `id` removes the
// ambiguity; the resulting `(id ".")? id` is LR(1), with the `.` lookahead
// deciding qualifier vs. name. (The PEG keeps NamedType; this divergence is
// tree-sitter-only, like the other patches here.)
func patchDirectiveScopeAsId(rule treesitter.Rule) treesitter.Rule {
	return replaceTSSymbol(rule, "named_type", "id")
}

// replaceTSSymbol returns rule with every SYMBOL reference named from rewritten
// to to, recursing through Content and Members.
func replaceTSSymbol(rule treesitter.Rule, from, to treesitter.RuleName) treesitter.Rule {
	if rule.Type == treesitter.RuleTypeSymbol && rule.Name == from {
		rule.Name = to
		return rule
	}
	if rule.Content != nil {
		patched := replaceTSSymbol(*rule.Content, from, to)
		rule.Content = &patched
	}
	for i := range rule.Members {
		rule.Members[i] = replaceTSSymbol(rule.Members[i], from, to)
	}
	return rule
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

// patchTypeParamsName splits a function field with an optional type
// parameter list (`name[a](...)`) into the field without one and the field
// with one, whose name is a bare `id` (aliased as a `symbol`) rather than a
// `symbol`. A statement starting `name[` may also index a value (`xs[0]`);
// with both reading the name as a `symbol`, the precedence of `symbol` over
// `auto_call_symbol` settled the name as a field's before the `[`, so no
// expression could start by indexing a name. Reading the generic name as an
// `id` leaves the choice until the list or the index shows which it is.
func patchTypeParamsName(rule treesitter.Rule) treesitter.Rule {
	if rule.Type != treesitter.RuleTypeSeq {
		return rule
	}
	plain := treesitter.Rule{Type: treesitter.RuleTypeSeq}
	generic := treesitter.Rule{Type: treesitter.RuleTypeSeq}
	found := false
	for _, member := range rule.Members {
		if member.Type == treesitter.RuleTypeField && member.Name == "params" && member.Content != nil && member.Content.Type == treesitter.RuleTypeChoice {
			found = true
			list := member.Content.Members[0]
			generic.Members = append(generic.Members, treesitter.Rule{
				Type:    treesitter.RuleTypeField,
				Name:    member.Name,
				Content: &list,
			})
			continue
		}
		plain.Members = append(plain.Members, member)
		if member.Type == treesitter.RuleTypeField && member.Name == "name" {
			// Still a `symbol` node, for queries (outlines) that look for
			// a field's name as one.
			id := treesitter.Rule{Type: treesitter.RuleTypeSymbol, Name: "id"}
			member.Content = &treesitter.Rule{
				Type:    treesitter.RuleTypeAlias,
				Value:   "symbol",
				Named:   true,
				Content: &id,
			}
		}
		generic.Members = append(generic.Members, member)
	}
	if !found {
		return rule
	}
	return treesitter.Rule{
		Type:    treesitter.RuleTypeChoice,
		Members: []treesitter.Rule{plain, generic},
	}
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
		// A `{` after `rescue` is ambiguous between a clause block and a
		// block-expression fallback until a clause arrow (or its absence)
		// appears. GLR explores both; the PEG resolves the same ambiguity
		// by ordered choice (clauses first).
		{treesitter.Name("RescueClauses"), treesitter.Name("Block")},
		// A statement starting `name[` is ambiguous between a function
		// field with type parameters (`first[a](xs: [a!]!): a { ... }`)
		// and indexing a value (`xs[i]`); see patchTypeParamsName. GLR
		// explores both; what follows the `]` disambiguates.
		{treesitter.Name("AutoCallSymbol"), treesitter.Name("TypeAndBlockField"), treesitter.Name("TypeAndArgsAndBlockField")},
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

	// Precedence descends with rule order. Version-only rules don't count, so
	// they leave the precedences of the emitted rules unchanged.
	var emitted []*rule
	for _, rule := range g.rules {
		if tsVersionOnly(rule.name) {
			slog.Info("resolving version-only grammar rule", "rule", rule.name, "version", tsVersion.String())
			continue
		}
		emitted = append(emitted, rule)
	}

	for i, rule := range emitted {
		prec := len(emitted) - i
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
			if tsNeverMatches(expr) {
				// ruled out by a version predicate
				continue
			}
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
			if as, ok := tsExternalNodeAliases[alias]; ok {
				return &treesitter.Rule{
					Type:    treesitter.RuleTypeAlias,
					Value:   as,
					Named:   true,
					Content: &rule,
				}
			}
			return &rule
		}
		if _, ok := tsPredicates[t.name]; ok {
			// Matches nothing; the alternatives it rules out were pruned by
			// the enclosing choice (see tsNeverMatches).
			return nil
		}
		if tsInlinedRules[t.name] {
			return tsInlineRule(t.name, prec)
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
