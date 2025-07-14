package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/vito/sprout/tests/gqlserver"
)

func main() {
	// Start the GraphQL server
	server, err := gqlserver.StartServer()
	if err != nil {
		log.Fatalf("Failed to start GraphQL server: %v", err)
	}

	fmt.Printf("🚀 GraphQL playground is running!\n")
	fmt.Printf("📊 Playground: %s\n", server.URL())
	fmt.Printf("🔗 GraphQL endpoint: %s\n", server.QueryURL())
	fmt.Printf("\nPress Ctrl+C to stop the server...\n")

	// Wait for interrupt signal to gracefully shutdown
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	<-c

	fmt.Println("\n🛑 Shutting down server...")
	if err := server.Stop(); err != nil {
		log.Printf("Error during shutdown: %v", err)
	}
	fmt.Println("✅ Server stopped gracefully")
}
