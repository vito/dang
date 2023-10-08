package ast

import (
	"github.com/chewxy/hm"
)

type Block struct {
	Named string
	Form  Node
}

// func (f Block) Name() string {
// 	return fmt.Sprintf(f.Name)
// }

var _ hm.Expression = Block{}

func (f Block) Body() hm.Expression {
	return f.Form
}

var _ hm.Inferer = Block{}

func (b Block) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	sub := NewRecordType(b.Named)
	_, err := b.Form.Infer(sub, fresh)
	if err != nil {
		return nil, err
	}
	// blocks construct records?
	return sub, nil
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
// func (b Block) Fun() (FunDecl, error) {
// 	fun := FunDecl{
// 		Args: []Keyed[Type]{},
// 	}

// 	args := NewRecordType("_args_")

// 	scheme, err := hm.Infer(args, b.Body)
// 	if err != nil {
// 		return FunDecl{}, err
// 	}

// 	args.Fields

// 	for i := len(b.Exprs) - 1; i >= 0; i-- {
// 		expr := b.Exprs[i]

// 		switch x := expr.(type) {
// 		case SlotDecl:
// 			slot := x

// 			if slot.Value != nil {
// 				if fun.Form == nil {
// 					fun.Form = Self{}
// 				}

// 				if slot.Type_ == nil {
// 					env := NewRecordType("")
// 					s, err := hm.Infer(env, slot.Value)
// 					if err != nil {
// 						return FunDecl{}, err
// 					}
// 					t, isMono := s.Type()
// 					if !isMono {
// 						return FunDecl{}, fmt.Errorf("slot %s is not monomorphic", slot.Named)
// 					}
// 					slot.Type_ = t
// 				}

// 				fun.Form = Let{slot.Named, slot.Type_, slot.Value, fun.Form}

// 				// fun.Form does a let
// 				// TODO: let rec?
// 			}
// 		default:
// 			if fun.Form == nil {
// 				fun.Form = x
// 			} else {
// 				fun.Form = Seq{x, fun.Form}
// 			}
// 		}
// 	}

// 	// TODO [not sure if goes here] typecheck function's final form against the return type

// 	return fun, nil
// }
