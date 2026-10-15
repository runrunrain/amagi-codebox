package router

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReapDecision(t *testing.T) {
	cases := []struct {
		name                         string
		owned, parentAlive, wantReap bool
	}{
		{"确属本应用且父进程已死 → 回收", true, false, true},
		{"父进程仍存活（其他实例）→ 不回收", true, true, false},
		{"所有权不成立（他人内核/pid 复用）→ 不回收", false, false, false},
		{"双重不满足 → 不回收", false, true, false},
	}
	for _, c := range cases {
		if got := reapDecision(c.owned, c.parentAlive); got != c.wantReap {
			t.Errorf("%s: reapDecision(%v,%v)=%v want %v", c.name, c.owned, c.parentAlive, got, c.wantReap)
		}
	}
}

func TestPidPathLayout(t *testing.T) {
	m := NewManager("/core/path", filepath.Join(t.TempDir(), "runtime"))
	want := filepath.Join(m.dir, "core.pid")
	if got := m.pidPath(); got != want {
		t.Fatalf("pidPath=%q want %q", got, want)
	}
	// pidfile 与内核配置文件（core.json）同目录：孤儿回收以该路径为所有权标记。
	if filepath.Dir(m.pidPath()) != filepath.Dir(filepath.Join(m.dir, "core.json")) {
		t.Fatal("pidfile 应与 core.json 同目录")
	}
}

func TestStalePidfileIgnoredWhenMissingOrInvalid(t *testing.T) {
	dir := t.TempDir()
	// 无 pidfile：返回 0，不报错（正常路径零成本）。
	if pid := reapStaleCore("/nonexistent/sing-box", filepath.Join(dir, "core.pid")); pid != 0 {
		t.Fatalf("missing pidfile: reapStaleCore=%d want 0", pid)
	}
	// 非法内容：清理 pidfile 后返回 0。
	bad := filepath.Join(dir, "core.pid")
	if err := os.WriteFile(bad, []byte("not-a-pid\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if pid := reapStaleCore("/nonexistent/sing-box", bad); pid != 0 {
		t.Fatalf("invalid pidfile: reapStaleCore=%d want 0", pid)
	}
	if _, err := os.Stat(bad); !os.IsNotExist(err) {
		t.Fatal("invalid pidfile should be removed")
	}
}
