//go:build !windows

package router

import "os"

func interruptCore(p *os.Process) { _ = p.Signal(os.Interrupt) }

// tie/release 无平台资源需要绑定；孤儿回收由 reapStaleCore 在下次启动时兜底
// （unix 无 PDEATHSIG 类机制，macOS 尤其如此）。
func (p *process) tie()     {}
func (p *process) release() {}
