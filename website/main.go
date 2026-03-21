package main

import (
	_ "github.com/vito/dang/website/go" // registers dang + chroma plugins

	"github.com/vito/booklit/booklitcmd"
)

func main() {
	booklitcmd.Main()
}
