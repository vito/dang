package main

import (
	"fmt"
	"os"

	"github.com/vito/dash/pkg/ast"
)

func main() {
	if err := ast.CheckFile(os.Args[1]); err != nil {
		panic(err)
	}
	fmt.Println("ok!")
}
