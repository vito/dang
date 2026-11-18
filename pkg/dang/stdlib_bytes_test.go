package dang

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStringBytes(t *testing.T) {
	for _, text := range []string{"", "hello", "café", "日本語 🌍", "e\u0301", "\x00\r\n"} {
		t.Run(strconv.Quote(text), func(t *testing.T) {
			var bytes []string
			for i := range len(text) {
				bytes = append(bytes, strconv.Itoa(int(text[i])))
			}
			require.NoError(t, evalExample(fmt.Sprintf(`
let text: String! = %q
let bytes: [Int!]! = text.bytes
assert { bytes == [%s] }
let restored: String! = String.fromBytes(bytes: bytes)
assert { restored == text }
`, text, strings.Join(bytes, ", "))))
		})
	}
	require.NoError(t, evalExample(`
assert { String.fromBytes([]) == "" }
let construct = &String.fromBytes
assert { construct([65]) == "A" }
let read = &"é".bytes
assert { read() == [195, 169] }
`))
}

func TestStringBytesPreserveAllValues(t *testing.T) {
	var raw []byte
	var values []string
	for n := range 256 {
		raw = append(raw, byte(n))
		values = append(values, strconv.Itoa(n))
	}
	// Compare against base64 independently, not only the mutually inverse
	// primitives: both must preserve NUL and invalid UTF-8 byte-for-byte.
	require.NoError(t, evalExample(fmt.Sprintf(`
let bytes: [Int!]! = [%s]
let encoded = %q
let text = String.fromBytes(bytes)
assert { text.bytes == bytes }
assert { text.toBase64 == encoded }
assert { encoded.fromBase64.bytes == bytes }
assert { String.fromBytes(encoded.fromBase64.bytes) == encoded.fromBase64 }
assert { String.fromBytes([255, 192, 128, 237, 160, 128]).bytes == [255, 192, 128, 237, 160, 128] }
`, strings.Join(values, ", "), base64.StdEncoding.EncodeToString(raw))))
}

func TestStringFromBytesRangeErrors(t *testing.T) {
	for _, n := range []int{-1, 256, -1000, 1000} {
		t.Run(strconv.Itoa(n), func(t *testing.T) {
			require.ErrorContains(t, evalExample(fmt.Sprintf(`String.fromBytes([65, %d])`, n)),
				fmt.Sprintf("eval: fromBytes: byte at index 1 must be between 0 and 255, got %d", n))
		})
	}
	require.NoError(t, evalExample(`assert { (String.fromBytes([256]) rescue "bad byte") == "bad byte" }`))
}

func TestStringBytesTypeErrors(t *testing.T) {
	for name, script := range map[string]string{
		"null list":        `String.fromBytes(null)`,
		"null element":     `String.fromBytes([null])`,
		"mixed null":       `String.fromBytes([65, null])`,
		"string element":   `String.fromBytes(["65"])`,
		"float element":    `String.fromBytes([65.0])`,
		"boolean element":  `String.fromBytes([true])`,
		"not list":         `String.fromBytes(65)`,
		"missing argument": `String.fromBytes()`,
		"static on value":  `"".fromBytes([65])`,
		"bytes on type":    `String.bytes`,
		"bytes result":     `let bytes: [String!]! = "A".bytes`,
		"string result":    `let text: [Int!]! = String.fromBytes([65])`,
	} {
		t.Run(name, func(t *testing.T) {
			require.ErrorContains(t, evalExample(script), "type:")
		})
	}
	// Nullable receivers follow ordinary member-selection null propagation.
	require.NoError(t, evalExample(`
let absent: String = null
let bytes: [Int!] = absent.bytes
assert { bytes == null }
`))
}
