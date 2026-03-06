package main

import "github.com/vito/dang/pkg/repl"

func newReplHistory() *repl.History {
	return repl.NewHistory(repl.DefaultHistoryPath("dang"))
}
