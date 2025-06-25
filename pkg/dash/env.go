package dash

import (
	"fmt"
	"log"

	"github.com/chewxy/hm"
	"github.com/dagger/dagger/codegen/introspection"
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

func gqlToTypeNode(mod *Module, ref *introspection.TypeRef) (hm.Type, error) {
	switch ref.Kind {
	case introspection.TypeKindScalar:
		t, found := mod.NamedType(ref.Name)
		if !found {
			return nil, fmt.Errorf("gqlToTypeNode: %q not found", ref.Name)
		}
		return t, nil
	case introspection.TypeKindObject:
		t, found := mod.NamedType(ref.Name)
		if !found {
			return nil, fmt.Errorf("gqlToTypeNode: %q not found", ref.Name)
		}
		return t, nil
	// case introspection.TypeKindInterface:
	// 	return NamedTypeNode{t.Name}
	// case introspection.TypeKindUnion:
	// 	return NamedTypeNode{t.Name}
	case introspection.TypeKindEnum:
		t, found := mod.NamedType(ref.Name)
		if !found {
			return nil, fmt.Errorf("gqlToTypeNode: %q not found", ref.Name)
		}
		return t, nil
	case introspection.TypeKindInputObject:
		t, found := mod.NamedType(ref.Name)
		if !found {
			return nil, fmt.Errorf("gqlToTypeNode: %q not found", ref.Name)
		}
		return t, nil
	case introspection.TypeKindList:
		inner, err := gqlToTypeNode(mod, ref.OfType)
		if err != nil {
			return nil, fmt.Errorf("gqlToTypeNode List: %w", err)
		}
		return ListType{inner}, nil
	case introspection.TypeKindNonNull:
		inner, err := gqlToTypeNode(mod, ref.OfType)
		if err != nil {
			return nil, fmt.Errorf("gqlToTypeNode List: %w", err)
		}
		return NonNullType{inner}, nil
	default:
		return nil, fmt.Errorf("unhandled type kind: %s", ref.Kind)
	}
}

func NewEnv(schema *introspection.Schema) *Module {
	mod := NewModule("<dash>")

	for _, t := range schema.Types {
		sub, found := mod.NamedType(t.Name)
		if !found {
			sub = NewModule(t.Name)
			mod.AddClass(sub)
		}
		if t.Name == schema.QueryType.Name {
			// Set Query as the parent of the outermost module so that its fields are
			// defined globally.
			mod.Parent = sub
		}
	}

	for _, t := range schema.Types {
		install, found := mod.NamedType(t.Name)
		if !found {
			// we just set it above...
			// This should never happen, but handle gracefully
			continue
		}

		// TODO assign input fields, maybe input classes are "just" records?
		//t.InputFields

		// TODO assign enum constructors
		//t.EnumValues

		for _, f := range t.Fields {
			ret, err := gqlToTypeNode(mod, f.TypeRef)
			if err != nil {
				// Skip fields we can't convert
				continue
			}

			if len(f.Args) > 0 {
				args := NewRecordType("")
				for _, arg := range f.Args {
					argType, err := gqlToTypeNode(mod, arg.TypeRef)
					if err != nil {
						// Skip args we can't convert
						continue
					}
					args.Add(arg.Name, hm.NewScheme(nil, argType))
				}
				log.Println("ADDING FUN", t.Name, f.Name)
				install.Add(f.Name, hm.NewScheme(nil, hm.NewFnType(args, ret)))
			} else {
				log.Println("ADDING 0-ARITY FIELD", t.Name, f.Name)
				install.Add(f.Name, hm.NewScheme(nil, ret))
			}
		}
	}

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
