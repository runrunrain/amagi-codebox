//go:build windows

package paths

import (
	"errors"
	"strings"
	"testing"

	"amagi-codebox/internal/platform"
)

// TestListDirectories_LinuxStyleRootRejected pins the P4-A1/E8 contract: a
// Linux-form root (/mnt/<drive>, ~/..., bare ~, bare /) must fail with the
// structured *LinuxStylePathError instead of silently falling through
// filepath.Abs+Stat onto a mis-placed Windows home listing.
func TestListDirectories_LinuxStyleRootRejected(t *testing.T) {
	svc := NewPathsService(t.TempDir())
	for _, root := range []string{"/mnt/c/WorkSpace", "~/", "~", "/", "/home/u/repo"} {
		raw, err := svc.ListDirectories(root)
		if err == nil {
			t.Fatalf("ListDirectories(%q) must reject Linux-form roots, got payload %q", root, raw)
		}
		var lsErr *LinuxStylePathError
		if !errors.As(err, &lsErr) {
			t.Fatalf("ListDirectories(%q) error must be *LinuxStylePathError, got %T: %v", root, err, err)
		}
		if lsErr.Path != root {
			t.Errorf("error path = %q, want %q", lsErr.Path, root)
		}
		if !strings.Contains(err.Error(), LinuxStylePathMarker) {
			t.Errorf("error message %q must carry the stable marker %q for the frontend", err.Error(), LinuxStylePathMarker)
		}
	}
}

// TestListDirectories_LinuxRootRecordsDrvFSAdvisory verifies the DrvFS
// advisory entry point: only the /mnt/<drive> form feeds the shared registry
// (source "paths"); ext4-style Linux paths stay advisory-free.
func TestListDirectories_LinuxRootRecordsDrvFSAdvisory(t *testing.T) {
	svc := NewPathsService(t.TempDir())

	if _, err := svc.ListDirectories("/mnt/e/drvfs-probe"); err == nil {
		t.Fatal("expected Linux-form root to be rejected")
	}
	found := false
	for _, e := range platform.DrvFSAdvisories() {
		if e.Source == "paths" && e.WorkDir == "/mnt/e/drvfs-probe" {
			found = true
			if e.MountPath != "/mnt/e/drvfs-probe" {
				t.Errorf("advisory mountPath = %q, want /mnt/e/drvfs-probe", e.MountPath)
			}
		}
	}
	if !found {
		t.Fatal("expected a source=paths DrvFS advisory for the /mnt root")
	}

	// ext4 形态：报形态错误但不写 DrvFS advisory。
	if _, err := svc.ListDirectories("/home/u/ext4-probe"); err == nil {
		t.Fatal("expected Linux-form root to be rejected")
	}
	for _, e := range platform.DrvFSAdvisories() {
		if e.WorkDir == "/home/u/ext4-probe" {
			t.Fatalf("ext4 root must not record a DrvFS advisory: %#v", e)
		}
	}
}
