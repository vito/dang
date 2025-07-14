package gqlserver

//go:generate go run github.com/99designs/gqlgen generate

import (
	"context"
	"fmt"
	"net"
	"net/http"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/playground"
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

	// Create HTTP mux
	mux := http.NewServeMux()
	mux.Handle("/", playground.Handler("GraphQL playground", "/query"))
	mux.Handle("/query", srv)

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
