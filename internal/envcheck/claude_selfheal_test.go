package envcheck

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"amagi-codebox/internal/platform"
)

// ---------------------------------------------------------------------------
// P0-1: looksLikeClaudeNPMStagingConflict
// ---------------------------------------------------------------------------

func TestLooksLikeClaudeNPMStagingConflict(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "enotempty lower", err: errors.New("npm ERR! ENOTEMPTY: directory not empty"), want: true},
		{name: "ENOTEMPTY upper", err: errors.New("npm ERR! Code ENOTEMPTY"), want: true},
		{name: "errno -66 with space", err: errors.New("npm ERR! errno -66"), want: true},
		{name: "errno=-66 nospace", err: errors.New("npm ERR! errno=-66"), want: true},
		{name: "staging rename", err: errors.New("rename '.claude-code-XDWIThDw' -> 'claude-code' failed"), want: true},
		{name: "directory not empty alone", err: errors.New("directory not empty"), want: true},
		{name: "unrelated", err: errors.New("network timeout"), want: false},
		{name: "permission denied", err: errors.New("EACCES permission denied"), want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := looksLikeClaudeNPMStagingConflict(tc.err); got != tc.want {
				t.Fatalf("got %v, want %v for err=%v", got, tc.want, tc.err)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// P0-1/P0-2: globClaudeStagingDirs / cleanClaudeNPMResidue
// ---------------------------------------------------------------------------

// writeFile is a small helper for laying out fixture files in tests.
func writeFixture(t *testing.T, path string, contents []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, contents, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestGlobClaudeStagingDirs_FindsStagingOnly(t *testing.T) {
	scopedDir := t.TempDir()
	// Create: a staging dir, the main package dir, an unrelated package dir,
	// and a regular file (not a directory) matching the prefix.
	stagingA := filepath.Join(scopedDir, ".claude-code-XDWIThDw")
	stagingB := filepath.Join(scopedDir, ".claude-code-AbCdEf")
	mainPkg := filepath.Join(scopedDir, "claude-code")
	sibling := filepath.Join(scopedDir, "claude-code-helper") // must NOT match
	otherPkg := filepath.Join(scopedDir, "some-other-pkg")
	stagingFile := filepath.Join(scopedDir, ".claude-code-notadir")
	for _, d := range []string{stagingA, stagingB, mainPkg, sibling, otherPkg} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
	writeFixture(t, stagingFile, []byte("x"))

	matches := globClaudeStagingDirs(scopedDir)
	if len(matches) != 2 {
		t.Fatalf("expected 2 staging matches, got %d: %v", len(matches), matches)
	}
	for _, m := range matches {
		base := filepath.Base(m)
		if !strings.HasPrefix(base, ".claude-code-") {
			t.Fatalf("match %q does not have staging prefix", m)
		}
		// "claude-code-helper" must NOT match because it lacks the leading dot.
		if base == "claude-code-helper" {
			t.Fatalf("non-staging sibling %q was matched", m)
		}
	}
}

func TestGlobClaudeStagingDirs_EmptyAndDotPrefix(t *testing.T) {
	if got := globClaudeStagingDirs(""); len(got) != 0 {
		t.Fatalf("expected no matches for empty prefix, got %v", got)
	}
	if got := globClaudeStagingDirs("."); len(got) != 0 {
		t.Fatalf("expected no matches for dot prefix, got %v", got)
	}
	if got := globClaudeStagingDirs("   "); len(got) != 0 {
		t.Fatalf("expected no matches for whitespace prefix, got %v", got)
	}
}

// fakeNpmPrefixRunner is a mock ProcessRunner that returns a fixed npm prefix
// for `npm prefix -g` calls. It is the dependency-injection seam for testing
// cleanClaudeNPMResidue / detectClaudeNPMStagingResidue without hardcoding
// host paths.
type fakeNpmPrefixRunner struct {
	prefix string
	mu     sync.Mutex
	calls  []platform.CommandSpec
}

func (r *fakeNpmPrefixRunner) Run(_ context.Context, spec platform.CommandSpec) (*platform.ProcessResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, spec)
	if len(spec.Args) == 2 && spec.Args[0] == "prefix" && spec.Args[1] == "-g" {
		return &platform.ProcessResult{Stdout: r.prefix}, nil
	}
	return &platform.ProcessResult{}, nil
}

func (r *fakeNpmPrefixRunner) Start(_ platform.CommandSpec) (*exec.Cmd, error) {
	return nil, nil
}

func TestDetectClaudeNPMStagingResidue_FindsLeftover(t *testing.T) {
	root := t.TempDir()
	prefix := filepath.Join(root, "npm-global")
	scopedDir := filepath.Join(prefix, "node_modules", "@anthropic-ai")
	staging := filepath.Join(scopedDir, ".claude-code-DeadBeef")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatalf("mkdir staging: %v", err)
	}
	// Drop a placeholder file inside the staging dir so RemoveAll has work.
	writeFixture(t, filepath.Join(staging, "package.json"), []byte("{}"))

	runner := &fakeNpmPrefixRunner{prefix: prefix}
	svc := NewServiceWithRunner(runner)

	found, err := svc.detectClaudeNPMStagingResidue()
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if len(found) != 1 {
		t.Fatalf("expected 1 staging dir, got %d: %v", len(found), found)
	}
	if filepath.Clean(found[0]) != filepath.Clean(staging) {
		t.Fatalf("found %q, want %q", found[0], staging)
	}
}

func TestDetectClaudeNPMStagingResidue_EmptyWhenClean(t *testing.T) {
	root := t.TempDir()
	prefix := filepath.Join(root, "npm-global")
	if err := os.MkdirAll(filepath.Join(prefix, "node_modules", "@anthropic-ai"), 0o755); err != nil {
		t.Fatalf("mkdir scoped: %v", err)
	}
	runner := &fakeNpmPrefixRunner{prefix: prefix}
	svc := NewServiceWithRunner(runner)

	found, err := svc.detectClaudeNPMStagingResidue()
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if len(found) != 0 {
		t.Fatalf("expected 0 staging dirs on clean install, got %v", found)
	}
}

func TestCleanClaudeNPMResidue_RemovesStagingPackageAndBinLink(t *testing.T) {
	root := t.TempDir()
	prefix := filepath.Join(root, "npm-global")
	scopedDir := filepath.Join(prefix, "node_modules", "@anthropic-ai")
	staging := filepath.Join(scopedDir, ".claude-code-DeadBeef")
	mainPkg := filepath.Join(scopedDir, "claude-code")
	packageBinTarget := filepath.Join(mainPkg, "bin", "claude")
	for _, d := range []string{staging, filepath.Dir(packageBinTarget)} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
	writeFixture(t, filepath.Join(staging, "leftover"), []byte("x"))
	writeFixture(t, filepath.Join(mainPkg, "package.json"), []byte("{}"))
	writeFixture(t, packageBinTarget, []byte("#!/bin/sh\nexec claude\n"))

	// Create an orphan symlink under prefix/bin pointing at the (soon-removed)
	// main package directory. Symlink creation requires a target file to exist
	// at the time of link creation; we point at packageBinTarget.
	binDir := filepath.Join(prefix, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	symlinkPath := filepath.Join(binDir, "claude")
	if err := os.Symlink(packageBinTarget, symlinkPath); err != nil {
		// Some CI filesystems disallow symlinks; skip the assertion in that
		// case but still run the rest of the test.
		t.Logf("symlink creation failed (%v); skipping orphan-bin assertion", err)
		symlinkPath = ""
	}

	runner := &fakeNpmPrefixRunner{prefix: prefix}
	svc := NewServiceWithRunner(runner)

	result, err := svc.cleanClaudeNPMResidue()
	if err != nil {
		t.Fatalf("clean: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil cleanup result")
	}
	if len(result.StagingDirs) != 1 {
		t.Fatalf("staging dirs removed = %v, want 1", result.StagingDirs)
	}
	if len(result.PackageDirs) != 1 {
		t.Fatalf("package dirs removed = %v, want 1", result.PackageDirs)
	}
	if filepath.Clean(result.StagingDirs[0]) != filepath.Clean(staging) {
		t.Fatalf("removed staging %q, want %q", result.StagingDirs[0], staging)
	}
	if filepath.Clean(result.PackageDirs[0]) != filepath.Clean(mainPkg) {
		t.Fatalf("removed package %q, want %q", result.PackageDirs[0], mainPkg)
	}
	if _, statErr := os.Stat(staging); !os.IsNotExist(statErr) {
		t.Fatalf("staging dir still exists after cleanup: %v", statErr)
	}
	if _, statErr := os.Stat(mainPkg); !os.IsNotExist(statErr) {
		t.Fatalf("main package dir still exists after cleanup: %v", statErr)
	}
	if symlinkPath != "" {
		if _, statErr := os.Lstat(symlinkPath); !os.IsNotExist(statErr) {
			t.Fatalf("orphan bin link still exists after cleanup: %v", statErr)
		}
	}
}

func TestCleanClaudeNPMResidueIfPresent_NoOpWhenClean(t *testing.T) {
	root := t.TempDir()
	prefix := filepath.Join(root, "npm-global")
	if err := os.MkdirAll(filepath.Join(prefix, "node_modules", "@anthropic-ai"), 0o755); err != nil {
		t.Fatalf("mkdir scoped: %v", err)
	}
	runner := &fakeNpmPrefixRunner{prefix: prefix}
	svc := NewServiceWithRunner(runner)

	result, err := svc.cleanClaudeNPMResidueIfPresent()
	if err != nil {
		t.Fatalf("cleanIfPresent: %v", err)
	}
	if result != nil {
		t.Fatalf("expected nil result when no residue present, got %+v", result)
	}
}

func TestCleanClaudeNPMResidue_DoesNotTouchUserConfig(t *testing.T) {
	root := t.TempDir()
	prefix := filepath.Join(root, "npm-global")
	scopedDir := filepath.Join(prefix, "node_modules", "@anthropic-ai")
	staging := filepath.Join(scopedDir, ".claude-code-DeadBeef")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatalf("mkdir staging: %v", err)
	}

	// Sensitive user-config files that must NEVER be touched by self-heal.
	homeDir := filepath.Join(root, "home")
	dotClaudeJSON := filepath.Join(homeDir, ".claude.json")
	dotClaudeDir := filepath.Join(homeDir, ".claude", "settings.json")
	zshrc := filepath.Join(homeDir, ".zshrc")
	zprofile := filepath.Join(homeDir, ".zprofile")
	nativeVersions := filepath.Join(homeDir, ".local", "share", "claude", "versions", "2.1.143", "claude")
	for _, f := range []string{dotClaudeJSON, dotClaudeDir, zshrc, zprofile, nativeVersions} {
		writeFixture(t, f, []byte("keep-me"))
	}

	runner := &fakeNpmPrefixRunner{prefix: prefix}
	svc := NewServiceWithRunner(runner)
	if _, err := svc.cleanClaudeNPMResidue(); err != nil {
		t.Fatalf("clean: %v", err)
	}
	for _, f := range []string{dotClaudeJSON, dotClaudeDir, zshrc, zprofile, nativeVersions} {
		if _, err := os.Stat(f); err != nil {
			t.Fatalf("sensitive file %q was removed or became inaccessible: %v", f, err)
		}
	}
}

// ---------------------------------------------------------------------------
// P0-3: InspectClaudeBinaryIntegrity
// ---------------------------------------------------------------------------

func withIntegrityThreshold(t *testing.T, minBytes int64) {
	t.Helper()
	previous := claudeNPMIntegrityMinBytes
	claudeNPMIntegrityMinBytes = minBytes
	t.Cleanup(func() { claudeNPMIntegrityMinBytes = previous })
}

func withSimulatedGOOS(t *testing.T, goos string) {
	t.Helper()
	previous := runtimeGOOS
	runtimeGOOS = goos
	t.Cleanup(func() { runtimeGOOS = previous })
}

func TestInspectClaudeBinaryIntegrity_EmptyPath(t *testing.T) {
	r := InspectClaudeBinaryIntegrity("")
	if r.Exists || r.Corrupted == false && r.Reason == "" {
		// Reason is set to "empty path" but Corrupted stays false because an
		// empty path is not a corruption signal -- there is nothing to inspect.
	}
	if r.Reason != "empty path" {
		t.Fatalf("reason = %q, want %q", r.Reason, "empty path")
	}
}

func TestInspectClaudeBinaryIntegrity_MissingFile(t *testing.T) {
	r := InspectClaudeBinaryIntegrity(filepath.Join(t.TempDir(), "does-not-exist"))
	if r.Exists {
		t.Fatalf("missing file reported as exists")
	}
	if r.Corrupted {
		t.Fatalf("missing file reported as corrupted")
	}
}

func TestInspectClaudeBinaryIntegrity_HealthyOnNonDarwin(t *testing.T) {
	// Simulate Linux so the macOS codesign check is skipped.
	withSimulatedGOOS(t, "linux")
	withIntegrityThreshold(t, 1)

	dir := t.TempDir()
	bin := filepath.Join(dir, "claude")
	writeFixture(t, bin, []byte("ELF payload large enough"))
	r := InspectClaudeBinaryIntegrity(bin)
	if !r.Exists {
		t.Fatalf("expected Exists")
	}
	if !r.Signed {
		t.Fatalf("non-darwin must report Signed=true (skipped), got false")
	}
	if r.Corrupted {
		t.Fatalf("healthy binary flagged corrupted: %s", r.Reason)
	}
}

func TestInspectClaudeBinaryIntegrity_RejectsUndersizedShard(t *testing.T) {
	// Simulate Linux so codesign is skipped -- size check alone applies.
	withSimulatedGOOS(t, "linux")
	// Production threshold (100MB). Fixture is intentionally tiny.
	withIntegrityThreshold(t, 100*1024*1024)

	dir := t.TempDir()
	// 19.3MB truncated shard as observed in the field. Use a real file size
	// that is below the 100MB threshold without bloating the test artifact.
	shard := filepath.Join(dir, "claude")
	writeFixture(t, shard, bytes_30MB())
	r := InspectClaudeBinaryIntegrity(shard)
	if !r.Exists {
		t.Fatal("expected exists")
	}
	if !r.Corrupted {
		t.Fatalf("undersized shard must be flagged corrupted, got %+v", r)
	}
	if !strings.Contains(strings.ToLower(r.Reason), "size") && !strings.Contains(strings.ToLower(r.Reason), "truncated") {
		t.Fatalf("reason should mention size/truncation, got %q", r.Reason)
	}
}

// bytes_30MB returns a 30MB byte slice. We use 30MB (rather than the field-
// observed 19.3MB) because it stays comfortably below the 100MB threshold
// while keeping the fixture smaller than the alternative.
func bytes_30MB() []byte {
	return make([]byte, 30*1024*1024)
}

func TestInspectClaudeBinaryIntegrity_DirectoryIsCorrupted(t *testing.T) {
	withSimulatedGOOS(t, "linux")
	withIntegrityThreshold(t, 1)

	dir := t.TempDir()
	r := InspectClaudeBinaryIntegrity(dir)
	if !r.Exists {
		t.Fatal("directory should be Exists=true")
	}
	if !r.Corrupted {
		t.Fatalf("directory path must be corrupted")
	}
}

// ---------------------------------------------------------------------------
// P0-3: classifyClaudeVersionError (137 / SIGKILL identification)
// ---------------------------------------------------------------------------

func TestClassifyClaudeVersionError_IdentifiesSIGKILL(t *testing.T) {
	withSimulatedGOOS(t, "linux")
	withIntegrityThreshold(t, 1)

	err := classifyClaudeVersionError("", &platform.ProcessResult{
		Stderr: "signal: killed",
	}, errors.New("signal: killed"))
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "AMFI SIGKILL") && !strings.Contains(err.Error(), "corrupted") {
		t.Fatalf("expected SIGKILL/corrupted classification, got %q", err.Error())
	}
}

func TestClassifyClaudeVersionError_IdentifiesExitCode137(t *testing.T) {
	withSimulatedGOOS(t, "linux")
	withIntegrityThreshold(t, 1)

	err := classifyClaudeVersionError("", &platform.ProcessResult{
		Stdout: "exit code 137",
	}, errors.New("exit status 137"))
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "AMFI SIGKILL") {
		t.Fatalf("expected AMFI SIGKILL classification, got %q", err.Error())
	}
}

func TestClassifyClaudeVersionError_FallsBackToGeneric(t *testing.T) {
	withSimulatedGOOS(t, "linux")
	withIntegrityThreshold(t, 1)

	err := classifyClaudeVersionError("", &platform.ProcessResult{}, errors.New("exec: file not found"))
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "run claude --version") {
		t.Fatalf("expected generic 'run claude --version' classification, got %q", err.Error())
	}
	if strings.Contains(err.Error(), "AMFI SIGKILL") {
		t.Fatalf("file-not-found must not be classified as AMFI SIGKILL")
	}
}

func TestClassifyClaudeVersionError_NilErrorIsNil(t *testing.T) {
	if err := classifyClaudeVersionError("", nil, nil); err != nil {
		t.Fatalf("expected nil for nil error, got %v", err)
	}
}

func TestIsClaudeSIGKILLSignal(t *testing.T) {
	cases := []struct {
		msg  string
		err  error
		want bool
	}{
		{"signal: killed", errors.New("x"), true},
		{"exit code 137", nil, true},
		{"exit status 137", nil, true},
		{"unknown system error -88", nil, true},
		{"ENOTRECOVERABLE", nil, true},
		{"network timeout", nil, false},
		{"", nil, false},
	}
	for _, tc := range cases {
		if got := isClaudeSIGKILLSignal(strings.ToLower(tc.msg), tc.err); got != tc.want {
			t.Fatalf("msg=%q err=%v: got %v want %v", tc.msg, tc.err, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// P0-3: healthyClaudeBinaryCandidates filters out corrupted shards
// ---------------------------------------------------------------------------

func TestHealthyClaudeBinaryCandidates_FiltersShards(t *testing.T) {
	withSimulatedGOOS(t, "linux")
	withIntegrityThreshold(t, 100*1024*1024)

	dir := t.TempDir()
	healthy := filepath.Join(dir, "healthy")
	shard := filepath.Join(dir, "shard")
	missing := filepath.Join(dir, "missing")
	writeFixture(t, healthy, make([]byte, 150*1024*1024)) // above threshold
	writeFixture(t, shard, make([]byte, 1*1024))         // below threshold

	got := healthyClaudeBinaryCandidates([]string{healthy, shard, missing})
	if len(got) != 1 {
		t.Fatalf("expected 1 healthy candidate, got %d: %v", len(got), got)
	}
	if filepath.Clean(got[0]) != filepath.Clean(healthy) {
		t.Fatalf("kept %q, want %q", got[0], healthy)
	}
}

// ---------------------------------------------------------------------------
// P0-3: ensureClaudeBinaryHealthy
// ---------------------------------------------------------------------------

func TestEnsureClaudeBinaryHealthy_HealthyReturnsNil(t *testing.T) {
	withSimulatedGOOS(t, "linux")
	withIntegrityThreshold(t, 1)

	dir := t.TempDir()
	bin := filepath.Join(dir, "claude")
	writeFixture(t, bin, []byte("ok"))
	if err := ensureClaudeBinaryHealthy(bin); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestEnsureClaudeBinaryHealthy_CorruptedReturnsError(t *testing.T) {
	withSimulatedGOOS(t, "linux")
	withIntegrityThreshold(t, 100*1024*1024)

	dir := t.TempDir()
	shard := filepath.Join(dir, "claude")
	writeFixture(t, shard, []byte("tiny"))
	if err := ensureClaudeBinaryHealthy(shard); err == nil {
		t.Fatal("expected error for corrupted binary")
	}
}

func TestEnsureClaudeBinaryHealthy_EmptyPathReturnsError(t *testing.T) {
	if err := ensureClaudeBinaryHealthy(""); err == nil {
		t.Fatal("expected error for empty path")
	}
}

// ---------------------------------------------------------------------------
// P0-3: claudeBinaryCandidateLooksHealthy gates the fallback list
// ---------------------------------------------------------------------------

func TestClaudeBinaryCandidateLooksHealthy_MissingIsAcceptable(t *testing.T) {
	withSimulatedGOOS(t, "linux")
	withIntegrityThreshold(t, 1)
	missing := filepath.Join(t.TempDir(), "nope")
	if !claudeBinaryCandidateLooksHealthy(missing) {
		t.Fatal("missing candidate should be acceptable (caller handles absence)")
	}
}

func TestClaudeBinaryCandidateLooksHealthy_CorruptedRejected(t *testing.T) {
	withSimulatedGOOS(t, "linux")
	withIntegrityThreshold(t, 100*1024*1024)
	shard := filepath.Join(t.TempDir(), "claude")
	writeFixture(t, shard, []byte("tiny"))
	if claudeBinaryCandidateLooksHealthy(shard) {
		t.Fatal("corrupted shard should be rejected")
	}
}

// ---------------------------------------------------------------------------
// P0-4: timeout constants
// ---------------------------------------------------------------------------

func TestClaudeNPMInstallTimeoutExceedsDefault(t *testing.T) {
	if claudeNPMInstallTimeout <= installCommandTimeout {
		t.Fatalf("claudeNPMInstallTimeout (%v) must exceed default installCommandTimeout (%v) -- see Claude-Code-安装异常说明.md section 六",
			claudeNPMInstallTimeout, installCommandTimeout)
	}
	// The threshold should be at least 5 minutes: the field-observed
	// interrupt happened at the 120s mark, and 206MB binary downloads can
	// legitimately exceed that on slow networks.
	if claudeNPMInstallTimeout < 5*time.Minute {
		t.Fatalf("claudeNPMInstallTimeout (%v) should be at least 5 minutes", claudeNPMInstallTimeout)
	}
}

func TestNpmClaudeCommand_AppliesExtendedTimeout(t *testing.T) {
	for _, op := range []installOperation{installOperationInstall, installOperationUpdate} {
		cmd := npmClaudeCommand(op)
		if cmd.timeout != claudeNPMInstallTimeout {
			t.Fatalf("op=%v: cmd.timeout = %v, want %v", op, cmd.timeout, claudeNPMInstallTimeout)
		}
		if got := commandTimeout(cmd); got != claudeNPMInstallTimeout {
			t.Fatalf("op=%v: commandTimeout = %v, want %v", op, got, claudeNPMInstallTimeout)
		}
	}
}

// ---------------------------------------------------------------------------
// P0-3: production codesign path on darwin is wired through runtimeGOOS
// ---------------------------------------------------------------------------

func TestInspectClaudeBinaryIntegrity_ProductionDarwinUsesCodesign(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("codesign verification only runs on real macOS; skipping on simulated platforms")
	}
	// A non-binary tmp file will fail codesign on real macOS, exercising the
	// production path. Keep threshold low so the size check does not preempt.
	withIntegrityThreshold(t, 1)
	dir := t.TempDir()
	target := filepath.Join(dir, "claude")
	writeFixture(t, target, []byte("not a real mach-O"))
	r := InspectClaudeBinaryIntegrity(target)
	if !r.Exists {
		t.Fatal("expected exists")
	}
	if r.Signed {
		t.Fatalf("non-signed tmp file must not pass codesign verification; output=%q", r.CodesignOutput)
	}
	if !r.Corrupted {
		t.Fatalf("unsigned binary on darwin must be flagged corrupted")
	}
}

// ---------------------------------------------------------------------------
// LooksLikeOrphanClaudeBinLink
// ---------------------------------------------------------------------------

func TestLooksLikeOrphanClaudeBinLink_BrokenSymlinkIntoRemovedPackage(t *testing.T) {
	dir := t.TempDir()
	// Create a symlink whose target path string contains the package marker
	// but whose target does NOT exist (post-cleanup state).
	link := filepath.Join(dir, "claude")
	if err := os.Symlink(filepath.Join(dir, "node_modules", "@anthropic-ai", "claude-code", "bin", "claude"), link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("lstat: %v", err)
	}
	pkg := filepath.Join(dir, "node_modules", "@anthropic-ai", "claude-code")
	if !looksLikeOrphanClaudeBinLink(link, info, pkg) {
		t.Fatal("broken symlink into removed package must be considered orphan")
	}
}

func TestLooksLikeOrphanClaudeBinLink_UnrelatedSymlinkRejected(t *testing.T) {
	dir := t.TempDir()
	// Symlink to /tmp (or equivalent) -- unrelated to the removed package.
	target := filepath.Join(dir, "target")
	writeFixture(t, target, []byte("x"))
	link := filepath.Join(dir, "claude")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("lstat: %v", err)
	}
	pkg := filepath.Join(dir, "node_modules", "@anthropic-ai", "claude-code")
	if looksLikeOrphanClaudeBinLink(link, info, pkg) {
		t.Fatal("unrelated symlink must NOT be considered orphan")
	}
}

// ---------------------------------------------------------------------------
// M-1: looksLikeClaudeCorruptionError + detect-path self-heal
// ---------------------------------------------------------------------------

func TestLooksLikeClaudeCorruptionError(t *testing.T) {
	// After the structured-error refactor, looksLikeClaudeCorruptionError
	// keys off the *claudeVersionError.Kind carried on the error rather than
	// substring-matching the rendered message. The corruption-positive cases
	// therefore construct errors via classifyClaudeVersionError (the only
	// producer of *claudeVersionError) to exercise the real classifier path,
	// while the corruption-negative cases confirm that other errors --
	// including hand-crafted strings that USED to match by substring --
	// correctly miss the gate now that the gate is type-driven.
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "classified SIGKILL",
			err: classifyClaudeVersionError("", &platform.ProcessResult{Stderr: "signal: killed"}, errors.New("signal: killed")),
			want: true},
		{name: "classified exit code 137",
			err: classifyClaudeVersionError("", &platform.ProcessResult{Stdout: "exit code 137"}, errors.New("exit status 137")),
			want: true},
		{name: "classified corrupted on disk", err: buildClassifiedCorruptedErrorForTest(t), want: true},
		{name: "classified generic not corruption",
			err: classifyClaudeVersionError("", &platform.ProcessResult{}, errors.New("exec: file not found")),
			want: false},
		{name: "plain error string no longer matches (legacy substring)", err: errors.New("claude binary likely corrupted (AMFI SIGKILL / exit code 137): signal: killed"), want: false},
		{name: "plain exit code 137 string no longer matches (legacy substring)", err: errors.New("run failed: exit code 137"), want: false},
		{name: "not installed", err: errors.New("run claude --version: exec: file not found"), want: false},
		{name: "parse version error", err: errors.New(`parse Claude Code version from output "foo"`), want: false},
		{name: "empty", err: errors.New(""), want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := looksLikeClaudeCorruptionError(tc.err); got != tc.want {
				t.Fatalf("got %v, want %v for err=%v", got, tc.want, tc.err)
			}
		})
	}
}

// buildClassifiedCorruptedErrorForTest drives the real
// classifyClaudeVersionError through its integrity-inspection branch by
// placing a truncated shard on disk under a Linux-simulated environment.
// It exists so TestLooksLikeClaudeCorruptionError can assert corruption-class
// detection on the structured path rather than relying on substring
// matching of the rendered error string.
func buildClassifiedCorruptedErrorForTest(t *testing.T) error {
	t.Helper()
	withSimulatedGOOS(t, "linux")
	withIntegrityThreshold(t, 100*1024*1024)

	shard := filepath.Join(t.TempDir(), "claude")
	writeFixture(t, shard, bytes_30MB()) // 30MB < 100MB threshold
	err := classifyClaudeVersionError(shard, &platform.ProcessResult{}, errors.New("exec format error"))
	if err == nil {
		t.Fatal("expected classifyClaudeVersionError to return a non-nil error for a corrupted shard")
	}
	return err
}

// TestClaudeVersionError_StructuredKind ensures the structured error type
// carries the expected Kind for each classifyClaudeVersionError branch. This
// is the contract that decouples corruption detection from the rendered
// error string.
func TestClaudeVersionError_StructuredKind(t *testing.T) {
	withSimulatedGOOS(t, "linux")
	withIntegrityThreshold(t, 1)

	t.Run("SIGKILL branch yields SIGKILL kind", func(t *testing.T) {
		err := classifyClaudeVersionError("", &platform.ProcessResult{Stderr: "signal: killed"}, errors.New("signal: killed"))
		classified := asClaudeVersionError(err)
		if classified == nil {
			t.Fatalf("expected *claudeVersionError, got %T", err)
		}
		if classified.Kind() != claudeVersionErrorSIGKILL {
			t.Fatalf("kind = %v, want %v", classified.Kind(), claudeVersionErrorSIGKILL)
		}
		if !classified.Kind().isCorruption() {
			t.Fatalf("SIGKILL kind must be corruption-class")
		}
		if !strings.Contains(classified.Error(), "AMFI SIGKILL") {
			t.Fatalf("error message lost diagnostic: %q", classified.Error())
		}
	})

	t.Run("generic branch yields Generic kind", func(t *testing.T) {
		err := classifyClaudeVersionError("", &platform.ProcessResult{}, errors.New("exec: file not found"))
		classified := asClaudeVersionError(err)
		if classified == nil {
			t.Fatalf("expected *claudeVersionError, got %T", err)
		}
		if classified.Kind() != claudeVersionErrorGeneric {
			t.Fatalf("kind = %v, want %v", classified.Kind(), claudeVersionErrorGeneric)
		}
		if classified.Kind().isCorruption() {
			t.Fatalf("Generic kind must NOT be corruption-class")
		}
		if !strings.Contains(classified.Error(), "run claude --version") {
			t.Fatalf("generic message lost prefix: %q", classified.Error())
		}
	})

	t.Run("errors.As unwraps wrapped structured error", func(t *testing.T) {
		inner := classifyClaudeVersionError("", &platform.ProcessResult{Stderr: "exit code 137"}, errors.New("exit status 137"))
		wrapped := fmt.Errorf("check failed: %w", inner)
		if !looksLikeClaudeCorruptionError(wrapped) {
			t.Fatal("wrapped *claudeVersionError must still be recognized as corruption via errors.As")
		}
	})
}

// TestSelfHealClaudeNPMResidueForDetection_TriggersCleanupOnCorruption lays
// out staging residue + a truncated binary on disk, then verifies that the
// detect-path self-heal entry point removes the residue and reports both
// the integrity finding and the cleanup result.
func TestSelfHealClaudeNPMResidueForDetection_TriggersCleanupOnCorruption(t *testing.T) {
	withSimulatedGOOS(t, "linux")
	withIntegrityThreshold(t, 100 * 1024 * 1024)

	root := t.TempDir()
	prefix := filepath.Join(root, "npm-global")
	scopedDir := filepath.Join(prefix, "node_modules", "@anthropic-ai")
	staging := filepath.Join(scopedDir, ".claude-code-DeadBeef")
	mainPkg := filepath.Join(scopedDir, "claude-code")
	for _, d := range []string{staging, mainPkg} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
	writeFixture(t, filepath.Join(staging, "leftover"), []byte("x"))
	writeFixture(t, filepath.Join(mainPkg, "package.json"), []byte("{}"))

	// Truncated binary shard on disk, below the production threshold.
	shardDir := filepath.Join(root, "shard-dir")
	if err := os.MkdirAll(shardDir, 0o755); err != nil {
		t.Fatalf("mkdir shard: %v", err)
	}
	shard := filepath.Join(shardDir, "claude")
	writeFixture(t, shard, bytes_30MB()) // 30MB < 100MB threshold

	runner := &fakeNpmPrefixRunner{prefix: prefix}
	svc := NewServiceWithRunner(runner)

	outcome := svc.selfHealClaudeNPMResidueForDetection(shard)
	if !outcome.Triggered {
		t.Fatal("expected Triggered=true")
	}
	if !outcome.Integrity.Exists {
		t.Fatal("expected integrity report to see the binary on disk")
	}
	if !outcome.Integrity.Corrupted {
		t.Fatalf("expected integrity to flag shard corrupted, got reason=%q", outcome.Integrity.Reason)
	}
	if outcome.CleanupErr != nil {
		t.Fatalf("cleanup returned error: %v", outcome.CleanupErr)
	}
	if outcome.Cleanup == nil {
		t.Fatal("expected non-nil cleanup result")
	}
	if len(outcome.Cleanup.StagingDirs) != 1 {
		t.Fatalf("staging dirs removed = %v, want 1", outcome.Cleanup.StagingDirs)
	}
	if len(outcome.Cleanup.PackageDirs) != 1 {
		t.Fatalf("package dirs removed = %v, want 1", outcome.Cleanup.PackageDirs)
	}
	if _, err := os.Stat(staging); !os.IsNotExist(err) {
		t.Fatalf("staging dir still exists: %v", err)
	}
	if _, err := os.Stat(mainPkg); !os.IsNotExist(err) {
		t.Fatalf("main package dir still exists: %v", err)
	}
}

// TestSelfHealClaudeNPMResidueForDetection_SurfacesPartialCleanupFailure
// exercises the path where cleanClaudeNPMResidue itself returns an error
// (e.g. npm prefix unavailable). The detect-path entry point must NOT
// short-circuit; it must propagate the error through outcome.CleanupErr so
// checkClaudeCode can still build a useful CheckStatus.
func TestSelfHealClaudeNPMResidueForDetection_SurfacesPartialCleanupFailure(t *testing.T) {
	withSimulatedGOOS(t, "linux")
	withIntegrityThreshold(t, 100 * 1024 * 1024)

	// fakeFailingPrefixRunner returns an error for `npm prefix -g`, so
	// cleanClaudeNPMResidue cannot resolve the scoped dir.
	runner := &fakeFailingNpmPrefixRunner{}
	svc := NewServiceWithRunner(runner)

	outcome := svc.selfHealClaudeNPMResidueForDetection("")
	if !outcome.Triggered {
		t.Fatal("expected Triggered=true even when cleanup fails")
	}
	if outcome.CleanupErr == nil {
		t.Fatal("expected CleanupErr to be non-nil when npm prefix unavailable")
	}
}

// fakeFailingNpmPrefixRunner is a mock ProcessRunner whose `npm prefix -g`
// always returns an error, simulating an environment where cleanClaudeNPMResidue
// cannot resolve the global npm prefix.
type fakeFailingNpmPrefixRunner struct{}

func (r *fakeFailingNpmPrefixRunner) Run(_ context.Context, spec platform.CommandSpec) (*platform.ProcessResult, error) {
	if len(spec.Args) == 2 && spec.Args[0] == "prefix" && spec.Args[1] == "-g" {
		return nil, errors.New("npm prefix -g: command not found")
	}
	return &platform.ProcessResult{}, nil
}

func (r *fakeFailingNpmPrefixRunner) Start(_ platform.CommandSpec) (*exec.Cmd, error) {
	return nil, nil
}

// TestSelfHealClaudeNPMResidueForDetection_EmptyBinaryPathStillRunsCleanup
// documents that even without a binary path (e.g. classifyClaudeVersionError
// fired via SIGKILL alone with no path to inspect), the detect-path entry
// still triggers npm staging cleanup -- the staging residue is what poisons
// future installs, and it is independent of the binary path.
func TestSelfHealClaudeNPMResidueForDetection_EmptyBinaryPathStillRunsCleanup(t *testing.T) {
	withSimulatedGOOS(t, "linux")

	root := t.TempDir()
	prefix := filepath.Join(root, "npm-global")
	scopedDir := filepath.Join(prefix, "node_modules", "@anthropic-ai")
	staging := filepath.Join(scopedDir, ".claude-code-DeadBeef")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatalf("mkdir staging: %v", err)
	}
	writeFixture(t, filepath.Join(staging, "leftover"), []byte("x"))

	runner := &fakeNpmPrefixRunner{prefix: prefix}
	svc := NewServiceWithRunner(runner)

	outcome := svc.selfHealClaudeNPMResidueForDetection("")
	if !outcome.Triggered {
		t.Fatal("expected Triggered=true")
	}
	if outcome.Integrity.Exists {
		t.Fatal("empty path should not be reported as Exists")
	}
	if outcome.CleanupErr != nil {
		t.Fatalf("cleanup returned error: %v", outcome.CleanupErr)
	}
	if outcome.Cleanup == nil || len(outcome.Cleanup.StagingDirs) != 1 {
		t.Fatalf("expected 1 staging dir removed, got %+v", outcome.Cleanup)
	}
	if _, err := os.Stat(staging); !os.IsNotExist(err) {
		t.Fatalf("staging dir still exists: %v", err)
	}
}
