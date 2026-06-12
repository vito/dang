package main

import (
	_ "github.com/vito/dang/docs/go" // registers the dang plugin

	"github.com/vito/booklit/booklitcmd"
)

func main() {
	booklitcmd.Main()
}
