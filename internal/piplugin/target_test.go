package piplugin

// target_test.go — P3-S5 目标化接缝的行为钉子：ParseSourceSpec 导出语义、
// ref 形态判定、Windows 实体锚点读取（npm package.json version / git .git/HEAD
// commit，只读不 spawn）、Windows 登记过滤清单读取。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseSourceSpecNpmForms(t *testing.T) {
	cases := []struct {
		raw, name, ref string
	}{
		{"npm:@earendil-works/pi-web-access", "@earendil-works/pi-web-access", ""},
		{"npm:@scope/pkg@1.2.3", "@scope/pkg", "1.2.3"},
		{"npm:plain@2.0.0", "plain", "2.0.0"},
		{"npm:plain", "plain", ""},
	}
	for _, c := range cases {
		spec := ParseSourceSpec(c.raw)
		if spec.Type != sourceNPM || spec.Name != c.name || spec.Ref != c.ref {
			t.Errorf("parse %q → %+v (want npm %s@%s)", c.raw, spec, c.name, c.ref)
		}
	}
}

func TestParseSourceSpecGitForms(t *testing.T) {
	cases := []struct {
		raw, host, path, ref string
	}{
		{"git:github.com/u/repo", "github.com", "u/repo", ""},
		{"git:github.com/u/repo@v1", "github.com", "u/repo", "v1"},
		{"https://github.com/u/repo.git@v2", "github.com", "u/repo", "v2"},
	}
	for _, c := range cases {
		spec := ParseSourceSpec(c.raw)
		if spec.Type != sourceGit || spec.Host != c.host || spec.Path != c.path || spec.Ref != c.ref {
			t.Errorf("parse %q → %+v (want git %s/%s@%s)", c.raw, spec, c.host, c.path, c.ref)
		}
	}
}

func TestParseSourceSpecLocalFallback(t *testing.T) {
	spec := ParseSourceSpec(`C:\work\plugin`)
	if spec.Type != sourceLocal || spec.LocalPath != `C:\work\plugin` {
		t.Fatalf("windows path must parse as local: %+v", spec)
	}
}

func TestIsExactNpmVersionAndCommitSHA(t *testing.T) {
	if !IsExactNpmVersion("1.2.3") || IsExactNpmVersion("^1.0.0") || IsExactNpmVersion("latest") {
		t.Fatalf("semver exactness wrong")
	}
	sha40 := strings.Repeat("ab", 20)
	sha64 := strings.Repeat("cd", 32)
	if !IsFullCommitSHA(sha40) || !IsFullCommitSHA(sha64) {
		t.Fatalf("full shas must be recognized")
	}
	if IsFullCommitSHA("v1.2.3") || IsFullCommitSHA(strings.Repeat("ab", 39)) {
		t.Fatalf("non-sha refs must be rejected")
	}
}

func writeTargetFixture(t *testing.T, agentDir string, packages []any) {
	t.Helper()
	data, _ := json.Marshal(map[string]any{"packages": packages})
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestResolvedEntityVersion(t *testing.T) {
	agentDir := t.TempDir()
	svc := NewServiceWithDeps(agentDir, nil, testResolver{}, &testRunner{})
	writeTestNpmEntity(t, agentDir, "foo", map[string]any{"name": "foo", "version": "1.4.2"})
	if v := svc.ResolvedEntityVersion("npm:foo"); v != "1.4.2" {
		t.Fatalf("entity version anchor: got %q", v)
	}
	if v := svc.ResolvedEntityVersion("npm:missing-pkg"); v != "" {
		t.Fatalf("missing entity must yield empty anchor: %q", v)
	}
	// git 源不适用版本锚点。
	if v := svc.ResolvedEntityVersion("git:github.com/u/r"); v != "" {
		t.Fatalf("git source must not yield version anchor: %q", v)
	}
}

func TestResolvedEntityCommit(t *testing.T) {
	agentDir := t.TempDir()
	svc := NewServiceWithDeps(agentDir, nil, testResolver{}, &testRunner{})
	sha := strings.Repeat("9f", 20)
	root := writeTestGitEntity(t, agentDir, "github.com", "u/repo", map[string]any{"name": "repo"})
	gitDir := filepath.Join(root, ".git")
	if err := os.MkdirAll(filepath.Join(gitDir, "refs", "heads"), 0o755); err != nil {
		t.Fatal(err)
	}
	// symbolic ref（loose）
	_ = os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644)
	_ = os.WriteFile(filepath.Join(gitDir, "refs", "heads", "main"), []byte(sha+"\n"), 0o644)
	if c := svc.ResolvedEntityCommit("git:github.com/u/repo"); c != sha {
		t.Fatalf("loose head commit: got %q", c)
	}
	// packed-refs 兜底
	_ = os.Remove(filepath.Join(gitDir, "refs", "heads", "main"))
	packed := "# pack-refs with: peeled fully-peeled\n" + sha + " refs/heads/main\n"
	_ = os.WriteFile(filepath.Join(gitDir, "packed-refs"), []byte(packed), 0o644)
	if c := svc.ResolvedEntityCommit("git:github.com/u/repo"); c != sha {
		t.Fatalf("packed-refs commit: got %q", c)
	}
	// detached HEAD
	_ = os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte(sha+"\n"), 0o644)
	if c := svc.ResolvedEntityCommit("git:github.com/u/repo"); c != sha {
		t.Fatalf("detached head commit: got %q", c)
	}
	// npm 源不适用 commit 锚点。
	if c := svc.ResolvedEntityCommit("npm:foo"); c != "" {
		t.Fatalf("npm source must not yield commit anchor: %q", c)
	}
}

func TestWindowsEntryFilters(t *testing.T) {
	agentDir := t.TempDir()
	svc := NewServiceWithDeps(agentDir, nil, testResolver{}, &testRunner{})
	writeTargetFixture(t, agentDir, []any{
		"npm:plain",
		map[string]any{"source": "npm:filtered", "extensions": []string{"a.ts"}, "skills": []string{"b"}},
	})
	ext, sk, pr, th := svc.WindowsEntryFilters("npm:filtered")
	if len(ext) != 1 || ext[0] != "a.ts" || len(sk) != 1 || sk[0] != "b" || pr != nil || th != nil {
		t.Fatalf("filters: ext=%v skills=%v prompts=%v themes=%v", ext, sk, pr, th)
	}
	if ext, _, _, _ := svc.WindowsEntryFilters("npm:plain"); len(ext) != 0 {
		t.Fatalf("plain entry has no filters: %v", ext)
	}
}
