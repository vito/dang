package dang

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/Khan/genqlient/graphql"
	"github.com/stretchr/testify/require"
)

// fakeSchema is a small Dagger-like schema for fakeClient to serve.
const fakeSchema = `
directive @expectedType(name: String!) on ARGUMENT_DEFINITION | FIELD_DEFINITION

type Query {
  container: Container!
  containers: [Container!]!
  engineVersion: String!
  setSecret(name: String!, plaintext: String!): Secret!
}

type Mutation {
  createContainer(name: String!): Container!
}

type Container {
  from(address: String!): Container!
  withEnv(value: String!, name: String!): Container!
  withSecretVariable(name: String!, secret: ID! @expectedType(name: "Secret")): Container!
  withExec(args: [String!]!): Container!
  stdout: String!
  plaintext: String!
  prune: Void
  exitCode: Int!
  id: ContainerID!
  sync: ContainerID!
}

type Secret {
  id: SecretID!
  plaintext: String!
}

scalar ContainerID
scalar SecretID
scalar Void
`

// fakeClient serves fakeSchema's leaf fields, recording each query: stdout
// is "hi\n", exitCode fails except on containers, which has two elements,
// and engineVersion is "v1". A request fails with its context's error once
// that is done.
type fakeClient struct {
	mu      sync.Mutex
	queries []string
}

func (c *fakeClient) MakeRequest(ctx context.Context, req *graphql.Request, resp *graphql.Response) error {
	c.mu.Lock()
	c.queries = append(c.queries, req.Query)
	c.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}

	path := queryFieldPath(req.Query)
	var leaf any
	switch path[len(path)-1] {
	case "stdout":
		leaf = "hi\n"
	case "exitCode":
		if path[0] != "containers" {
			return errors.New("no command has been set")
		}
		leaf = 0
	case "engineVersion":
		leaf = "v1"
	default:
		return errors.New("unexpected field " + path[len(path)-1])
	}
	var data any = leaf
	for i := len(path) - 2; i >= 0; i-- {
		if path[i] == "containers" {
			data = []any{data, data}
		}
		data = map[string]any{path[i]: data}
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		return err
	}
	*resp.Data.(*json.RawMessage) = encoded
	return nil
}

func (c *fakeClient) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.queries)
}

// queryFieldPath extracts the field names of a single-chain query, e.g.
// [container from stdout] from `query Query {container{from(...){stdout}}}`.
func queryFieldPath(query string) []string {
	var path []string
	var name strings.Builder
	depth, inString := 0, false
	flush := func() {
		if s := strings.TrimSpace(name.String()); s != "" {
			path = append(path, s)
		}
		name.Reset()
	}
	for i := strings.IndexByte(query, '{'); i < len(query); i++ {
		ch := query[i]
		switch {
		case inString:
			if ch == '\\' {
				i++
			} else if ch == '"' {
				inString = false
			}
		case ch == '"':
			inString = true
		case ch == '(':
			depth++
		case ch == ')':
			depth--
		case depth > 0:
		case ch == '{' || ch == '}':
			flush()
		default:
			name.WriteByte(ch)
		}
	}
	return path
}

// fakeGraphQLContext imports fakeSchema as Test, served by a fakeClient,
// auto-imported if autoImport is set.
func fakeGraphQLContext(t *testing.T, autoImport bool) (context.Context, *fakeClient) {
	t.Helper()
	schema, err := SchemaFromSDL(fakeSchema, "fake.graphqls")
	require.NoError(t, err)
	client := &fakeClient{}
	return ContextWithImportConfigs(context.Background(), ImportConfig{
		Name:       "Test",
		Schema:     schema,
		Client:     client,
		AutoImport: autoImport,
	}), client
}
