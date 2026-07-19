package envvars

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestSaveStoresEnvVarsInPrivateFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file permission bits are validated on macOS/Linux")
	}
	dir := filepath.Join(t.TempDir(), "envvars")
	svc := NewEnvVarsService(dir)
	if err := svc.Set("API_KEY", "test-secret"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat envvars dir: %v", err)
	}
	if got := dirInfo.Mode().Perm(); got != 0o700 {
		t.Fatalf("envvars dir mode = %o, want 700", got)
	}
	fileInfo, err := os.Stat(filepath.Join(dir, "envvars.json"))
	if err != nil {
		t.Fatalf("stat envvars file: %v", err)
	}
	if got := fileInfo.Mode().Perm(); got != 0o600 {
		t.Fatalf("envvars file mode = %o, want 600", got)
	}
}
