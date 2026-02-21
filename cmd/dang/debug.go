package main

import (
	"expvar"
	"log/slog"
	"net"
	"net/http"
	"net/http/pprof"
	"runtime"
)

func setupDebugHandlers(addr string) error {
	m := http.NewServeMux()
	m.Handle("/debug/vars", expvar.Handler())
	m.Handle("/debug/pprof/", http.HandlerFunc(pprof.Index))
	m.Handle("/debug/pprof/cmdline", http.HandlerFunc(pprof.Cmdline))
	m.Handle("/debug/pprof/profile", http.HandlerFunc(pprof.Profile))
	m.Handle("/debug/pprof/symbol", http.HandlerFunc(pprof.Symbol))
	m.Handle("/debug/pprof/trace", http.HandlerFunc(pprof.Trace))
	m.Handle("/debug/pprof/heap", pprof.Handler("heap"))
	m.Handle("/debug/pprof/goroutine", pprof.Handler("goroutine"))

	m.Handle("/debug/gc", http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		runtime.GC()
		slog.Warn("triggered GC from debug endpoint")
	}))

	l, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	slog.Info("debug handlers listening", "debugAddr", addr)
	go http.Serve(l, m) //nolint:errcheck
	return nil
}
