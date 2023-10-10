package ast

import (
	"errors"
	"log"

	"github.com/chewxy/hm"
	"github.com/kr/pretty"
)

type Block struct {
	Forms []Node
}

var _ hm.Expression = Block{}

func (f Block) Body() hm.Expression { return f }

type Hoister interface {
	Hoist(hm.Env, hm.Fresher) error
}

var _ Hoister = Block{}

type Set[T comparable] map[T]struct{}

func (b Block) Hoist(env hm.Env, fresh hm.Fresher) error {
	return b.hoist(env, fresh)
	for {
		err := b.hoist(env, fresh)
		if err == nil {
			return nil
		}
		var unresolved UnresolvedTypeError
		if !errors.As(err, &unresolved) {
			return err
		}
		log.Println("AGAIN", err)
	}
}

func (b Block) hoist(env hm.Env, fresh hm.Fresher) error {
	var errs []error
	for _, form := range b.Forms {
		if hoister, ok := form.(Hoister); ok {
			if err := hoister.Hoist(env, fresh); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

var _ hm.Inferer = Block{}

func (b Block) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	forms := b.Forms
	if len(forms) == 0 {
		forms = append(forms, Null{})
	}

	var t hm.Type
	for _, form := range forms {
		log.Printf("CHECKING: %T", form)
		et, err := form.Infer(env, fresh)
		if err != nil {
			return nil, err
		}
		t = et
		pretty.Logln("INFERRED", et)
	}

	return t, nil
}
