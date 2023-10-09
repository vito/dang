package ast

import (
	"fmt"

	"github.com/chewxy/hm"
)

// TODO: is this just ClassType? are Classes just named Envs?
type Module struct {
	Named string

	Parent *Module

	classes map[string]*Module
	vars    map[string]*hm.Scheme
}

func NewModule(name string) *Module {
	env := &Module{
		Named:   name,
		classes: make(map[string]*Module),
		vars:    make(map[string]*hm.Scheme),
	}
	return env
}

func NewEnv() *Module {
	mod := NewModule("<env>")
	mod.AddClass(BooleanType)
	mod.AddClass(IntegerType)
	mod.AddClass(StringType)
	return mod
}

var _ hm.Substitutable = (*Module)(nil)

func (e *Module) Apply(subs hm.Subs) hm.Substitutable {
	retVal := e.Clone().(*Module)
	for _, v := range retVal.vars {
		v.Apply(subs)
	}
	return retVal
}

func (e *Module) FreeTypeVar() hm.TypeVarSet {
	var retVal hm.TypeVarSet
	for _, v := range e.vars {
		retVal = v.FreeTypeVar().Union(retVal)
	}
	return retVal
}

func (e *Module) Add(name string, s *hm.Scheme) hm.Env {
	e.vars[name] = s
	return e
}

func (e *Module) SchemeOf(name string) (*hm.Scheme, bool) {
	s, ok := e.vars[name]
	if ok {
		return s, ok
	}
	if e.Parent != nil {
		return e.Parent.SchemeOf(name)
	}
	return nil, false
}

func (e *Module) Clone() hm.Env {
	mod := NewModule(e.Named)
	mod.Parent = e
	return mod
}

func (e *Module) AddClass(c *Module) *Module {
	e.classes[c.Named] = c
	return e
}

func (e *Module) NamedType(name string) (*Module, bool) {
	t, ok := e.classes[name]
	if ok {
		return t, ok
	}
	if e.Parent != nil {
		return e.Parent.NamedType(name)
	}
	return nil, false
}

func (e *Module) Remove(name string) hm.Env {
	// TODO: lol, tombstone???? idk if i ever use this method. maybe i don't need
	// to conform to hm.Env?
	delete(e.vars, name)
	return e
}

var _ hm.Type = (*Module)(nil)

func (t *Module) Name() string                               { return t.Named }
func (t *Module) Normalize(k, v hm.TypeVarSet) (Type, error) { return t, nil }
func (t *Module) Types() hm.Types                            { return nil }
func (t *Module) String() string                             { return t.Named }
func (t *Module) Format(s fmt.State, c rune)                 { fmt.Fprintf(s, "%s", t.Named) }
func (t *Module) Eq(other Type) bool                         { return other == t }
