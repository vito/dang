package treesitter

import (
	"bytes"
	"encoding/json"

	"github.com/iancoleman/strcase"
)

type Grammar struct {
	Name       string                      `json:"name"`
	Word       RuleName                    `json:"word"`
	Rules      *OrderedMap[RuleName, Rule] `json:"rules"`
	Extras     []Rule                      `json:"extras"`
	Supertypes []string                    `json:"supertypes"`
}

func NewGrammar(name string) Grammar {
	return Grammar{
		Name:  name,
		Rules: newOrderedMap[RuleName, Rule](),
	}
}

type RuleName string

func Name(name string) RuleName {
	return RuleName(strcase.ToSnake(name))
}

type Rule struct {
	Type    RuleType `json:"type"`
	Name    RuleName `json:"name,omitempty"`
	Value   any      `json:"value,omitempty"`
	Members []Rule   `json:"members,omitempty"`
	Content *Rule    `json:"content,omitempty"`
	Flags   string   `json:"flags,omitempty"` // regex flags
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
	RuleTypeRepeatOne      RuleType = "REPEAT1"
	RuleTypeSeq            RuleType = "SEQ"
	RuleTypeString         RuleType = "STRING"
	RuleTypeSymbol         RuleType = "SYMBOL"
	RuleTypeToken          RuleType = "TOKEN"
)

type OrderedMap[K comparable, T any] struct {
	keys   []K
	values map[K]T
}

func newOrderedMap[K comparable, T any]() *OrderedMap[K, T] {
	return &OrderedMap[K, T]{
		keys:   []K{},
		values: map[K]T{},
	}
}

func (o *OrderedMap[K, T]) Add(key K, value T) {
	if _, exists := o.values[key]; !exists {
		o.keys = append(o.keys, key)
	}
	o.values[key] = value
}

func (o *OrderedMap[K, T]) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	for i, k := range o.keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		if err := encoder.Encode(k); err != nil {
			return nil, err
		}
		buf.WriteByte(':')
		if err := encoder.Encode(o.values[k]); err != nil {
			return nil, err
		}
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}
