package dang

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vito/dang/v2/pkg/hm"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	semconv "go.opentelemetry.io/otel/semconv/v1.39.0"
)

// evalTraced evaluates src, as file assert_test.dang, under a recording
// parent span, returning the assert spans it emitted.
func evalTraced(t *testing.T, src string) ([]sdktrace.ReadOnlySpan, error) {
	t.Helper()
	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	ctx, parent := tp.Tracer("test").Start(context.Background(), "parent")

	parsed, err := ParseWithRecovery("assert_test.dang", []byte(src), GlobalStore("filePath", "assert_test.dang"))
	require.NoError(t, err, "parse")
	file, ok := parsed.(*FileBlock)
	require.True(t, ok, "unexpected parse result %T", parsed)
	typeScope, valueScope := BuildScopesFromImports("", nil)
	_, err = InferFormsWithPhases(ctx, file.Forms, typeScope, hm.NewSimpleFresher())
	require.NoError(t, err, "infer")
	ctx = WithEvalContext(ctx, NewEvalContext("assert_test.dang", src))
	for _, node := range file.Forms {
		if _, err = EvalNode(ctx, valueScope, node); err != nil {
			break
		}
	}
	parent.End()

	var spans []sdktrace.ReadOnlySpan
	for _, span := range recorder.Ended() {
		if span.Name() == "parent" {
			continue
		}
		require.Equal(t, parent.SpanContext().SpanID(), span.Parent().SpanID(), "assert span should be a child of the current span")
		spans = append(spans, span)
	}
	return spans, err
}

func spanAttrs(span sdktrace.ReadOnlySpan) map[attribute.Key]attribute.Value {
	attrs := map[attribute.Key]attribute.Value{}
	for _, kv := range span.Attributes() {
		attrs[kv.Key] = kv.Value
	}
	return attrs
}

func TestAssertSpanPass(t *testing.T) {
	spans, err := evalTraced(t, "let x = 1\nassert { x + 1 == 2 }\nassert(\"x is one\") { x == 1 }\n")
	require.NoError(t, err)
	require.Len(t, spans, 2)

	expr := spans[0]
	require.Equal(t, "x + 1 == 2", expr.Name())
	require.Equal(t, codes.Ok, expr.Status().Code)
	attrs := spanAttrs(expr)
	require.Equal(t, "x + 1 == 2", attrs[semconv.TestCaseNameKey].AsString())
	require.Equal(t, semconv.TestCaseResultStatusPass.Value, attrs[semconv.TestCaseResultStatusKey])
	require.True(t, attrs[uiBoundaryAttr].AsBool())
	require.Equal(t, "assert_test.dang", attrs[semconv.CodeFilePathKey].AsString())
	require.EqualValues(t, 2, attrs[semconv.CodeLineNumberKey].AsInt64())
	require.EqualValues(t, 10, attrs[semconv.CodeColumnNumberKey].AsInt64())

	msg := spans[1]
	require.Equal(t, "x is one", msg.Name())
	require.Equal(t, "x is one", spanAttrs(msg)[semconv.TestCaseNameKey].AsString())
	require.EqualValues(t, 3, spanAttrs(msg)[semconv.CodeLineNumberKey].AsInt64())
}

func TestAssertSpanFail(t *testing.T) {
	spans, err := evalTraced(t, "let xs = [1, 2]\nassert { xs.length == 3 }\n")
	require.Error(t, err)
	var assertion *AssertionError
	require.ErrorAs(t, err, &assertion)
	require.Len(t, spans, 1)

	span := spans[0]
	require.Equal(t, "xs.length == 3", span.Name())
	require.Equal(t, codes.Error, span.Status().Code)
	require.Equal(t, assertion.Error(), span.Status().Description)
	require.Contains(t, span.Status().Description, "Expression: xs.length == 3")
	attrs := spanAttrs(span)
	require.Equal(t, "xs.length == 3", attrs[semconv.TestCaseNameKey].AsString())
	require.Equal(t, semconv.TestCaseResultStatusFail.Value, attrs[semconv.TestCaseResultStatusKey])
	require.EqualValues(t, 2, attrs[semconv.CodeLineNumberKey].AsInt64())

	require.Len(t, span.Events(), 1, "the failure should be recorded as an exception event")
	require.Equal(t, "exception", span.Events()[0].Name)
}

// A block that raises fails its assertion too.
func TestAssertSpanBlockError(t *testing.T) {
	spans, err := evalTraced(t, "assert { raise \"boom\" }\n")
	require.Error(t, err)
	require.Len(t, spans, 1)
	require.Equal(t, codes.Error, spans[0].Status().Code)
	require.Contains(t, spans[0].Status().Description, "boom")
	require.Equal(t, semconv.TestCaseResultStatusFail.Value, spanAttrs(spans[0])[semconv.TestCaseResultStatusKey])
}

// Without a recording span in the context, asserts emit nothing.
func TestAssertSpanNoTracer(t *testing.T) {
	_, err := evalWithContext(t, context.Background(), "assert { 1 == 1 }\n")
	require.NoError(t, err)
}
