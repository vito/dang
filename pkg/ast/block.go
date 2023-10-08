package ast

type Block struct {
	Exprs []Node
}

// BlockToFun converts a series of slot declarations and forms into a lambda that
// takes the required slots and runs the slot assignments and forms in a new
// scope.
//
// Dash files and blocks are parsed as a series of slot declarations or forms.
//
// Each file or block demarcates a new scope. A scope with NonNull slots must
// be initialized with values for those slots.
//
// A slot declaration is a special form that declares a slot in the current
// scope, effectively appending to the scope "thus far" during both
// typechecking and evaluation. Its return value is a singleton record
// containing only the slot.
//
// A form is simply evaluated in the current scope. Its return value is the
// result of the evaluation.
//
// If no forms are present, the return value is null.
func (b Block) Fun() (FunDecl, error) {
	fun := FunDecl{
		Args: []Keyed[Type]{},
	}

	if len(b.Exprs) == 0 {
		b.Exprs = []Node{Null{}}
	}

	for _, expr := range b.Exprs {
		switch x := expr.(type) {
		case SlotDecl:
			switch t := x.Ret.(type) {
			case NonNullType:
				if x.Value == nil {
					fun.Args = append(fun.Args, Keyed[Type]{Key: x.Named, Value: t})
				}
			}
		}
	}

	for i := len(b.Exprs) - 1; i > 0; i-- {
		expr := b.Exprs[i]

		switch x := expr.(type) {
		case SlotDecl:
			slot := x

			if slot.Value != nil {
				if fun.Form == nil {
					fun.Form = Null{} // TODO: return self somehow?
				}

				fun.Form = Let{slot.Named, slot.Value, fun.Form}

				// fun.Form does a let
				// TODO: let rec?
			}
		default:
			if fun.Form == nil {
				fun.Form = x
			} else {
				fun.Form = Seq{x, fun.Form}
			}
		}
	}

	// TODO [not sure if goes here] typecheck function's final form against the return type

	return fun, nil
}
