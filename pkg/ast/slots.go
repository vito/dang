package ast

import "github.com/chewxy/hm"

type Let struct {
	Named string
	Def_  Node
	Body_ Node
}

var _ hm.Expression = Let{}

func (l Let) Body() hm.Expression { return l.Body_ }

var _ Node = Let{}

func (s Let) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	// TODO: is this right?
	if _, err := s.Def_.Infer(env, fresh); err != nil {
		return nil, err
	}
	return s.Body_.Infer(env, fresh)
}

var _ hm.Let = Let{}

func (l Let) Name() string       { return l.Named }
func (l Let) Def() hm.Expression { return l.Def_ }

type Seq struct {
	First  Node
	Second Node
}

var _ Node = Seq{}

func (s Seq) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	if _, err := s.First.Infer(env, fresh); err != nil {
		return nil, err
	}
	return s.Second.Infer(env, fresh)
}

func (s Seq) Body() hm.Expression { return s }
