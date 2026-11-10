package dang

import "strings"

// renderHandle renders a GraphQL handle as the lazy query it is: its GraphQL
// type name and the query chain it holds, in angle brackets so it can never
// read as Dang source or a printed string:
// `<Container: container.from(address: …).withEnv(name: …, value: …)>`.
//
// Argument values are never shown, only their names. A value may be a
// secret's plaintext, and nothing reliable says which are: Dagger marks
// sensitive arguments internally but doesn't expose that in introspection.
// An object argument is a value too, so a handle passed to another call
// doesn't expand into its own chain.
//
// A mutation's chain starts at `mutation.`, and a list handle's type is
// `[Type]`. Nothing is run, so rendering needs no context and can't fail.
// Display and Describe share it.
func renderHandle(g GraphQLValue) string {
	typeName := g.TypeName
	if g.Field != nil && isListTypeRef(g.Field.TypeRef) {
		// A list handle's TypeName is its elements'.
		typeName = "[" + typeName + "]"
	}
	chain := handleChain(g)
	if chain == "" {
		return "<" + typeName + ">"
	}
	return "<" + typeName + ": " + chain + ">"
}

// handleChain renders g's query chain. The handle's type is shown beside it,
// so an inline fragment narrowing the chain to that type isn't.
func handleChain(g GraphQLValue) string {
	if g.QueryChain == nil {
		return ""
	}
	var b strings.Builder
	wrote := false
	if g.IsMutation {
		b.WriteString("mutation")
		wrote = true
	}
	for _, step := range g.QueryChain.Steps() {
		if step.InlineFragment != "" {
			continue
		}
		if wrote {
			b.WriteString(".")
		}
		wrote = true
		if len(step.Fields) > 0 {
			b.WriteString("{{" + strings.Join(step.Fields, ", ") + "}}")
		} else {
			if step.Alias != "" {
				b.WriteString(step.Alias + ": ")
			}
			b.WriteString(step.Name)
		}
		if len(step.Args) > 0 {
			b.WriteString("(")
			for j, arg := range step.Args {
				if j > 0 {
					b.WriteString(", ")
				}
				b.WriteString(arg.Name + ": …")
			}
			b.WriteString(")")
		}
	}
	return b.String()
}
