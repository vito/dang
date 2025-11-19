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
