//go:build windows

package platform

import (
	"os/exec"
	"testing"
)

// TestApplyProcessPolicySysProcAttr 断言 applyProcessPolicy 在真实
// syscall.SysProcAttr 上的落位（HideWindow + CreationFlags）。
//
// 本文件带 windows build tag，只能在 Windows 上执行；Linux/macOS 侧通过
// `GOOS=windows go vet ./internal/platform/` 保证其编译进 Windows 构建。
// 跨平台可执行的标志决策断言见 process_policy_test.go。
func TestApplyProcessPolicySysProcAttr(t *testing.T) {
	cmd := &exec.Cmd{}
	applyProcessPolicy(cmd, DefaultProcessPolicy())
	attr := cmd.SysProcAttr
	if attr == nil {
		t.Fatal("applyProcessPolicy did not set SysProcAttr")
	}
	if attr.CreationFlags != flagCreateNoWindow {
		t.Errorf("CreationFlags = %#x, want CREATE_NO_WINDOW (%#x)", attr.CreationFlags, flagCreateNoWindow)
	}
	if !attr.HideWindow {
		t.Error("HideWindow not set for HideConsoleWindow policy")
	}

	cmd = &exec.Cmd{}
	applyProcessPolicy(cmd, ProcessPolicy{Detached: true})
	attr = cmd.SysProcAttr
	if attr == nil {
		t.Fatal("applyProcessPolicy did not set SysProcAttr for Detached policy")
	}
	if attr.CreationFlags != flagDetachedProcess {
		t.Errorf("Detached CreationFlags = %#x, want DETACHED_PROCESS (%#x)", attr.CreationFlags, flagDetachedProcess)
	}
}
