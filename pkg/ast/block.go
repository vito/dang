package ast

import (
	"log"

	"github.com/chewxy/hm"
	"github.com/kr/pretty"
)

type Block struct {
	Forms []Node
}

// func (f Block) Name() string {
// 	return fmt.Sprintf(f.Name)
// }

var _ hm.Expression = Block{}

func (f Block) Body() hm.Expression { return f }

var _ hm.Inferer = Block{}

type Hoister interface {
	Hoist(hm.Env) hm.Env
}

func (b Block) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	forms := b.Forms
	if len(forms) == 0 {
		forms = append(forms, Null{})
	}

	for _, form := range forms {
		if hoister, ok := form.(Hoister); ok {
			pretty.Logln("HOISTING:", form)
			env = hoister.Hoist(env)
		}
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
	// if err != nil {
	// 	return nil, err
	// }
	// // blocks construct records?
	// return sub, nil
}
