package dangdocs

import (
	"strings"

	"github.com/alecthomas/chroma/v2"
)

// builtinTypes are highlighted as keywords so the docs keep distinguishing
// core scalars from user-defined types.
var builtinTypes = map[string]bool{
	"Int":     true,
	"Float":   true,
	"String":  true,
	"Boolean": true,
	"ID":      true,
	"Void":    true,
}

func punctTok(v string) chroma.Token { return chroma.Token{Type: chroma.Punctuation, Value: v} }
func textTok(v string) chroma.Token  { return chroma.Token{Type: chroma.Text, Value: v} }

// typeNameToken classifies a bare type name: core scalars as keywords,
// other capitalized names as classes, and lowercase type variables plain.
func typeNameToken(word string) chroma.Token {
	tt := chroma.Name
	if word[0] >= 'A' && word[0] <= 'Z' {
		if builtinTypes[word] {
			tt = chroma.KeywordType
		} else {
			tt = chroma.NameClass
		}
	}
	return chroma.Token{Type: tt, Value: word}
}

// typeTokens tokenizes a rendered type like `[String!]!` or `Map[a]`:
// identifier runs become type names, `!` stays an operator like the editors'
// non-null assert, and everything else is punctuation.
func typeTokens(s string) []chroma.Token {
	var toks []chroma.Token
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z'):
			j := i + 1
			for j < len(s) && (s[j] == '_' || (s[j] >= 'a' && s[j] <= 'z') || (s[j] >= 'A' && s[j] <= 'Z') || (s[j] >= '0' && s[j] <= '9')) {
				j++
			}
			toks = append(toks, typeNameToken(s[i:j]))
			i = j
		case c == '!':
			toks = append(toks, chroma.Token{Type: chroma.Operator, Value: "!"})
			i++
		case c == ' ':
			toks = append(toks, textTok(" "))
			i++
		default:
			toks = append(toks, punctTok(string(c)))
			i++
		}
	}
	return toks
}

// defaultTokens classifies a rendered default value literal.
func defaultTokens(s string) []chroma.Token {
	var tt chroma.TokenType
	switch {
	case strings.HasPrefix(s, `"`):
		tt = chroma.LiteralString
	case s == "null" || s == "true" || s == "false":
		tt = chroma.KeywordConstant
	case len(s) > 0 && (s[0] == '-' || (s[0] >= '0' && s[0] <= '9')):
		tt = chroma.LiteralNumber
	default:
		tt = chroma.Text
	}
	return []chroma.Token{{Type: tt, Value: s}}
}

func tokensString(toks []chroma.Token) string {
	var b strings.Builder
	for _, t := range toks {
		b.WriteString(t.Value)
	}
	return b.String()
}
