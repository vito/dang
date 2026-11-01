package dang

import (
	"fmt"
	"sort"
	"strings"

	"github.com/vito/dang/v2/pkg/hm"
)

// Redefinitions in interactive sessions.
//
// A REPL, literate docs or the playground infer and evaluate one entry at a
// time against the same top-level frame, and an entry may redefine a name an
// earlier one declared. The checker types later code against the new
// definition, so evaluation must bind it too (FieldDecl.rebind), and a
// redefined type starts afresh rather than accumulating the old one's members.
//
// Functions and types declared earlier look names up when they run, so they
// call the new definition as well, but they were checked against the old
// one. A redefinition is therefore rejected when earlier functions or types
// use the name and the new definition doesn't fit where the old one did (for
// a type: at all, since its identity changes), unless the same entry
// redefines those users too.

type sessionState struct {
	// users maps a top-level name to the functions and types declared by
	// earlier entries whose code refers to it.
	users map[string]map[string]bool
}

// sessionEntry is one entry's redefinitions, so they can be checked once it
// is inferred, or rolled back.
type sessionEntry struct {
	frame    *Type
	state    *sessionState
	declared map[string]bool
	// rebinds holds the redefined declarations, with their old schemes.
	rebinds map[*FieldDecl]*hm.Scheme
	// types holds the redefined types, with the old ones.
	types map[string]TypeScope
}

// beginSessionEntry prepares forms, an entry of the session whose top-level
// frame env is, for inference: it marks redefinitions and lets redefined
// types start afresh, or reports a type redefinition earlier code depends on.
func beginSessionEntry(forms []Node, env hm.Env) (*sessionEntry, error) {
	frame, ok := envFrame(env).(*Type)
	if !ok {
		return nil, nil
	}
	if frame.session == nil {
		frame.session = &sessionState{users: map[string]map[string]bool{}}
	}
	e := &sessionEntry{
		frame:    frame,
		state:    frame.session,
		declared: map[string]bool{},
		rebinds:  map[*FieldDecl]*hm.Scheme{},
		types:    map[string]TypeScope{},
	}
	for _, form := range forms {
		if name := sessionDeclName(form); name != "" {
			e.declared[name] = true
		}
	}
	for _, form := range forms {
		switch f := form.(type) {
		case *FieldDecl:
			if scheme, defined := frame.LocalSchemeOf(f.Name.Name); defined {
				f.rebind = true
				e.rebinds[f] = scheme
			}
		case *ObjectDecl, *ScalarDecl, *InterfaceDecl, *UnionDecl, *EnumDecl:
			name := sessionDeclName(form)
			old, defined := frame.LocalNamedType(name)
			if !defined {
				continue
			}
			if users := e.outsideUsers(name); len(users) > 0 {
				return nil, NewInferError(fmt.Errorf(
					"redefining type `%s` would leave %s using the old one; redefine %s in the same entry",
					name, describeUsers(users), describeUsers(users)), form)
			}
			e.types[name] = old
			delete(frame.objects, name)
		}
	}
	return e, nil
}

// finish checks the entry's redefined declarations against the code that
// uses them, once the entry is inferred, and records what its declarations
// use.
func (e *sessionEntry) finish() error {
	if e == nil {
		return nil
	}
	var fields []*FieldDecl
	for f := range e.rebinds {
		fields = append(fields, f)
	}
	sort.Slice(fields, func(i, j int) bool { return fields[i].Name.Name < fields[j].Name.Name })
	for _, f := range fields {
		name := f.Name.Name
		users := e.outsideUsers(name)
		if len(users) == 0 {
			continue
		}
		oldT, _ := e.rebinds[f].Type()
		newScheme, _ := e.frame.LocalSchemeOf(name)
		newT, _ := newScheme.Type()
		if _, err := hm.Assignable(newT, oldT); err != nil {
			return NewInferError(fmt.Errorf(
				"redefining `%s` changes its type from %s to %s, but %s, declared earlier, still uses it; keep the type, or redefine %s in the same entry",
				name, oldT, newT, describeUsers(users), describeUsers(users)), f)
		}
	}
	return nil
}

// record notes what the entry's functions and types use, once it is in.
func (e *sessionEntry) record(forms []Node) {
	if e == nil {
		return
	}
	for name := range e.declared {
		for _, users := range e.state.users {
			delete(users, name)
		}
	}
	for _, form := range forms {
		user := sessionDeclName(form)
		if user == "" || !sessionDeclRunsLater(form) {
			continue
		}
		form.Walk(func(n Node) bool {
			sym, ok := n.(*Symbol)
			if !ok || sym.Name == user {
				return true
			}
			if _, defined := e.frame.LocalSchemeOf(sym.Name); !defined {
				return true
			}
			if e.state.users[sym.Name] == nil {
				e.state.users[sym.Name] = map[string]bool{}
			}
			e.state.users[sym.Name][user] = true
			return true
		})
	}
}

// rollback restores what the entry redefined, after it failed to check, so
// the checker keeps agreeing with the values evaluation still holds.
func (e *sessionEntry) rollback() {
	if e == nil {
		return
	}
	for f, scheme := range e.rebinds {
		e.frame.Add(f.Name.Name, scheme)
	}
	for name, old := range e.types {
		e.frame.AddObject(name, old)
	}
}

// outsideUsers lists the earlier declarations using name that this entry
// doesn't redefine.
func (e *sessionEntry) outsideUsers(name string) []string {
	var out []string
	for user := range e.state.users[name] {
		if !e.declared[user] {
			out = append(out, user)
		}
	}
	sort.Strings(out)
	return out
}

func describeUsers(users []string) string {
	quoted := make([]string, len(users))
	for i, u := range users {
		quoted[i] = "`" + u + "`"
	}
	return strings.Join(quoted, ", ")
}

// sessionDeclName names what form declares, or "".
func sessionDeclName(form Node) string {
	switch f := form.(type) {
	case *FieldDecl:
		return f.Name.Name
	case *ObjectDecl:
		return f.Name.Name
	case *ScalarDecl:
		return f.Name.Name
	case *InterfaceDecl:
		return f.Name.Name
	case *UnionDecl:
		return f.Name.Name
	case *EnumDecl:
		return f.Name.Name
	}
	return ""
}

// sessionDeclRunsLater reports whether form declares code that runs after
// its entry, when it may meet later redefinitions: a function or a type.
func sessionDeclRunsLater(form Node) bool {
	switch f := form.(type) {
	case *FieldDecl:
		return f.funDecl() != nil
	case *ObjectDecl, *ScalarDecl:
		return true
	}
	return false
}
