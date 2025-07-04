package bind

import (
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/vito/bind/introspection"
	"github.com/vito/bind/pkg/hm"
)

type Env interface {
	hm.Env
	hm.Type
	NamedType(string) (Env, bool)
	AddClass(string, Env)
	LocalSchemeOf(string) (*hm.Scheme, bool)
}

// TODO: is this just ClassType? are Classes just named Envs?
type Module struct {
	Named string

	Parent Env

	classes map[string]Env
	vars    map[string]*hm.Scheme
}

//	type CompositeModule struct {
//		Reads Env
//	}
func NewModule(name string) *Module {
	env := &Module{
		Named:   name,
		classes: make(map[string]Env),
		vars:    make(map[string]*hm.Scheme),
	}
	return env
}

func gqlToTypeNode(mod Env, ref *introspection.TypeRef) (hm.Type, error) {
	switch ref.Kind {
	case introspection.TypeKindScalar:
		if strings.HasSuffix(ref.Name, "ID") {
			return gqlToTypeNode(mod, &introspection.TypeRef{
				Name: strings.TrimSuffix(ref.Name, "ID"),
				Kind: introspection.TypeKindObject,
			})
		}
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
		return hm.NonNullType{Type: inner}, nil
	default:
		return nil, fmt.Errorf("unhandled type kind: %s", ref.Kind)
	}
}

func NewEnv(schema *introspection.Schema) *Module {
	mod := NewModule("<bind>")
	mod.AddClass("String", StringType)
	mod.AddClass("Int", IntType)
	mod.AddClass("Boolean", BooleanType)

	for _, t := range schema.Types {
		sub, found := mod.NamedType(t.Name)
		if !found {
			sub = NewModule(t.Name)
			mod.AddClass(t.Name, sub)
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

			args := NewRecordType("")
			for _, arg := range f.Args {
				argType, err := gqlToTypeNode(mod, arg.TypeRef)
				if err != nil {
					// Skip args we can't convert
					continue
				}
				args.Add(arg.Name, hm.NewScheme(nil, argType))
			}
			slog.Debug("adding function binding", "type", t.Name, "function", f.Name)
			install.Add(f.Name, hm.NewScheme(nil, hm.NewFnType(args, ret)))
		}
	}

	// Add builtin functions to the type environment
	addBuiltinTypes(mod)

	return mod
}

// addBuiltinTypes adds the type signatures for builtin functions
func addBuiltinTypes(mod *Module) {
	// print function: print(value: a) -> Null
	printArgType := hm.TypeVariable('a')
	printArgs := NewRecordType("")
	printArgs.Add("value", hm.NewScheme(nil, printArgType))
	printType := hm.NewFnType(printArgs, hm.TypeVariable('n')) // returns null

	slog.Debug("adding builtin function", "function", "print")
	mod.Add("print", hm.NewScheme(nil, printType))

	// json function: json(value: b) -> String!
	jsonArgType := hm.TypeVariable('b')
	jsonArgs := NewRecordType("")
	jsonArgs.Add("value", hm.NewScheme(nil, jsonArgType))
	jsonReturnType := hm.NonNullType{Type: StringType}
	jsonType := hm.NewFnType(jsonArgs, jsonReturnType)

	slog.Debug("adding builtin function", "function", "json")
	mod.Add("json", hm.NewScheme(nil, jsonType))
}

var _ hm.Substitutable = (*Module)(nil)

func (e *Module) Apply(subs hm.Subs) hm.Substitutable {
	if len(subs) == 0 || len(e.FreeTypeVar()) == 0 {
		return e
	}
	retVal := e.Clone().(*Module)
	for _, v := range retVal.vars {
		v.Apply(subs)
	}
	return retVal
}

func (e *Module) FreeTypeVar() hm.TypeVarSet {
	var retVal hm.TypeVarSet
	// for _, v := range e.vars {
	// 	retVal = v.FreeTypeVar().Union(retVal)
	// }
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

func (e *Module) LocalSchemeOf(name string) (*hm.Scheme, bool) {
	s, ok := e.vars[name]
	return s, ok
}

func (e *Module) Clone() hm.Env {
	mod := NewModule(e.Named)
	mod.Parent = e
	return mod
}

func (e *Module) AddClass(name string, c Env) {
	e.classes[name] = c
}

func (e *Module) NamedType(name string) (Env, bool) {
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

func (e *Module) AsRecord() *RecordType {
	var rec RecordType
	for name, scheme := range e.vars {
		rec.Fields = append(rec.Fields, Keyed[*hm.Scheme]{
			Key:   name,
			Value: scheme,
		})
	}
	sort.Slice(rec.Fields, func(i, j int) bool {
		return rec.Fields[i].Key < rec.Fields[j].Key
	})
	return &rec
}

var _ hm.Type = (*Module)(nil)

func (t *Module) Name() string                               { return t.Named }
func (t *Module) Normalize(k, v hm.TypeVarSet) (Type, error) { return t, nil }
func (t *Module) Types() hm.Types                            { return nil }

func (t *Module) String() string {
	if t.Named != "" {
		return t.Named
	}
	return t.AsRecord().String()
}

//	func (t *Module) Format(s fmt.State, c rune) {
//		switch c {
//		case 'v':
//			fmt.Fprintf(s, "%+v", t.)
//		case 's':
//			fmt.Fprintf(s, "%s", t.String())
//		default:
//			fmt.Fprintf(s, "%#v", t)
//		}
//	}
func (t *Module) Eq(other Type) bool {
	otherMod, ok := other.(*Module)
	if !ok {
		return false
	}
	if t.Named != "" {
		return t == otherMod
	}
	return t.AsRecord().Eq(otherMod.AsRecord())
}
