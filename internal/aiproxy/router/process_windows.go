//go:build windows

package router

import (
	"os"
	"syscall"
	"unsafe"
)

var (
	kernel32                     = syscall.NewLazyDLL("kernel32.dll")
	procOpenProcess              = kernel32.NewProc("OpenProcess")
	procCreateJobObjectW         = kernel32.NewProc("CreateJobObjectW")
	procSetInformationJobObject  = kernel32.NewProc("SetInformationJobObject")
	procAssignProcessToJobObject = kernel32.NewProc("AssignProcessToJobObject")
	procCloseHandle              = kernel32.NewProc("CloseHandle")
)

const (
	processTerminate                = 0x0001
	processSetQuota                 = 0x0100
	jobObjectExtendedLimitInfoClass = 9
	jobObjectLimitKillOnJobClose    = 0x2000
)

type ioCounters struct {
	ReadOperationCount, WriteOperationCount, OtherOperationCount uint64
	ReadTransferCount, WriteTransferCount, OtherTransferCount    uint64
}

type jobObjectBasicLimitInformation struct {
	PerProcessUserTimeLimit int64
	PerJobUserTimeLimit     int64
	LimitFlags              uint32
	MinimumWorkingSetSize   uintptr
	MaximumWorkingSetSize   uintptr
	ActiveProcessLimit      uint32
	Affinity                uintptr
	PriorityClass           uint32
	SchedulingClass         uint32
}

type jobObjectExtendedLimitInformation struct {
	BasicLimitInformation jobObjectBasicLimitInformation
	IoInfo                ioCounters
	ProcessMemoryLimit    uintptr
	JobMemoryLimit        uintptr
	PeakProcessMemoryUsed uintptr
	PeakJobMemoryUsed     uintptr
}

func interruptCore(p *os.Process) { _ = p.Kill() }

// tie 把内核子进程绑进 kill-on-close 的 Job Object：CodeBox 无论以何种方式退出
// （正常关闭、崩溃、被强杀、被更新器替换），Windows 内核都会在最后一个 job
// 句柄关闭时连带终止 sing-box，从根上杜绝孤儿占住代理端口。
// 尽力而为：Assign 失败（如已被更严格的 Job 约束）不阻断运行，仅失去该保障，
// 孤儿仍可由下次启动的 reapStaleCore 回收。
func (p *process) tie() {
	if p.cmd.Process == nil {
		return
	}
	h, _, _ := procOpenProcess.Call(processTerminate|processSetQuota, 0, uintptr(p.cmd.Process.Pid))
	if h == 0 {
		return
	}
	defer procCloseHandle.Call(h)
	job, _, _ := procCreateJobObjectW.Call(0, 0)
	if job == 0 {
		return
	}
	info := jobObjectExtendedLimitInformation{}
	info.BasicLimitInformation.LimitFlags = jobObjectLimitKillOnJobClose
	_, _, _ = procSetInformationJobObject.Call(job, jobObjectExtendedLimitInfoClass,
		uintptr(unsafe.Pointer(&info)), unsafe.Sizeof(info))
	if r, _, _ := procAssignProcessToJobObject.Call(job, h); r == 0 {
		procCloseHandle.Call(job)
		return
	}
	p.job = job
}

// release 在内核进程退出后关闭 job 句柄（进程已死，KILL_ON_JOB_CLOSE 不会
// 再触发终止）；CodeBox 自身异常退出时句柄由内核回收，正好触发连带终止。
func (p *process) release() {
	if p.job != 0 {
		procCloseHandle.Call(p.job)
		p.job = 0
	}
}
