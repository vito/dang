package dang

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Khan/genqlient/graphql"
	"github.com/stretchr/testify/require"
	"github.com/vito/dang/v2/pkg/hm"
	"github.com/vito/dang/v2/pkg/ioctx"
)

// structuralSchema has an Error type of its own, as Dagger's schema does,
// which declares no interface, and an interface of its own, which stays
// nominal.
const structuralSchema = `
type Query {
  error(message: String!): Error!
  thing: Thing!
  node(id: ID!): Node
  problem: Problem!
}

type Error {
  message: String!
  id: ID!
}

type Other {
  message: String!
}

union Problem = Error | Other

type Thing implements Node {
  id: ID!
  title: String!
}

interface Node {
  id: ID!
}
`

// structuralClient answers every message with "boom from the server" and
// every title with "a thing".
type structuralClient struct{}

func (structuralClient) MakeRequest(_ context.Context, req *graphql.Request, resp *graphql.Response) error {
	path := queryFieldPath(req.Query)
	var data any
	switch path[len(path)-1] {
	case "message":
		data = "boom from the server"
	case "title":
		data = "a thing"
	default:
		return errors.New("unexpected field " + path[len(path)-1])
	}
	for i := len(path) - 2; i >= 0; i-- {
		data = map[string]any{path[i]: data}
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		return err
	}
	*resp.Data.(*json.RawMessage) = encoded
	return nil
}

// evalStructural runs src against structuralSchema, imported as Test,
// returning the inference error, or else the evaluation error, raw.
func evalStructural(t *testing.T, src string) (inferErr, evalErr error) {
	t.Helper()
	schema, err := SchemaFromSDL(structuralSchema, "structural.graphqls")
	require.NoError(t, err)

	parsed, err := ParseWithRecovery("structural_test", []byte(src))
	require.NoError(t, err, "parse")
	file := parsed.(*FileBlock)

	typeScope, valueScope := BuildScopesFromImports("", []ImportConfig{{Name: "Test", Schema: schema, Client: structuralClient{}}})
	if _, err := InferFormsWithPhases(context.Background(), file.Forms, typeScope, hm.NewSimpleFresher()); err != nil {
		return err, nil
	}

	var out bytes.Buffer
	ctx := ioctx.StdoutToContext(context.Background(), &out)
	ctx = ioctx.StderrToContext(ctx, &out)
	for _, node := range file.Forms {
		if _, err := EvalNode(ctx, valueScope, node); err != nil {
			return nil, err
		}
	}
	return nil, nil
}

// A schema's Error type has the shape of Dang's Error interface, so its
// values are raised and rescued as errors, though the schema knows nothing
// of Dang's interface.
func TestRaiseSchemaError(t *testing.T) {
	inferErr, err := evalStructural(t, `
let described = { raise Test.error(message: "boom") } rescue {
  e: Error! => "caught: " + e.message
}
assert { described == "caught: boom from the server" }

let specific = { raise Test.error(message: "boom") } rescue {
  e: Test.Error! => "the schema's: " + e.message
}
assert { specific == "the schema's: boom from the server" }

raise Test.error(message: "boom")
`)
	require.NoError(t, inferErr)
	raised := raisedFrom(t, err)
	require.Equal(t, "boom from the server", raised.Error())
	val, ok := raised.Value.(GraphQLValue)
	require.True(t, ok, "raised %T", raised.Value)
	require.Equal(t, "Error", val.TypeName)
}

// A rescued schema error raised again is the same object.
func TestReRaiseSchemaError(t *testing.T) {
	inferErr, err := evalStructural(t, `
{ raise Test.error(message: "boom") } rescue {
  e: Test.Error! => raise e
}
`)
	require.NoError(t, inferErr)
	raised := raisedFrom(t, err)
	require.IsType(t, GraphQLValue{}, raised.Value)
	require.Nil(t, raised.Cause, "a plain re-raise records no cause")
	require.Equal(t, "boom from the server", raised.Error())
}

// A schema object without an Error's shape can't be raised, nor a schema's
// union of errors: a handle on one has no message to read until the server
// says which member it holds.
func TestRaiseSchemaObjectWithoutMessage(t *testing.T) {
	inferErr, _ := evalStructural(t, `raise Test.thing`)
	require.ErrorContains(t, inferErr, "raise requires a String! or Error!")

	inferErr, _ = evalStructural(t, `raise Test.problem`)
	require.ErrorContains(t, inferErr, "raise requires a String! or Error!")
}

// A schema error in an error's explicit cause shows in the report, however
// deep in the chain, and when the wrapper is raised again.
func TestSchemaErrorAsExplicitCause(t *testing.T) {
	const wrapped = `
type Wrapped {
  pub message: String!
  pub cause: Error
}
`
	for name, src := range map[string]string{
		"direct": `
{ raise Test.error(message: "boom") } rescue {
  e: Error! => raise Wrapped("wrapped", e)
}`,
		"nested": `
{ raise Test.error(message: "boom") } rescue {
  e: Error! => raise Wrapped("wrapped", Wrapped("inner", e))
}`,
		"re-raised": `
{
  { raise Test.error(message: "boom") } rescue {
    e: Error! => raise Wrapped("wrapped", e)
  }
} rescue {
  w: Wrapped! => raise w
}`,
	} {
		t.Run(name, func(t *testing.T) {
			inferErr, err := evalStructural(t, wrapped+src)
			require.NoError(t, inferErr)
			raised := raisedFrom(t, err)
			require.Equal(t, "wrapped", raised.Error())
			chain := causeChain(raised)
			require.NotEmpty(t, chain)
			last := chain[len(chain)-1]
			require.Equal(t, "boom from the server", last.Error())
			require.Equal(t, "Error: boom from the server", errorSummary(last))
		})
	}
}

// An error whose message is a method reports the text, not the method; one
// whose message fails is raised all the same, with the failure as its
// message, so that a rescue still matches it by type.
func TestRaiseComputedMessage(t *testing.T) {
	inferErr, err := evalStructural(t, `
type Oops {
  pub code: Int!
  pub message: String! { "failed with " + toString(code) }
}

raise Oops(4)
`)
	require.NoError(t, inferErr)
	raised := raisedFrom(t, err)
	require.Equal(t, "failed with 4", raised.Error())
	require.Equal(t, "Oops: failed with 4", errorSummary(raised))

	inferErr, err = evalStructural(t, `
type Bad {
  pub code: Int!
  pub message: String! { "ratio " + toString(10 / code) }
}

let caught = { raise Bad(0) } rescue {
  b: Bad! => "caught bad"
  e: Error! => "other: " + e.message
}
assert { caught == "caught bad" }

raise Bad(0)
`)
	require.NoError(t, inferErr)
	raised = raisedFrom(t, err)
	require.IsType(t, &Object{}, raised.Value)
	require.Contains(t, raised.Error(), "its message failed")
}

// A Dang interface's `id` is a field like any other: a type without one
// doesn't have the shape.
func TestStructuralIDIsRequired(t *testing.T) {
	inferErr, _ := evalStructural(t, `
interface HasID { id: ID! }
type Rock { pub weight: Int! }
show(h: HasID!): String! { toString(h.id) }
show(Rock(3))
`)
	require.ErrorContains(t, inferErr, `missing field "id"`)
}

// Interfaces a schema declares stay nominal: a Dang type with their fields
// is not one of them.
func TestSchemaInterfacesStayNominal(t *testing.T) {
	inferErr, _ := evalStructural(t, `
type Lookalike { pub id: ID! }
let n: Test.Node! = Lookalike(id: "x")
`)
	require.Error(t, inferErr)

	// A Dang interface with the same field takes both.
	inferErr, err := evalStructural(t, `
interface HasID { id: ID! }
type Lookalike { pub id: ID! }
let ids: [HasID!]! = [(Lookalike(id: "x") :: HasID!), Test.thing]
assert { ids.length == 2 }
`)
	require.NoError(t, inferErr)
	require.NoError(t, err)
}

// A rescued error raised again as it was keeps the Go error it was made
// of, for a host to report as it would have, had nothing rescued it.
func TestReRaiseKeepsOrigin(t *testing.T) {
	inferErr, err := evalStructural(t, `
{ Test.thing.id } rescue {
  e: RuntimeError! => raise e
}
`)
	require.NoError(t, inferErr)
	raised := raisedFrom(t, err)
	require.NotNil(t, raised.Origin())
	require.ErrorContains(t, raised.Origin(), "unexpected field id")

	// Any other raise in the arm has none.
	inferErr, err = evalStructural(t, `
{ Test.thing.id } rescue {
  e: RuntimeError! => raise "wrapped: " + e.message
}
`)
	require.NoError(t, inferErr)
	require.Nil(t, raisedFrom(t, err).Origin())
}
