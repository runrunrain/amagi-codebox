package updater

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const windowsUpdateHelperFlag = "--amagi-update-helper"

const (
	windowsUpdateWaitTimeout = 60 * time.Second
	windowsUpdatePollDelay   = 200 * time.Millisecond
)

// MaybeRunWindowsUpdateHelper intercepts the private updater helper command
// before Wails starts. It returns handled=true whenever the helper flag was
// present, including malformed invocations that must not launch the normal UI.
func MaybeRunWindowsUpdateHelper(args []string) (handled bool, err error) {
	if len(args) < 2 || args[1] != windowsUpdateHelperFlag {
		return false, nil
	}
	if runtime.GOOS != "windows" {
		return true, fmt.Errorf("update helper is only supported on Windows")
	}
	if len(args) != 5 {
		return true, fmt.Errorf("invalid update helper arguments")
	}

	pid, err := strconv.Atoi(args[2])
	if err != nil || pid <= 0 {
		return true, fmt.Errorf("invalid update helper pid %q", args[2])
	}
	if err := runWindowsUpdateHelper(pid, args[3], args[4]); err != nil {
		return true, err
	}
	return true, nil
}

func runWindowsUpdateHelper(parentPID int, currentExePath string, stagedExePath string) error {
	currentExePath, err := filepath.Abs(currentExePath)
	if err != nil {
		return fmt.Errorf("resolve current executable: %w", err)
	}
	stagedExePath, err = filepath.Abs(stagedExePath)
	if err != nil {
		return fmt.Errorf("resolve staged executable: %w", err)
	}
	if !strings.EqualFold(filepath.Ext(currentExePath), ".exe") || !strings.EqualFold(filepath.Ext(stagedExePath), ".exe") {
		return fmt.Errorf("update helper only accepts .exe paths")
	}
	if strings.EqualFold(currentExePath, stagedExePath) {
		return fmt.Errorf("staged and current executable paths must differ")
	}
	if _, err := os.Stat(stagedExePath); err != nil {
		return fmt.Errorf("stat staged executable: %w", err)
	}

	deadline := time.Now().Add(windowsUpdateWaitTimeout)
	for processExists(parentPID) {
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for process %d to exit", parentPID)
		}
		time.Sleep(windowsUpdatePollDelay)
	}

	backupPath := currentExePath + ".old"
	if err := removeFileIfExists(backupPath); err != nil {
		return fmt.Errorf("remove old backup: %w", err)
	}
	if err := renameWithRetry(currentExePath, backupPath); err != nil {
		return fmt.Errorf("backup current executable: %w", err)
	}
	if err := copyFile(stagedExePath, currentExePath); err != nil {
		_ = os.Remove(currentExePath)
		_ = os.Rename(backupPath, currentExePath)
		return fmt.Errorf("replace executable: %w", err)
	}

	if err := startUpdatedExecutableFromHelper(currentExePath); err != nil {
		_ = os.Remove(currentExePath)
		_ = os.Rename(backupPath, currentExePath)
		return fmt.Errorf("start updated executable: %w", err)
	}
	// The helper is the staged executable itself, so Windows may still have it
	// locked here. Schedule best-effort deletion at reboot to avoid accumulating
	// one temp executable per successful update.
	_ = scheduleDeleteAfterReboot(stagedExePath)
	return nil
}

func renameWithRetry(oldPath string, newPath string) error {
	var lastErr error
	for attempt := 0; attempt < 20; attempt++ {
		if err := os.Rename(oldPath, newPath); err == nil {
			return nil
		} else {
			lastErr = err
		}
		time.Sleep(100 * time.Millisecond)
	}
	return lastErr
}

func removeFileIfExists(path string) error {
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func startUpdatedExecutableFromHelper(exePath string) error {
	cmd := exec.Command(exePath)
	cmd.Dir = filepath.Dir(exePath)
	return cmd.Start()
}
