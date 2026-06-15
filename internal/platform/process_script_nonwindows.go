//go:build !windows

package platform

// wrapWindowsScript is a no-op on non-Windows platforms.
func wrapWindowsScript(spec CommandSpec) CommandSpec {
	return spec
}
