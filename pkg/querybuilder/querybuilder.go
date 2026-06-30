package querybuilder

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"runtime/debug"
	"strings"
	"sync"

	"github.com/Khan/genqlient/graphql"
	"golang.org/x/sync/errgroup"
)

// decodeJSON unmarshals JSON into v with UseNumber, so numbers landing in an
// interface{} are preserved as json.Number rather than collapsed to float64.
// This keeps integers exact through the decode and lets the value layer decide
// Int vs Float from the declared GraphQL type instead of the value's shape.
func decodeJSON(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	return dec.Decode(v)
}

// QueryBuilder represents a GraphQL query builder using a chain-based approach
type QueryBuilder struct {
	name     string
	alias    string
	args     map[string]*argument
	bind     any
	multiple bool

	// Support for multi-field selections. An ordered list so GraphQL aliases
	// and repeated field names (e.g. avatarUrl aliased at two sizes) are both
	// representable, and so field order is deterministic.
	selections []selectionField

	// inlineFragment is the type name for an inline fragment (... on TypeName).
	// When set, this step emits "... on TypeName" instead of a field name.
	// It does not add a nesting level in the response — unpack skips it.
	inlineFragment string

	prev *QueryBuilder

	client     graphql.Client
	isMutation bool
}

// Query creates a new QueryBuilder
func Query() *QueryBuilder {
	return &QueryBuilder{}
}

// Keep the old name for backward compatibility
func QueryV2() *QueryBuilder {
	return Query()
}

// Mutation creates a new QueryBuilder for a mutation operation.
func Mutation() *QueryBuilder {
	return &QueryBuilder{isMutation: true}
}

// Type alias for backward compatibility
type Selection = QueryBuilder

func (q *QueryBuilder) path() []*QueryBuilder {
	selections := []*QueryBuilder{}
	for sel := q; sel.prev != nil; sel = sel.prev {
		selections = append([]*QueryBuilder{sel}, selections...)
	}
	return selections
}

func (q *QueryBuilder) Root() *QueryBuilder {
	return &QueryBuilder{
		client:     q.client,
		isMutation: q.isMutation,
	}
}

func (q *QueryBuilder) SelectWithAlias(alias, name string) *QueryBuilder {
	sel := &QueryBuilder{
		name:       name,
		prev:       q,
		alias:      alias,
		client:     q.client,
		isMutation: q.isMutation,
	}
	return sel
}

func (q *QueryBuilder) Select(name string) *QueryBuilder {
	return q.SelectWithAlias("", name)
}

// InlineFragment adds an inline fragment type condition (... on TypeName).
// Subsequent selections will be nested inside the fragment. The response
// data is flat — the fragment doesn't add a nesting level during unpack.
func (q *QueryBuilder) InlineFragment(typeName string) *QueryBuilder {
	return &QueryBuilder{
		inlineFragment: typeName,
		prev:           q,
		client:         q.client,
		isMutation:     q.isMutation,
	}
}

func (q *QueryBuilder) SelectMultiple(name ...string) *QueryBuilder {
	sel := q.SelectWithAlias("", strings.Join(name, " "))
	sel.multiple = true
	return sel
}

// selectionField is one entry in a multi-field selection set: an optional
// GraphQL alias (the response/output key), the field name, and an optional
// sub-selection. The sub-selection also carries the field's arguments; it is
// nil for a plain scalar field with no arguments.
type selectionField struct {
	alias string
	name  string
	sub   *QueryBuilder
}

// SelectionField describes one field of a multi-field selection for
// SelectAliased: an optional alias (output key), the field name, and an
// optional sub-selection carrying nested fields and/or arguments.
type SelectionField struct {
	Alias string
	Name  string
	Sub   *QueryBuilder
}

// SelectFields selects multiple plain scalar fields at the current level.
func (q *QueryBuilder) SelectFields(fields ...string) *QueryBuilder {
	sels := make([]selectionField, len(fields))
	for i, f := range fields {
		sels[i] = selectionField{name: f}
	}
	return &QueryBuilder{
		prev:       q,
		client:     q.client,
		isMutation: q.isMutation,
		selections: sels,
	}
}

// SelectNested selects a field with nested sub-selections
func (q *QueryBuilder) SelectNested(field string, subSelection *QueryBuilder) *QueryBuilder {
	return &QueryBuilder{
		prev:       q,
		client:     q.client,
		isMutation: q.isMutation,
		selections: []selectionField{{name: field, sub: subSelection}},
	}
}

// SelectMixed allows mixing simple fields and nested selections at the same level
func (q *QueryBuilder) SelectMixed(simpleFields []string, nestedSelections map[string]*QueryBuilder) *QueryBuilder {
	sels := make([]selectionField, 0, len(simpleFields)+len(nestedSelections))
	for _, f := range simpleFields {
		sels = append(sels, selectionField{name: f})
	}
	for name, sub := range nestedSelections {
		sels = append(sels, selectionField{name: name, sub: sub})
	}
	return &QueryBuilder{
		prev:       q,
		client:     q.client,
		isMutation: q.isMutation,
		selections: sels,
	}
}

// SelectAliased selects an ordered set of fields, each with an optional GraphQL
// alias and an optional sub-selection. Unlike SelectMixed it preserves order
// and allows the same field name to appear more than once under distinct
// aliases (e.g. small: avatarUrl(size: 100), large: avatarUrl(size: 200)).
func (q *QueryBuilder) SelectAliased(fields []SelectionField) *QueryBuilder {
	sels := make([]selectionField, len(fields))
	for i, f := range fields {
		sels[i] = selectionField{alias: f.Alias, name: f.Name, sub: f.Sub}
	}
	return &QueryBuilder{
		prev:       q,
		client:     q.client,
		isMutation: q.isMutation,
		selections: sels,
	}
}

func (q *QueryBuilder) Arg(name string, value any) *QueryBuilder {
	sel := *q
	if sel.args == nil {
		sel.args = map[string]*argument{}
	}

	sel.args[name] = &argument{
		value: value,
	}
	return &sel
}

func (q *QueryBuilder) Bind(v any) *QueryBuilder {
	sel := *q
	sel.bind = v
	return &sel
}

func (q *QueryBuilder) Client(c graphql.Client) *QueryBuilder {
	sel := *q
	sel.client = c
	return &sel
}

func (q *QueryBuilder) marshalArguments(ctx context.Context) error {
	eg, gctx := errgroup.WithContext(ctx)
	q.scheduleArgMarshalling(eg, gctx)
	return eg.Wait()
}

// scheduleArgMarshalling queues marshalling for every argument reachable from
// this selection. Arguments on nested fields live in their sub-selections,
// which are not part of the linear path(), so they are visited recursively.
func (q *QueryBuilder) scheduleArgMarshalling(eg *errgroup.Group, ctx context.Context) {
	for _, sel := range q.path() {
		for _, arg := range sel.args {
			eg.Go(func() error {
				return arg.marshal(ctx)
			})
		}
		for _, fsel := range sel.selections {
			if fsel.sub != nil {
				fsel.sub.scheduleArgMarshalling(eg, ctx)
			}
		}
	}
}

// writeArgs renders a GraphQL argument list, e.g. (first:3, after:"x").
func writeArgs(b *strings.Builder, args map[string]*argument) {
	b.WriteRune('(')
	i := 0
	for name, arg := range args {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(name)
		b.WriteRune(':')
		b.WriteString(arg.marshalled)
		i++
	}
	b.WriteRune(')')
}

// rendersSelectionSet reports whether Build emits a non-empty selection set
// ("{...}") for this selection. A bare argument carrier — a field that takes
// arguments but selects no sub-fields — emits nothing.
func (q *QueryBuilder) rendersSelectionSet() bool {
	return len(q.selections) > 0 ||
		q.inlineFragment != "" || q.multiple || q.name != ""
}

func (q *QueryBuilder) Build(ctx context.Context) (string, error) {
	if err := q.marshalArguments(ctx); err != nil {
		return "", err
	}

	var b strings.Builder

	path := q.path()

	for _, sel := range path {
		if sel.prev != nil && sel.prev.multiple {
			return "", fmt.Errorf("sibling selections not end of chain")
		}

		b.WriteRune('{')

		// Handle multi-field selections (SelectFields, SelectMixed, SelectAliased)
		if len(sel.selections) > 0 {
			for i, fsel := range sel.selections {
				if i > 0 {
					b.WriteRune(' ')
				}
				// Emit the GraphQL alias (output key) when one is set, e.g.
				// stars:stargazerCount.
				if fsel.alias != "" {
					b.WriteString(fsel.alias)
					b.WriteRune(':')
				}
				b.WriteString(fsel.name)
				if subSel := fsel.sub; subSel != nil {
					// Arguments on a nested field are rendered here: Build
					// ignores them for multi-field nodes and would misplace
					// them for others.
					if len(subSel.args) > 0 {
						writeArgs(&b, subSel.args)
					}
					if subSel.rendersSelectionSet() {
						// Render the selection set without the field's own
						// arguments, already emitted above.
						inner := subSel
						if len(subSel.args) > 0 {
							stripped := *subSel
							stripped.args = nil
							inner = &stripped
						}
						subQuery, err := inner.Build(ctx)
						if err != nil {
							return "", err
						}
						b.WriteString(subQuery)
					}
				}
			}
		} else if sel.inlineFragment != "" {
			b.WriteString("... on ")
			b.WriteString(sel.inlineFragment)
		} else {
			// Handle regular single field selection
			if sel.alias != "" {
				b.WriteString(sel.alias)
				b.WriteRune(':')
			}

			b.WriteString(sel.name)

			if len(sel.args) > 0 {
				writeArgs(&b, sel.args)
			}
		}
	}

	b.WriteString(strings.Repeat("}", len(path)))
	return b.String(), nil
}

func (q *QueryBuilder) unpack(data any) error {
	for _, i := range q.path() {
		// Inline fragments don't add a nesting level in the response.
		if i.inlineFragment != "" {
			if i.bind != nil {
				marshalled, err := json.Marshal(data)
				if err != nil {
					return err
				}
				if err := decodeJSON(marshalled, i.bind); err != nil {
					return err
				}
			}
			continue
		}

		k := i.name
		if i.alias != "" {
			k = i.alias
		}

		// Handle SelectFields case - when we have fields but no name,
		// or when we have subselections but no name (mixed selection case)
		// don't navigate deeper, just bind at the current level
		if len(i.selections) > 0 && i.name == "" {
			// This is a SelectFields or mixed selection - bind directly to current data
			if i.bind != nil {
				marshalled, err := json.Marshal(data)
				if err != nil {
					return err
				}
				if err := decodeJSON(marshalled, i.bind); err != nil {
					return err
				}
			}
			continue
		}

		if !i.multiple {
			if f, ok := data.(map[string]any); ok {
				data = f[k]
			}
		}

		if i.bind != nil {
			marshalled, err := json.Marshal(data)
			if err != nil {
				return err
			}
			if err := decodeJSON(marshalled, i.bind); err != nil {
				return err
			}
		}
	}

	return nil
}

func (q *QueryBuilder) Execute(ctx context.Context) error {
	if q.client == nil {
		debug.PrintStack()
		return fmt.Errorf("no client configured for selection")
	}

	query, err := q.Build(ctx)
	if err != nil {
		return err
	}

	opType := "query"
	opName := "Query"
	if q.isMutation {
		opType = "mutation"
		opName = "Mutation"
	}

	payload := opType + " " + opName + " " + query
	slog.DebugContext(ctx, "executing GraphQL request", "query", payload)

	// Capture the raw "data" bytes and decode them ourselves with UseNumber,
	// rather than letting the client decode into interface{} (which would
	// collapse every number to float64 before we can honor the declared type).
	var rawData json.RawMessage
	err = q.client.MakeRequest(ctx,
		&graphql.Request{
			Query:  payload,
			OpName: opName,
		},
		&graphql.Response{Data: &rawData},
	)
	if err != nil {
		return err
	}

	var response any
	if len(rawData) > 0 {
		if err := decodeJSON(rawData, &response); err != nil {
			return err
		}
	}

	return q.unpack(response)
}

type argument struct {
	value any

	marshalled    string
	marshalledErr error
	once          sync.Once
}

func (a *argument) marshal(ctx context.Context) error {
	a.once.Do(func() {
		a.marshalled, a.marshalledErr = MarshalGQL(ctx, a.value)
	})
	return a.marshalledErr
}
