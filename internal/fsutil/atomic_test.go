package fsutil

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func assertNoTempFiles(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir(%s): %v", dir, err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".tmp") {
			t.Fatalf("unexpected leftover temp file %q in %s", entry.Name(), dir)
		}
	}
}

func TestEnsurePrivateDir_PositiveAndNegative(t *testing.T) {
	root := t.TempDir()
	nestedFile := filepath.Join(root, "private", "nested", "secret.json")
	if err := EnsurePrivateDir(nestedFile); err != nil {
		t.Fatalf("EnsurePrivateDir(missing): %v", err)
	}
	dir := filepath.Dir(nestedFile)
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		t.Fatalf("expected created directory at %s (info=%v, err=%v)", dir, info, err)
	}
	if runtime.GOOS != "windows" {
		if perm := info.Mode().Perm(); perm != 0o700 {
			t.Fatalf("created dir perm = %04o, want 0700", perm)
		}
	}

	// Idempotent on existing directory.
	if err := EnsurePrivateDir(nestedFile); err != nil {
		t.Fatalf("EnsurePrivateDir(existing): %v", err)
	}

	// Negative: parent path is an existing regular file.
	blocker := filepath.Join(root, "blocker")
	if err := os.WriteFile(blocker, []byte("file-not-dir"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := EnsurePrivateDir(filepath.Join(blocker, "child.json")); err == nil {
		t.Fatal("expected error when parent path is an existing file")
	}
	if err := EnsurePrivateDir(filepath.Join(blocker, "sub", "child.json")); err == nil {
		t.Fatal("expected error when ancestor path is an existing file")
	}
}

func TestWritePrivateFile_NewAndOverwrite(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sub")
	target := filepath.Join(dir, "config.json")

	// 1. Write new file (also ensures parent directory).
	first := []byte("{\"token\":\"alpha\"}\n")
	if err := WritePrivateFile(target, first); err != nil {
		t.Fatalf("WritePrivateFile(new): %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("ReadFile(new): %v", err)
	}
	if !bytes.Equal(got, first) {
		t.Fatalf("new file bytes = %q, want %q", got, first)
	}
	assertNoTempFiles(t, dir)
	if runtime.GOOS != "windows" {
		info, err := os.Stat(target)
		if err != nil {
			t.Fatal(err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Fatalf("new file perm = %04o, want 0600", perm)
		}
	}

	// 2. Overwrite existing file with permissive mode (e.g. 0644) and different length.
	if err := os.Chmod(target, 0o644); err != nil {
		t.Fatal(err)
	}
	second := []byte("{\"token\":\"beta-longer-value\"}\n")
	if err := WritePrivateFile(target, second); err != nil {
		t.Fatalf("WritePrivateFile(overwrite longer): %v", err)
	}
	got, err = os.ReadFile(target)
	if err != nil {
		t.Fatalf("ReadFile(overwrite longer): %v", err)
	}
	if !bytes.Equal(got, second) {
		t.Fatalf("overwritten bytes = %q, want %q", got, second)
	}
	assertNoTempFiles(t, dir)

	third := []byte("{}\n")
	if err := WritePrivateFile(target, third); err != nil {
		t.Fatalf("WritePrivateFile(overwrite shorter): %v", err)
	}
	got, err = os.ReadFile(target)
	if err != nil {
		t.Fatalf("ReadFile(overwrite shorter): %v", err)
	}
	if !bytes.Equal(got, third) {
		t.Fatalf("short overwritten bytes = %q, want %q", got, third)
	}
	assertNoTempFiles(t, dir)
	if runtime.GOOS != "windows" {
		info, err := os.Stat(target)
		if err != nil {
			t.Fatal(err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Fatalf("overwritten file perm = %04o, want 0600", perm)
		}
	}
}

func TestWritePrivateFile_ParentIsExistingFile(t *testing.T) {
	root := t.TempDir()
	blocker := filepath.Join(root, "blocker")
	orig := []byte("keep-intact")
	if err := os.WriteFile(blocker, orig, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := WritePrivateFile(filepath.Join(blocker, "config.json"), []byte("new")); err == nil {
		t.Fatal("expected error when parent path is an existing file")
	}
	if err := WritePrivateFile(filepath.Join(blocker, "nested", "config.json"), []byte("new")); err == nil {
		t.Fatal("expected error when ancestor path is an existing file")
	}

	got, err := os.ReadFile(blocker)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, orig) {
		t.Fatalf("blocker content mutated = %q, want %q", got, orig)
	}
	assertNoTempFiles(t, root)
}

func TestWritePrivateFile_TargetIsDirectoryCleansTempFile(t *testing.T) {
	root := t.TempDir()
	targetDir := filepath.Join(root, "existing-dir")
	if err := os.MkdirAll(targetDir, 0o700); err != nil {
		t.Fatal(err)
	}

	if err := WritePrivateFile(targetDir, []byte("cannot-replace-dir")); err == nil {
		t.Fatal("expected error when target path is an existing directory")
	}
	assertNoTempFiles(t, root)
}
