package dang

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParameterTypesWithoutConstructorSignature(t *testing.T) {
	// Type returns an hm.Type interface containing a nil *hm.FunctionType.
	constructor := &ConstructorFunction{}
	require.Empty(t, parameterTypes(constructor))
}
