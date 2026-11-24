package v2rayn

import (
	"os"
	"path/filepath"
	"testing"
)

func writeInstallFile(t *testing.T, dir, relativePath string) string {
	t.Helper()
	path := filepath.Join(dir, relativePath)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLocateExplicitPath(t *testing.T) {
	path := writeInstallFile(t, t.TempDir(), "数据 #1.db")
	got, err := Locate(" " + path + " ")
	if err != nil || got != path {
		t.Fatalf("Locate = %q, %v; want %q", got, err, path)
	}
	for _, invalid := range []string{filepath.Dir(path), path + ".missing"} {
		if got, err := Locate(invalid); err == nil || got != "" {
			t.Fatalf("manual path %q must fail without falling back: %q, %v", invalid, got, err)
		}
	}
}

func TestFirstFileSkipsMissingAndNonFiles(t *testing.T) {
	missing, invalid, portable, standard := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(invalid, databaseFile), 0700); err != nil {
		t.Fatal(err)
	}
	want := writeInstallFile(t, portable, databaseFile)
	writeInstallFile(t, standard, databaseFile)
	if got := firstFile([]string{missing, invalid, portable, standard}, databaseFile); got != want {
		t.Fatalf("firstFile = %q, want %q", got, want)
	}
}
