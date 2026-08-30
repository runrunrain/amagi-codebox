package main

import (
	"fmt"
	"strconv"

	"amagi-codebox/internal/platform"
)

// app_system_proxy.go：全局设备显式代理（系统级 HTTP(S) 代理开关）的 App 绑定
// 面。活状态真值是操作系统配置（Windows Internet Settings 注册表 / 未来 macOS
// networksetup），由 internal/platform 的 SystemProxyControl 门面读写；本层只做
// 聚合：实时状态 + 持久化端点（settings.Service）+ 代理进程可达性探测，一次往返
// 给前端完整渲染所需的数据（同 CodexGlobalHeadroomStatus 的聚合模式）。
//
// 与 CLI 会话代理注入（SystemProxyEnv）的关系：两者都读系统代理，但互不写入
// 对方状态——本开关只操作系统配置层，不改进程环境变量、不动 headroom/relay。

// SystemProxyStatus 是前端渲染「全局设备显式代理」卡片所需的完整快照：
// enabled/host/port/reachable 反映系统当前真实状态（关闭时 host/port 仍可能是
// 上次保留的地址，与 v2rayN 等客户端行为一致）；configuredHost/configuredPort
// 是下次开启将写入的持久化端点。
type SystemProxyStatus struct {
	Supported      bool   `json:"supported"`
	Enabled        bool   `json:"enabled"`
	Host           string `json:"host"`
	Port           int    `json:"port"`
	Reachable      bool   `json:"reachable"`
	ConfiguredHost string `json:"configuredHost"`
	ConfiguredPort int    `json:"configuredPort"`
}

// GetSystemProxyStatus 返回系统显式代理实时状态 + 持久化端点 + 可达性探测
// （仅启用且地址有效时探测，避免对任意地址发请求）。
func (a *App) GetSystemProxyStatus() SystemProxyStatus {
	live := platform.ReadSystemProxyControlState()
	status := SystemProxyStatus{
		Supported: live.Supported,
		Enabled:   live.Enabled,
		Host:      live.Host,
	}
	if port, err := strconv.Atoi(live.Port); err == nil {
		status.Port = port
	}
	if a.Settings != nil {
		endpoint := a.Settings.GetSystemProxyEndpoint()
		status.ConfiguredHost = endpoint.Host
		status.ConfiguredPort = endpoint.Port
	}
	if live.Enabled && live.Host != "" && status.Port > 0 {
		status.Reachable = platform.ProbeProxyEndpoint(live.Host, status.Port)
	}
	return status
}

// SetSystemProxyEnabled 开启/关闭全局设备显式代理（写系统配置并广播刷新，
// 已运行的应用立即感知）。
//
// enabled=true：端点优先取持久化配置；为空时回落到系统现有地址（如代理客户端
// 已写入的 ProxyServer）；两者皆空则报错引导先配置地址。开启成功后把实际生效
// 的端点回写持久化配置（失败不阻断——开关本身已生效）。
//
// enabled=false：仅摘启用位、保留地址与例外列表（重开免重填）。
//
// 返回操作后的完整状态；不支持的平台返回 ErrSystemProxyControlUnsupported
// （UI 由 capabilities.systemProxyControlSupported 门控，正常不会触达）。
func (a *App) SetSystemProxyEnabled(enabled bool) (SystemProxyStatus, error) {
	if a.Settings == nil {
		return SystemProxyStatus{}, fmt.Errorf("settings service is not initialized")
	}
	if enabled {
		host, port := "", 0
		if endpoint := a.Settings.GetSystemProxyEndpoint(); endpoint.Host != "" && endpoint.Port > 0 {
			host, port = endpoint.Host, endpoint.Port
		} else if live := platform.ReadSystemProxyControlState(); live.Host != "" {
			if p, err := strconv.Atoi(live.Port); err == nil {
				host, port = live.Host, p
			}
		}
		if host == "" || port <= 0 {
			return a.GetSystemProxyStatus(), fmt.Errorf("代理地址为空：请先配置主机与端口再开启")
		}
		if err := platform.SetSystemProxyEnabled(true, host, port); err != nil {
			return a.GetSystemProxyStatus(), err
		}
		// 记住本次生效端点，下次开启（含其它设备同步场景）免重填；纯持久化
		// 失败不影响已生效的开关，不作为错误上抛。
		_ = a.Settings.SetSystemProxyEndpoint(host, port)
	} else if err := platform.SetSystemProxyEnabled(false, "", 0); err != nil {
		return a.GetSystemProxyStatus(), err
	}
	return a.GetSystemProxyStatus(), nil
}
