package pty

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"amagi-codebox/internal/platform"
)

// Set AMAGI_CODEBOX_TEST_PWSH to the absolute pwsh.exe path to exercise the
// real Windows ConPTY/PowerShell boundary without touching any Pi settings.
func TestPiPowerShellLaunchThroughConPTY(t *testing.T) {
	pwsh := os.Getenv("AMAGI_CODEBOX_TEST_PWSH")
	if pwsh == "" {
		t.Skip("set AMAGI_CODEBOX_TEST_PWSH to a real pwsh.exe")
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "pi.cmd"), []byte("@exit /b 42\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Like the npm distribution, PowerShell resolves the ps1 before the cmd
	// shim. Emit the exact received argv without running the real agent.
	if err := os.WriteFile(filepath.Join(bin, "pi.ps1"), []byte(
		"foreach ($a in $args) { [Console]::WriteLine('PI_ARG:' + [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($a))) }"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	model := "测试 ‘x’ 'y' & $(z) \"quote\""
	spec, err := platform.NewCLIResolver(platform.CurrentCapabilities()).Resolve(platform.ResolveRequest{
		AppType: "pi", LaunchMode: "embedded", RequestedShellPath: pwsh, Env: os.Environ(), WorkDir: bin,
		CLIArgs: []string{"--model", model},
	})
	if err != nil {
		t.Fatal(err)
	}
	svc := NewService(nil)
	if auto := svc.StartupAutoCommand(spec); auto != "" {
		t.Fatalf("Pi must start without delayed PTY input, got %q", auto)
	}
	const id = "pwsh-pi-startup"
	started := time.Now()
	if _, err := svc.StartResolved(id, spec); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close(id) })
	want := "PI_ARG:" + base64.StdEncoding.EncodeToString([]byte(model))
	for time.Since(started) < 5*time.Second {
		history, err := svc.GetOutputHistory(id)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(history), want) {
			t.Logf("Pi shim received exact argv after %s", time.Since(started))
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	history, _ := svc.GetOutputHistory(id)
	t.Fatalf("Pi shim did not receive exact argv; ConPTY output: %q", history)
}
