//go:build !(js && wasm)

// This package only builds for js/wasm (see main.go). This stub keeps
// `go build ./...` and editor tooling happy on other platforms.
package main

func main() {}
