package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

// resolveLaunchWorkDir 归一化并校验启动工作目录，是全部 App.Launch* 入口
// （LaunchSession/LaunchCodexSession/LaunchPiSession/LaunchOmpSession/LaunchOpenCode）
// 的统一入口。规则见 resolveLaunchWorkDirChain；回退发生时记录 Warn
// （含原始无效路径、原因与回退目标）。全部候选无效时才返回错误。
func (a *App) resolveLaunchWorkDir(workDir string) (string, error) {
	return resolveLaunchWorkDirChain(workDir, a.Paths.GetDefaultPath(), func(requested, fallback, reason string) {
		a.Log.Warn("session", "启动工作目录不可用，已回退",
			fmt.Sprintf("requested=%q reason=%s fallback=%q", requested, reason, fallback))
	})
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
