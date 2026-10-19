package ioctx

import (
	"context"
	"io"
)

type stdoutKey struct{}
type stderrKey struct{}

func StderrFromContext(ctx context.Context) io.Writer {
	logger := ctx.Value(stderrKey{})
	if logger == nil {
		logger = io.Discard
	}

	return logger.(io.Writer)
}

func StderrToContext(ctx context.Context, w io.Writer) context.Context {
	return context.WithValue(ctx, stderrKey{}, w)
}

func StdoutFromContext(ctx context.Context) io.Writer {
	writer := ctx.Value(stdoutKey{})
	if writer == nil {
		writer = io.Discard
	}

	return writer.(io.Writer)
}

func StdoutToContext(ctx context.Context, w io.Writer) context.Context {
	return context.WithValue(ctx, stdoutKey{}, w)
}

// IsStdoutKey reports whether key is the context key StdoutToContext stores
// its writer under.
//
// A context.Context implementation that caches the writers in effect where it
// was created can answer lookups for them itself (returning the writer from
// StdoutFromContext), so they stop there instead of walking the rest of a
// possibly deep context chain.
func IsStdoutKey(key any) bool {
	_, ok := key.(stdoutKey)
	return ok
}

// IsStderrKey reports whether key is the context key StderrToContext stores
// its writer under. See IsStdoutKey.
func IsStderrKey(key any) bool {
	_, ok := key.(stderrKey)
	return ok
}
