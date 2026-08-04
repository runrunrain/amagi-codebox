package envcheck

// r4_002_cleanheadroom_drain_release_test.go — R4-002: CleanHeadroom must invoke
// the stopper's releaseDrain callback after the venv removal completes (or on
// abort), so the SharedServiceCoordinator install-drain is held across the whole
// uninstall and released exactly once. This proves the drain lifecycle is bound
// to CleanHeadroom (which the frontend calls directly), not just the App stopper.

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

// TestR4_002_CleanHeadroom_ReleasesDrainAfterRemoval proves the releaseDrain
// callback returned by the stopper is invoked AFTER the venv is removed (the
// drain spans stop + RemoveAll) and exactly once on the success path.
func TestR4_002_CleanHeadroom_ReleasesDrainAfterRemoval(t *testing.T) {
	venvDir := filepath.Join(t.TempDir(), "headroom-venv")
	binDir := filepath.Join(venvDir, "bin")
	if runtime.GOOS == "windows" {
		binDir = filepath.Join(venvDir, "Scripts")
	}
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir venv bin: %v", err)
	}
	writeTestExecutable(t, binDir, "headroom")
	t.Setenv("PATH", t.TempDir())

	runner := &mockRunner{responses: []mockResponse{
		{pathPrefix: "headroom", stdout: "headroom 0.30.0"},
	}}
	svc := NewServiceWithRunner(runner)
	svc.SetHeadroomVenvDir(venvDir)

	var mu sync.Mutex
	releaseCalls := 0
	venvPresentAtRelease := false
	svc.SetHeadroomStopper(func() (error, func()) {
		return nil, func() {
			mu.Lock()
			defer mu.Unlock()
			releaseCalls++
			if _, err := os.Stat(venvDir); err == nil {
				venvPresentAtRelease = true
			}
		}
	})

	if _, err := svc.CleanHeadroom(); err != nil {
		t.Fatalf("CleanHeadroom: %v", err)
	}
	mu.Lock()
	calls := releaseCalls
	present := venvPresentAtRelease
	mu.Unlock()
	if calls != 1 {
		t.Fatalf("R4-002: releaseDrain called %d times, want exactly 1 (after RemoveAll)", calls)
	}
	if present {
		t.Fatal("R4-002: releaseDrain fired while the venv was still present; it must fire AFTER RemoveAll")
	}
}

// TestR4_002_CleanHeadroom_ReleasesDrainOnAbort proves the releaseDrain callback
// is invoked even when the stopper rejects with ErrHeadroomInUse (abort path), so
// the drain never leaks when an uninstall is rejected.
func TestR4_002_CleanHeadroom_ReleasesDrainOnAbort(t *testing.T) {
	venvDir := filepath.Join(t.TempDir(), "headroom-venv")
	binDir := filepath.Join(venvDir, "bin")
	if runtime.GOOS == "windows" {
		binDir = filepath.Join(venvDir, "Scripts")
	}
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir venv bin: %v", err)
	}
	writeTestExecutable(t, binDir, "headroom")
	t.Setenv("PATH", t.TempDir())

	runner := &mockRunner{responses: []mockResponse{
		{pathPrefix: "headroom", stdout: "headroom 0.30.0"},
	}}
	svc := NewServiceWithRunner(runner)
	svc.SetHeadroomVenvDir(venvDir)

	released := make(chan struct{}, 1)
	svc.SetHeadroomStopper(func() (error, func()) {
		return wrapErrHeadroomInUse(errors.New("in use")), func() {
			select {
			case released <- struct{}{}:
			default:
			}
		}
	})

	if _, err := svc.CleanHeadroom(); !errors.Is(err, ErrHeadroomInUse) {
		t.Fatalf("expected ErrHeadroomInUse on abort, got %v", err)
	}
	select {
	case <-released:
		// drain released on abort — good.
	default:
		t.Fatal("R4-002: releaseDrain was NOT invoked on the ErrHeadroomInUse abort path (drain leak)")
	}
	// Venv preserved on abort.
	if _, statErr := os.Stat(venvDir); os.IsNotExist(statErr) {
		t.Fatal("venv must be preserved on in-use rejection")
	}
}
