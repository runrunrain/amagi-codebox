//go:build !windows

package updater

import "fmt"

func startUpdatedExecutable(exePath string) error {
	return fmt.Errorf("starting updated executable is not implemented for %s", exePath)
}
