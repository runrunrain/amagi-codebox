// Package fsutil provides shared filesystem primitives for private, atomic writes.
package fsutil

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// EnsurePrivateDir creates the parent directory of filePath with 0700
// permissions if it does not already exist.
func EnsurePrivateDir(filePath string) error {
	dir := filepath.Dir(filePath)
	info, err := os.Stat(dir)
	if err == nil {
		if !info.IsDir() {
			return fmt.Errorf("parent path %s is not a directory", dir)
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat directory %s: %w", dir, err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create directory %s: %w", dir, err)
	}
	return nil
}

// WritePrivateFile atomically writes data to path with 0600 permissions by
// writing, syncing, and closing a temporary file in the same directory before
// renaming. On Windows, if rename fails due to an open handle locking the
// target file, it falls back to direct overwrite and removes the temporary file.
func WritePrivateFile(path string, data []byte) error {
	if err := EnsurePrivateDir(path); err != nil {
		return err
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp.*")
	if err != nil {
		return fmt.Errorf("create temp file in %s: %w", dir, err)
	}
	tmpPath := tmp.Name()
	closed := false
	cleanup := true
	defer func() {
		if !closed {
			_ = tmp.Close()
		}
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		return fmt.Errorf("write temp file %s: %w", tmpPath, err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("sync temp file %s: %w", tmpPath, err)
	}
	if err := tmp.Close(); err != nil {
		closed = true
		return fmt.Errorf("close temp file %s: %w", tmpPath, err)
	}
	closed = true

	if err := os.Chmod(tmpPath, 0o600); err != nil {
		return fmt.Errorf("chmod temp file %s: %w", tmpPath, err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		if runtime.GOOS == "windows" {
			if overwriteErr := os.WriteFile(path, data, 0o600); overwriteErr == nil {
				_ = os.Chmod(path, 0o600)
				return nil
			}
		}
		return fmt.Errorf("replace file %s: %w", path, err)
	}
	cleanup = false
	return nil
}
