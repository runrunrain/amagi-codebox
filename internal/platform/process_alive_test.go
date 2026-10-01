package platform

import (
	"os"
	"os/exec"
	"runtime"
	"testing"
)

// TestProcessAlive — pid 存活性判定的跨平台决策内核：
// 自身进程存活；非正 pid 不存活；已退出并被回收的子进程不存活。
// （Windows 分支在 CI windows-latest 仅编译；行为同 internal/updater 的
// processExists 模式，此处用自身/子进程做平台无关断言。）
func TestProcessAlive(t *testing.T) {
	if !ProcessAlive(os.Getpid()) {
		t.Fatalf("自身进程（pid=%d）应判定存活", os.Getpid())
	}
	for _, pid := range []int{0, -1} {
		if ProcessAlive(pid) {
			t.Fatalf("非正 pid=%d 应判定不存活", pid)
		}
	}

	// 已退出的子进程：Wait 回收后应判定不存活。
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("cmd", "/c", "exit", "0")
	} else {
		cmd = exec.Command("true")
	}
	if err := cmd.Run(); err != nil {
		t.Fatalf("启动子进程: %v", err)
	}
	if ProcessAlive(cmd.Process.Pid) {
		t.Fatalf("已退出的子进程（pid=%d）应判定不存活", cmd.Process.Pid)
	}
}
