package platform

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCodexHomeDirPrefersExplicitEnvironment(t *testing.T) {
	want := filepath.Join(t.TempDir(), "custom-codex")
	got, err := CodexHomeDir([]string{"HOME=" + t.TempDir(), "CODEX_HOME=" + want})
	if err != nil {
		t.Fatalf("CodexHomeDir: %v", err)
	}
	if got != want {
		t.Fatalf("CodexHomeDir = %q, want %q", got, want)
	}
}

func TestCodexHomeDirFallsBackToUserCodexDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if os.Getenv("HOME") != home {
		t.Fatal("test HOME was not applied")
	}
	got, err := CodexHomeDir([]string{"HOME=" + home})
	if err != nil {
		t.Fatalf("CodexHomeDir: %v", err)
	}
	want := filepath.Join(home, ".codex")
	if got != want {
		t.Fatalf("CodexHomeDir = %q, want %q", got, want)
	}
}
