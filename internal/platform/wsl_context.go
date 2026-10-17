package platform

// wsl_context.go — WSL 探测原语的 context 变体（wsl-goal-closure 攻坚 G0）。
//
// 旧无 ctx 函数（WSLTargetIdentity/WSLFileMTime/WSLFileMode/WSLChmod，经
// wslScriptRunner）保留供既有消费者兼容；本文件提供真实可取消的 Context
// 变体，供 A2 凭据同步等需要「每个同步 I/O 后可查取消」的链路使用。
//
// 真实性约束：Context 变体一律经 wslScriptRunnerContext（exec.CommandContext
// 构造），不做 goroutine 包旧函数的假取消；调用方在取消后必须按失败处理，
// 不得把「wsl.exe 已被杀」当作「distro 内 Linux 子进程已退出」——Linux 侧
// 后继写是否发生需 Windows 真机验证（本仓恒标注未真机验证）。
//
// 脚本语义与旧函数逐字一致（--exec / quoting / 输出解码 / fail-closed 解析），
// 差异仅在：入口先查 ctx.Err()，探测经可取消 runner 执行。

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
)

// wslScriptRunnerContext runs `wsl.exe -d <distro> --exec sh -lc <script>` with
// a real context (exec.CommandContext). Package var so tests can inject a fake
// and assert the context actually flows to the runner; the default
// implementation honors ctx cancellation at Start and kills wsl.exe when the
// context is done while the process runs.
var wslScriptRunnerContext = func(ctx context.Context, distro string, script string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "wsl.exe", "-d", distro, "--exec", "sh", "-lc", script)
	// 与无 ctx 版本同策略：GUI 父进程下抑制 console 子系统 exe 闪窗。
	SuppressConsoleWindow(cmd)
	return cmd.Output()
}

// WSLTargetIdentityContext 是 WSLTargetIdentity 的可取消变体：批量探测 distro
// 默认用户 uid/$HOME/machine-id。ctx 取消时返回 OK=false；语义（不缓存、
// 解析 fail-closed）与无 ctx 版本一致。
func WSLTargetIdentityContext(ctx context.Context, distro string) WSLUserIdentity {
	distro = strings.TrimSpace(distro)
	if distro == "" {
		return WSLUserIdentity{}
	}
	if err := ctx.Err(); err != nil {
		return WSLUserIdentity{}
	}
	out, err := wslScriptRunnerContext(ctx, distro, wslIdentityScript)
	if err != nil {
		return WSLUserIdentity{}
	}
	return parseWSLUserIdentity(decodeWSLListOutput(out))
}

// stat 的生产入口按 build tags 分文件；此核心保留显式 OS 参数供离线测试。
func wslFileMTimeContextForOS(ctx context.Context, osName, distro, linuxPath string) (int64, bool) {
	distro = strings.TrimSpace(distro)
	if distro == "" || osName != "windows" || !strings.HasPrefix(linuxPath, "/") {
		return 0, false
	}
	if err := ctx.Err(); err != nil {
		return 0, false
	}
	out, err := wslScriptRunnerContext(ctx, distro, "stat -c %Y "+wslShSingleQuote(linuxPath))
	if err != nil {
		return 0, false
	}
	sec, perr := strconv.ParseInt(strings.TrimSpace(strings.Trim(decodeWSLListOutput(out), "\r\x00 ")), 10, 64)
	if perr != nil || sec < 0 {
		return 0, false
	}
	return sec, true
}

func wslFileModeContextForOS(ctx context.Context, osName, distro, linuxPath string) (string, bool) {
	distro = strings.TrimSpace(distro)
	if distro == "" || osName != "windows" || !strings.HasPrefix(linuxPath, "/") {
		return "", false
	}
	if err := ctx.Err(); err != nil {
		return "", false
	}
	out, err := wslScriptRunnerContext(ctx, distro, "stat -c %a "+wslShSingleQuote(linuxPath))
	if err != nil {
		return "", false
	}
	return parseWSLStatModeOutput(decodeWSLListOutput(out))
}

// WSLChmodContext 是 WSLChmod 的可取消变体（distro 内 chmod；mode 校验与
// 路径转义复用同一 buildWSLChmodScript）。ctx 取消时返回 ctx 错误且不执行
// 任何 chmod。
func WSLChmodContext(ctx context.Context, distro string, mode string, linuxPaths ...string) error {
	distro = strings.TrimSpace(distro)
	if distro == "" || len(linuxPaths) == 0 {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	script, err := buildWSLChmodScript(mode, linuxPaths)
	if err != nil {
		return err
	}
	if _, err := wslScriptRunnerContext(ctx, distro, script); err != nil {
		return err
	}
	return nil
}
