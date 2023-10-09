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
	// panic("INFMERMARY")
	sub := NewRecordType(b.Named)
	return b.Form.Infer(sub, fresh)
	// if err != nil {
	// 	return nil, err
	// }
	// // blocks construct records?
	// return sub, nil
}
