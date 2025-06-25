package dash

import (
	"context"
	"github.com/chewxy/hm"
)

type Record []Keyed[Node]

var _ hm.Inferer = Record{}
var _ Evaluator = Record{}

func (r Record) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	var fields []Keyed[*hm.Scheme]
	for _, f := range r {
		s, err := Infer(env, f.Value, false)
		if err != nil {
			return nil, err
		}
		fields = append(fields, Keyed[*hm.Scheme]{Key: f.Key, Value: s, Positional: f.Positional})
	}
	return NewRecordType("", fields...), nil
}

var _ hm.Expression = Record{}

func (r Record) Body() hm.Expression { return r }

func (r Record) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	fields := make(map[string]Value)
	schemeFields := make([]Keyed[*hm.Scheme], len(r))

	for i, f := range r {
		val, err := EvalNode(ctx, env, f.Value)
		if err != nil {
			return nil, err
		}
		fields[f.Key] = val
		schemeFields[i] = Keyed[*hm.Scheme]{Key: f.Key, Value: hm.NewScheme(nil, val.Type()), Positional: f.Positional}
	}

	return RecordValue{
		Fields:  fields,
		RecType: NewRecordType("", schemeFields...),
	}, nil
}
