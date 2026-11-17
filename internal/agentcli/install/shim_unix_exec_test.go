//go:build !windows

package install

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestUnixShimExecSmoke 端到端冒烟：shim 路径、exePath 均含空格与单引号，
// 真实执行 shim 应正确转发 cli 子命令与参数，并导出 AMAGI_CODEBOX_HOME。
func TestUnixShimExecSmoke(t *testing.T) {
	dir := t.TempDir()
	weird := filepath.Join(dir, "My App's bin", "code box") // 空格 + 单引号
	if err := os.MkdirAll(filepath.Dir(weird), 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nprintf '%s\\n' \"$AMAGI_CODEBOX_HOME\" \"$@\"\n"
	if err := os.WriteFile(weird, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	m := newManager(home, t.TempDir(), weird, nil, shimUnix)
	if err := m.InstallShim(); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(m.ShimPath(), "status", "--json").Output()
	if err != nil {
		t.Fatalf("exec shim: %v", err)
	}
	got := strings.Split(strings.TrimSpace(string(out)), "\n")
	want := []string{home, "cli", "status", "--json"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("shim 转发 = %q, want %q", got, want)
	}
}
