//go:build windows

package pty

import (
	"testing"

	"amagi-codebox/internal/logging"
	"amagi-codebox/internal/platform"
)

// TestBuildWSLCommandLineRecordsDrvFSAdvisory pins the C1/P4-A1 contract: a
// DrvFS workdir feeds the shared platform advisory registry (source "pty")
// alongside the unchanged log.Warn, while an ext4 workdir records nothing.
// The advisory is what GitAssist/PathPicker failure surfaces correlate against
// via App.GetDrvFSAdvisories.
func TestBuildWSLCommandLineRecordsDrvFSAdvisory(t *testing.T) {
	if platform.DefaultWSLDistro(nil) == "" {
		t.Skip("no usable WSL distro on this machine; skipping WSL command line build")
	}

	log := logging.NewService(t.TempDir())
	t.Cleanup(func() { log.Close() })

	buildResolvedStartupPlan(platform.ResolvedLaunchSpec{
		BootstrapMode: platform.BootstrapWSL,
		WorkDir:       `D:\WorkPace`,
	}, log)

	found := false
	for _, e := range platform.DrvFSAdvisories() {
		if e.Source == "pty" && e.WorkDir == `D:\WorkPace` {
			found = true
			if e.MountPath != "/mnt/d/WorkPace" {
				t.Errorf("advisory mountPath = %q, want /mnt/d/WorkPace", e.MountPath)
			}
			if e.Detail == "" {
				t.Error("advisory detail must be user-facing, got empty")
			}
		}
	}
	if !found {
		t.Fatal("expected a source=pty advisory for the DrvFS workdir")
	}

	// ext4 形态不写 advisory。
	buildResolvedStartupPlan(platform.ResolvedLaunchSpec{
		BootstrapMode: platform.BootstrapWSL,
		WorkDir:       "/home/u/projects",
	}, log)
	for _, e := range platform.DrvFSAdvisories() {
		if e.Source == "pty" && e.WorkDir == "/home/u/projects" {
			t.Fatalf("ext4 workdir must not record a DrvFS advisory: %#v", e)
		}
	}
}
