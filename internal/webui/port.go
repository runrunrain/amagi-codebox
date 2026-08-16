// Package webui 是 pi Web UI 壳集成的 codebox 侧服务（蓝图 T-1.5）。
//
// 职责（契约 v1.0.2，amagi-pi docs/webui-protocol.md 为接口权威）：
//   - port.go    空闲端口分配（§7.2 通道 1：AMAGI_WEBUI_PORT env 注入）
//   - token.go   每会话 capability token 生成（v1.0.2：AMAGI_WEBUI_TOKEN 注入）
//   - probe.go   /api/info 探测状态机与注册表回退扫描（§4.1 / §7.3）
//   - service.go Wails 绑定服务，向前端暴露 per-session webui 状态
//
// 数据面 server 由 amagi-pi 扩展进程实现；本包只做发现与探测，不实现 server。
package webui

import (
	"fmt"
	"net"
)

// AllocateFreePort 让系统在回环地址上分配一个空闲端口并立即释放，供
// AMAGI_WEBUI_PORT env 注入（契约 §7.2 通道 1）。
//
// 关闭监听与 pi 子进程实际 bind 之间存在竞态窗口（端口可能被第三方抢占）；
// 契约 §7.2 已为此冻结兜底：扩展 bind 失败回退系统自选端口并写注册表，
// codebox 探测 env 端口失败后扫注册表回退，因此此处无需持锁占位。
func AllocateFreePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("allocate free port: %w", err)
	}
	defer l.Close()
	addr, ok := l.Addr().(*net.TCPAddr)
	if !ok {
		return 0, fmt.Errorf("allocate free port: unexpected addr type %T", l.Addr())
	}
	return addr.Port, nil
}
