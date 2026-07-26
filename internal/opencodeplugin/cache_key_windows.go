//go:build windows

package opencodeplugin

import "strings"

// openCodeCacheKey mirrors packages/core/src/npm.ts in OpenCode. Windows
// rejects these characters in directory names, so OpenCode replaces them.
func openCodeCacheKey(spec string) string {
	return strings.Map(func(char rune) rune {
		if char < 32 || strings.ContainsRune(`<>:"|?*`, char) {
			return '_'
		}
		return char
	}, spec)
}
