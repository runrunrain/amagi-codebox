package v2rayn

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Use the test binary itself as a harmless process with the portable app's
// image name. No installed v2rayN, credentials or filesystem-wide scan needed.
func TestRunningPortableInstall(t *testing.T) {
	const child = "CODEBOX_V2RAYN_TEST_CHILD"
	if os.Getenv(child) == "1" {
		var b [1]byte
		_, _ = os.Stdin.Read(b[:])
		return
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	image, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "便携版 #1")
	path := writeInstallFile(t, root, databaseFile)
	exe := filepath.Join(root, "V2rayN.exe")
	if err := os.WriteFile(exe, image, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "-test.run=^TestRunningPortableInstall$")
	cmd.Env = append(os.Environ(), child+"=1")
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		_ = input.Close()
		t.Fatal(err)
	}
	defer func() { _ = input.Close(); _ = cmd.Wait() }()
	var found string
	for ctx.Err() == nil && found == "" {
		for _, candidate := range runningInstallRoots() {
			if strings.EqualFold(candidate, root) {
				found = candidate
				break
			}
		}
		if found == "" {
			time.Sleep(20 * time.Millisecond)
		}
	}
	if got := firstFile([]string{found}, databaseFile); !strings.EqualFold(got, path) {
		t.Fatalf("running portable database = %q, want %q", got, path)
	}
	wantCore := writeInstallFile(t, root, bundledCoreFile)
	if got := firstFile([]string{found}, bundledCoreFile); !strings.EqualFold(got, wantCore) {
		t.Fatalf("running portable core = %q, want %q", got, wantCore)
	}
}

func TestLocalV2rayNAutoDiscovery(t *testing.T) {
	want := os.Getenv("V2RAYN_TEST_DATABASE")
	if want == "" || os.Getenv("V2RAYN_TEST_AUTO") != "1" {
		t.Skip("set V2RAYN_TEST_AUTO=1 and V2RAYN_TEST_DATABASE for real Windows auto-discovery")
	}
	got, err := Locate("")
	if err != nil || !strings.EqualFold(got, filepath.Clean(want)) {
		t.Fatalf("auto-discovered database = %q, %v; want %q", got, err, want)
	}
	core := FindBundledCore()
	if core == "" {
		t.Fatal("bundled sing-box not detected")
	}
	t.Logf("database: %s; core: %s", got, core)
}
