package router

import "os"

func interruptCore(p *os.Process) { _ = p.Kill() }
