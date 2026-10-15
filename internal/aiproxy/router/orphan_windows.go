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

// reapStaleCore 回收上次异常退出遗留的孤儿内核（Windows）。
//
// Windows 无法低成本读取命令行，所有权证明降级为双条件：
//  1. pidfile 记录的 pid 仍存活，且可执行文件名与当前内核路径的 basename 一致；
//  2. 其父 pid 不在全量进程表中（父进程已死）。
//
// 两个条件同时命中的概率对无关进程可忽略；活着的其他 CodeBox 实例内核
// 因父进程存活不会被回收。job object（见 process_windows.go）保证新版本
// 不再产生孤儿，这里主要兜底升级前遗留与极端竞态。返回被回收的 pid。
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
	procs := snapshotProcesses()
	if procs == nil {
		return 0
	}
	target, ok := procs[uint32(pid)]
	if !ok {
		_ = os.Remove(pidPath)
		return 0
	}
	want := strings.ToLower(filepath.Base(corePath))
	owned := strings.EqualFold(strings.ToLower(utf16ToString(target.ExeName[:])), want)
	_, parentAlive := procs[target.ParentProcessID]
	if !reapDecision(owned, parentAlive) {
		return 0
	}
	if err := exec.Command("taskkill", "/PID", strconv.Itoa(pid), "/F").Run(); err != nil {
		return 0
	}
	for range 40 { // ≤2s 等待端口随进程释放
		if _, ok := snapshotProcesses()[uint32(pid)]; !ok {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	return pid
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
