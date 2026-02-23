package dang

import (
	"context"
	crand "crypto/rand"
	"fmt"
	"math/big"
	"math/rand/v2"

	"github.com/google/uuid"
	"github.com/vito/dang/pkg/hm"
)

// RandomModule is the "Random" namespace for random value generation
var RandomModule = NewModule("Random", ObjectKind)

// CharsetEnum is the Random.Charset enum type
var CharsetEnum = NewModule("Charset", EnumKind)

// UUIDModule is the "UUID" namespace for UUID generation
var UUIDModule = NewModule("UUID", ObjectKind)

// charsetValues maps enum value names to their character sets
var charsetChars = map[string]string{
	"ALPHANUMERIC": "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789",
	"ALPHA":        "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ",
	"NUMERIC":      "0123456789",
	"HEX":          "0123456789abcdef",
}

// Ordered list for the values() method and default
var charsetNames = []string{"ALPHANUMERIC", "ALPHA", "NUMERIC", "HEX"}

// registerRandomAndUUID is called from registerStdlib to ensure correct init ordering
func registerRandomAndUUID() {
	registerRandom()
	registerUUID()
}

func registerRandom() {
	RandomModule.SetModuleDocString("functions for generating random values")

	// Set up Charset enum inside Random
	RandomModule.AddClass("Charset", CharsetEnum)
	RandomModule.Add("Charset", hm.NewScheme(nil, NonNull(CharsetEnum)))
	RandomModule.SetVisibility("Charset", PublicVisibility)

	for _, name := range charsetNames {
		CharsetEnum.Add(name, hm.NewScheme(nil, NonNull(CharsetEnum)))
		CharsetEnum.SetVisibility(name, PublicVisibility)
	}

	// Add values() method to the enum
	valuesType := hm.NewScheme(nil, NonNull(ListType{NonNull(CharsetEnum)}))
	CharsetEnum.Add("values", valuesType)
	CharsetEnum.SetVisibility("values", PublicVisibility)

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
			return ToValue(min + rand.IntN(max-min))
		})

	// Random.float() -> Float!
	StaticMethod(RandomModule, "float").
		Doc("generates a random float between 0.0 (inclusive) and 1.0 (exclusive)").
		Returns(NonNull(FloatType)).
		Impl(func(ctx context.Context, args Args) (Value, error) {
			return ToValue(rand.Float64())
		})

	// Random.string(length: Int!, charset: Charset! = ALPHANUMERIC) -> String!
	StaticMethod(RandomModule, "string").
		Doc("generates a random string of the given length using the specified character set").
		Params(
			"length", NonNull(IntType),
			"charset", NonNull(CharsetEnum), EnumValue{Val: "ALPHANUMERIC", EnumType: CharsetEnum},
		).
		Returns(NonNull(StringType)).
		Impl(func(ctx context.Context, args Args) (Value, error) {
			length := args.GetInt("length")
			charset := args.GetEnum("charset")

			if length < 0 {
				return nil, fmt.Errorf("Random.string: length must be non-negative, got %d", length)
			}
			if length == 0 {
				return ToValue("")
			}

			chars, ok := charsetChars[charset]
			if !ok {
				return nil, fmt.Errorf("Random.string: unknown charset %q", charset)
			}

			result := make([]byte, length)
			for i := range result {
				idx, err := crand.Int(crand.Reader, big.NewInt(int64(len(chars))))
				if err != nil {
					return nil, fmt.Errorf("Random.string: %w", err)
				}
				result[i] = chars[idx.Int64()]
			}
			return ToValue(string(result))
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
