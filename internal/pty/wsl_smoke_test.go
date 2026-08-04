//go:build windows

package pty

import (
	"strings"
	"testing"

	"amagi-codebox/internal/platform"
)

// TestWSLRealMachineSmoke is a real-machine L1 smoke that uses the ACTUAL WSL
// probe (no stub) to confirm the end-to-end command line. It is skipped when no
// usable WSL distro is installed so it never fails on CI.
func TestWSLRealMachineSmoke(t *testing.T) {
	distro := platform.DefaultWSLDistro(nil)
	if distro == "" {
		t.Skip("no usable WSL distro on this machine; skipping real-machine smoke")
	}
	resolver := platform.NewCLIResolver(platform.CurrentCapabilities())
	spec, err := resolver.Resolve(platform.ResolveRequest{
		AppType:    "claudecode",
		LaunchMode: "embedded",
		WorkDir:    `D:\WorkPace`,
		Env:        []string{"ANTHROPIC_API_KEY=sk-smoke", "ANTHROPIC_BASE_URL=http://x"},
		CLIArgs:    []string{"--session-id", "smoke"},
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if spec.BootstrapMode != platform.BootstrapWSL {
		t.Fatalf("bootstrap = %q, want wsl", spec.BootstrapMode)
	}
	cmdLine, autoCmd := buildResolvedStartupPlan(spec, nil)
	t.Logf("distro=%s", distro)
	t.Logf("commandLine=%s", cmdLine)
	t.Logf("autoCmd=%q", autoCmd)
	t.Logf("WSLENV=%s", envValueForTest(spec.Env.Variables, "WSLENV"))

	if !strings.HasPrefix(cmdLine, "wsl.exe -d "+distro+" ") {
		t.Fatalf("commandLine missing bare distro after -d: %s", cmdLine)
	}
	if strings.Contains(cmdLine, `-d "`) {
		t.Fatalf("distro must not be double-quoted (wsl.exe rejects it): %s", cmdLine)
	}
	if !strings.Contains(cmdLine, `--cd "D:\WorkPace"`) {
		t.Fatalf("commandLine missing --cd workdir: %s", cmdLine)
	}
	if !strings.Contains(cmdLine, "bash -lic") {
		t.Fatalf("commandLine missing bash -lic: %s", cmdLine)
	}
	// Inner payload: bash-single-quoted CLI tokens + exec bash, wrapped as one
	// Windows double-quoted argv token.
	if !strings.Contains(cmdLine, `'claude' '--session-id' 'smoke'; exec bash -li`) {
		t.Fatalf("commandLine missing quoted inner cli + exec bash: %s", cmdLine)
	}
	if autoCmd != "" {
		t.Fatalf("WSL mode must not use typed-in autoCommand, got %q", autoCmd)
	}
}

func envValueForTest(env []string, key string) string {
	for _, kv := range env {
		i := strings.IndexByte(kv, '=')
		if i <= 0 {
			continue
		}
		if strings.EqualFold(kv[:i], key) {
			return kv[i+1:]
		}
	}
	return ""
}