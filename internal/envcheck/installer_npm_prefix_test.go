package envcheck

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNPMPrefixFromInstalledPath covers npm global prefix derivation for the
// layouts seen in the wild: fnm/Unix lib layouts, bare node_modules layouts,
// Windows paths, Homebrew/native paths without node_modules, and package-
// internal nested node_modules directories (outermost prefix must win).
func TestNPMPrefixFromInstalledPath(t *testing.T) {
	cases := []struct {
		name    string
		path    string
		prefix  string
		wantOK  bool
	}{
		{
			name:   "fnm unix lib layout",
			path:   "/Users/u/.local/share/fnm/node-versions/v24.18.0/installation/lib/node_modules/@earendil-works/pi-coding-agent/dist/bundle/cli.js",
			prefix: "/Users/u/.local/share/fnm/node-versions/v24.18.0/installation",
			wantOK: true,
		},
		{
			name:   "user local node lib layout",
			path:   "/Users/u/.local/node/lib/node_modules/@earendil-works/pi-coding-agent/dist/bundle/cli.js",
			prefix: "/Users/u/.local/node",
			wantOK: true,
		},
		{
			name:   "unix bare node_modules layout",
			path:   "/Users/u/.npm-global/node_modules/opencode-ai/bin/opencode",
			prefix: "/Users/u/.npm-global",
			wantOK: true,
		},
		{
			name:   "nested package node_modules resolves to outermost prefix",
			path:   "/Users/u/.local/node/lib/node_modules/@scope/pkg/node_modules/dep/bin/dep",
			prefix: "/Users/u/.local/node",
			wantOK: true,
		},
		{
			name:   "windows slash layout keeps original casing",
			path:   `C:\Users\u\AppData\Roaming\npm\node_modules\@openai\codex\bin\codex.js`,
			prefix: "C:/Users/u/AppData/Roaming/npm",
			wantOK: true,
		},
		{name: "homebrew bin has no node_modules", path: "/opt/homebrew/bin/codex"},
		{name: "native home dir bin", path: "/Users/u/.local/bin/claude"},
		{name: "empty path", path: "   "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prefix, ok := npmPrefixFromInstalledPath(tc.path)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v (prefix=%q)", ok, tc.wantOK, prefix)
			}
			if ok && prefix != tc.prefix {
				t.Fatalf("prefix = %q, want %q", prefix, tc.prefix)
			}
		})
	}
}

// TestNPMExecutableForInstallPrefersToolOwnPrefix verifies the npm channel
// alignment fix: when the detected tool lives in an npm prefix that ships its
// own npm binary (fnm, ~/.local/node), install/update commands must use THAT
// npm instead of the first npm found on the resolver PATH. This is the root
// cause of the "pi 更新多次失败" incident: detection pointed at the fnm
// prefix while resolveNPMPath picked ~/.local/bin/npm, so every update landed
// in a parallel prefix and verification reported "version unchanged".
func TestNPMExecutableForInstallPrefersToolOwnPrefix(t *testing.T) {
	prefix := t.TempDir()
	if err := os.MkdirAll(filepath.Join(prefix, "lib", "node_modules", "@earendil-works", "pi-coding-agent", "dist"), 0o755); err != nil {
		t.Fatal(err)
	}
	toolPath := filepath.Join(prefix, "lib", "node_modules", "@earendil-works", "pi-coding-agent", "dist", "cli.js")
	if err := os.WriteFile(toolPath, []byte("#!/usr/bin/env node\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	binDir := filepath.Join(prefix, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	prefixNPM := filepath.Join(binDir, "npm")
	if err := os.WriteFile(prefixNPM, []byte("#!/usr/bin/env node\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	s := NewService()

	t.Run("npm install picks prefix npm", func(t *testing.T) {
		current := &CheckStatus{
			Installed:      true,
			InstallMethod:  InstallMethodNPM,
			ExecutablePath: toolPath,
		}
		if got := s.npmExecutableForInstall(current); got != prefixNPM {
			t.Fatalf("npmExecutableForInstall = %q, want %q", got, prefixNPM)
		}
		cmd := s.resolveCommandNPMPathForTool(npmPiCommand(installOperationUpdate), current)
		if cmd.path != prefixNPM {
			t.Fatalf("command path = %q, want %q", cmd.path, prefixNPM)
		}
		if !strings.Contains(cmd.args[len(cmd.args)-1], "@earendil-works/pi-coding-agent@latest") {
			t.Fatalf("unexpected args: %v", cmd.args)
		}
	})

	t.Run("homebrew install method does not hijack npm channel", func(t *testing.T) {
		current := &CheckStatus{
			Installed:      true,
			InstallMethod:  InstallMethodHomebrew,
			ExecutablePath: toolPath,
		}
		// Homebrew installs are never npm-managed: the prefix npm must NOT be
		// selected. The generic resolver result varies by host, so assert the
		// prefix npm is not returned instead of a concrete path.
		if got := s.npmExecutableForInstall(current); got == prefixNPM {
			t.Fatalf("npmExecutableForInstall must not resolve to prefix npm for a Homebrew install, got %q", got)
		}
	})

	t.Run("missing current falls back to generic resolution", func(t *testing.T) {
		if got := s.npmExecutableForInstall(nil); got == prefixNPM {
			t.Fatalf("nil current must not resolve to prefix npm, got %q", got)
		}
	})
}

// TestPrependPATHEntries covers the PATH injection used to keep an npm
// command's shebang `#!/usr/bin/env node` pinned to the same prefix's node.
func TestPrependPATHEntries(t *testing.T) {
	env := []string{"HOME=/tmp/h", "PATH=/usr/bin:/bin"}
	got := prependPATHEntries(env, []string{" ", "/opt/prefix/bin"})
	if len(got) != 2 {
		t.Fatalf("env size changed: %v", got)
	}
	if got[0] != "HOME=/tmp/h" {
		t.Fatalf("non-PATH entries must be preserved in order: %v", got)
	}
	want := "PATH=/opt/prefix/bin:/usr/bin:/bin"
	if got[1] != want {
		t.Fatalf("PATH = %q, want %q", got[1], want)
	}

	// Already-first entry must not be duplicated.
	got = prependPATHEntries([]string{"PATH=/opt/prefix/bin:/usr/bin"}, []string{"/opt/prefix/bin"})
	if got[0] != "PATH=/opt/prefix/bin:/usr/bin" {
		t.Fatalf("duplicate not suppressed: %q", got[0])
	}

	// Empty dirs are ignored; env without PATH passes through unchanged.
	got = prependPATHEntries([]string{"FOO=1"}, []string{""})
	if len(got) != 1 || got[0] != "FOO=1" {
		t.Fatalf("env without PATH must pass through: %v", got)
	}
}
