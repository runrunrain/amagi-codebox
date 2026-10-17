package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"amagi-codebox/internal/platform"
)

// launch_workdir.go 是所有嵌入式终端启动的"工作目录"单一校验/回退 choke point。
//
// 背景：Windows 用户启动会话报
//
//	start pty: conpty start: Failed to create console process: The directory name is invalid.
//
// 根因：ConPtyWorkDir 最终传给 CreateProcessW 的 lpCurrentDirectory，目录无效时
// 进程创建直接失败（ERROR_DIRECTORY 267）。而 workDir 全链路
// （前端 payload → App.Launch* → resolveEmbeddedLaunchSpec → PTY spec → conpty）
// 此前只在 workDir=="" 时回退 GetDefaultPath/home，非空值从不校验
// "绝对化 + 存在 + 是目录"，陈旧的默认目录、笔误、带引号路径都会击穿到 conpty。

// launchWorkDirRejection 记录一个被淘汰的候选目录，用于回退日志与最终错误信息。
type launchWorkDirRejection struct {
	label  string // 候选来源（requested workDir / default path / home）
	path   string // 原始值
	reason string // 淘汰原因
}

// launchTargetsWSLFn 仅供 env 防线的预先介入判定（WSL 会话需在解析器拼
// WSLENV 清单之前完成宿主盘符路径值剥离，无法等解析后定谳）。生产实现
// 与桌面 resolveEmbeddedLaunchSpec 的 WSL 防线门同源（EmbeddedLaunchTargetsWSL
// 谓词 + DefaultWSLDistro 双条件；内部含 distro 探测，探测结果进程内缓存）。
// 包级 var 是测试接缝。注意：工作目录的目标判定不经本接缝——见
// resolveLaunchWorkDirForSpec（纯目标判据，零探测）。
var launchTargetsWSLFn = func(launchMode, requestedShellPath string) bool {
	return platform.EmbeddedLaunchTargetsWSL(launchMode, requestedShellPath, nil) &&
		platform.DefaultWSLDistro(nil) != ""
}

// isLinuxStyleWorkDirFn 报告目录字符串是否为 Linux 形态路径（/…、~、~/…）。
// 生产实现 = platform.IsLinuxStylePath（Windows 宿主实现；非 Windows 宿主恒
// false）。包级 var 是测试接缝：macOS CI 上注入镜像 Windows 语义的假实现
// 驱动同一条纯逻辑，不触达真实 wsl.exe。
var isLinuxStyleWorkDirFn = platform.IsLinuxStylePath

// resolveLaunchWorkDir 归一化并校验启动工作目录，是全部 App.Launch* 入口
// （LaunchSession/LaunchCodexSession/LaunchPiSession/LaunchOmpSession/LaunchOpenCode）
// 的统一宿主侧入口（原生目标/会话记录/外部终端模式的权威值）。规则见
// resolveLaunchWorkDirChain；回退发生时记录 Warn（含原始无效路径、原因与
// 回退目标）。全部候选无效时才返回错误。
//
// WSL 目标的最终工作目录不在这里定谳：入口把宿主链产物与本函数的原始
// 请求一并交给 resolveEmbeddedLaunchSpec，由解析产出的 spec.BootstrapMode
// 后置定谳（见 resolveLaunchWorkDirForSpec，纯目标判据、零探测）。
func (a *App) resolveLaunchWorkDir(workDir string) (string, error) {
	return resolveLaunchWorkDirChain(workDir, a.Paths.GetDefaultPath(), func(requested, fallback, reason string) {
		a.Log.Warn("session", "启动工作目录不可用，已回退",
			fmt.Sprintf("requested=%q reason=%s fallback=%q", requested, reason, fallback))
	})
}

// resolveLaunchWorkDirForSpec 在解析完成后定谳启动工作目录（纯目标判据：
// 以 resolver 产出的 spec.BootstrapMode 为准，本函数不做任何 WSL 探测）。
//
//   - 非 WSL 目标：返回 hostResolved（入口宿主链产物），原生 Windows/macOS
//     回退语义与修复前完全一致；
//   - WSL 目标 + 显式非空请求：Linux 形态（/home/…、~、~/…）原样保留
//     （TrimSpace 后交给 wsl.exe --cd，存在性由目标 distro 判定）；其余
//     形态（Windows 盘符/UNC/相对路径）仍由宿主校验，校验失败直接报错
//     ——任务契约：WSL 明确请求的目录不得静默换工程；空请求才走回退；
//   - WSL 目标 + 空请求：defaultPath 为 Linux 形态时同样保留（用户配置的
//     默认 ~/project 不得被宿主校验吞掉）；否则维持 hostResolved（默认
//     目录→Home 的既有回退链）。
//
// onWSLPreserve（可为 nil）在保留 Linux 形态目录时回调一次，供入口记
// INFO（与宿主链的回退 WARN 互补，拼出完整可观测叙事）。
func resolveLaunchWorkDirForSpec(bootstrapWSL bool, requested, hostResolved, defaultPath string, onWSLPreserve func(preserved string)) (string, error) {
	if !bootstrapWSL {
		return hostResolved, nil
	}
	trimmed := strings.TrimSpace(requested)
	if trimmed != "" {
		if isLinuxStyleWorkDirFn(trimmed) {
			if onWSLPreserve != nil {
				onWSLPreserve(trimmed)
			}
			return trimmed, nil
		}
		// 显式非空的非 Linux 形态：宿主可判其存在性（ConPtyWorkDir 亦消费
		// 该形态）；失败即拒绝启动，不回退换目录。
		abs, reason, ok := validateLaunchWorkDir(trimmed)
		if !ok {
			return "", fmt.Errorf("WSL 会话请求的工作目录不可用: %q: %s；已拒绝启动（不回退换目录）", trimmed, reason)
		}
		return abs, nil
	}
	if def := strings.TrimSpace(defaultPath); def != "" && isLinuxStyleWorkDirFn(def) {
		if onWSLPreserve != nil {
			onWSLPreserve(def)
		}
		return def, nil
	}
	return hostResolved, nil
}

// resolveLaunchWorkDirChain 执行 候选链：requested → defaultPath → 用户 Home。
//
//   - requested 先 filepath.Clean + filepath.Abs：相对路径（如 "." 或项目名）
//     基于进程 cwd 解析，解析后存在即可用；
//   - 候选仅当 os.Stat 确认存在且为目录时可用；带引号/尾部空格的路径在
//     Clean 后 Stat 自然失败并回退，不做额外字符串清洗；Windows 反斜杠/
//     正斜杠差异由 os.Stat 统一吸收，无需分支处理；
//   - requested 为空按"无偏好"处理，静默走回退链（与既有行为一致）；
//   - 非空但被淘汰的候选在链上有幸存者时逐个回调 onFallback（可为 nil），
//     携带原始路径、最终采用的目录与淘汰原因；
//   - 全部候选无效才返回错误，错误信息包含每个候选的原始路径与原因，
//     可直接定位问题来源。
func resolveLaunchWorkDirChain(requested, defaultPath string, onFallback func(requested, fallback, reason string)) (string, error) {
	home, homeErr := os.UserHomeDir()
	if homeErr != nil {
		home = ""
	}
	candidates := []struct{ label, path string }{
		{"requested workDir", requested},
		{"default path", defaultPath},
		{"home", home},
	}

	var rejected []launchWorkDirRejection
	for _, cand := range candidates {
		if cand.path == "" {
			continue
		}
		abs, reason, ok := validateLaunchWorkDir(cand.path)
		if !ok {
			rejected = append(rejected, launchWorkDirRejection{label: cand.label, path: cand.path, reason: reason})
			continue
		}
		for _, rej := range rejected {
			if onFallback != nil {
				onFallback(rej.path, abs, rej.reason)
			}
		}
		return abs, nil
	}
	if len(rejected) == 0 {
		return "", fmt.Errorf("no usable working directory: workdir, default path and home are all empty")
	}
	details := make([]string, 0, len(rejected))
	for _, rej := range rejected {
		details = append(details, fmt.Sprintf("%s %q: %s", rej.label, rej.path, rej.reason))
	}
	return "", fmt.Errorf("no usable working directory: %s", strings.Join(details, "; "))
}

// validateLaunchWorkDir 归一化（Clean+Abs）并验证存在且为目录。
// ok=false 时 reason 给出人可读的淘汰原因（供日志与错误信息）。
func validateLaunchWorkDir(raw string) (abs, reason string, ok bool) {
	abs, err := filepath.Abs(filepath.Clean(raw))
	if err != nil {
		return "", fmt.Sprintf("cannot make absolute: %v", err), false
	}
	info, err := os.Stat(abs)
	if err != nil {
		return abs, fmt.Sprintf("stat: %v", err), false
	}
	if !info.IsDir() {
		return abs, "exists but is not a directory", false
	}
	return abs, "", true
}
