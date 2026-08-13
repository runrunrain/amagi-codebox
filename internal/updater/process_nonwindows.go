//go:build !windows

package updater

func processExists(pid int) bool {
	return false
}

func scheduleDeleteAfterReboot(path string) error {
	return nil
}
