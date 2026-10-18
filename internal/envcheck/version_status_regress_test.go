package envcheck

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"amagi-codebox/internal/platform"
)

// ---------------------------------------------------------------------------
// steer #5 / integration finding #4: "healthy" and "verified current" must be
// separate states. An empty LatestVersion must never masquerade as "latest":
// install may no-op but must say the latest version is unknown; update must
// NOT skip the update and claim "already latest" without a verified registry
// version; an unchanged version after an update is reported honestly.
// ---------------------------------------------------------------------------

func newVersionStatusFixture(t *testing.T) *npmLayoutFixture {
	f := newNPMLayoutFixture(t, "opencode")
	f.installFakePackage(t, "opencode-ai", "opencode")
	return f
}

// versionStatusResponder answers opencode version probes with the given
// outputs (per probe index) and npm view with the given latest version (or an
// error when latestUnknown is set).
type versionStatusResponder struct {
	versions      []string // one per version probe, in order
	latestUnknown bool
	latestVersion string
	installOutput string
}

func (r *versionStatusResponder) respond(f *npmLayoutFixture) func(platform.CommandSpec) (*platform.ProcessResult, error) {
	versionIdx := 0
	return func(spec platform.CommandSpec) (*platform.ProcessResult, error) {
		base := filepath.Base(spec.Path)
		switch {
		case strings.Contains(base, "opencode") && !strings.Contains(base, "npm") && len(spec.Args) > 0 &&
			(spec.Args[0] == "--version" || spec.Args[0] == "-v"):
			out := "opencode 1.0.0"
			if versionIdx < len(r.versions) {
				out = r.versions[versionIdx]
				versionIdx++
			} else if len(r.versions) > 0 {
				out = r.versions[len(r.versions)-1]
			}
			return &platform.ProcessResult{Stdout: out}, nil
		case strings.Contains(base, "npm") && len(spec.Args) >= 1 && spec.Args[0] == "--version":
			return &platform.ProcessResult{Stdout: "10.0.0"}, nil
		case strings.Contains(base, "npm") && len(spec.Args) >= 3 && spec.Args[0] == "view":
			if r.latestUnknown {
				return &platform.ProcessResult{}, os.ErrNotExist
			}
			return &platform.ProcessResult{Stdout: r.latestVersion}, nil
		case strings.Contains(base, "npm") && len(spec.Args) >= 2 && spec.Args[0] == "install":
			return &platform.ProcessResult{Stdout: r.installOutput}, nil
		default:
			return &platform.ProcessResult{}, os.ErrNotExist
		}
	}
}

func TestUpdateProceedsWhenLatestVersionUnknown(t *testing.T) {
	f := newVersionStatusFixture(t)
	responder := &versionStatusResponder{
		versions:      []string{"opencode 1.0.0", "opencode 2.0.0"}, // before, after
		latestUnknown: true,
		installOutput: "changed 1 package",
	}
	runner := &specRunner{respond: responder.respond(f)}
	svc := NewServiceWithRunner(runner)

	result, err := svc.Update(ToolOpenCode)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if result == nil || !result.Success {
		t.Fatalf("update should run and verify when latest is unknown, got %+v (err=%v)", result, err)
	}
	if !runner.sawCall("npm", "install", "-g", "opencode-ai@latest") {
		t.Fatal("unknown latest version must NOT skip the update command")
	}
	if !strings.Contains(result.Message, "成功") {
		t.Fatalf("successful update message expected, got %q", result.Message)
	}
}

func TestUpdateNoopOnlyWhenLatestVerifiedCurrent(t *testing.T) {
	f := newVersionStatusFixture(t)
	responder := &versionStatusResponder{
		versions:      []string{"opencode 1.0.0"},
		latestVersion: "1.0.0", // registry confirms current
	}
	runner := &specRunner{respond: responder.respond(f)}
	svc := NewServiceWithRunner(runner)

	result, err := svc.Update(ToolOpenCode)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if result == nil || !result.Success {
		t.Fatalf("verified-current update should no-op successfully, got %+v", result)
	}
	if runner.sawCall("npm", "install", "-g", "opencode-ai@latest") {
		t.Fatal("verified-current update must skip the update command")
	}
	if !strings.Contains(result.Message, "已是最新版本") {
		t.Fatalf("no-op message should state verified latest, got %q", result.Message)
	}
}

func TestInstallNoopMessageHonestWhenLatestUnknown(t *testing.T) {
	f := newVersionStatusFixture(t)
	responder := &versionStatusResponder{
		versions:      []string{"opencode 1.0.0"},
		latestUnknown: true,
	}
	runner := &specRunner{respond: responder.respond(f)}
	svc := NewServiceWithRunner(runner)

	result, err := svc.Install(ToolOpenCode)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if result == nil || !result.Success {
		t.Fatalf("healthy install no-op should still succeed, got %+v", result)
	}
	if runner.sawCall("npm", "install", "-g") {
		t.Fatal("healthy install must not re-run the installer")
	}
	if !strings.Contains(result.Message, "最新版本未知") {
		t.Fatalf("install no-op must not claim 'latest' without a verified version, got %q", result.Message)
	}
}

func TestUpdateVersionUnchangedWithUnknownLatestIsHonestFailure(t *testing.T) {
	f := newVersionStatusFixture(t)
	responder := &versionStatusResponder{
		versions:      []string{"opencode 1.0.0", "opencode 1.0.0"}, // before, after (unchanged)
		latestUnknown: true,
		installOutput: "changed 0 packages", // npm exit 0, nothing to do
	}
	runner := &specRunner{respond: responder.respond(f)}
	svc := NewServiceWithRunner(runner)

	result, err := svc.Update(ToolOpenCode)
	if err == nil {
		t.Fatal("version unchanged after update must fail (no fabricated upgrade)")
	}
	if result == nil || result.Success {
		t.Fatalf("unchanged version must not report success, got %+v", result)
	}
	if !strings.Contains(result.Error, "version unchanged") {
		t.Fatalf("failure must mention version unchanged, got %q", result.Error)
	}
	if !strings.Contains(result.Error, "最新版本号未知") {
		t.Fatalf("failure must disclose that the latest version is unverified instead of implying a broken install, got %q", result.Error)
	}
}
