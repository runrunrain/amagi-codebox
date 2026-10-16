package wslsupply

// probes.go — 协调器的平台探测接缝（依赖注入而非包级可变 var：协调器实例
// 各自持有探测面，测试构造假探测即可，不污染全局）。生产实现指向
// internal/platform（WSL 探测/UNC 映射/权限补偿）。

import (
	"amagi-codebox/internal/platform"
)

// Logger 是协调器的最小日志接缝（nil 安全）。日志不携带凭据正文与 hash。
type Logger interface {
	Info(scope, msg, detail string)
	Warn(scope, msg, detail string)
}

// IdentityProbe 批量解析 distro 默认用户身份（uid/home/machine-id），是
// TargetRef 的唯一身份数据源；一次供给链至多触发两次（ResolveTarget 与
// Apply 复核）。
type IdentityProbe = func(distro string) platform.WSLUserIdentity

// UNCMapper 把 distro 内绝对 Linux 路径映射为 Windows UNC（不可达返回空串）。
type UNCMapper = func(distro, linuxPath string) string

// ChmodProbe 在 distro 内执行 chmod 补偿（Windows os.Chmod 经 9P 不落 POSIX
// mode 位，0600/0700 契约必须经 distro 内 chmod 达成）。
type ChmodProbe = func(distro, mode string, linuxPaths ...string) error

// Probes 是协调器的平台探测面。
type Probes struct {
	Identity IdentityProbe
	ToUNC    UNCMapper
	Chmod    ChmodProbe
}

// DefaultProbes 返回指向 internal/platform 的生产探测面。
func DefaultProbes() Probes {
	return Probes{
		Identity: platform.WSLTargetIdentity,
		ToUNC:    platform.WSLToUNC,
		Chmod:    platform.WSLChmod,
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
