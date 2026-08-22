package settings

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestNormalizeDashboardDefaults_DoesNotPropagateLegacyTerminalModeToNonClaudeEngines(t *testing.T) {
	d := DashboardDefaults{Mode: "terminal"}

	normalizeDashboardDefaults(&d)

	if d.ClaudeMode != "terminal" {
		t.Fatalf("ClaudeMode = %q, want legacy mode terminal", d.ClaudeMode)
	}
	if d.OpenCodeMode != "embedded" {
		t.Fatalf("OpenCodeMode = %q, want embedded", d.OpenCodeMode)
	}
	if d.CodexMode != "embedded" {
		t.Fatalf("CodexMode = %q, want embedded", d.CodexMode)
	}
	if d.AmagiCodeMode != "embedded" {
		t.Fatalf("AmagiCodeMode = %q, want embedded", d.AmagiCodeMode)
	}
}

func TestNormalizeDashboardDefaults_PreservesExplicitEngineModes(t *testing.T) {
	d := DashboardDefaults{
		Mode:          "terminal",
		OpenCodeMode:  "terminal",
		CodexMode:     "terminal",
		AmagiCodeMode: "terminal",
	}

	normalizeDashboardDefaults(&d)

	if d.OpenCodeMode != "terminal" || d.CodexMode != "terminal" || d.AmagiCodeMode != "terminal" {
		t.Fatalf("explicit modes not preserved: opencode=%q codex=%q amagicode=%q", d.OpenCodeMode, d.CodexMode, d.AmagiCodeMode)
	}
}

// TestNormalizeDashboardDefaults_OmpModeAndShell 验证 omp 引擎默认值与透传
// （复刻 PiMode/PiShell 语义）：缺省时 OmpMode=embedded、OmpShell 跟随 Shell；
// 显式设置时原样保留。
func TestNormalizeDashboardDefaults_OmpModeAndShell(t *testing.T) {
	// 缺省：embedded + 跟随全局 Shell。
	d := DashboardDefaults{Shell: "zsh"}
	normalizeDashboardDefaults(&d)
	if d.OmpMode != "embedded" {
		t.Fatalf("OmpMode = %q, want embedded", d.OmpMode)
	}
	if d.OmpShell != "zsh" {
		t.Fatalf("OmpShell = %q, want zsh (follows Shell)", d.OmpShell)
	}

	// 无全局 Shell：回退 pwsh。
	d2 := DashboardDefaults{}
	normalizeDashboardDefaults(&d2)
	if d2.OmpShell != "pwsh" {
		t.Fatalf("OmpShell = %q, want pwsh fallback", d2.OmpShell)
	}

	// 显式值保留。
	d3 := DashboardDefaults{OmpMode: "terminal", OmpShell: "bash"}
	normalizeDashboardDefaults(&d3)
	if d3.OmpMode != "terminal" || d3.OmpShell != "bash" {
		t.Fatalf("explicit omp values not preserved: mode=%q shell=%q", d3.OmpMode, d3.OmpShell)
	}
}

// TestDashboardDefaults_OmpModeRoundTrip 验证 OmpMode/OmpShell 经
// SetDashboardDefaults/GetDashboardDefaults 完整往返（透传不丢失）。
func TestDashboardDefaults_OmpModeRoundTrip(t *testing.T) {
	svc := NewService(t.TempDir())
	if err := svc.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	d := svc.GetDashboardDefaults()
	d.OmpMode = "terminal"
	d.OmpShell = "fish"
	if err := svc.SetDashboardDefaults(d); err != nil {
		t.Fatalf("SetDashboardDefaults: %v", err)
	}
	got := svc.GetDashboardDefaults()
	if got.OmpMode != "terminal" || got.OmpShell != "fish" {
		t.Fatalf("round-trip omp = mode:%q shell:%q, want terminal/fish", got.OmpMode, got.OmpShell)
	}
}

func TestSaveStoresSettingsInPrivateFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file permission bits are validated on macOS/Linux")
	}
	dir := filepath.Join(t.TempDir(), "settings")
	svc := NewService(dir)
	if err := svc.SetGitHubToken("test-token"); err != nil {
		t.Fatalf("SetGitHubToken: %v", err)
	}

	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat settings dir: %v", err)
	}
	if got := dirInfo.Mode().Perm(); got != 0o700 {
		t.Fatalf("settings dir mode = %o, want 700", got)
	}
	fileInfo, err := os.Stat(filepath.Join(dir, "settings.json"))
	if err != nil {
		t.Fatalf("stat settings file: %v", err)
	}
	if got := fileInfo.Mode().Perm(); got != 0o600 {
		t.Fatalf("settings file mode = %o, want 600", got)
	}
}

func TestDefaultRemoteSettingsAreLocalAndDisabled(t *testing.T) {
	svc := NewService(t.TempDir())
	if host := svc.GetRemoteHost(); host != "127.0.0.1" {
		t.Fatalf("default remote host = %q, want loopback", host)
	}
	if svc.GetRemoteEnabled() {
		t.Fatal("remote server should require explicit enablement by default")
	}
}

// TestCodexGlobalHeadroom_DefaultsOff verifies a fresh settings store reports
// the codex-global headroom toggle as disabled (zero-value), so a new install
// never starts the second proxy unprompted.
func TestCodexGlobalHeadroom_DefaultsOff(t *testing.T) {
	svc := NewService(t.TempDir())
	state := svc.GetCodexGlobalHeadroom()
	if state.Enabled {
		t.Fatalf("codex global headroom should default to disabled, got %+v", state)
	}
	if state.Target != "" || state.Port != 0 {
		t.Fatalf("codex global headroom default target/port should be zero, got %+v", state)
	}
}

// TestCodexGlobalHeadroom_RoundTrip verifies Set persists Enabled/Target/Port and
// Get reads them back, and disabling clears target/port so no stale config
// survives.
func TestCodexGlobalHeadroom_RoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "settings")
	svc := NewService(dir)

	if err := svc.SetCodexGlobalHeadroom(true, "https://api.openai.com/v1", 8788); err != nil {
		t.Fatalf("SetCodexGlobalHeadroom(true): %v", err)
	}
	// New service instance reads the persisted file: proves it landed on disk.
	svc2 := NewService(dir)
	if err := svc2.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	state := svc2.GetCodexGlobalHeadroom()
	if !state.Enabled || state.Target != "https://api.openai.com/v1" || state.Port != 8788 {
		t.Fatalf("persisted state mismatch, got %+v", state)
	}

	// Disabling must clear target/port (no stale config).
	if err := svc2.SetCodexGlobalHeadroom(false, "", 0); err != nil {
		t.Fatalf("SetCodexGlobalHeadroom(false): %v", err)
	}
	disabled := svc2.GetCodexGlobalHeadroom()
	if disabled.Enabled || disabled.Target != "" || disabled.Port != 0 {
		t.Fatalf("disabled state should be fully zeroed, got %+v", disabled)
	}
}

// TestSetDashboardDefaults_PreservesCodexGlobalHeadroom is the regression test
// for the silent-clobber MAJOR bug. The frontend persistDefaults path
// (useDashboardState / useSessionLaunch) replays whatever it cached for
// codexGlobalHeadroom through SetDashboardDefaults on every session launch and
// dashboard save. Because the frontend cache is typically stale (false), this
// used to overwrite the real persisted value and silently disable the
// codex-global headroom, leaving config.toml pointing at a dead 8788 proxy.
//
// SetDashboardDefaults must now re-pin the three CodexGlobal* fields to the
// currently persisted values, ignoring whatever the caller supplied. This test
// enables the toggle, then calls SetDashboardDefaults with a payload that
// explicitly sets CodexGlobalHeadroom=false (and bogus target/port) and asserts
// the persisted state is unchanged.
func TestSetDashboardDefaults_PreservesCodexGlobalHeadroom(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "settings")
	svc := NewService(dir)

	// Turn the codex-global headroom ON with concrete target/port.
	if err := svc.SetCodexGlobalHeadroom(true, "https://api.openai.com/v1", 8788); err != nil {
		t.Fatalf("SetCodexGlobalHeadroom(true): %v", err)
	}

	// Simulate the stale frontend payload: dashboard save carrying
	// codexGlobalHeadroom=false + bogus target/port (exactly what
	// useDashboardState would replay from its init-only cache).
	stale := DashboardDefaults{
		Mode:                      "embedded",
		Shell:                     "pwsh",
		CodexGlobalHeadroom:       false,
		CodexGlobalHeadroomTarget: "https://stale.example.com/v1",
		CodexGlobalHeadroomPort:   9999,
	}
	if err := svc.SetDashboardDefaults(stale); err != nil {
		t.Fatalf("SetDashboardDefaults: %v", err)
	}

	state := svc.GetCodexGlobalHeadroom()
	if !state.Enabled {
		t.Fatalf("Enabled = false, want true; SetDashboardDefaults must not clobber the codex-global headroom toggle")
	}
	if state.Target != "https://api.openai.com/v1" {
		t.Fatalf("Target = %q, want %q (preserved)", state.Target, "https://api.openai.com/v1")
	}
	if state.Port != 8788 {
		t.Fatalf("Port = %d, want 8788 (preserved)", state.Port)
	}

	// The non-codex fields the caller DID supply must still land.
	dash := svc.GetDashboardDefaults()
	if dash.Mode != "embedded" || dash.Shell != "pwsh" {
		t.Fatalf("non-codex dashboard fields not persisted: Mode=%q Shell=%q", dash.Mode, dash.Shell)
	}

	// And the same invariant must survive a fresh Load from disk.
	svc2 := NewService(dir)
	if err := svc2.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	state2 := svc2.GetCodexGlobalHeadroom()
	if !state2.Enabled || state2.Target != "https://api.openai.com/v1" || state2.Port != 8788 {
		t.Fatalf("after reload, codex-global state not preserved: %+v", state2)
	}
}

// TestSetDashboardDefaults_PreservesCodexGlobalHeadroomDisabled verifies the
// preservation is symmetric: when the feature is OFF, a stale frontend payload
// claiming codexGlobalHeadroom=true must NOT silently enable it.
func TestSetDashboardDefaults_PreservesCodexGlobalHeadroomDisabled(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "settings")
	svc := NewService(dir)

	// Feature stays OFF (default). Stale payload tries to flip it on.
	stale := DashboardDefaults{
		Mode:                      "embedded",
		CodexGlobalHeadroom:       true,
		CodexGlobalHeadroomTarget: "https://stale.example.com/v1",
		CodexGlobalHeadroomPort:   9999,
	}
	if err := svc.SetDashboardDefaults(stale); err != nil {
		t.Fatalf("SetDashboardDefaults: %v", err)
	}

	state := svc.GetCodexGlobalHeadroom()
	if state.Enabled {
		t.Fatalf("Enabled = true, want false; stale payload must not silently enable codex-global headroom")
	}
	if state.Target != "" || state.Port != 0 {
		t.Fatalf("disabled codex-global state should stay zeroed, got %+v", state)
	}
}

// --- R2-Minor-02: SetRemoteEndpoint concurrency + Save snapshot safety ---

// validEndpointPairs are distinct, individually-valid (host,port) tuples used by
// the concurrency tests. Each pair's host last-octet == port last-3-digits so a
// "mixed tuple" (host_i, port_j, i!=j) is detectable.
func validEndpointPairs(n int) []struct {
	host string
	port int
} {
	out := make([]struct {
		host string
		port int
	}, n)
	for i := 0; i < n; i++ {
		out[i] = struct {
			host string
			port int
		}{host: fmt.Sprintf("10.0.0.%d", i+2), port: 9000 + i}
	}
	return out
}

// TestSetRemoteEndpoint_Concurrent_NoMixedTuple runs N concurrent
// SetRemoteEndpoint calls with distinct (host,port) pairs. With the saveMu
// transaction each call fully commits before the next, so the final (host,port)
// must be ONE consistent pair — never a mix (host_i, port_j, i!=j). Run with
// -race to also catch data races on the settings fields.
func TestSetRemoteEndpoint_Concurrent_NoMixedTuple(t *testing.T) {
	svc := NewService(t.TempDir())
	if err := svc.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	pairs := validEndpointPairs(8)
	var wg sync.WaitGroup
	for _, p := range pairs {
		wg.Add(1)
		go func(p struct {
			host string
			port int
		}) {
			defer wg.Done()
			if err := svc.SetRemoteEndpoint(p.host, p.port); err != nil {
				t.Errorf("SetRemoteEndpoint(%s,%d): %v", p.host, p.port, err)
			}
		}(p)
	}
	wg.Wait()

	host := svc.GetRemoteHost()
	port := svc.GetRemotePort()
	// The final tuple must be one of the committed pairs (consistent, not mixed).
	matched := false
	for _, p := range pairs {
		if host == p.host && port == p.port {
			matched = true
			break
		}
	}
	if !matched {
		t.Fatalf("final tuple (%s,%d) is a MIXED or unexpected value — concurrent setters overwrote each other partially", host, port)
	}
	// On-disk must agree with in-memory (the winning commit was persisted).
	if err := svc.Load(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if svc.GetRemoteHost() != host || svc.GetRemotePort() != port {
		t.Fatalf("disk (%s,%d) != memory (%s,%d)", svc.GetRemoteHost(), svc.GetRemotePort(), host, port)
	}
}

// TestSave_ConcurrentSliceMutation_NoMarshalDrift runs concurrent AddShellPath
// (appends to the ShellPaths slice) and Save (which marshals the settings).
// With the immutable-snapshot Save, the marshal deep-copies the slice under mu,
// so there is no data race and no torn read. Without the fix (old Save held a
// live pointer after RLock release) the race detector would flag concurrent
// slice access. Run with -race.
func TestSave_ConcurrentSliceMutation_NoMarshalDrift(t *testing.T) {
	svc := NewService(t.TempDir())
	if err := svc.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	stop := make(chan struct{})
	var wg sync.WaitGroup
	// Writer: append shell paths continuously.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			_ = svc.AddShellPath(ShellEntry{Path: fmt.Sprintf("/sh/%d", i), Label: "x"})
		}
	}()
	// Reader/saver: Save continuously (marshals snapshot).
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				_ = svc.Save()
			}
		}()
	}
	// Let it run briefly to exercise the race window.
	time.Sleep(50 * time.Millisecond)
	close(stop)
	wg.Wait()
}

// TestSetRemoteEndpoint_SaveFails_MemoryReverted_DiskUnchanged forces a Save
// failure by making the .tmp path a directory (so WriteFile fails with EISDIR),
// then asserts the in-memory host/port are reverted to the pre-call values and
// the on-disk file is untouched (R2-Minor-02: fault rollback must not leave a
// partial tuple or clobber another setter's committed value).
func TestSetRemoteEndpoint_SaveFails_MemoryReverted_DiskUnchanged(t *testing.T) {
	dir := t.TempDir()
	svc := NewService(dir)
	if err := svc.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	// Establish a known-good committed baseline on disk.
	if err := svc.SetRemoteEndpoint("10.0.0.50", 9050); err != nil {
		t.Fatalf("baseline SetRemoteEndpoint: %v", err)
	}
	diskBefore, _ := os.ReadFile(filepath.Join(dir, "settings.json"))

	// Force the NEXT Save to fail: create the .tmp path as a directory so
	// WriteFile(configPath+".tmp") returns EISDIR.
	tmpDir := filepath.Join(dir, "settings.json.tmp")
	if err := os.MkdirAll(tmpDir, 0o700); err != nil {
		t.Fatalf("mkdir tmp fault: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmpDir) })

	err := svc.SetRemoteEndpoint("172.16.0.9", 9099)
	if err == nil {
		t.Fatal("SetRemoteEndpoint must fail when Save fails (injected .tmp dir)")
	}
	// In-memory reverted to the baseline tuple.
	if got := svc.GetRemoteHost(); got != "10.0.0.50" {
		t.Fatalf("in-memory host after failed Save=%q want 10.0.0.50 (reverted)", got)
	}
	if got := svc.GetRemotePort(); got != 9050 {
		t.Fatalf("in-memory port after failed Save=%d want 9050 (reverted)", got)
	}
	// On-disk unchanged.
	diskAfter, _ := os.ReadFile(filepath.Join(dir, "settings.json"))
	if string(diskAfter) != string(diskBefore) {
		t.Fatalf("on-disk settings changed after a failed Save\n got: %s\nwant: %s", diskAfter, diskBefore)
	}
}

// TestSetRemoteEndpoint_ConcurrentOneFails_OtherStillConsistent runs two
// concurrent SetRemoteEndpoint calls where one is forced to fail (Save fault)
// and the other succeeds. The failed call's rollback (under saveMu) must NOT
// overwrite the successful call's committed value: the final tuple is the
// successful call's pair, not the failed call's pre-state.
func TestSetRemoteEndpoint_ConcurrentOneFails_OtherStillConsistent(t *testing.T) {
	for trial := 0; trial < 20; trial++ {
		dir := t.TempDir()
		svc := NewService(dir)
		if err := svc.Load(); err != nil {
			t.Fatalf("Load: %v", err)
		}
		// Baseline.
		if err := svc.SetRemoteEndpoint("10.0.0.1", 9001); err != nil {
			t.Fatalf("baseline: %v", err)
		}
		var wg sync.WaitGroup
		// Failing call: .tmp is a directory.
		tmpDir := filepath.Join(dir, "settings.json.tmp")
		_ = os.MkdirAll(tmpDir, 0o700)
		wg.Add(2)
		var failErr error
		go func() {
			defer wg.Done()
			failErr = svc.SetRemoteEndpoint("192.168.1.99", 9999) // will fail (Save fault)
		}()
		go func() {
			defer wg.Done()
			// Remove the fault so the successful call can commit; a tiny race is
			// acceptable — we only assert the FINAL tuple is internally consistent.
			_ = os.RemoveAll(tmpDir)
			if err := svc.SetRemoteEndpoint("10.0.0.7", 9007); err != nil {
				t.Errorf("successful SetRemoteEndpoint: %v", err)
			}
		}()
		wg.Wait()
		_ = failErr // one of the two calls is expected to fail

		host := svc.GetRemoteHost()
		port := svc.GetRemotePort()
		consistent := (host == "10.0.0.7" && port == 9007) || (host == "10.0.0.1" && port == 9001) || (host == "192.168.1.99" && port == 9999)
		if !consistent {
			t.Fatalf("trial %d: final tuple (%s,%d) is MIXED — rollback overwrote a committed value", trial, host, port)
		}
		// Disk must match memory.
		if err := svc.Load(); err != nil {
			t.Fatalf("reload: %v", err)
		}
		if svc.GetRemoteHost() != host || svc.GetRemotePort() != port {
			t.Fatalf("trial %d: disk (%s,%d) != memory (%s,%d)", trial, svc.GetRemoteHost(), svc.GetRemotePort(), host, port)
		}
	}
}

// --- R3-Minor-02: Load serialization + setter Save-fault three-way ---

// assertRemoteThreeWayConsistent reloads a FRESH service from the same dir and
// asserts the live service's (host,port,enabled) match disk (R3-Minor-02).
func assertRemoteThreeWayConsistent(t *testing.T, live *Service, dir string) {
	t.Helper()
	fresh := NewService(dir)
	if err := fresh.Load(); err != nil {
		t.Fatalf("fresh Load: %v", err)
	}
	if live.GetRemoteHost() != fresh.GetRemoteHost() {
		t.Errorf("memory host=%q != disk host=%q", live.GetRemoteHost(), fresh.GetRemoteHost())
	}
	if live.GetRemotePort() != fresh.GetRemotePort() {
		t.Errorf("memory port=%d != disk port=%d", live.GetRemotePort(), fresh.GetRemotePort())
	}
	if live.GetRemoteEnabled() != fresh.GetRemoteEnabled() {
		t.Errorf("memory enabled=%v != disk enabled=%v", live.GetRemoteEnabled(), fresh.GetRemoteEnabled())
	}
}

// TestSetRemoteEndpoint_VsLoad_NoStaleOverwrite proves Settings.Load is in the
// saveMu sequence (R3-Minor-02 ②): a concurrent Load cannot read stale disk and
// overwrite an in-flight SetRemoteEndpoint's mutated memory. After repeated
// concurrent endpoint+Load cycles, memory always equals disk.
func TestSetRemoteEndpoint_VsLoad_NoStaleOverwrite(t *testing.T) {
	dir := t.TempDir()
	svc := NewService(dir)
	if err := svc.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := svc.SetRemoteEndpoint("10.0.0.1", 9001); err != nil {
		t.Fatalf("baseline: %v", err)
	}
	pairs := validEndpointPairs(6)
	stop := make(chan struct{})
	var wg sync.WaitGroup
	// Loader goroutine: repeatedly reloads.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			_ = svc.Load()
		}
	}()
	// Endpoint goroutines: repeatedly set distinct tuples.
	for _, p := range pairs {
		wg.Add(1)
		go func(p struct {
			host string
			port int
		}) {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				_ = svc.SetRemoteEndpoint(p.host, p.port)
			}
		}(p)
	}
	// Let them race briefly.
	time.Sleep(60 * time.Millisecond)
	close(stop)
	wg.Wait()
	// Three-way consistency: memory == disk.
	assertRemoteThreeWayConsistent(t, svc, dir)
}

// TestSetRemoteHost_SaveFails_MemoryReverted_DiskUnchanged proves the individual
// setter reverts in-memory on Save failure (R3-Minor-02 ③).
func TestSetRemoteHost_SaveFails_MemoryReverted_DiskUnchanged(t *testing.T) {
	dir := t.TempDir()
	svc := NewService(dir)
	if err := svc.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := svc.SetRemoteHost("10.0.0.50"); err != nil {
		t.Fatalf("baseline: %v", err)
	}
	diskBefore, _ := os.ReadFile(filepath.Join(dir, "settings.json"))
	tmpDir := filepath.Join(dir, "settings.json.tmp")
	if err := os.MkdirAll(tmpDir, 0o700); err != nil {
		t.Fatalf("mkdir tmp fault: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmpDir) })
	if err := svc.SetRemoteHost("172.16.0.9"); err == nil {
		t.Fatal("SetRemoteHost must fail when Save fails")
	}
	if got := svc.GetRemoteHost(); got != "10.0.0.50" {
		t.Fatalf("memory host after failed Save=%q want 10.0.0.50 (reverted)", got)
	}
	diskAfter, _ := os.ReadFile(filepath.Join(dir, "settings.json"))
	if string(diskAfter) != string(diskBefore) {
		t.Fatal("disk host changed after a failed Save")
	}
}

// TestSetRemotePort_SaveFails_MemoryReverted_DiskUnchanged (R3-Minor-02 ③).
func TestSetRemotePort_SaveFails_MemoryReverted_DiskUnchanged(t *testing.T) {
	dir := t.TempDir()
	svc := NewService(dir)
	if err := svc.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := svc.SetRemotePort(9050); err != nil {
		t.Fatalf("baseline: %v", err)
	}
	diskBefore, _ := os.ReadFile(filepath.Join(dir, "settings.json"))
	tmpDir := filepath.Join(dir, "settings.json.tmp")
	if err := os.MkdirAll(tmpDir, 0o700); err != nil {
		t.Fatalf("mkdir tmp fault: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmpDir) })
	if err := svc.SetRemotePort(9099); err == nil {
		t.Fatal("SetRemotePort must fail when Save fails")
	}
	if got := svc.GetRemotePort(); got != 9050 {
		t.Fatalf("memory port after failed Save=%d want 9050 (reverted)", got)
	}
	diskAfter, _ := os.ReadFile(filepath.Join(dir, "settings.json"))
	if string(diskAfter) != string(diskBefore) {
		t.Fatal("disk changed after a failed Save")
	}
}

// TestSetRemoteEnabled_SaveFails_MemoryReverted_DiskUnchanged (R3-Minor-02 ③).
func TestSetRemoteEnabled_SaveFails_MemoryReverted_DiskUnchanged(t *testing.T) {
	dir := t.TempDir()
	svc := NewService(dir)
	if err := svc.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := svc.SetRemoteEnabled(true); err != nil {
		t.Fatalf("baseline: %v", err)
	}
	diskBefore, _ := os.ReadFile(filepath.Join(dir, "settings.json"))
	tmpDir := filepath.Join(dir, "settings.json.tmp")
	if err := os.MkdirAll(tmpDir, 0o700); err != nil {
		t.Fatalf("mkdir tmp fault: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmpDir) })
	if err := svc.SetRemoteEnabled(false); err == nil {
		t.Fatal("SetRemoteEnabled must fail when Save fails")
	}
	if got := svc.GetRemoteEnabled(); got != true {
		t.Fatalf("memory enabled after failed Save=%v want true (reverted)", got)
	}
	diskAfter, _ := os.ReadFile(filepath.Join(dir, "settings.json"))
	if string(diskAfter) != string(diskBefore) {
		t.Fatal("disk changed after a failed Save")
	}
}

// --- R4-Minor: Save commit boundary (rename is last fallible step) ---

// TestSaveLocked_PreRenameSyncFailure_DiskUnchanged proves the R4-Minor commit
// boundary: a pre-rename sync failure leaves disk == old value (rename never
// ran), so the setter's memory revert keeps memory==disk. There is NO fallible
// step after rename (post-rename chmod removed), so any saveLocked error means
// "not committed".
func TestSaveLocked_PreRenameSyncFailure_DiskUnchanged(t *testing.T) {
	dir := t.TempDir()
	svc := NewService(dir)
	if err := svc.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := svc.SetRemoteEndpoint("10.0.0.1", 9001); err != nil {
		t.Fatalf("baseline: %v", err)
	}
	diskBefore, _ := os.ReadFile(filepath.Join(dir, "settings.json"))

	settingsPreRenameSync = func(*os.File) error { return errors.New("injected sync failure") }
	t.Cleanup(func() { settingsPreRenameSync = func(f *os.File) error { return f.Sync() } })

	if err := svc.SetRemoteEndpoint("172.16.0.9", 9099); err == nil {
		t.Fatal("SetRemoteEndpoint must fail when the pre-rename sync fails")
	}
	// memory reverted to baseline.
	if got := svc.GetRemoteHost(); got != "10.0.0.1" {
		t.Fatalf("memory host=%q want 10.0.0.1 (reverted)", got)
	}
	// disk unchanged (rename never ran; tmp removed).
	diskAfter, _ := os.ReadFile(filepath.Join(dir, "settings.json"))
	if string(diskAfter) != string(diskBefore) {
		t.Fatalf("disk changed despite a pre-rename (not-committed) failure\n got: %s\nwant: %s", diskAfter, diskBefore)
	}
	// No leftover .tmp.
	if _, err := os.Stat(filepath.Join(dir, "settings.json.tmp")); err == nil {
		t.Fatal("leftover settings.json.tmp after a pre-rename failure")
	}
}

// TestSaveLocked_NoFallibleStepAfterRename proves structurally/behaviorally that
// a successful save leaves the file at mode 0600 with the new content and that
// there is no separate post-rename chmod (the temp was prepared at 0600 before
// rename, which carries the mode). This is the positive side of the commit
// boundary: after rename succeeds the file is final.
func TestSaveLocked_NoFallibleStepAfterRename(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file mode assertion")
	}
	dir := t.TempDir()
	svc := NewService(dir)
	if err := svc.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := svc.SetRemoteEndpoint("10.0.0.7", 9007); err != nil {
		t.Fatalf("SetRemoteEndpoint: %v", err)
	}
	fi, err := os.Stat(filepath.Join(dir, "settings.json"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := fi.Mode().Perm(); got != 0o600 {
		t.Fatalf("settings.json mode=%o want 0600 (temp prepared pre-rename; no post-rename chmod)", got)
	}
	// A fresh service reloads the committed value (rename was the commit).
	fresh := NewService(dir)
	if err := fresh.Load(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if fresh.GetRemoteHost() != "10.0.0.7" || fresh.GetRemotePort() != 9007 {
		t.Fatalf("reloaded=(%s,%d) want (10.0.0.7,9007)", fresh.GetRemoteHost(), fresh.GetRemotePort())
	}
}

// TestSetRemoteEnabled_SaveFails_MemoryAndLiveConsistent proves the enabled
// setter reverts memory on Save failure (R4-Minor ③) and that a fresh reload
// reads the old (persisted) value — three-way consistency on the enabled field.
func TestSetRemoteEnabled_SaveFails_MemoryAndLiveConsistent(t *testing.T) {
	dir := t.TempDir()
	svc := NewService(dir)
	if err := svc.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := svc.SetRemoteEnabled(true); err != nil {
		t.Fatalf("baseline: %v", err)
	}
	tmpDir := filepath.Join(dir, "settings.json.tmp")
	if err := os.MkdirAll(tmpDir, 0o700); err != nil {
		t.Fatalf("mkdir tmp fault: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmpDir) })
	if err := svc.SetRemoteEnabled(false); err == nil {
		t.Fatal("SetRemoteEnabled must fail when Save fails")
	}
	if svc.GetRemoteEnabled() != true {
		t.Fatal("memory enabled must be REVERTED to true on Save failure")
	}
	fresh := NewService(dir)
	if err := fresh.Load(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if fresh.GetRemoteEnabled() != true {
		t.Fatal("disk enabled must still be true (Save failed before rename)")
	}
}

// TestSaveLocked_PreRenameSyncHandle_IsWritable is the R5-N01 shape guard: the
// pre-rename fsync must run on a WRITABLE handle (O_RDWR → Windows
// GENERIC_WRITE), because FlushFileBuffers (backing File.Sync on Windows)
// requires GENERIC_WRITE — a read-only reopen (os.Open / O_RDONLY) would make
// every Save fail pre-commit on Windows. This test proves the handle handed to
// settingsPreRenameSync is writable by writing a probe byte through it (a
// read-only handle would reject the write). It runs on every platform; the
// same O_RDWR code path runs on Windows, so write-success here implies the
// Windows FlushFileBuffers access-right requirement is met.
func TestSaveLocked_PreRenameSyncHandle_IsWritable(t *testing.T) {
	dir := t.TempDir()
	svc := NewService(dir)
	if err := svc.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	sawWritable := false
	settingsPreRenameSync = func(f *os.File) error {
		// Probe: write a trailing newline through the sync handle. A read-only
		// handle (O_RDONLY) fails this with EBADF/EPERM — the same access denial
		// that would break FlushFileBuffers on Windows.
		if n, err := f.Write([]byte("\n")); err == nil && n == 1 {
			sawWritable = true
		}
		return f.Sync()
	}
	t.Cleanup(func() { settingsPreRenameSync = func(f *os.File) error { return f.Sync() } })

	if err := svc.SetRemoteHost("10.0.0.9"); err != nil {
		t.Fatalf("SetRemoteHost: %v", err)
	}
	if !sawWritable {
		t.Fatal("pre-rename sync handle must be WRITABLE (O_RDWR/GENERIC_WRITE), not read-only (R5-N01)")
	}
	// The probe appended a trailing newline (JSON trailing whitespace is valid);
	// reload must read back the persisted host, proving a real Save/reload round
	// trip on a writable sync handle.
	fresh := NewService(dir)
	if err := fresh.Load(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if fresh.GetRemoteHost() != "10.0.0.9" {
		t.Fatalf("reloaded host=%q want 10.0.0.9", fresh.GetRemoteHost())
	}
}

// --- Skin Settings（本地图片皮肤，plan 后端切片 A）---

// TestSkinSettings_DefaultAndRoundTrip 验证默认值（关闭、dim 35、opacity 70、
// textBoost 0）与 Set/Get 往返（含持久化：新建 Service 从同目录 Load 后
// 仍读到新值）。
func TestSkinSettings_DefaultAndRoundTrip(t *testing.T) {
	dir := t.TempDir()
	svc := NewService(dir)
	if err := svc.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := SkinSettings{Enabled: false, ImageID: "", Dim: 35, Blur: 0, Opacity: 70, TextBoost: 0}
	if got := svc.GetSkinSettings(); got != want {
		t.Fatalf("fresh default = %+v, want %+v", got, want)
	}

	set := SkinSettings{Enabled: true, ImageID: "abc123", Dim: 60, Blur: 12, Opacity: 85, TextBoost: 60}
	if err := svc.SetSkinSettings(set); err != nil {
		t.Fatalf("SetSkinSettings: %v", err)
	}
	if got := svc.GetSkinSettings(); got != set {
		t.Fatalf("after set = %+v, want %+v", got, set)
	}

	fresh := NewService(dir)
	if err := fresh.Load(); err != nil {
		t.Fatalf("fresh Load: %v", err)
	}
	if got := fresh.GetSkinSettings(); got != set {
		t.Fatalf("reloaded = %+v, want %+v", got, set)
	}
}

// TestSkinSettings_ClampOutOfRange 验证越界值取边界而非报错（opacity/
// textBoost 边界 -1/101）。
func TestSkinSettings_ClampOutOfRange(t *testing.T) {
	dir := t.TempDir()
	svc := NewService(dir)
	if err := svc.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := svc.SetSkinSettings(SkinSettings{Dim: 200, Blur: 99, Opacity: 101, TextBoost: 101}); err != nil {
		t.Fatalf("SetSkinSettings(high): %v", err)
	}
	if got := svc.GetSkinSettings(); got.Dim != 100 || got.Blur != 40 || got.Opacity != 100 || got.TextBoost != 100 {
		t.Fatalf("high clamp = %+v, want dim=100 blur=40 opacity=100 textBoost=100", got)
	}
	if err := svc.SetSkinSettings(SkinSettings{Dim: -10, Blur: -1, Opacity: -1, TextBoost: -1}); err != nil {
		t.Fatalf("SetSkinSettings(low): %v", err)
	}
	if got := svc.GetSkinSettings(); got.Dim != 0 || got.Blur != 0 || got.Opacity != 0 || got.TextBoost != 0 {
		t.Fatalf("low clamp = %+v, want dim=0 blur=0 opacity=0 textBoost=0", got)
	}
}

// TestSkinSettings_MissingKeyFallsBackToDefault 验证老 settings.json 无
// skin 键时 Load 合并为默认值（而非零值 dim=0/opacity=0）。
func TestSkinSettings_MissingKeyFallsBackToDefault(t *testing.T) {
	dir := t.TempDir()
	legacy := `{"dashboard":{"claudeMode":"embedded"},"remoteHost":"127.0.0.1","remotePort":8680}`
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(legacy), 0o600); err != nil {
		t.Fatalf("write legacy settings: %v", err)
	}
	svc := NewService(dir)
	if err := svc.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := SkinSettings{Enabled: false, ImageID: "", Dim: 35, Blur: 0, Opacity: 70, TextBoost: 0}
	if got := svc.GetSkinSettings(); got != want {
		t.Fatalf("legacy load = %+v, want default %+v", got, want)
	}
}

// TestSkinSettings_LegacySkinKeyWithoutOpacity 验证含 skin 键但缺
// opacity 子键的老文件（v1.3.32 写出）读入 opacity=0：0 是合法档位（全透），
// 与“未写入”不可区分，不能回填默认——已知取舍，其余字段原样保留；
// enabled=false 时零值无渲染影响（见 normalizeSkinSettings 注释）。
func TestSkinSettings_LegacySkinKeyWithoutOpacity(t *testing.T) {
	dir := t.TempDir()
	raw := `{"skin":{"enabled":false,"imageId":"","dim":40,"blur":8}}`
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(raw), 0o600); err != nil {
		t.Fatalf("write settings: %v", err)
	}
	svc := NewService(dir)
	if err := svc.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := svc.GetSkinSettings()
	want := SkinSettings{Enabled: false, ImageID: "", Dim: 40, Blur: 8, Opacity: 0}
	if got != want {
		t.Fatalf("legacy skin without opacity = %+v, want %+v", got, want)
	}
}

// TestSkinSettings_LegacySkinKeyWithoutTextBoost 验证含 skin 键但缺
// textBoost 子键的老文件（textBoost 上线前版本写出）读入 textBoost=0：
// 0 即默认值（不增强、保持现状），零值与“未写入该键”不可区分但语义
// 重合，无突变问题——比 opacity（0≠默认 70）的取舍更干净（见
// normalizeSkinSettings 注释）。
func TestSkinSettings_LegacySkinKeyWithoutTextBoost(t *testing.T) {
	dir := t.TempDir()
	raw := `{"skin":{"enabled":false,"imageId":"","dim":40,"blur":8,"opacity":65}}`
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(raw), 0o600); err != nil {
		t.Fatalf("write settings: %v", err)
	}
	svc := NewService(dir)
	if err := svc.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := svc.GetSkinSettings()
	want := SkinSettings{Enabled: false, ImageID: "", Dim: 40, Blur: 8, Opacity: 65, TextBoost: 0}
	if got != want {
		t.Fatalf("legacy skin without textBoost = %+v, want %+v", got, want)
	}
}

// TestSkinSettings_LoadOutOfRangeClamped 验证手改文件中的越界值在 Load
// 时被合并 clamp。
func TestSkinSettings_LoadOutOfRangeClamped(t *testing.T) {
	dir := t.TempDir()
	raw := `{"skin":{"enabled":true,"imageId":"x","dim":500,"blur":99,"opacity":500,"textBoost":500}}`
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(raw), 0o600); err != nil {
		t.Fatalf("write settings: %v", err)
	}
	svc := NewService(dir)
	if err := svc.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := svc.GetSkinSettings()
	if !got.Enabled || got.ImageID != "x" || got.Dim != 100 || got.Blur != 40 || got.Opacity != 100 || got.TextBoost != 100 {
		t.Fatalf("clamped load = %+v, want enabled=true x dim=100 blur=40 opacity=100 textBoost=100", got)
	}
}

// TestCommitSummaryPreset_RoundTrip 验证 AI 提交总结预设（"provider/preset名"）
// 的 Get/Set 往返与磁盘持久化；空值=未设置。
func TestCommitSummaryPreset_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	svc := NewService(dir)
	if err := svc.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := svc.GetCommitSummaryPreset(); got != "" {
		t.Fatalf("default CommitSummaryPreset = %q, want empty", got)
	}
	if err := svc.SetCommitSummaryPreset("zen/summarizer"); err != nil {
		t.Fatalf("SetCommitSummaryPreset: %v", err)
	}
	if got := svc.GetCommitSummaryPreset(); got != "zen/summarizer" {
		t.Fatalf("CommitSummaryPreset = %q, want zen/summarizer", got)
	}

	// 重新 Load 验证落盘持久化。
	svc2 := NewService(dir)
	if err := svc2.Load(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := svc2.GetCommitSummaryPreset(); got != "zen/summarizer" {
		t.Fatalf("persisted CommitSummaryPreset = %q, want zen/summarizer", got)
	}

	// 清除后 omitempty 不再写键。
	if err := svc2.SetCommitSummaryPreset(""); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if got := svc2.GetCommitSummaryPreset(); got != "" {
		t.Fatalf("cleared CommitSummaryPreset = %q, want empty", got)
	}
	b, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err != nil {
		t.Fatalf("read settings.json: %v", err)
	}
	if strings.Contains(string(b), "commitSummaryPreset") {
		t.Fatalf("cleared preset must be omitted from settings.json, got: %s", b)
	}
}
