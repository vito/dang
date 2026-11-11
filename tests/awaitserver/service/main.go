// service starts the fake-engine GraphQL server and prints its endpoint
// configuration as JSON to stdout, then stays running until killed.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/vito/dang/v2/tests/awaitserver"
)

func main() {
	server, err := awaitserver.StartServer()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to start: %v\n", err)
		os.Exit(1)
	}
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{
		"endpoint": server.QueryURL(),
	})
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	<-c
	_ = server.Stop()
}
