// Package gqlsse carries GraphQL subscription operations over the graphql-sse
// protocol in "distinct connections mode"
// (https://github.com/enisdenjo/graphql-sse/blob/master/PROTOCOL.md).
//
// Each subscription is one HTTP request: the client POSTs the ordinary GraphQL
// request JSON ({"query", "operationName", and "variables" when there are
// any) to the same endpoint it uses for queries, with
// `Accept: text/event-stream`. The server answers
// `200 Content-Type: text/event-stream` and writes one `next` event per pushed
// ExecutionResult, then a `complete` event when the stream ends. Closing the
// request (cancelling its context) cancels the subscription server-side.
// Queries and mutations keep using the plain JSON request/response.
package gqlsse

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
	"sync"

	"github.com/Khan/genqlient/graphql"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

// Subscriber is implemented by GraphQL clients that can carry subscription
// operations. Dang type-asserts its graphql.Client to this interface when a
// program consumes a subscription stream.
type Subscriber interface {
	// Subscribe sends req and returns the stream of ExecutionResults the
	// server pushes for it. Cancelling ctx, or closing the stream, ends the
	// subscription.
	Subscribe(ctx context.Context, req *graphql.Request) (Stream, error)
}

// Stream is an open subscription: a sequence of ExecutionResults.
type Stream interface {
	// Next blocks until the server pushes the next result. It returns io.EOF
	// once the server completes the stream, and the context's error if the
	// subscription's context is cancelled first.
	Next() (*Result, error)
	// Close ends the subscription, aborting the HTTP request. It is safe to
	// call more than once and after the stream completed.
	Close() error
}

// Result is one GraphQL ExecutionResult, as carried by a `next` event.
type Result struct {
	Data       json.RawMessage `json:"data"`
	Errors     gqlerror.List   `json:"errors,omitempty"`
	Extensions map[string]any  `json:"extensions,omitempty"`
}

// Client is a graphql.Client for an HTTP endpoint that also implements
// Subscriber. Queries and mutations go through genqlient's client unchanged.
type Client struct {
	graphql.Client

	endpoint string
	doer     graphql.Doer
}

var _ graphql.Client = (*Client)(nil)
var _ Subscriber = (*Client)(nil)

// NewClient returns a client for endpoint. A nil httpClient means
// http.DefaultClient, as with graphql.NewClient.
func NewClient(endpoint string, httpClient graphql.Doer) *Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Client{
		Client:   graphql.NewClient(endpoint, httpClient),
		endpoint: endpoint,
		doer:     httpClient,
	}
}

// Subscribe implements Subscriber.
func (c *Client) Subscribe(ctx context.Context, req *graphql.Request) (Stream, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("encoding subscription request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := c.doer.Do(httpReq)
	if err != nil {
		return nil, err
	}
	return newStream(ctx, resp)
}

// newStream turns a response to a subscription request into a Stream.
func newStream(ctx context.Context, resp *http.Response) (Stream, error) {
	mediaType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))

	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		// A GraphQL server rejecting the operation may still answer with an
		// ExecutionResult; surface its errors rather than the status.
		var result Result
		if mediaType != "text/event-stream" && json.Unmarshal(raw, &result) == nil && len(result.Errors) > 0 {
			return nil, result.Errors
		}
		return nil, fmt.Errorf("subscription request failed: %s: %s", resp.Status, strings.TrimSpace(string(raw)))
	}

	if mediaType != "text/event-stream" {
		// The server answered with a single ExecutionResult instead of a
		// stream (e.g. it does not support subscriptions and reports an
		// error). Deliver it as a one-result stream.
		defer resp.Body.Close()
		var result Result
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			return nil, fmt.Errorf("subscription response is %q, not text/event-stream, and not a GraphQL result: %w", mediaType, err)
		}
		return &singleResult{result: &result}, nil
	}

	return &sseStream{
		ctx:    ctx,
		body:   resp.Body,
		reader: bufio.NewReader(resp.Body),
	}, nil
}

// sseStream parses a graphql-sse event stream. It runs no goroutine: Next
// reads from the response body on the caller's goroutine, so cancelling the
// request's context (or Close) is all it takes to stop it.
type sseStream struct {
	ctx    context.Context
	body   io.ReadCloser
	reader *bufio.Reader

	closeOnce sync.Once
	done      bool
}

// Next implements Stream. It follows the event stream format
// (https://html.spec.whatwg.org/multipage/server-sent-events.html): lines are
// split on LF (a trailing CR is stripped); a line starting with ':' is a
// comment (keep-alive) and ignored; `field: value` lines (one optional space
// after the colon) accumulate the event, `data` lines joined with "\n"; a
// blank line dispatches it. `event: next` decodes the data as an
// ExecutionResult; `event: complete` ends the stream. Other event types and
// fields (`id`, `retry`) are ignored.
func (s *sseStream) Next() (*Result, error) {
	if s.done {
		return nil, io.EOF
	}
	var (
		eventType string
		data      strings.Builder
		hasData   bool
	)
	for {
		line, err := s.reader.ReadString('\n')
		if err != nil {
			if ctxErr := s.ctx.Err(); ctxErr != nil {
				return nil, context.Cause(s.ctx)
			}
			if errors.Is(err, io.EOF) {
				s.finish()
				return nil, fmt.Errorf("subscription stream ended without a complete event")
			}
			return nil, fmt.Errorf("reading subscription stream: %w", err)
		}
		line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")

		if line == "" {
			// Dispatch.
			switch eventType {
			case "next":
				var result Result
				if err := json.Unmarshal([]byte(data.String()), &result); err != nil {
					return nil, fmt.Errorf("decoding subscription event: %w", err)
				}
				return &result, nil
			case "complete":
				s.finish()
				return nil, io.EOF
			}
			eventType = ""
			data.Reset()
			hasData = false
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "event":
			eventType = value
		case "data":
			if hasData {
				data.WriteByte('\n')
			}
			data.WriteString(value)
			hasData = true
		}
	}
}

func (s *sseStream) finish() {
	s.done = true
	_ = s.Close()
}

// Close implements Stream.
func (s *sseStream) Close() error {
	var err error
	s.closeOnce.Do(func() {
		err = s.body.Close()
	})
	return err
}

// singleResult is a Stream of exactly one result.
type singleResult struct {
	result *Result
}

func (s *singleResult) Next() (*Result, error) {
	if s.result == nil {
		return nil, io.EOF
	}
	r := s.result
	s.result = nil
	return r, nil
}

func (s *singleResult) Close() error {
	s.result = nil
	return nil
}
