package gqlsse

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Khan/genqlient/graphql"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

func subscribe(t *testing.T, handler http.HandlerFunc) (Stream, error) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return NewClient(srv.URL, nil).Subscribe(context.Background(), &graphql.Request{
		Query:  "subscription Subscription {ticks(n:2){n}}",
		OpName: "Subscription",
	})
}

func collect(t *testing.T, s Stream) ([]string, error) {
	t.Helper()
	var data []string
	for {
		r, err := s.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return data, nil
			}
			return data, err
		}
		if len(r.Errors) > 0 {
			data = append(data, "errors:"+r.Errors[0].Message)
			continue
		}
		data = append(data, string(r.Data))
	}
}

func TestSubscribeRequest(t *testing.T) {
	var gotAccept, gotContentType, gotMethod string
	var gotBody map[string]any
	s, err := subscribe(t, func(w http.ResponseWriter, r *http.Request) {
		gotAccept = r.Header.Get("Accept")
		gotContentType = r.Header.Get("Content-Type")
		gotMethod = r.Method
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: complete\n\n")
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := collect(t, s); err != nil {
		t.Fatal(err)
	}
	if gotMethod != http.MethodPost || gotAccept != "text/event-stream" || gotContentType != "application/json" {
		t.Fatalf("unexpected request: %s Accept=%q Content-Type=%q", gotMethod, gotAccept, gotContentType)
	}
	if gotBody["query"] != "subscription Subscription {ticks(n:2){n}}" || gotBody["operationName"] != "Subscription" {
		t.Fatalf("unexpected body: %v", gotBody)
	}
}

func TestParseEvents(t *testing.T) {
	s, err := subscribe(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		// A comment, CRLF line endings, a multi-line data field, an id, an
		// unknown event type, and an event carrying errors.
		fmt.Fprint(w, ":\n\n")
		fmt.Fprint(w, "event: next\r\ndata: {\"data\":{\"ticks\":{\"n\":0}}}\r\n\r\n")
		fmt.Fprint(w, "id: 7\nevent: next\ndata: {\"data\":\ndata: {\"ticks\":{\"n\":1}}}\n\n")
		fmt.Fprint(w, "event: ping\ndata: whatever\n\n")
		fmt.Fprint(w, "event:next\ndata:{\"data\":null,\"errors\":[{\"message\":\"boom\"}]}\n\n")
		fmt.Fprint(w, "event: complete\ndata:\n\n")
		fmt.Fprint(w, "event: next\ndata: {\"data\":{\"after\":\"complete\"}}\n\n")
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := collect(t, s)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{`{"ticks":{"n":0}}`, "{\"ticks\":{\"n\":1}}", "errors:boom"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestMissingComplete(t *testing.T) {
	s, err := subscribe(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: next\ndata: {\"data\":{}}\n\n")
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = collect(t, s)
	if err == nil || errors.Is(err, io.EOF) {
		t.Fatalf("expected an error for a stream without complete, got %v", err)
	}
}

func TestJSONFallback(t *testing.T) {
	s, err := subscribe(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"errors":[{"message":"subscriptions not supported"}]}`)
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := collect(t, s)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(got) != "[errors:subscriptions not supported]" {
		t.Fatalf("got %q", got)
	}
}

func TestNonOKStatus(t *testing.T) {
	_, err := subscribe(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"errors":[{"message":"bad query"}]}`)
	})
	var list gqlerror.List
	if !errors.As(err, &list) || list[0].Message != "bad query" {
		t.Fatalf("expected GraphQL errors, got %v", err)
	}
}

func TestCancel(t *testing.T) {
	serverDone := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(serverDone)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: next\ndata: {\"data\":{}}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithCancel(context.Background())
	s, err := NewClient(srv.URL, nil).Subscribe(ctx, &graphql.Request{Query: "subscription {x}"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Next(); err != nil {
		t.Fatal(err)
	}
	cancel()
	if _, err := s.Next(); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	_ = s.Close()
	select {
	case <-serverDone:
	case <-time.After(5 * time.Second):
		t.Fatal("server did not observe the cancelled request")
	}
}
