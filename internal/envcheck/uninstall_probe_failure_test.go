package envcheck

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"amagi-codebox/internal/platform"
)

func TestVerifyUninstalledExecutableRequiresPositiveAbsence(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "entry")
	if err := os.WriteFile(file, []byte("remaining"), 0600); err != nil {
		t.Fatal(err)
	}
	if verifyUninstalledExecutable(file) == nil {
		t.Fatal("remaining file was treated as removed")
	}
	if verifyUninstalledExecutable("") == nil {
		t.Fatal("unknown original path was treated as removed")
	}
	if err := verifyUninstalledExecutable(filepath.Join(root, "absent")); err != nil {
		t.Fatalf("confirmed missing path rejected: %v", err)
	}
	if verifyUninstalledExecutable(filepath.Join(file, "child")) == nil {
		t.Fatal("non-ENOENT filesystem error was treated as removed")
	}
}

// R-1: an npm fallback candidate that exists but cannot run is not absent.
func TestUninstallFallbackProbeFailureNeverMeansRemoved(t *testing.T) {
	for _, tool := range []CLITool{ToolPi, ToolOmp, ToolCodex, ToolOpenCode} {
		for _, initiallyBroken := range []bool{false, true} {
			name := string(tool) + "/broken-after-command"
			if initiallyBroken {
				name = string(tool) + "/already-broken"
			}
			t.Run(name, func(t *testing.T) {
				guiEnvIsolation(t)
				command, pkg := string(tool), npmPackageName(tool)
				f := newNPMLayoutFixture(t, command)
				f.installFakePackage(t, pkg, command)
				// Only npm is in PATH; the CLI is discovered exclusively via npm prefix.
				npmBin := t.TempDir()
				writeTestExecutable(t, npmBin, "npm")
				t.Setenv("PATH", npmBin)
				if rr := resolveExecutable(command); rr.executablePath != "" {
					t.Fatalf("fixture unexpectedly visible to resolver: %+v", rr)
				}
				broken := initiallyBroken
				fallback := f.npmResponder(tool, command, pkg, false)
				runner := &specRunner{respond: func(spec platform.CommandSpec) (*platform.ProcessResult, error) {
					base := filepath.Base(spec.Path)
					if strings.Contains(base, "npm") && len(spec.Args) > 0 && spec.Args[0] == "uninstall" {
						broken = true
						return &platform.ProcessResult{Stderr: "partial uninstall; entry remains"}, errors.New("uninstall exit 1")
					}
					if base == command && broken {
						return &platform.ProcessResult{Stderr: "dependency broken"}, errors.New("version probe failed")
					}
					return fallback(spec)
				}}
				svc := NewServiceWithRunner(runner)
				if initiallyBroken {
					status, err := svc.CheckOne(tool)
					if err != nil || status == nil || !status.Installed || status.Error == "" {
						t.Fatalf("broken existing candidate must remain installed with error: %+v, %v", status, err)
					}
				}
				if _, err := svc.startUninstallTool(tool); err != nil {
					t.Fatal(err)
				}
				op := waitForTerminalOperation(t, svc, 10*time.Second)
				if op.Status != OperationStatusFailed || op.Result == nil || op.Result.Success {
					t.Fatalf("remaining broken entry must not be successful/no-op: %+v", op)
				}
				if _, err := os.Stat(filepath.Join(f.rootDir, pkg, "bin", command)); err != nil {
					t.Fatalf("fixture entry unexpectedly gone: %v", err)
				}
			})
		}
	}
}
