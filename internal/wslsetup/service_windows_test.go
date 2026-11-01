//go:build windows

package wslsetup

import "testing"

// TestRealRunnerWiredOnInit pins the Windows wiring contract: init() must
// install a fully populated real wsl.exe runner so the portable business flow
// (service_flow.go) executes against the live distro. The flow behavior itself
// is covered by the portable fake-runner tests, which run on every OS.
func TestRealRunnerWiredOnInit(t *testing.T) {
	// Restore any runner a parallel test may have injected; assert the wired
	// one captured at package init below.
	t.Cleanup(func() { wslRunnerImpl = realRunnerOnInit })

	if realRunnerOnInit == nil {
		t.Fatal("wslRunnerImpl must be wired by init() on Windows")
	}
	r := realRunnerOnInit
	if r.defaultDistro == nil || r.distroVersion == nil || r.execUser == nil ||
		r.execRoot == nil || r.execLogin == nil || r.searchStatus == nil || r.resetSearchCache == nil {
		t.Fatal("real runner wired on Windows has nil functions")
	}
}
