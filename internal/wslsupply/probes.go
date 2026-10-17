package wslsupply

// probes.go — 协调器的平台探测接缝（依赖注入而非包级可变 var：协调器实例
// 各自持有探测面，测试构造假探测即可，不污染全局）。生产实现指向
// internal/platform（WSL 探测/UNC 映射/权限补偿）。
//
// 攻坚契约（PR02/PR07）：Identity/Chmod/StatMode 改带 ctx，真实调用平台的
// Context 变体（exec.CommandContext）——ctx 贯通到实际探测/写边界；不能回退
// 无 ctx probe 或用 goroutine 包旧函数冒充取消。

import (
	"context"

	"amagi-codebox/internal/platform"
)

// Logger 是协调器的最小日志接缝（nil 安全）。日志不携带凭据正文与 hash。
type Logger interface {
	Info(scope, msg, detail string)
	Warn(scope, msg, detail string)
}

// IdentityProbe 批量解析 distro 默认用户身份（uid/home/machine-id），是
// TargetRef 的唯一身份数据源；一次供给链至多触发两次（ResolveTarget 与
// Apply 复核）。ctx 取消时探测应尽快退出（平台 Context 变体保证）。
type IdentityProbe = func(ctx context.Context, distro string) platform.WSLUserIdentity

// UNCMapper 把 distro 内绝对 Linux 路径映射为 Windows UNC（不可达返回空串）。
// 纯本地映射，无 I/O，不需要 ctx。
type UNCMapper = func(distro, linuxPath string) string

// ChmodProbe 在 distro 内执行 chmod 补偿（Windows os.Chmod 经 9P 不落 POSIX
// mode 位，0600/0700 契约必须经 distro 内 chmod 达成）。ctx 取消时尽力终止
// wsl.exe（Linux 子进程存活不据此宣称）。
type ChmodProbe = func(ctx context.Context, distro, mode string, linuxPaths ...string) error

// StatModeProbe 在 distro 内读取一个绝对 Linux 路径的八进制权限位（如 "600"）。
// 这是「私有 staging 已隔离」的唯一权威验证面：UNC/宿主侧 stat 呈现的 mode
// 位经 9P 翻译不可信。探测失败返回 ok=false，调用方必须按「隔离未验证」
// fail closed，不得把失败当作已验证（SA-06）。
type StatModeProbe = func(ctx context.Context, distro, linuxPath string) (mode string, ok bool)

// Probes 是协调器的平台探测面。
type Probes struct {
	Identity IdentityProbe
	ToUNC    UNCMapper
	Chmod    ChmodProbe
	// StatMode 是敏感提交原语的隔离验证探测（SA-06：秘密字节落盘前确认
	// 0700 staging 真实生效）。nil 时由 NewCoordinator 从 DefaultProbes 补齐。
	StatMode StatModeProbe
}

// DefaultProbes 返回指向 internal/platform 的生产探测面（Context 变体：
// 真实 exec.CommandContext——ctx 取消时探测进程随 ctx 终止；取消语义的
// Linux 子进程边界由平台层如实呈现）。
func DefaultProbes() Probes {
	return Probes{
		Identity: platform.WSLTargetIdentityContext,
		ToUNC:    platform.WSLToUNC,
		Chmod:    platform.WSLChmodContext,
		StatMode: platform.WSLFileModeContext,
	}
}

// logInfo/logWarn 是 nil 安全的日志出口。
func logInfo(log Logger, scope, msg, detail string) {
	if log == nil {
		return
	}
	log.Info(scope, msg, detail)
}

func logWarn(log Logger, scope, msg, detail string) {
	if log == nil {
		return
	}
	log.Warn(scope, msg, detail)
}
