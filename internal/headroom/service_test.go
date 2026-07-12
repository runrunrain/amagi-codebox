package headroom

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// envMap converts a "KEY=VALUE" slice into a map for easier assertions.
func envMap(env []string) map[string]string {
	m := make(map[string]string, len(env))
	for _, kv := range env {
		key, value, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		m[key] = value
	}
	return m
}

// pathEntries splits a PATH string into a normalized set for comparison.
func pathEntries(pathValue string) map[string]struct{} {
	out := make(map[string]struct{})
	for _, entry := range filepath.SplitList(pathValue) {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		out[filepath.Clean(entry)] = struct{}{}
	}
	return out
}

// TestBuildChildEnvWithBase_StripsAnthropicBaseURL verifies the death-loop
// prevention: any inherited ANTHROPIC_BASE_URL must be removed regardless of
// case so headroom forwards to the real upstream, not back into itself.
func TestBuildChildEnvWithBase_StripsAnthropicBaseURL(t *testing.T) {
	base := []string{
		"PATH=/usr/bin:/bin",
		"ANTHROPIC_BASE_URL=http://127.0.0.1:8787",
		"anthropic_base_url=http://127.0.0.1:5280",
		"HOME=/tmp/test-home",
	}
	env := buildChildEnvWithBase("https://api.example.com", 8787, base)
	m := envMap(env)
	if _, ok := m["ANTHROPIC_BASE_URL"]; ok {
		t.Fatalf("ANTHROPIC_BASE_URL should be stripped, got %q", m["ANTHROPIC_BASE_URL"])
	}
	if _, ok := m["anthropic_base_url"]; ok {
		t.Fatalf("lowercase anthropic_base_url should be stripped, got %q", m["anthropic_base_url"])
	}
}

// TestBuildChildEnvWithBase_InjectsHeadroomVars verifies the three injected
// variables headroom needs to locate its listen port and real upstream.
func TestBuildChildEnvWithBase_InjectsHeadroomVars(t *testing.T) {
	base := []string{"PATH=/usr/bin:/bin"}
	env := buildChildEnvWithBase("https://api.example.com/", 9999, base)
	m := envMap(env)

	if m["ANTHROPIC_TARGET_API_URL"] != "https://api.example.com" {
		t.Errorf("ANTHROPIC_TARGET_API_URL = %q, want %q", m["ANTHROPIC_TARGET_API_URL"], "https://api.example.com")
	}
	if m["HEADROOM_PORT"] != "9999" {
		t.Errorf("HEADROOM_PORT = %q, want %q", m["HEADROOM_PORT"], "9999")
	}
	if m["HEADROOM_HOST"] != "127.0.0.1" {
		t.Errorf("HEADROOM_HOST = %q, want %q", m["HEADROOM_HOST"], "127.0.0.1")
	}
}

// TestBuildChildEnvWithBase_PreservesEnhancedPATH is the regression test for the
// macOS "executable file not found in $PATH" failure.
//
// resolveHeadroomBinWithEnv augments PATH with common install locations so it
// can find headroom under a minimal GUI PATH. buildChildEnvWithBase must carry
// that augmented PATH into the child process environment -- if it silently
// resets PATH to the (minimal) process environment, the child cannot locate
// headroom and fails with "executable file not found in $PATH".
func TestBuildChildEnvWithBase_PreservesEnhancedPATH(t *testing.T) {
	// Simulate an enhanced PATH that includes a directory the minimal GUI PATH
	// lacks (e.g. a pip install location under the user home).
	enhancedDir := filepath.Join(os.TempDir(), "headroom-augmented-bin")
	enhancedEnv := []string{
		"PATH=/usr/bin:/bin:" + enhancedDir,
		"HOME=/tmp/test-home",
	}
	env := buildChildEnvWithBase("https://api.example.com", 8787, enhancedEnv)
	m := envMap(env)

	entries := pathEntries(m["PATH"])
	if _, ok := entries[filepath.Clean(enhancedDir)]; !ok {
		t.Fatalf("enhanced PATH entry %q missing from child env PATH=%q; child would not find headroom", enhancedDir, m["PATH"])
	}
}

// TestBuildChildEnvWithBase_DoesNotMutateBase verifies the base env slice is
// not mutated (callers may reuse it).
func TestBuildChildEnvWithBase_DoesNotMutateBase(t *testing.T) {
	base := []string{"PATH=/usr/bin:/bin", "HOME=/tmp/test-home"}
	baseCopy := append([]string(nil), base...)
	_ = buildChildEnvWithBase("https://api.example.com", 8787, base)
	if len(base) != len(baseCopy) {
		t.Fatalf("base env was mutated: len %d, want %d", len(base), len(baseCopy))
	}
	for i := range base {
		if base[i] != baseCopy[i] {
			t.Fatalf("base env entry %d mutated: %q, want %q", i, base[i], baseCopy[i])
		}
	}
}

// TestBuildChildEnv_BackwardCompatible verifies the legacy buildChildEnv wrapper
// still produces a valid environment (strips ANTHROPIC_BASE_URL, injects vars).
func TestBuildChildEnv_BackwardCompatible(t *testing.T) {
	// buildChildEnv reads os.Environ(); set a sentinel via t.Setenv so we can
	// confirm it flows through.
	t.Setenv("HEADROOM_TEST_SENTINEL", "present")
	env := buildChildEnv("https://api.example.com", 8787)
	m := envMap(env)
	if m["HEADROOM_TEST_SENTINEL"] != "present" {
		t.Errorf("buildChildEnv did not carry process env: missing HEADROOM_TEST_SENTINEL")
	}
	if m["HEADROOM_HOST"] != "127.0.0.1" {
		t.Errorf("buildChildEnv did not inject HEADROOM_HOST: %q", m["HEADROOM_HOST"])
	}
}

// TestResolveHeadroomBinWithEnv_AugmentsPATH verifies the returned environment
// has a PATH that is a superset of the process PATH -- i.e. the resolver's
// augmentation directories are present. This is the core guarantee that lets
// the child process find headroom under a minimal macOS GUI PATH.
func TestResolveHeadroomBinWithEnv_AugmentsPATH(t *testing.T) {
	// Snapshot the process PATH entries before resolution.
	processPathValue := os.Getenv("PATH")
	processEntries := pathEntries(processPathValue)

	svc := NewHeadroomService(nil, nil)
	_, enhancedEnv := svc.resolveHeadroomBinWithEnv()
	m := envMap(enhancedEnv)
	enhancedEntries := pathEntries(m["PATH"])

	// Every entry present in the original PATH must still be present in the
	// enhanced PATH (augmentation only adds, never removes).
	var missing []string
	for entry := range processEntries {
		if _, ok := enhancedEntries[entry]; !ok {
			missing = append(missing, entry)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("enhanced PATH lost entries from process PATH: %v", missing)
	}

	// On darwin the resolver is expected to add at least one controlled
	// directory (e.g. /opt/homebrew/bin, ~/.local/bin). This is the whole point
	// of the fix: the child needs these entries to locate headroom.
	if runtime.GOOS == "darwin" {
		if len(enhancedEntries) <= len(processEntries) {
			t.Fatalf("enhanced PATH (%d entries) did not add any directories over process PATH (%d entries); augmentation expected on darwin", len(enhancedEntries), len(processEntries))
		}
	}
}

// TestResolveHeadroomBinWithEnv_ReturnsBinPath verifies the function always
// returns a non-empty bin path (absolute when resolved, bare "headroom" as
// fallback), so callers can always construct a CommandSpec.
func TestResolveHeadroomBinWithEnv_ReturnsBinPath(t *testing.T) {
	svc := NewHeadroomService(nil, nil)
	binPath, enhancedEnv := svc.resolveHeadroomBinWithEnv()
	if strings.TrimSpace(binPath) == "" {
		t.Fatal("binPath should never be empty")
	}
	if len(enhancedEnv) == 0 {
		t.Fatal("enhancedEnv should never be empty")
	}
}

// TestResolveHeadroomBinWithEnv_PrependsVenvBin verifies the venv bin
// directory is prepended to the enhanced PATH so the venv-installed headroom
// wins over any system headroom. This injection is independent of envcheck's
// buildEnhancedEnv and must stay in sync with it.
func TestResolveHeadroomBinWithEnv_PrependsVenvBin(t *testing.T) {
	venvBin := filepath.Join(t.TempDir(), "venv-bin")
	if err := os.MkdirAll(venvBin, 0o755); err != nil {
		t.Fatalf("mkdir venv bin: %v", err)
	}
	svc := NewHeadroomService(nil, nil)
	svc.SetVenvBinDir(venvBin)
	_, enhancedEnv := svc.resolveHeadroomBinWithEnv()
	m := envMap(enhancedEnv)
	entries := pathEntries(m["PATH"])
	if _, ok := entries[filepath.Clean(venvBin)]; !ok {
		t.Fatalf("venv bin %q missing from enhanced PATH=%q", venvBin, m["PATH"])
	}
	// venv bin must be the FIRST entry so it takes priority.
	first := strings.Split(m["PATH"], string(os.PathListSeparator))[0]
	if filepath.Clean(first) != filepath.Clean(venvBin) {
		t.Fatalf("venv bin %q should be the first PATH entry, got %q", venvBin, first)
	}
}
