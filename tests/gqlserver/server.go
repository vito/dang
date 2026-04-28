package gqlserver

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
	"github.com/99designs/gqlgen/graphql/playground"
	"github.com/vito/dang/pkg/dang"
	"github.com/vito/dang/pkg/introspection"
)

type Server struct {
	httpServer *http.Server
	listener   net.Listener
	port       int
}

// StartServer starts a GraphQL server on an available port and returns the server instance
func StartServer() (*Server, error) {
	// Create listener on available port
	listener, err := net.Listen("tcp", ":0")
	if err != nil {
		return nil, fmt.Errorf("failed to create listener: %w", err)
	}

	port := listener.Addr().(*net.TCPAddr).Port

	// Create GraphQL handler
	srv := handler.NewDefaultServer(NewExecutableSchema(Config{Resolvers: &Resolver{}}))

	introspectionSchema, err := loadIntrospectionSchema()
	if err != nil {
		return nil, err
	}

	// Create HTTP mux
	mux := http.NewServeMux()
	mux.Handle("/", playground.Handler("GraphQL playground", "/query"))
	mux.Handle("/query", introspectionHandler(srv, introspectionSchema))

	// Create HTTP server
	httpServer := &http.Server{
		Handler: mux,
	}

	server := &Server{
		httpServer: httpServer,
		listener:   listener,
		port:       port,
	}

	// Start server in goroutine
	go func() {
		if err := httpServer.Serve(listener); err != nil && err != http.ErrServerClosed {
			fmt.Printf("GraphQL server error: %v\n", err)
		}
	}()

	return server, nil
}

func loadIntrospectionSchema() (*introspection.Schema, error) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		return nil, fmt.Errorf("failed to locate gqlserver source")
	}
	schemaPath := filepath.Join(filepath.Dir(filename), "schema.graphqls")
	schema, err := dang.SchemaFromSDLFile(schemaPath)
	if err != nil {
		return nil, fmt.Errorf("loading GraphQL test schema for introspection: %w", err)
	}
	return schema, nil
}

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
		if err := json.Unmarshal(body, &req); err == nil && isExtendedIntrospectionQuery(req.Query) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(struct {
				Data struct {
					Schema *introspection.Schema `json:"__schema"`
				} `json:"data"`
			}{
				Data: struct {
					Schema *introspection.Schema `json:"__schema"`
				}{
					Schema: schema,
				},
			})
			return
		}

		r.Body = io.NopCloser(bytes.NewReader(body))
		next.ServeHTTP(w, r)
	})
}

func isExtendedIntrospectionQuery(query string) bool {
	return strings.Contains(query, "_DirectiveApplication")
}

// Stop gracefully stops the server
func (s *Server) Stop() error {
	return s.httpServer.Shutdown(context.Background())
}

// URL returns the server's base URL
func (s *Server) URL() string {
	return fmt.Sprintf("http://localhost:%d", s.port)
}

// QueryURL returns the GraphQL query endpoint URL
func (s *Server) QueryURL() string {
	return fmt.Sprintf("http://localhost:%d/query", s.port)
}
