//go:build !windows

package platform

func EnsureSingleInstance(mutexName, windowTitle string) bool {
	_ = mutexName
	_ = windowTitle
	return true
}
