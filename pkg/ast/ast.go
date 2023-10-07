package ast

type Node interface{}

type Type struct {
	// A named GraphQL type.
	Named string

	// A list of other types.
	ListOf *Type

	// A set of fields and associated types.
	//
	// TODO: GraphQL doesn't have this, just guessing it might be useful for
	// intra-language stuff, maybe like generic functions that can act on objects
	// having certain fields
	RecordOf map[string]Type

	// Whether the value can be null.
	Nullable bool
}

// Kind is the kind of a type.
type TypeKind int

const (
	KindInvalid TypeKind = iota
	KindNamed
	KindList
	KindRecord
)

// Kind returns the kind of type.
func (t Type) Kind() TypeKind {
	if t.ListOf != nil {
		return KindList
	}
	if t.RecordOf != nil {
		return KindRecord
	}
	if t.Named != "" {
		return KindNamed
	}
	return KindInvalid
}

type Keyed[X any] struct {
	Key   string
	Value X
}

type FunDecl struct {
	Name string
	Args []Keyed[Type]
	Ret  Type
	Body Block
}

type Visibility int

const (
	PublicVisibility Visibility = iota
	PrivateVisibility
)

type Call struct {
	Fun  Node
	Args []Keyed[Node]
}

type ClassDecl struct {
	Name  string
	Slots []SlotDecl
}

type SlotDecl struct {
	Name       string
	Args       []Keyed[Type]
	Type       Type
	Visibility Visibility
	Body       Block
}

type List struct {
	Elements []Node
}

type Block struct {
	Body []Node
}

type Symbol struct {
	Name string
}
