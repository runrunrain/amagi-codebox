//go:build !windows

package platform

import "testing"

// AttachParentConsole 在非 Windows 平台是空实现：调用应静默无副作用。
func TestAttachParentConsoleNonWindowsNoop(t *testing.T) {
	AttachParentConsole()
}
