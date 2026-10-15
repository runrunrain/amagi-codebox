//go:build windows

package router

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
	"unsafe"
)

var (
	procCreateToolhelp32Snapshot = kernel32.NewProc("CreateToolhelp32Snapshot")
	procProcess32FirstW          = kernel32.NewProc("Process32FirstW")
	procProcess32NextW           = kernel32.NewProc("Process32NextW")
)

const (
	th32csSnapProcess = 0x2
	invalidHandle     = ^uintptr(0)
)

type processEntryW struct {
	Size            uint32
	Usage           uint32
	ProcessID       uint32
	DefaultHeapID   uintptr
	ModuleID        uint32
	Threads         uint32
	ParentProcessID uint32
	PriorityClass   int32
	Flags           uint32
	ExeName         [260]uint16
}

// snapshotProcesses 返回当前全量进程表（pid → 条目）。失败返回 nil，调用方
// 以“无法判定”保守处理（不回收，让端口探测给出原有报错）。
func snapshotProcesses() map[uint32]processEntryW {
	snap, _, _ := procCreateToolhelp32Snapshot.Call(th32csSnapProcess, 0)
	if snap == 0 || snap == invalidHandle {
		return nil
	}
	defer procCloseHandle.Call(snap)
	procs := map[uint32]processEntryW{}
	entry := processEntryW{Size: uint32(unsafe.Sizeof(processEntryW{}))}
	next := func(first bool) bool {
		proc := procProcess32FirstW
		if !first {
			proc = procProcess32NextW
		}
		r, _, _ := proc.Call(snap, uintptr(unsafe.Pointer(&entry)))
		return r != 0
	}
	for first := true; next(first); first = false {
		procs[entry.ProcessID] = processEntryW{
			ProcessID:       entry.ProcessID,
			ParentProcessID: entry.ParentProcessID,
			ExeName:         entry.ExeName,
		}
	}
	return procs
}

// reapStaleCore 回收上次异常退出遗留的孤儿内核（Windows），返回被回收的 pid 列表。
//
// 不依赖 pidfile（覆盖旧版本升级场景）。Windows 无法低成本读取命令行，所有权
// 证明降级为双条件：
//  1. 可执行文件名与当前内核路径的 basename 一致（sing-box.exe）；
//  2. 其父 pid 不在全量进程表中（父进程已死）。
//
// 对无关进程的误判概率可忽略；v2rayN 自身崩溃遗留的孤儿内核若被顺带回收也无害
// （父进程已死，核心已无用途，v2rayN 重启会重新拉起）。活着的其他 CodeBox 实例
// 内核因父进程存活不会被回收。job object（process_windows.go）保证新版本不再
// 产生孤儿，这里兜底升级前遗留与极端竞态。
func reapStaleCore(corePath, runtimeDir string) []int {
	procs := snapshotProcesses()
	if procs == nil {
		return nil
	}
	self := uint32(os.Getpid())
	want := strings.ToLower(filepath.Base(corePath))
	var reaped []int
	for pid, entry := range procs {
		if pid == self || entry.ProcessID == 0 {
			continue
		}
		owned := strings.EqualFold(strings.ToLower(utf16ToString(entry.ExeName[:])), want)
		if !reapDecision(owned, parentAlive(procs, entry.ParentProcessID)) {
			continue
		}
		if err := exec.Command("taskkill", "/PID", strconv.Itoa(int(pid)), "/F").Run(); err != nil {
			continue
		}
		waitPidGone(pid)
		reaped = append(reaped, int(pid))
	}
	// 旧版遗留的失效 pidfile（若有）一并清理；当前实例的 pidfile 在 stop 时删除。
	if len(reaped) > 0 {
		_ = os.Remove(filepath.Join(runtimeDir, "core.pid"))
	}
	return reaped
}

func parentAlive(procs map[uint32]processEntryW, ppid uint32) bool {
	_, ok := procs[ppid]
	return ok
}

// waitPidGone 有界等待 pid 从进程表消失（监听端口随进程立即释放）。
func waitPidGone(pid uint32) {
	for range 40 { // ≤2s
		if procs := snapshotProcesses(); procs != nil {
			if _, ok := procs[pid]; !ok {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func utf16ToString(s []uint16) string {
	if i := len(s); i > 0 {
		for j, c := range s {
			if c == 0 {
				return string(utf16.Decode(s[:j]))
			}
		}
	}
	return ""
}
