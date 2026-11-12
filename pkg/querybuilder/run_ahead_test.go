package querybuilder

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

// pendingID is an object argument whose ID takes a request: get answers it.
type pendingID struct {
	get func(ctx context.Context) (string, error)
}

func (p pendingID) XXX_GraphQLType() string   { return "File" }
func (p pendingID) XXX_GraphQLIDType() string { return "ID" }
func (p pendingID) XXX_GraphQLID(ctx context.Context) (string, error) {
	return p.get(ctx)
}
func (p pendingID) MarshalJSON() ([]byte, error) { return nil, nil }

// recordingClient answers every request with data and records its query.
type recordingClient struct {
	data string

	mu      sync.Mutex
	queries []string
}

func (c *recordingClient) MakeRequest(ctx context.Context, req *graphql.Request, resp *graphql.Response) error {
	c.mu.Lock()
	c.queries = append(c.queries, req.Query)
	c.mu.Unlock()
	*resp.Data.(*json.RawMessage) = json.RawMessage(c.data)
	return nil
}

func loadContainer(id string) *QueryBuilder {
	return Query().Select("node").Arg("id", id).InlineFragment("Container")
}

// A chain off a mutation is sent whole: every query off it re-sends the
// mutation, so running its receiver ahead would run the mutation twice.
func TestRunAheadNotOffAMutation(t *testing.T) {
	client := &recordingClient{data: `{"create":{"withFile":{"stdout":"ok"}}}`}
	arg := pendingID{get: func(context.Context) (string, error) { return "file-id", nil }}
	var out string
	err := Mutation().Select("create").Reloadable(loadContainer).
		Select("withFile").Arg("source", arg).
		Select("stdout").Bind(&out).Client(client).Execute(context.Background())
	require.NoError(t, err)
	require.Equal(t, "ok", out)
	require.Len(t, client.queries, 1)
	require.True(t, strings.HasPrefix(client.queries[0], "mutation"))
}

// An argument's marshal cut short by its context is tried again by the next
// run of the same chain, rather than failing it with the old cancellation.
func TestArgumentMarshalCancelledIsRetried(t *testing.T) {
	client := &recordingClient{data: `{"base":{"withFile":{"stdout":"ok"}}}`}
	first := true
	arg := pendingID{get: func(ctx context.Context) (string, error) {
		if first {
			first = false
			<-ctx.Done()
			return "", ctx.Err()
		}
		return "file-id", nil
	}}
	var out string
	chain := Query().Select("base").Select("withFile").Arg("source", arg).Select("stdout").Bind(&out).Client(client)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, chain.Execute(ctx), context.Canceled)

	require.NoError(t, chain.Execute(context.Background()))
	require.Equal(t, "ok", out)
	require.Len(t, client.queries, 1)
	require.Contains(t, client.queries[0], `source:"file-id"`)
}

// funcClient answers each request with handle's data, and records its query.
type funcClient struct {
	handle func(ctx context.Context, query string) (string, error)

	mu      sync.Mutex
	queries []string
}

func (c *funcClient) MakeRequest(ctx context.Context, req *graphql.Request, resp *graphql.Response) error {
	c.mu.Lock()
	c.queries = append(c.queries, req.Query)
	c.mu.Unlock()
	data, err := c.handle(ctx, req.Query)
	if err != nil {
		return err
	}
	*resp.Data.(*json.RawMessage) = json.RawMessage(data)
	return nil
}

// In a chain with two steps whose arguments need requests, the receiver of
// the later one runs ahead of its argument too: here withDir, which waits
// for its own argument, runs while withFile's argument resolves. That
// argument waits until withDir has started, so if withDir ran after it, it
// would fail.
func TestRunAheadEveryReceiver(t *testing.T) {
	withDirStarted := make(chan struct{})
	client := &funcClient{handle: func(ctx context.Context, q string) (string, error) {
		switch {
		case q == `query Query {base{id}}`:
			return `{"base":{"id":"c1"}}`, nil
		case strings.Contains(q, `node(id:"c1"){... on Container{withDir(source:"d1"){id}}}`):
			close(withDirStarted)
			return `{"node":{"withDir":{"id":"c2"}}}`, nil
		case strings.Contains(q, `node(id:"c2"){... on Container{withFile(source:"f2"){stdout}}}`):
			return `{"node":{"withFile":{"stdout":"ok"}}}`, nil
		}
		return "", errors.New("unexpected query " + q)
	}}
	dir := pendingID{get: func(context.Context) (string, error) { return "d1", nil }}
	file := pendingID{get: func(ctx context.Context) (string, error) {
		select {
		case <-withDirStarted:
			return "f2", nil
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(5 * time.Second):
			return "", errors.New("withDir did not start while this argument resolved")
		}
	}}
	var out string
	err := Query().Select("base").Reloadable(loadContainer).
		Select("withDir").Arg("source", dir).Reloadable(loadContainer).
		Select("withFile").Arg("source", file).
		Select("stdout").Bind(&out).Client(client).Execute(context.Background())
	require.NoError(t, err)
	require.Equal(t, "ok", out)
}

// A chain run again, after its arguments got their IDs, is sent whole: no
// argument needs a request any more, so running the receiver ahead would
// only add one.
func TestRunAheadNotForMarshalledArguments(t *testing.T) {
	client := &funcClient{handle: func(ctx context.Context, q string) (string, error) {
		switch {
		case q == `query Query {base{id}}`:
			return `{"base":{"id":"c1"}}`, nil
		case strings.Contains(q, `node(id:"c1"){... on Container{withFile(source:"f1"){stdout}}}`):
			return `{"node":{"withFile":{"stdout":"ok"}}}`, nil
		case q == `query Query {base{withFile(source:"f1"){stdout}}}`:
			return `{"base":{"withFile":{"stdout":"ok"}}}`, nil
		}
		return "", errors.New("unexpected query " + q)
	}}
	file := pendingID{get: func(context.Context) (string, error) { return "f1", nil }}
	var out string
	chain := Query().Select("base").Reloadable(loadContainer).
		Select("withFile").Arg("source", file).
		Select("stdout").Bind(&out).Client(client)
	require.NoError(t, chain.Execute(context.Background()))
	require.Len(t, client.queries, 2)
	require.NoError(t, chain.Execute(context.Background()))
	require.Equal(t, "ok", out)
	require.Len(t, client.queries, 3)
	require.Equal(t, `query Query {base{withFile(source:"f1"){stdout}}}`, client.queries[2])
}
