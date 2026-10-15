//go:build !windows

package router

import "os"

func interruptCore(p *os.Process) { _ = p.Signal(os.Interrupt) }
