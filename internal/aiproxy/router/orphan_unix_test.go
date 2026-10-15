//go:build !windows

package router

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// 真实孤儿回收端到端：构造父进程已死、命令行含 runtime core.json 标记的遗留进程
// （后台启动后外层 shell 立即退出，脚本进程被 init 收养），reapStaleCore 应识别
// 并回收——含无 pidfile 的旧版本（≤1.3.103）升级场景；无所有权标记或父进程存活
// 的进程不被回收。
//
// fake-core 用常驻循环而非 exec sleep：exec 会替换 argv 使命令行标记消失，
// 无法稳定验证所有权判定。
func TestReapStaleCoreRealOrphan(t *testing.T) {
	if testing.Short() {
		t.Skip("端到端孤儿回收需要真实进程与 init 收养时序")
	}
	dir := t.TempDir()
	runtime := filepath.Join(dir, "runtime")
	if err := os.MkdirAll(runtime, 0o700); err != nil {
		t.Fatal(err)
	}
	// 所有权标记：runtime 目录下的 core.json 路径必须出现在命令行中。
	marker := filepath.Join(runtime, "core.json")
	script := filepath.Join(dir, "fake-core")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nwhile :; do sleep 1; done\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	pidfile := filepath.Join(runtime, "core.pid")

	// spawnOrphan 后台启动脚本进程；外层 sh 立即退出，脚本进程被 init/launchd
	// 收养（ppid=1），命令行保留传入的全部参数。等待它在进程表中出现后返回 pid。
	spawnOrphan := func(withArgs ...string) int {
		t.Helper()
		line := script + " " + strings.Join(withArgs, " ")
		if err := exec.Command("/bin/sh", "-c", line+" >/dev/null 2>&1 &").Run(); err != nil {
			t.Fatal(err)
		}
		return waitForProcess(t, append([]string{script}, withArgs...)...)
	}
	writePidfile := func(t *testing.T, pid int) {
		t.Helper()
		if err := os.WriteFile(pidfile, []byte(strconv.Itoa(pid)), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("无 pidfile 的孤儿被回收（旧版升级场景）", func(t *testing.T) {
		pid := spawnOrphan("run", "-c", marker, "--disable-color")
		if _, err := os.Stat(pidfile); !os.IsNotExist(err) {
			t.Fatal("前置条件：旧版不写 pidfile")
		}
		got := reapStaleCore("/nonexistent/sing-box", runtime)
		if len(got) != 1 || got[0] != pid {
			t.Fatalf("reapStaleCore=%v want [%d]", got, pid)
		}
		waitGone(t, pid)
	})

	t.Run("有 pidfile 的孤儿被回收", func(t *testing.T) {
		pid := spawnOrphan("run", "-c", marker, "--disable-color")
		writePidfile(t, pid)
		got := reapStaleCore("/nonexistent/sing-box", runtime)
		if len(got) != 1 || got[0] != pid {
			t.Fatalf("reapStaleCore=%v want [%d]", got, pid)
		}
		waitGone(t, pid)
		if _, err := os.Stat(pidfile); !os.IsNotExist(err) {
			t.Fatal("回收后应清理 pidfile")
		}
	})

	t.Run("无所有权标记不回收", func(t *testing.T) {
		other := filepath.Join(dir, "other.json")
		pid := spawnOrphan("run", "-c", other)
		t.Cleanup(func() { _ = exec.Command("kill", strconv.Itoa(pid)).Run() })
		if got := reapStaleCore("/nonexistent/sing-box", runtime); len(got) != 0 {
			t.Fatalf("命令行不含 core.json 标记的进程不应回收，got %v", got)
		}
		if !unixSignalAlive(pid) {
			t.Fatal("无关进程不应被杀死")
		}
	})

	t.Run("父进程存活的内核不回收", func(t *testing.T) {
		// 直接子进程：父进程是本测试进程（存活），即便命令行带标记也不回收。
		cmd := exec.Command(script, "run", "-c", marker, "--disable-color")
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = cmd.Process.Kill() })
		if got := reapStaleCore("/nonexistent/sing-box", runtime); len(got) != 0 {
			t.Fatalf("父进程存活的内核不应回收，got %v", got)
		}
		if !unixSignalAlive(cmd.Process.Pid) {
			t.Fatal("父进程存活的内核不应被杀死")
		}
	})
}

// waitForProcess 轮询 ps 直到出现命令行同时包含全部 substrings 的进程。
func waitForProcess(t *testing.T, substrings ...string) int {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		out, err := exec.Command("ps", "-eo", "pid=,command=").Output()
		if err == nil {
		lines:
			for _, line := range strings.Split(string(out), "\n") {
				for _, s := range substrings {
					if !strings.Contains(line, s) {
						continue lines
					}
				}
				fields := strings.Fields(line)
				if len(fields) > 0 {
					if pid, err := strconv.Atoi(fields[0]); err == nil {
						return pid
					}
				}
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("目标进程未在 3s 内出现（查找 %v）", substrings)
	return 0
}

// waitGone 有界等待 pid 消失（SIGKILL 后内核通常立即回收，此处兜底断言稳定）。
func waitGone(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for unixSignalAlive(pid) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if unixSignalAlive(pid) {
		t.Fatalf("孤儿进程 %d 未被回收", pid)
	}
}
