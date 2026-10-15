//go:build !windows

package router

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// reapStaleCore 回收上次异常退出遗留的孤儿内核（unix/macOS），返回被回收的 pid 列表。
//
// 不依赖 pidfile：v1.3.103 及以前的版本不写 pidfile，升级后首次启动仍会遇到
// 旧版孤儿占住端口，因此这里直接全量扫描进程表。判定链（全部满足才回收）：
//  1. 命令行包含本 runtime 的 core.json 路径——所有权证明，防止误杀 v2rayN
//     自己或其他实例的 sing-box（它们用各自的配置路径）；
//  2. 父进程已死：ppid==1（已被 init/launchd 收养）或对 ppid 探测返回 ESRCH。
//     父进程还活着说明它是另一个运行中的 CodeBox 实例的内核，不回收。
//
// corePath 参数仅在 Windows 侧用于可执行文件名匹配，unix 以配置路径为准。
// 回收使用 SIGKILL 并有界等待进程消失（监听端口随进程立即释放）。
func reapStaleCore(corePath, runtimeDir string) []int {
	_ = corePath
	marker := filepath.Join(runtimeDir, "core.json")
	pidPath := filepath.Join(runtimeDir, "core.pid")
	out, err := exec.Command("ps", "-eo", "pid=,ppid=,command=").Output()
	if err != nil {
		return nil // 无法扫描时保守放行，让端口探测给出原有报错
	}
	self := os.Getpid()
	var reaped []int
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.Contains(line, marker) {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil || pid == self {
			continue
		}
		ppid, err := strconv.Atoi(fields[1])
		if err != nil {
			continue
		}
		if ppid != 1 && unixSignalAlive(ppid) {
			continue // 父进程存活：其他运行中实例的内核
		}
		if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
			continue
		}
		waitProcessGone(pid)
		reaped = append(reaped, pid)
	}
	// 旧版遗留的失效 pidfile（若有）一并清理；当前实例的 pidfile 在 stop 时删除。
	if len(reaped) > 0 {
		_ = os.Remove(pidPath)
	}
	return reaped
}

// waitProcessGone 有界等待 pid 消失（SIGKILL 后内核通常立即回收）。
func waitProcessGone(pid int) {
	for range 40 { // ≤2s
		if !unixSignalAlive(pid) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// unixSignalAlive 探测 pid 是否存活（权限不足视为存活，方向保守）。
func unixSignalAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}
