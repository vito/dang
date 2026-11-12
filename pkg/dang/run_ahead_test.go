package dang

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Khan/genqlient/graphql"
	"github.com/stretchr/testify/require"
)

// runAheadSchema has a slow receiver, base, and a slow argument, file, for
// a call that takes both: base.withFile(path:, source: file(name:)).
const runAheadSchema = `
directive @expectedType(name: String!) on ARGUMENT_DEFINITION | FIELD_DEFINITION

interface Node {
  id: ID!
}

type Query {
  node(id: ID!): Node
  base: Container!
  maybeBase: Container
  file(name: String!): File!
}

type Container implements Node {
  id: ID!
  withFile(path: String!, source: ID! @expectedType(name: "File")): Container!
  stdout: String!
}

type File implements Node {
  id: ID!
  sync: ID! @expectedType(name: "File")
}
`

// scriptClient answers each request with handle's data for its query, and
// records every query it was sent.
type scriptClient struct {
	handle func(ctx context.Context, query string) (any, error)

	mu      sync.Mutex
	queries []string
}

func (c *scriptClient) MakeRequest(ctx context.Context, req *graphql.Request, resp *graphql.Response) error {
	c.mu.Lock()
	c.queries = append(c.queries, req.Query)
	c.mu.Unlock()
	data, err := c.handle(ctx, req.Query)
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		return err
	}
	*resp.Data.(*json.RawMessage) = encoded
	return nil
}

func (c *scriptClient) sent() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.queries...)
}

// evalRunAhead evaluates source against runAheadSchema served by client.
func evalRunAhead(t *testing.T, client *scriptClient, source string) (Value, error) {
	t.Helper()
	schema, err := SchemaFromSDL(runAheadSchema, "runahead.graphqls")
	require.NoError(t, err)
	ctx := ContextWithImportConfigs(context.Background(), ImportConfig{
		Name:       "Test",
		Schema:     schema,
		Client:     client,
		AutoImport: true,
	})
	m, err := LoadDir(ctx, t.TempDir(), LoadOptions{})
	require.NoError(t, err)
	t.Cleanup(func() { m.Close() })
	val, _, err := m.Eval(ctx, "", source)
	return val, err
}

// waitOrFail waits for ch, or for ctx to be done, or fails the request after
// a while: what it waits for never happened. A nil ch waits for ctx alone.
func waitOrFail(ctx context.Context, ch <-chan struct{}, what string) error {
	select {
	case <-ch:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(5 * time.Second):
		return errors.New(what + " did not start while this request ran")
	}
}

const withFileSource = `base.withFile(path: "/a", source: file(name: "a")).stdout`

// The receiver's chain runs while the argument gets its ID, and the rest of
// the query is selected from the receiver's ID. The argument's request
// waits until the receiver's has started, so if they ran one after another
// it would fail.
func TestRunAheadReceiverRunsWhileArgumentResolves(t *testing.T) {
	baseStarted := make(chan struct{})
	client := &scriptClient{handle: func(ctx context.Context, q string) (any, error) {
		switch {
		case strings.Contains(q, `node(id:"base-id")`) && strings.Contains(q, `source:"file-id"`):
			return map[string]any{"node": map[string]any{"withFile": map[string]any{"stdout": "ok"}}}, nil
		case strings.Contains(q, "{base{id}}"):
			close(baseStarted)
			return map[string]any{"base": map[string]any{"id": "base-id"}}, nil
		case strings.Contains(q, `{file(name:"a"){id}}`):
			if err := waitOrFail(ctx, baseStarted, "base"); err != nil {
				return nil, err
			}
			return map[string]any{"file": map[string]any{"id": "file-id"}}, nil
		}
		return nil, errors.New("unexpected query " + q)
	}}
	val, err := evalRunAhead(t, client, withFileSource)
	require.NoError(t, err)
	require.Equal(t, "ok", val.(StringValue).Val)
	require.Len(t, client.sent(), 3)
}

// An argument whose ID is already known needs no request, so the query is
// sent whole, as one request, with nothing run ahead.
func TestRunAheadNotForKnownArguments(t *testing.T) {
	client := &scriptClient{handle: func(ctx context.Context, q string) (any, error) {
		switch {
		case strings.Contains(q, `{file(name:"a"){sync}}`):
			return map[string]any{"file": map[string]any{"sync": "file-id"}}, nil
		case strings.Contains(q, `{base{withFile(`) && strings.Contains(q, `source:"file-id"`):
			return map[string]any{"base": map[string]any{"withFile": map[string]any{"stdout": "ok"}}}, nil
		}
		return nil, errors.New("unexpected query " + q)
	}}
	val, err := evalRunAhead(t, client, `base.withFile(path: "/a", source: file(name: "a").sync).stdout`)
	require.NoError(t, err)
	require.Equal(t, "ok", val.(StringValue).Val)
	require.Len(t, client.sent(), 2)
}

// A receiver that may be null has no ID to select the rest from, so its
// query is sent whole once the argument has its ID, as before.
func TestRunAheadNotForNullableReceiver(t *testing.T) {
	client := &scriptClient{handle: func(ctx context.Context, q string) (any, error) {
		switch {
		case strings.Contains(q, `{file(name:"a"){id}}`):
			return map[string]any{"file": map[string]any{"id": "file-id"}}, nil
		case strings.Contains(q, `{maybeBase{withFile(`):
			return map[string]any{"maybeBase": map[string]any{"withFile": map[string]any{"stdout": "ok"}}}, nil
		}
		return nil, errors.New("unexpected query " + q)
	}}
	val, err := evalRunAhead(t, client, `maybeBase.withFile(path: "/a", source: file(name: "a")).stdout`)
	require.NoError(t, err)
	require.Equal(t, "ok", val.(StringValue).Val)
	for _, q := range client.sent() {
		require.NotContains(t, q, "node(")
	}
}

// When the argument fails, its error is the query's, and the receiver's
// request, still running, is cancelled.
func TestRunAheadArgumentFails(t *testing.T) {
	baseCancelled := make(chan struct{})
	client := &scriptClient{handle: func(ctx context.Context, q string) (any, error) {
		switch {
		case strings.Contains(q, "{base{id}}"):
			if err := waitOrFail(ctx, nil, "the cancellation"); !errors.Is(err, context.Canceled) {
				return nil, err
			}
			close(baseCancelled)
			return nil, ctx.Err()
		case strings.Contains(q, `{file(name:"a"){id}}`):
			return nil, errors.New("no such file")
		}
		return nil, errors.New("unexpected query " + q)
	}}
	_, err := evalRunAhead(t, client, withFileSource)
	require.ErrorContains(t, err, "no such file")
	select {
	case <-baseCancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("the receiver's request was not cancelled")
	}
}

// When the receiver fails, its error is the query's, and the argument's
// request, still running, is cancelled.
func TestRunAheadReceiverFails(t *testing.T) {
	client := &scriptClient{handle: func(ctx context.Context, q string) (any, error) {
		switch {
		case strings.Contains(q, "{base{id}}"):
			return nil, errors.New("base failed")
		case strings.Contains(q, `{file(name:"a"){id}}`):
			return nil, waitOrFail(ctx, nil, "base")
		}
		return nil, errors.New("unexpected query " + q)
	}}
	_, err := evalRunAhead(t, client, withFileSource)
	require.ErrorContains(t, err, "base failed")
}
