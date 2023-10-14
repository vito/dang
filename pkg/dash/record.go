package dash

import "github.com/chewxy/hm"

type Record []Keyed[Node]

var _ hm.Inferer = Record{}

func (r Record) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	var fields []Keyed[*hm.Scheme]
	for _, f := range r {
		s, err := Infer(env, f.Value, false)
		if err != nil {
			return nil, err
		}
		fields = append(fields, Keyed[*hm.Scheme]{f.Key, s})
	}
	return NewRecordType("", fields...), nil
}

var _ hm.Expression = Record{}

func (r Record) Body() hm.Expression { return r }
