//go:build windows

package platform

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestMoveNoReplaceWindowsDeniedClassification(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		source, target, conflict bool
	}{
		{"both-present", true, true, true},
		{"target-absent", true, false, false},
		{"source-absent", false, true, false},
		{"both-absent", false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			src := filepath.Join(root, "source")
			dst := filepath.Join(root, "target")
			if tc.source {
				writeMoveFixture(t, src, "source-bytes")
			}
			if tc.target {
				writeMoveFixture(t, dst, "target-bytes")
			}
			err := classifyMoveNoReplaceError(src, dst, windows.ERROR_ACCESS_DENIED)
			if errors.Is(err, os.ErrExist) != tc.conflict {
				t.Fatalf("conflict=%v want %v: %v", errors.Is(err, os.ErrExist), tc.conflict, err)
			}
			if !errors.Is(err, windows.ERROR_ACCESS_DENIED) || !errors.Is(err, os.ErrPermission) {
				t.Fatalf("original denial lost: %v", err)
			}
			for p, want := range map[string]struct {
				present bool
				body    string
			}{src: {tc.source, "source-bytes"}, dst: {tc.target, "target-bytes"}} {
				b, e := os.ReadFile(p)
				if want.present {
					if e != nil || string(b) != want.body {
						t.Fatalf("classification modified fixture: %q %v", b, e)
					}
				} else if !errors.Is(e, os.ErrNotExist) {
					t.Fatalf("classification created fixture: %v", e)
				}
			}
		})
	}
	root := t.TempDir()
	src := filepath.Join(root, "source")
	dst := filepath.Join(root, "target")
	writeMoveFixture(t, src, "source")
	writeMoveFixture(t, dst, "target")
	if got := classifyMoveNoReplaceError(src, dst, nil); got != nil {
		t.Fatalf("success changed: %v", got)
	}
	if got := classifyMoveNoReplaceError(src, dst, windows.ERROR_SHARING_VIOLATION); got != windows.ERROR_SHARING_VIOLATION {
		t.Fatalf("unrelated error normalized: %v", got)
	}
}
