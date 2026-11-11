package awaitserver

//go:generate go run github.com/99designs/gqlgen generate

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/vito/dang/v2/pkg/dang"
	"github.com/vito/dang/v2/pkg/introspection"
)

// Server serves the fake engine schema; see schema.graphqls.
type Server struct {
	httpServer *http.Server
	port       int
}

// StartServer starts the server on an available port.
func StartServer() (*Server, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("failed to create listener: %w", err)
	}
	srv := handler.NewDefaultServer(NewExecutableSchema(Config{Resolvers: &Resolver{}}))

	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		return nil, fmt.Errorf("failed to locate awaitserver source")
	}
	schema, err := dang.SchemaFromSDLFile(filepath.Join(filepath.Dir(filename), "schema.graphqls"))
	if err != nil {
		return nil, err
	}

	mux := http.NewServeMux()
	mux.Handle("/query", introspectionHandler(srv, schema))
	s := &Server{
		httpServer: &http.Server{Handler: mux},
		port:       listener.Addr().(*net.TCPAddr).Port,
	}
	go func() {
		if err := s.httpServer.Serve(listener); err != nil && err != http.ErrServerClosed {
			fmt.Printf("GraphQL server error: %v\n", err)
		}
	}()
	return s, nil
}

// introspectionHandler answers Dang's extended (directive-aware)
// introspection from the SDL, as tests/gqlserver does, and passes everything
// else to gqlgen.
func introspectionHandler(next http.Handler, schema *introspection.Schema) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		_ = r.Body.Close()
		var req struct {
			Query string `json:"query"`
		}
		if err := json.Unmarshal(body, &req); err == nil && strings.Contains(req.Query, "_DirectiveApplication") {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{"__schema": schema},
			})
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		next.ServeHTTP(w, r)
	})
}

// Stop gracefully stops the server.
func (s *Server) Stop() error {
	return s.httpServer.Shutdown(context.Background())
}

// QueryURL returns the GraphQL query endpoint URL.
func (s *Server) QueryURL() string {
	return fmt.Sprintf("http://127.0.0.1:%d/query", s.port)
}
