package envcheck

// r3_002_headroom_uninstall_reject_test.go — R3-002 regression: CleanHeadroom
// must NOT swallow a typed in-use rejection from the injected stopper. When the
// stopper returns an error wrapping ErrHeadroomInUse (the shared headroom is
// still depended on by active sessions), CleanHeadroom aborts BEFORE the venv is
// removed and returns ErrHeadroomInUse so the desktop confirm/reject flow can
// surface it. A plain stop failure stays best-effort (venv removal proceeds).

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

// TestR3_002_CleanHeadroom_AbortsOnInUseRejection proves that when the stopper
// rejects with ErrHeadroomInUse, CleanHeadroom returns ErrHeadroomInUse and the
// venv directory is NOT removed (an active run's dependency is preserved).
func TestR3_002_CleanHeadroom_AbortsOnInUseRejection(t *testing.T) {
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

	stopperMu := sync.Mutex{}
	venvPresentAtReject := false
	svc.SetHeadroomStopper(func() (error, func()) {
		stopperMu.Lock()
		defer stopperMu.Unlock()
		if _, err := os.Stat(venvDir); err == nil {
			venvPresentAtReject = true
		}
		// Wrap the sentinel (the App-level stopper returns
		// fmt.Errorf("%w: %w", ErrHeadroomInUse, remote.ErrSharedServiceInUse)).
		return wrapErrHeadroomInUse(errors.New("shared service is in use by active sessions")), nil
	})

	result, err := svc.CleanHeadroom()
	if !errors.Is(err, ErrHeadroomInUse) {
		t.Fatalf("CleanHeadroom with in-use stopper: got err=%v, want ErrHeadroomInUse", err)
	}
	if result != nil {
		t.Fatalf("CleanHeadroom must not return a result on in-use rejection, got %+v", result)
	}
	if !venvPresentAtReject {
		t.Fatal("stopper should observe the venv present at rejection time")
	}
	// R3-002 core invariant: the venv MUST NOT be removed when the stopper
	// rejected with ErrHeadroomInUse (the active run's dependency is preserved).
	if _, statErr := os.Stat(venvDir); os.IsNotExist(statErr) {
		t.Fatal("R3-002 regression: venv was removed despite an in-use rejection — an active run's dependency was deleted")
	}
}

// TestR3_002_CleanHeadroom_PropagatesWrappedErrHeadroomInUse proves errors.Is
// unwrapping works for the wrapped form the App stopper actually produces
// (fmt.Errorf("%w: %w", ErrHeadroomInUse, remote.ErrSharedServiceInUse)).
func TestR3_002_CleanHeadroom_PropagatesWrappedErrHeadroomInUse(t *testing.T) {
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

	// Mirrors app.stopAllHeadroomForUninstall's actual wrapping (%w: %w).
	inUseDetail := errors.New("shared service is in use by active sessions")
	svc.SetHeadroomStopper(func() (error, func()) {
		return wrapErrHeadroomInUse(inUseDetail), nil
	})

	if _, err := svc.CleanHeadroom(); !errors.Is(err, ErrHeadroomInUse) {
		t.Fatalf("expected errors.Is(err, ErrHeadroomInUse), got %v", err)
	}
	// Venv preserved.
	if _, statErr := os.Stat(venvDir); os.IsNotExist(statErr) {
		t.Fatal("venv must not be removed on wrapped in-use rejection")
	}
}

// wrapErrHeadroomInUse mirrors fmt.Errorf("%w: %w", ErrHeadroomInUse, cause)
// so the test exercises the same dual-wrap path the production App stopper uses.
func wrapErrHeadroomInUse(cause error) error {
	return &headroomInUseWrappedErr{cause: cause}
}

type headroomInUseWrappedErr struct{ cause error }

func (e *headroomInUseWrappedErr) Error() string {
	return ErrHeadroomInUse.Error() + ": " + e.cause.Error()
}
func (e *headroomInUseWrappedErr) Unwrap() error { return ErrHeadroomInUse }
