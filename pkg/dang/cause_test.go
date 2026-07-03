package dang

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vito/dang/v2/pkg/hm"
	"github.com/vito/dang/v2/pkg/ioctx"
)

// evalForError runs a snippet through parse → infer → eval and returns the
// error from evaluation, un-translated, so tests can inspect the raw
// *RaisedError wrapper (RunFile would flatten it at the boundary).
func evalForError(t *testing.T, src string) error {
	t.Helper()

	parsed, err := ParseWithRecovery("cause_test", []byte(src))
	require.NoError(t, err, "parse")
	file, ok := parsed.(*FileBlock)
	require.True(t, ok, "unexpected parse result %T", parsed)

	typeScope, valueScope := BuildScopesFromImports("", nil)
	fresh := hm.NewSimpleFresher()
	_, err = InferFormsWithPhases(context.Background(), file.Forms, typeScope, fresh)
	require.NoError(t, err, "infer")

	var out bytes.Buffer
	ctx := ioctx.StdoutToContext(context.Background(), &out)
	ctx = ioctx.StderrToContext(ctx, &out)
	for _, node := range file.Forms {
		if _, err := EvalNode(ctx, valueScope, node); err != nil {
			return err
		}
	}
	return nil
}

// raisedFrom unwraps the evaluation error to the *RaisedError wrapper.
func raisedFrom(t *testing.T, err error) *RaisedError {
	t.Helper()
	require.Error(t, err)
	var raised *RaisedError
	require.True(t, errors.As(err, &raised), "expected a *RaisedError, got %T: %v", err, err)
	return raised
}

func errMessage(t *testing.T, raised *RaisedError) string {
	t.Helper()
	obj, ok := raised.Value.(*Object)
	require.True(t, ok)
	msg, found := obj.lookupValue("message")
	require.True(t, found)
	return msg.String()
}

func TestImplicitCauseRecordedInRescueArm(t *testing.T) {
	err := evalForError(t, `
{ raise "original failure" } rescue {
  e: Error => raise "wrapped: " + e.message
}
`)
	raised := raisedFrom(t, err)
	require.Equal(t, "wrapped: original failure", errMessage(t, raised))
	require.NotNil(t, raised.Cause, "raise during a rescue arm should record the in-flight error")
	require.Equal(t, "original failure", errMessage(t, raised.Cause))
	require.Nil(t, raised.Cause.Cause)
}

func TestImplicitCauseAppliesToRaisedObjects(t *testing.T) {
	err := evalForError(t, `
type WrapError implements Error {
  message: String!
}

{ raise "root" } rescue {
  e: Error => raise WrapError(message: "wrapper")
}
`)
	raised := raisedFrom(t, err)
	require.Equal(t, "wrapper", errMessage(t, raised))
	require.NotNil(t, raised.Cause)
	require.Equal(t, "root", errMessage(t, raised.Cause))
}

func TestPlainReRaiseDoesNotSelfCause(t *testing.T) {
	err := evalForError(t, `
{ raise "pass me along" } rescue {
  e: Error => raise e
}
`)
	raised := raisedFrom(t, err)
	require.Equal(t, "pass me along", errMessage(t, raised))
	require.Nil(t, raised.Cause, "re-raising the rescued error must not self-cause")
}

func TestExplicitCauseFieldWins(t *testing.T) {
	err := evalForError(t, `
type ChainedError implements Error {
  message: String!
  cause: Error
}

{ raise "root" } rescue {
  e: Error => raise ChainedError(message: "wrapper", cause: e)
}
`)
	raised := raisedFrom(t, err)
	require.Equal(t, "wrapper", errMessage(t, raised))
	require.Nil(t, raised.Cause, "an explicit non-null cause field suppresses the implicit record")
}

func TestNullExplicitCauseStillRecordsImplicit(t *testing.T) {
	err := evalForError(t, `
type ChainedError implements Error {
  message: String!
  cause: Error
}

{ raise "root" } rescue {
  e: Error => raise ChainedError(message: "wrapper")
}
`)
	raised := raisedFrom(t, err)
	require.NotNil(t, raised.Cause, "a null cause field is no explicit cause; the implicit record applies")
	require.Equal(t, "root", errMessage(t, raised.Cause))
}

func TestCauseChainsAcrossNestedRescues(t *testing.T) {
	err := evalForError(t, `
{
  { raise "innermost" } rescue {
    e: Error => raise "middle"
  }
} rescue {
  e: Error => raise "outermost"
}
`)
	raised := raisedFrom(t, err)
	require.Equal(t, "outermost", errMessage(t, raised))
	require.NotNil(t, raised.Cause)
	require.Equal(t, "middle", errMessage(t, raised.Cause))
	require.NotNil(t, raised.Cause.Cause, "the middle raise happened during the innermost rescue")
	require.Equal(t, "innermost", errMessage(t, raised.Cause.Cause))
	require.Nil(t, raised.Cause.Cause.Cause)
}

func TestImplicitCauseAppliesInFallbackForm(t *testing.T) {
	err := evalForError(t, `
boom: Int! { raise "fallback exploded" }
orig: Int! { raise "original" }

orig rescue boom
`)
	raised := raisedFrom(t, err)
	require.Equal(t, "fallback exploded", errMessage(t, raised))
	require.NotNil(t, raised.Cause, "the fallback expression evaluates in the rescue's dynamic extent")
	require.Equal(t, "original", errMessage(t, raised.Cause))
}

func TestNoCauseOutsideRescue(t *testing.T) {
	err := evalForError(t, fmt.Sprintf("raise %q", "standalone"))
	raised := raisedFrom(t, err)
	require.Nil(t, raised.Cause)
}
