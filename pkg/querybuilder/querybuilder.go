package querybuilder

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime/debug"
	"strings"
	"sync"

	"github.com/Khan/genqlient/graphql"
	"golang.org/x/sync/errgroup"
)

func Query() *Selection {
	return &Selection{}
}

type Selection struct {
	name     string
	alias    string
	args     map[string]*argument
	bind     any
	multiple bool

	// Support for multi-field selections
	fields        []string
	subSelections map[string]*Selection

	prev *Selection

	client graphql.Client
}

func (s *Selection) path() []*Selection {
	selections := []*Selection{}
	for sel := s; sel.prev != nil; sel = sel.prev {
		selections = append([]*Selection{sel}, selections...)
	}

	return selections
}

func (s *Selection) Root() *Selection {
	return &Selection{
		client: s.client,
	}
}

func (s *Selection) SelectWithAlias(alias, name string) *Selection {
	sel := &Selection{
		name:   name,
		prev:   s,
		alias:  alias,
		client: s.client,
	}
	return sel
}

func (s *Selection) Select(name string) *Selection {
	return s.SelectWithAlias("", name)
}

func (s *Selection) SelectMultiple(name ...string) *Selection {
	sel := s.SelectWithAlias("", strings.Join(name, " "))
	sel.multiple = true
	return sel
}

// SelectFields selects multiple fields at the current level
func (s *Selection) SelectFields(fields ...string) *Selection {
	sel := &Selection{
		prev:          s,
		client:        s.client,
		fields:        fields,
		subSelections: make(map[string]*Selection),
	}
	return sel
}

// SelectNested selects a field with nested sub-selections
func (s *Selection) SelectNested(field string, subSelection *Selection) *Selection {
	sel := &Selection{
		prev:          s,
		client:        s.client,
		subSelections: make(map[string]*Selection),
	}
	sel.subSelections[field] = subSelection
	return sel
}

func (s *Selection) Arg(name string, value any) *Selection {
	sel := *s
	if sel.args == nil {
		sel.args = map[string]*argument{}
	}

	sel.args[name] = &argument{
		value: value,
	}
	return &sel
}

func (s *Selection) Bind(v interface{}) *Selection {
	sel := *s
	sel.bind = v
	return &sel
}

func (s *Selection) marshalArguments(ctx context.Context) error {
	eg, gctx := errgroup.WithContext(ctx)
	for _, sel := range s.path() {
		for _, arg := range sel.args {
			arg := arg
			eg.Go(func() error {
				return arg.marshal(gctx)
			})
		}
	}

	return eg.Wait()
}

func (s *Selection) Build(ctx context.Context) (string, error) {
	if err := s.marshalArguments(ctx); err != nil {
		return "", err
	}

	var b strings.Builder
	b.WriteString("query Query")

	path := s.path()

	for _, sel := range path {
		if sel.prev != nil && sel.prev.multiple {
			return "", fmt.Errorf("sibling selections not end of chain")
		}

		b.WriteRune('{')

		// Handle multi-field selections
		if len(sel.fields) > 0 {
			for i, field := range sel.fields {
				if i > 0 {
					b.WriteRune(' ')
				}
				b.WriteString(field)
			}
		} else if len(sel.subSelections) > 0 {
			// Handle nested selections
			i := 0
			for field, subSel := range sel.subSelections {
				if i > 0 {
					b.WriteRune(' ')
				}
				b.WriteString(field)
				// Build sub-selection
				if subSel != nil {
					subQuery, err := subSel.Build(ctx)
					if err != nil {
						return "", err
					}
					// Extract the query content without the "query" wrapper
					content := strings.TrimPrefix(subQuery, "query")
					b.WriteString(content)
				}
				i++
			}
		} else {
			// Handle regular single field selection
			if sel.alias != "" {
				b.WriteString(sel.alias)
				b.WriteRune(':')
			}

			b.WriteString(sel.name)

			if len(sel.args) > 0 {
				b.WriteRune('(')
				i := 0
				for name, arg := range sel.args {
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
		}
	}

	b.WriteString(strings.Repeat("}", len(path)))
	return b.String(), nil
}

func (s *Selection) unpack(data any) error {
	for _, i := range s.path() {
		k := i.name
		if i.alias != "" {
			k = i.alias
		}

		// Handle SelectFields case - when we have fields but no name,
		// don't navigate deeper, just bind at the current level
		if len(i.fields) > 0 && i.name == "" {
			// This is a SelectFields selection - bind directly to current data
			if i.bind != nil {
				marshalled, err := json.Marshal(data)
				if err != nil {
					return err
				}
				if err := json.Unmarshal(marshalled, i.bind); err != nil {
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
			if err := json.Unmarshal(marshalled, i.bind); err != nil {
				return err
			}
		}
	}

	return nil
}

func (s *Selection) Client(c graphql.Client) *Selection {
	sel := *s
	sel.client = c
	return &sel
}

func (s *Selection) Execute(ctx context.Context) error {
	if s.client == nil {
		debug.PrintStack()
		return fmt.Errorf("no client configured for selection")
	}

	query, err := s.Build(ctx)
	if err != nil {
		return err
	}

	var response any
	err = s.client.MakeRequest(ctx,
		&graphql.Request{
			Query:  query,
			OpName: "Query",
		},
		&graphql.Response{Data: &response},
	)
	if err != nil {
		return err
	}

	return s.unpack(response)
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
