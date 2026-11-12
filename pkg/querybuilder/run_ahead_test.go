package querybuilder

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

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
