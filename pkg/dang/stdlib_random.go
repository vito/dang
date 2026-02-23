package dang

import (
	"context"
	"crypto/rand"
	"fmt"
	mrand "math/rand/v2"

	"github.com/google/uuid"
)

// RandomModule is the "Random" namespace for random value generation
var RandomModule = NewModule("Random", ObjectKind)

// UUIDModule is the "UUID" namespace for UUID generation
var UUIDModule = NewModule("UUID", ObjectKind)

// registerRandomAndUUID is called from registerStdlib to ensure correct init ordering
func registerRandomAndUUID() {
	registerRandom()
	registerUUID()
}

func registerRandom() {
	RandomModule.SetModuleDocString("functions for generating random values")

	// Random.int(min: Int!, max: Int!) -> Int!
	StaticMethod(RandomModule, "int").
		Doc("generates a random integer between min (inclusive) and max (exclusive)").
		Params("min", NonNull(IntType), "max", NonNull(IntType)).
		Returns(NonNull(IntType)).
		Impl(func(ctx context.Context, args Args) (Value, error) {
			min := args.GetInt("min")
			max := args.GetInt("max")
			if min >= max {
				return nil, fmt.Errorf("Random.int: min (%d) must be less than max (%d)", min, max)
			}
			return ToValue(min + mrand.IntN(max-min))
		})

	// Random.float() -> Float!
	StaticMethod(RandomModule, "float").
		Doc("generates a random float between 0.0 (inclusive) and 1.0 (exclusive)").
		Returns(NonNull(FloatType)).
		Impl(func(ctx context.Context, args Args) (Value, error) {
			return ToValue(mrand.Float64())
		})

	// Random.string() -> String!
	StaticMethod(RandomModule, "string").
		Doc("generates a cryptographically random base32 string with at least 128 bits of entropy").
		Returns(NonNull(StringType)).
		Impl(func(ctx context.Context, args Args) (Value, error) {
			return ToValue(rand.Text())
		})
}

func registerUUID() {
	UUIDModule.SetModuleDocString("functions for generating UUIDs")

	// UUID.v4() -> String!
	StaticMethod(UUIDModule, "v4").
		Doc("generates a random UUID v4 string").
		Returns(NonNull(StringType)).
		Impl(func(ctx context.Context, args Args) (Value, error) {
			return ToValue(uuid.New().String())
		})

	// UUID.v7() -> String!
	StaticMethod(UUIDModule, "v7").
		Doc("generates a time-ordered UUID v7 string").
		Returns(NonNull(StringType)).
		Impl(func(ctx context.Context, args Args) (Value, error) {
			id, err := uuid.NewV7()
			if err != nil {
				return nil, fmt.Errorf("UUID.v7: %w", err)
			}
			return ToValue(id.String())
		})
}
