package platform

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWindowsPiPowerShellLaunchIsInline(t *testing.T) {
	bin := t.TempDir()
	for _, name := range []string{"pi.cmd", "pwsh.exe", "cmd.exe", "opencode.cmd"} {
		if err := os.WriteFile(filepath.Join(bin, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	r := NewCLIResolver(capabilitiesForTarget("windows", "amd64"))
	for _, tt := range []struct {
		app, shell string
		mode       LaunchBootstrapMode
	}{
		{"pi", filepath.Join(bin, "pwsh.exe"), BootstrapShellInline},
		{"pi", "cmd", BootstrapShellAttach},
		{"opencode", "pwsh", BootstrapShellAttach},
	} {
		spec, err := r.Resolve(ResolveRequest{AppType: tt.app, LaunchMode: "embedded", RequestedShellPath: tt.shell,
			Env: []string{"PATH=" + bin}, CLIArgs: []string{"--model", "sample"}})
		if err != nil {
			t.Fatal(err)
		}
		if spec.BootstrapMode != tt.mode {
			t.Errorf("%s via %s: mode = %s, want %s", tt.app, tt.shell, spec.BootstrapMode, tt.mode)
		}
		if tt.mode == BootstrapShellInline && spec.StartupCommand != "& 'pi' '--model' 'sample'" {
			t.Errorf("unexpected Pi startup command: %s", spec.StartupCommand)
		}
	}
}
