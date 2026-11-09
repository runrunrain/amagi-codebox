//go:build !windows

package platform

// AttachParentConsole 在非 Windows 平台是空实现：终端句柄天然继承，
// 无需（也无法）附着控制台。跨平台契约见 console_attach.go。
func AttachParentConsole() {}
