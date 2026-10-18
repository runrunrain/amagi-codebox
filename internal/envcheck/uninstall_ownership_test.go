package envcheck

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Leader integration regressions: real Homebrew prefixes and npm shim boundaries.
func TestHomebrewFormulaRelativeToVerifiedPrefix(t *testing.T) {
	for _, tc := range []struct{ path, prefix, want string }{
		{"/opt/homebrew/Cellar/omp/1.0/bin/omp", "/opt/homebrew", "omp"},
		{"/opt/homebrew/opt/opencode/bin/opencode", "/opt/homebrew", "opencode"},
		{"/usr/local/Cellar/codex/1.0/bin/codex", "/usr/local", "codex"},
		{"/opt/other/Cellar/omp/1.0/bin/omp", "/opt/homebrew", ""},
		{"/opt/homebrew/lib/node_modules/pkg/opt/omp/bin/omp", "/opt/homebrew", ""},
	} {
		t.Run(tc.path, func(t *testing.T) {
			if got := homebrewFormulaFromPath(filepath.FromSlash(tc.path), filepath.FromSlash(tc.prefix)); got != tc.want {
				t.Fatalf("formula=%q, want %q", got, tc.want)
			}
		})
	}
}

func TestNPMShimOwnershipRequiresExecutablePackageReference(t *testing.T) {
	root := filepath.Join(t.TempDir(), "node_modules")
	for _, tc := range []struct {
		name, content string
		want          bool
	}{
		{"cmd", `@ECHO off
"%_prog%" "%dp0%\node_modules\@mariozechner\pi-coding-agent\dist\cli.js" %*`, true},
		{"powershell", `& "$basedir/node$exe" "$basedir/node_modules/@mariozechner/pi-coding-agent/dist/cli.js" $args`, true},
		{"shell", `exec node "$basedir/node_modules/@mariozechner/pi-coding-agent/dist/cli.js" "$@"`, true},
		{"sibling", `exec node "$basedir/node_modules/@mariozechner/pi-coding-agent-extra/dist/cli.js" "$@"`, false},
		{"comment", `# "$basedir/node_modules/@mariozechner/pi-coding-agent/dist/cli.js"
exec node "$basedir/node_modules/other/bin/pi.js"`, false},
		{"foreign-absolute-path", `exec node "/other/node_modules/@mariozechner/pi-coding-agent/dist/cli.js"`, false},
		{"oversized", strings.Repeat("x", 64*1024+1), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			shim := filepath.Join(t.TempDir(), "pi")
			if err := os.WriteFile(shim, []byte(tc.content), 0700); err != nil {
				t.Fatal(err)
			}
			if got := npmShimBindsToPackage(shim, root, "@mariozechner/pi-coding-agent"); got != tc.want {
				t.Fatalf("ownership=%v, want %v", got, tc.want)
			}
		})
	}
}

func TestCanonicalDirPathEmptyRemainsEmpty(t *testing.T) {
	if got := canonicalDirPath(""); got != "" {
		t.Fatalf("empty path became %q", got)
	}
}
