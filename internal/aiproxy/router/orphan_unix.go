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

// reapStaleCore 回收上次异常退出遗留的孤儿内核（unix/macOS）。
//
// 判定链（全部满足才回收，见 reapDecision）：
//  1. pidfile 记录的进程仍存活；
//  2. ps 命令行包含本 runtime 的 core.json 路径——所有权证明，
//     防止 pid 复用后误杀 v2rayN 自己或其他实例的 sing-box；
//  3. 父进程已死：ppid==1（已被 init/launchd 收养）或对 ppid 探测返回 ESRCH。
//     父进程还活着说明它是另一个运行中的 CodeBox 实例的内核，不回收。
//
// 回收使用 SIGKILL 并等待进程消失（有界）；pidfile 失效（进程已死/无法判定）
// 时顺手清理。返回被回收的 pid，0 表示无需回收。
func reapStaleCore(corePath, pidPath string) int {
	raw, err := os.ReadFile(pidPath)
	if err != nil {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || pid <= 0 {
		_ = os.Remove(pidPath)
		return 0
	}
	if !unixSignalAlive(pid) {
		_ = os.Remove(pidPath)
		return 0
	}
	out, err := exec.Command("ps", "-o", "ppid=,command=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return 0 // 无法判定所有权，保守放行，让端口探测给出原有报错
	}
	ppid, command, ok := parseUnixPSLine(string(out))
	if !ok {
		return 0
	}
	marker := filepath.Join(filepath.Dir(pidPath), "core.json")
	// pidfile 与内核配置同目录：corePath 仅用于将来可能的扩展，这里以配置路径为准。
	parentAlive := ppid != 1 && unixSignalAlive(ppid)
	if !reapDecision(strings.Contains(command, marker), parentAlive) {
		return 0
	}
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
		return 0
	}
	for range 40 { // ≤2s 等待端口随进程释放
		if !unixSignalAlive(pid) {
			return pid
		}
		time.Sleep(50 * time.Millisecond)
	}
	return pid
}

// unixSignalAlive 探测 pid 是否存活（权限不足视为存活，方向保守）。
func unixSignalAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

// parseUnixPSLine 解析 `ps -o ppid=,command= -p PID` 的输出：
// `     1 /path/sing-box run -c /dir/core.json --disable-color`。
// 第一个空白前的整数为 ppid，其余为完整命令行（含参数，不能按字段切分）。
func parseUnixPSLine(out string) (ppid int, command string, ok bool) {
	s := strings.TrimSpace(out)
	if s == "" {
		return 0, "", false
	}
	i := strings.IndexAny(s, " \t")
	if i < 0 {
		ppid, err := strconv.Atoi(s)
		if err != nil {
			return 0, "", false
		}
		return ppid, "", true
	}
	ppid, err := strconv.Atoi(s[:i])
	if err != nil {
		return 0, "", false
	}
	return ppid, strings.TrimSpace(s[i+1:]), true
}
