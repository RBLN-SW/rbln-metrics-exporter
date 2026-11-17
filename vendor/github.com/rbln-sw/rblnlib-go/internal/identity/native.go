package identity

import (
	"regexp"
	"strconv"
	"strings"
)

// NativePFOrdinal is independent of usable-device and group-local ordinals.
func NativePFOrdinal(name string) (uint32, bool) {
	text, ok := strings.CutPrefix(name, "rbln")
	if !ok {
		return 0, false
	}
	n, err := strconv.ParseUint(text, 10, 32)
	return uint32(n), err == nil && strconv.FormatUint(n, 10) == text
}

var nativeFunctionPattern = regexp.MustCompile(`^rbln[0-9]+(?:-[0-9]+)?$`)

func NativeFunction(name string) bool { return nativeFunctionPattern.MatchString(name) }
