package main

import (
	"errors"

	"amagi-codebox/internal/platform"
)

// --- Headroom × WSL 网络准入（D1/D6 共用窄判定） ---
//
// Headroom 压缩入口（claude 会话级 8787 / codex 全局 8788）监听宿主回环
// (127.0.0.1)。WSL2 默认 NAT 模式下 distro 内的 127.0.0.1 指向发行版自身，
// 宿主回环不可达；mirrored（%USERPROFILE%\.wslconfig 的 [wsl2] 段
// networkingMode=mirrored）下 localhost 双向互通。判定与
// aiproxy.PinProxyEnvironment 同源：只读 .wslconfig，不 fork wsl.exe；
// 空/未知按 NAT 对待（fail-safe）；mirrored 仅准入，不构成连通性声明。

// headroomWSLNetworkAdmission 对「将落在 WSL distro 内的 Headroom 会话」执行
// NAT/未知网络模式的准入判定：非 WSL 目标直接放行；mirrored 放行（遵循现有
// 准入策略，不宣称连通）；NAT/未知返回带修复指引的中文拒绝。调用方必须在
// Headroom.Start、admission 获取、会话预留与 PTY 启动等副作用之前调用。
func headroomWSLNetworkAdmission(targetsWSL bool, env []string) error {
	if !targetsWSL {
		return nil
	}
	if platform.WSLNetworkingMode(env) == "mirrored" {
		return nil
	}
	return errors.New(`Headroom 压缩入口监听宿主回环（127.0.0.1:8787），当前 WSL 为 NAT 网络模式，发行版内不可达，本次未启动。可在 %USERPROFILE%\.wslconfig 的 [wsl2] 段设置 networkingMode=mirrored，执行 wsl --shutdown 重启 WSL 后重试；或关闭 Headroom 后启动 WSL 会话`)
}

// headroomSpecTargetsWSL 报告已解析 spec 是否落在 WSL distro 内（planner 链
// 的 WSL 判定输入，与桌面链 EmbeddedLaunchTargetsWSL 语义对齐）。
func headroomSpecTargetsWSL(spec platform.ResolvedLaunchSpec) bool {
	return spec.BootstrapMode == platform.BootstrapWSL
}

// --- Codex 全局 Headroom × WSL（D6） ---
//
// codex 全局压缩的路由载体是宿主侧 ~/.codex/config.toml 的 openai_base_url
// 标记块（syncCodexGlobalHeadroomConfig）；WSL codex 会话读取发行版侧独立
// 配置，无同步（wslsupply 的 UnitCodexConfig 不含 headroom 标记块），因此
// 任何网络模式下压缩都不会对 WSL 会话生效。

// codexHeadroomWSLIneffectiveMsg 是「codex 全局压缩对 WSL 会话不生效」的
// advisory 文案（桌面链启动日志 + 拒绝语义共用，供单测钉文案）。
func codexHeadroomWSLIneffectiveMsg() string {
	return `codex 全局压缩（127.0.0.1:8788）的路由写入 Windows 侧 ~/.codex/config.toml，WSL 会话读取发行版侧配置，压缩不会生效；本次 WSL 会话将直连所选 provider 端点`
}

// errCodexHeadroomWSLUnsupported 是 remote/restart 计划链对「显式选择压缩的
// WSL codex 会话」的拒绝：无法兑现的压缩选择按明确失败处理，不默默以直连
// 冒充压缩生效（与 Claude D1 拒绝对称；v1 客户端收到既有 capability 失败
// 文案，判定细节由宿主侧与桌面链日志/文案承载）。
func errCodexHeadroomWSLUnsupported() error {
	return errors.New("codex 压缩路由仅写入宿主侧 config.toml，对 WSL 会话不生效：" + codexHeadroomWSLIneffectiveMsg())
}
