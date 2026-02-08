// service starts the test GraphQL server and prints its endpoint URL
// to stdout. It stays running until killed.
package main

import (
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

	// Print endpoint URL — this is the protocol: first line = endpoint
	fmt.Println(server.QueryURL())

	// Wait for signal
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	<-c

	_ = server.Stop()
}
