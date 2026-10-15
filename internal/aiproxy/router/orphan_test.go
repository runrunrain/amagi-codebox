package router

import (
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

// 无孤儿进程时回收为空操作：正常路径零成本、不报错。
func TestReapStaleCoreNoopWithoutOrphans(t *testing.T) {
	dir := t.TempDir()
	if got := reapStaleCore("/nonexistent/sing-box", dir); len(got) != 0 {
		t.Fatalf("reapStaleCore=%v want empty", got)
	}
}
