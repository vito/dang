package pitui

// Key constants for common terminal input sequences.
// These match the raw bytes received from a terminal in raw mode.
const (
	KeyCtrlA     = "\x01"
	KeyCtrlB     = "\x02"
	KeyCtrlC     = "\x03"
	KeyCtrlD     = "\x04"
	KeyCtrlE     = "\x05"
	KeyCtrlF     = "\x06"
	KeyCtrlG     = "\x07"
	KeyCtrlH     = "\x08" // Often backspace
	KeyTab       = "\x09"
	KeyCtrlJ     = "\x0a" // Often enter/newline
	KeyCtrlK     = "\x0b"
	KeyCtrlL     = "\x0c"
	KeyEnter     = "\x0d" // CR
	KeyCtrlN     = "\x0e"
	KeyCtrlO     = "\x0f"
	KeyCtrlP     = "\x10"
	KeyCtrlR     = "\x12"
	KeyCtrlT     = "\x14"
	KeyCtrlU     = "\x15"
	KeyCtrlW     = "\x17"
	KeyEscape    = "\x1b"
	KeyBackspace = "\x7f"

	// Arrow keys
	KeyUp    = "\x1b[A"
	KeyDown  = "\x1b[B"
	KeyRight = "\x1b[C"
	KeyLeft  = "\x1b[D"

	// Home/End
	KeyHome = "\x1b[H"
	KeyEnd  = "\x1b[F"
	// Alternate home/end
	KeyHome2 = "\x1b[1~"
	KeyEnd2  = "\x1b[4~"

	// Delete
	KeyDelete = "\x1b[3~"

	// Alt+arrow (word movement)
	KeyAltLeft  = "\x1b[1;3D"
	KeyAltRight = "\x1b[1;3C"
	KeyAltB     = "\x1bb" // Alt+B (word back)
	KeyAltF     = "\x1bf" // Alt+F (word forward)
	KeyAltD     = "\x1bd" // Alt+D (delete word forward)

	// Shift+Tab
	KeyShiftTab = "\x1b[Z"

	// Ctrl+arrow
	KeyCtrlLeft  = "\x1b[1;5D"
	KeyCtrlRight = "\x1b[1;5C"
)

// HasPrefix reports whether data starts with prefix.
func HasPrefix(data []byte, prefix string) bool {
	if len(data) < len(prefix) {
		return false
	}
	for i := 0; i < len(prefix); i++ {
		if data[i] != prefix[i] {
			return false
		}
	}
	return true
}

// Matches reports whether data exactly equals the key sequence.
func Matches(data []byte, key string) bool {
	return string(data) == key
}
