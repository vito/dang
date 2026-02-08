// service starts the test GraphQL server and prints its endpoint
// configuration as JSON to stdout, then closes stdout and stays
// running until killed.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/vito/dang/tests/gqlserver"
)

func main() {
	server, err := gqlserver.StartServer()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to start: %v\n", err)
		os.Exit(1)
	}

	// Print endpoint configuration as a JSON line.
	// The parent reads the first line to discover how to reach the service.
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{
		"endpoint": server.QueryURL(),
	})

	// Wait for signal
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	<-c

	_ = server.Stop()
}
