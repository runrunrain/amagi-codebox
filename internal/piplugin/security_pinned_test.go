package piplugin

import (
	"strings"
	"testing"
)

// TestValidateSourceRejectsCmdMetachars (P1-1) verifies the security gate that
// prevents Windows cmd.exe command injection via package sources. On Windows the
// pi CLI shim is a .cmd that CodeBox runs through `cmd.exe /c pi.cmd <source>`;
// shell metacharacters in <source> would be interpreted by cmd.exe. None of the
// three legal source grammars (npm/git/local) legitimately contain these chars.
func TestValidateSourceRejectsCmdMetachars(t *testing.T) {
	cases := []string{
		"npm:foo&calc",   // & command separator
		"npm:foo|calc",   // | pipe
		"npm:foo<calc",   // < redirect
		"npm:foo>calc",   // > redirect
		"npm:foo^calc",   // ^ escape (enables %VAR% expansion tricks)
		"npm:foo%PATH%",  // % env var expansion
		"npm:foo(calc)",  // ( ) command grouping
		"git:github.com/a/b&rm",  // injection in a git url
		"/abs/path&(x)",  // injection in a local path
	}
	for _, src := range cases {
		if _, err := validateSource(src); err == nil {
			t.Errorf("validateSource(%q) = nil, want rejection (cmd.exe metachar)", src)
		}
	}
}

// TestValidateSourceAcceptsCleanSources ensures the metachar gate does not reject
// well-formed sources across all three types.
func TestValidateSourceAcceptsCleanSources(t *testing.T) {
	cases := []string{
		"npm:@scope/pkg@1.2.3",
		"npm:plain",
		"git:github.com/owner/repo@v1",
		"git:git@github.com:owner/repo",
		"https://github.com/owner/repo.git",
		"/abs/path/to/pkg",
		"./rel/pkg",
	}
	for _, src := range cases {
		if _, err := validateSource(src); err != nil {
			t.Errorf("validateSource(%q) = %v, want accept", src, err)
		}
	}
}

// TestIsExactSemver (P2-4) verifies that only exact semver is treated as pinned
// by the npm path, matching pi's own pinned semantics. Carets, tildes, ranges,
// wildcards and dist-tags are NOT pinned.
func TestIsExactSemver(t *testing.T) {
	pinned := []string{"1.2.3", "v1.2.3", "0.0.1", "1.0.0-beta", "1.0.0-beta.1", "2.5.0+build.7", "v1.2.3-rc.1+x"}
	for _, v := range pinned {
		if !isExactSemver(v) {
			t.Errorf("isExactSemver(%q) = false, want true (exact)", v)
		}
	}
	notPinned := []string{"", "^1.2.0", "~1.2.0", ">=1.0.0", ">1.0.0", "1.x", "1.2.x", "*", "latest", "next", "1.2", "1",
		// 非法 exact-looking 值（审核 Minor-1：对齐 npm semver.valid 严格语义）
		"01.2.3", "1.02.3", "1.2.03", // 数字段前导零
		"1.2.3-..", "1.2.3-", "1.2.3-01", // prerelease 空标识符 / 数字前导零
		"1.2.3+", "1.2.3+..", "1.2.3+a_b", // build 空标识符 / 非法字符
		"1.2.3.4", "v", "1.2.3-alpha..1",
		// npm semver 附加限制（R3 复审 Minor-3）
		strings.Repeat("1", 257),            // 超 256 字符上限
		"9007199254740992.0.0",             // 超 MAX_SAFE_INTEGER
		"1.0.0-9007199254740992",          // prerelease 数字超 MAX_SAFE_INTEGER
	}
	for _, v := range notPinned {
		if isExactSemver(v) {
			t.Errorf("isExactSemver(%q) = true, want false (not exact)", v)
		}
	}
}

// TestNpmPinnedOnlyForExactSemver (P2-4) checks the end-to-end pinned flag on the
// Package returned by ListInstalledPackages: only npm exact versions are pinned.
func TestNpmPinnedOnlyForExactSemver(t *testing.T) {
	cases := []struct {
		source   string
		wantPin  bool
	}{
		{"npm:pkg@1.2.3", true},
		{"npm:pkg@^1.2.0", false},
		{"npm:pkg@~1.2.0", false},
		{"npm:pkg@latest", false},
		{"npm:pkg@*", false},
		{"npm:pkg", false}, // no ref
	}
	for _, c := range cases {
		agentDir := t.TempDir()
		writeTestSettings(t, agentDir, []any{c.source})
		writeTestNpmEntity(t, agentDir, "pkg", map[string]any{"name": "pkg"})
		svc := NewServiceWithDeps(agentDir, nil, testResolver{}, &testRunner{})
		packages, err := svc.ListInstalledPackages()
		if err != nil {
			t.Fatalf("ListInstalledPackages(%q): %v", c.source, err)
		}
		if len(packages) != 1 {
			t.Fatalf("%q: expected 1 package, got %d", c.source, len(packages))
		}
		if packages[0].Pinned != c.wantPin {
			t.Errorf("%q: Pinned=%v, want %v", c.source, packages[0].Pinned, c.wantPin)
		}
	}
}
