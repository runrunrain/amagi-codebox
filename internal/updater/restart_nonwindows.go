//go:build !windows

package updater

import "fmt"

func launchWindowsUpdateHelper(stagedExePath string, currentExePath string, currentPID int) error {
	return fmt.Errorf("Windows update helper is not available on this platform")
}
