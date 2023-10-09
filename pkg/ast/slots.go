package ast

import (
	"fmt"

	"github.com/chewxy/hm"
	"github.com/kr/pretty"
)

type Let struct {
	Named string
	Type_ Type
	Def_  Node
	Body_ Node
}

var _ hm.Expression = Let{}

func (l Let) Body() hm.Expression { return l.Body_ }

var _ Node = Let{}

func (s Let) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	pretty.Logln("LET.INFER", s.Named, s.Type_)

	dt, err := s.Def_.Infer(env, fresh)
	if err != nil {
		return nil, err
	}

	if s.Type_ != nil {
		if !s.Type_.Eq(dt) {
			return nil, fmt.Errorf("Let.Infer: %q mismatch: defined as %s, expected %s", s.Named, dt, s.Type_)
		} else {
			pretty.Logf("Let.Infer: %q matches: defined as %s, expected %v (%T)", s.Named, dt, s.Type_, s.Type_)
		}
	}

	cur, defined := env.SchemeOf(s.Named)
	if defined {
		curT, curMono := cur.Type()
		if !curMono {
			return nil, fmt.Errorf("Let.Infer: TODO: type is not monomorphic")
		}

		if !dt.Eq(curT) {
			return nil, fmt.Errorf("Let.Infer: %q already defined as %s", s.Named, curT)
		}
	} else {
		env = env.Add(s.Named, hm.NewScheme(nil, dt))
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
