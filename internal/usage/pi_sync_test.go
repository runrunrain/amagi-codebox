package usage

import (
	"os"
	"path/filepath"
	"testing"
)

// TestEnumeratePiSessionFilesDedupsSymlink (P2-1) verifies that when the two Pi
// session roots alias the same physical file (e.g. via a symlink), the file is
// enumerated only once instead of double-counted.
func TestEnumeratePiSessionFilesDedupsSymlink(t *testing.T) {
	configDir := t.TempDir()
	home := t.TempDir()

	// A real session file under the codebox pi-runtime root.
	codeboxSessions := filepath.Join(configDir, "pi-runtime", "sessions", "--proj--")
	if err := os.MkdirAll(codeboxSessions, 0o755); err != nil {
		t.Fatal(err)
	}
	real := filepath.Join(codeboxSessions, "1734000000000_uuid.jsonl")
	if err := os.WriteFile(real, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// The default root points at the SAME physical directory via a symlink, so
	// the file surfaces under both roots with different logical paths.
	defaultRoot := filepath.Join(home, ".pi", "agent", "sessions")
	if err := os.MkdirAll(filepath.Dir(defaultRoot), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(codeboxSessions, defaultRoot); err != nil {
		t.Skipf("symlink not supported on this platform: %v", err)
	}

	got := enumeratePiSessionFiles(configDir, home)

	count := 0
	for _, p := range got {
		if p == real {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("aliased file enumerated %d times, want 1 (canonical dedup); got=%v", count, got)
	}
}

// TestEnumeratePiSessionFilesBothRootsDistinct confirms that two genuinely
// distinct files (no symlink) are both returned.
func TestEnumeratePiSessionFilesBothRootsDistinct(t *testing.T) {
	configDir := t.TempDir()
	home := t.TempDir()

	mk := func(dir, name string) string {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	a := mk(filepath.Join(configDir, "pi-runtime", "sessions", "d"), "a.jsonl")
	b := mk(filepath.Join(home, ".pi", "agent", "sessions", "d"), "b.jsonl")

	got := enumeratePiSessionFiles(configDir, home)
	want := map[string]bool{a: true, b: true}
	for _, p := range got {
		delete(want, p)
	}
	if len(want) != 0 {
		t.Fatalf("missing expected files after enumeration: %v; got=%v", want, got)
	}
}
