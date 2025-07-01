package dash

import (
	"context"
	"errors"

	"github.com/chewxy/hm"
)

type Block struct {
	Forms []Node
	Loc   *SourceLocation
}

var _ hm.Expression = Block{}
var _ Evaluator = Block{}

func (f Block) Body() hm.Expression { return f }

func (f Block) GetSourceLocation() *SourceLocation { return f.Loc }

type Hoister interface {
	Hoist(hm.Env, hm.Fresher, int) error
}

var _ Hoister = Block{}

func (b Block) Hoist(env hm.Env, fresh hm.Fresher, depth int) error {
	var errs []error
	for _, form := range b.Forms {
		if hoister, ok := form.(Hoister); ok {
			if err := hoister.Hoist(env, fresh, depth); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

var _ hm.Inferer = Block{}

func (b Block) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	newEnv := env.Clone()

	forms := b.Forms
	if len(forms) == 0 {
		forms = append(forms, Null{})
	}

	var t hm.Type
	for _, form := range forms {
		et, err := form.Infer(newEnv, fresh)
		if err != nil {
			return nil, err
		}
		t = et
	}

	return t, nil
}

func (b Block) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	forms := b.Forms
	if len(forms) == 0 {
		return NullValue{}, nil
	}

	newEnv := env.Clone()

	var result Value
	for _, form := range forms {
		val, err := EvalNode(ctx, newEnv, form)
		if err != nil {
			return nil, err
		}
		result = val
	}

	return result, nil
}

type Object struct {
	Slots []SlotDecl
	Loc   *SourceLocation

	// Filled in during inference phase
	// This is a little weird but has come up twice, maybe OK pattern?
	// Requires mutating node in-place.
	Mod *Module
}

var _ Node = &Object{}

func (f *Object) Body() hm.Expression { return f }

func (f *Object) GetSourceLocation() *SourceLocation { return f.Loc }

var _ hm.Inferer = &Object{}

func (o *Object) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	mod := NewModule("<lit>")
	inferEnv := &CompositeModule{
		primary: mod,
		lexical: env.(Env),
	}
	for _, slot := range o.Slots {
		_, err := slot.Infer(inferEnv, fresh)
		if err != nil {
			return nil, err
		}
	}
	o.Mod = mod
	return NonNullType{mod}, nil
}

var _ Evaluator = &Object{}

func (o *Object) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	if o.Mod == nil {
		return nil, errors.New("object has no module inferred")
	}
	newMod := NewModuleValue(o.Mod)
	evalEnv := &CompositeEnv{
		primary: newMod,
		lexical: env,
	}
	for _, slot := range o.Slots {
		_, err := EvalNode(ctx, evalEnv, slot)
		if err != nil {
			return nil, err
		}
	}
	return newMod, nil
}
