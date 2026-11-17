package platform

// wsl_launch_probe.go — WSL 启动预检融合探测（启动 RPC 关键路径去串行化）。
//
// 一次 wsl.exe 调用同时取得启动链需要的两份事实：
//   - 身份三件套（uid/$HOME/machine-id，脚本逐字复用 wslIdentityScript）；
//   - 工作目录定谳（buildWSLWorkDirCheck 的目录校验 + 与
//     WSLResolveDirectoryContext 完全一致的 __AMAGI_RESOLVED_DIRECTORY__ 标记协议）。
//
// 结果只在单次启动 RPC 作用域内显式传递（供给闸门、PTY 启动共用），不做任何
// 跨启动缓存；冷 distro 的 VM 唤醒 + 登录 shell 解析成本由多次串行探测合并为
// 一次。任一环节失败即整体失败（fail closed），中文错误文案携带 distro 与
// workDir（风格参照 pty/runtime_target_windows.go 的目录拒绝文案）。

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"time"
)

// wslResolvedDirectoryMarker 是目录定谳标记（与 WSLResolveDirectoryContext 同
// 协议：NUL 框住路径，防 shell 混入输出被误读为路径）。
const wslResolvedDirectoryMarker = "\x00__AMAGI_RESOLVED_DIRECTORY__\x00"

// WSLLaunchPreflight 是一次融合预检的结果：身份三件套 + 定谳后的工作目录。
// 构造成功的返回值 Identity.OK 恒为 true；ResolvedWorkDir 是 distro 内绝对
// Linux 物理路径（cd -P 后的 $PWD）。
type WSLLaunchPreflight struct {
	Identity        WSLUserIdentity
	ResolvedWorkDir string
}

// WSLLaunchPreflightProbe 一次 wsl.exe 调用融合取得身份三件套与工作目录定谳。
// 脚本 = wslIdentityScript（三行身份在前）+ buildWSLWorkDirCheck（目录校验）+
// 定谳标记 printf；10s 预算，经 wslScriptRunnerContext 执行（可取消）。解析：
// 原始输出先按目录标记切分；标记前输出的前三行喂 parseWSLUserIdentity（身份
// 行之后可能混入目录脚本的 OK 标记行，截前三行避免污染）；标记段按
// WSLResolveDirectoryContext 的既有校验（NUL 结尾、/ 前缀、无内嵌 NUL）取路径。
// 任一失败返回错误——fail closed，不把失败当空值。
func WSLLaunchPreflightProbe(ctx context.Context, distro, workDir string) (WSLLaunchPreflight, error) {
	distro = strings.TrimSpace(distro)
	if distro == "" {
		return WSLLaunchPreflight{}, fmt.Errorf("WSL 启动预检失败：没有可用的发行版（工作目录 %q），已拒绝启动", workDir)
	}
	script, err := buildWSLWorkDirCheck(workDir)
	if err != nil {
		return WSLLaunchPreflight{}, fmt.Errorf("WSL %s 启动预检失败：工作目录 %q 无法构造校验脚本: %w", distro, workDir, err)
	}
	script = wslIdentityScript + "\n" + script
	script += `printf '\000__AMAGI_RESOLVED_DIRECTORY__\000%s\000' "$PWD"` + "\n"
	probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := probeCtx.Err(); err != nil {
		return WSLLaunchPreflight{}, fmt.Errorf("WSL %s 启动预检未完成（工作目录 %q）: %w", distro, workDir, err)
	}
	raw, err := wslScriptRunnerContext(probeCtx, distro, script)
	if probeCtx.Err() != nil {
		return WSLLaunchPreflight{}, fmt.Errorf("WSL %s 启动预检未完成（工作目录 %q）: %w", distro, workDir, probeCtx.Err())
	}
	if err != nil {
		return WSLLaunchPreflight{}, fmt.Errorf("WSL %s 工作目录 %q 不可用，已拒绝启动（不回退换目录）；请重新选择发行版内目录或本机 Windows 盘符路径: %w", distro, workDir, err)
	}
	return parseWSLLaunchPreflight(raw, distro, workDir)
}

// parseWSLLaunchPreflight 解析融合脚本输出（独立函数便于表驱动测试）。
func parseWSLLaunchPreflight(raw []byte, distro, workDir string) (WSLLaunchPreflight, error) {
	at := bytes.Index(raw, []byte(wslResolvedDirectoryMarker))
	if at < 0 {
		return WSLLaunchPreflight{}, fmt.Errorf("WSL %s 启动预检未取得目录定谳标记（工作目录 %q），已拒绝启动", distro, workDir)
	}
	// 身份段：标记前输出的前三行（身份三行之后的 __AMAGI_WORKDIR_OK__ 等
	// 目录脚本输出行不得进入 machine-id 解析）。
	identity := parseWSLUserIdentity(firstNLines(decodeWSLListOutput(raw[:at]), 3))
	if !identity.OK {
		return WSLLaunchPreflight{}, fmt.Errorf("WSL %s 启动预检身份解析失败（uid/$HOME 输出形态异常，工作目录 %q），已拒绝启动", distro, workDir)
	}
	// 目录段：与 WSLResolveDirectoryContext 相同的校验口径。
	path := string(raw[at+len(wslResolvedDirectoryMarker):])
	if !strings.HasSuffix(path, "\x00") {
		return WSLLaunchPreflight{}, fmt.Errorf("WSL %s 启动预检目录输出不完整（工作目录 %q），已拒绝启动", distro, workDir)
	}
	path = strings.TrimSuffix(path, "\x00")
	if !strings.HasPrefix(path, "/") || strings.ContainsRune(path, 0) {
		return WSLLaunchPreflight{}, fmt.Errorf("WSL %s 启动预检返回无效路径 %q（工作目录 %q），已拒绝启动", distro, path, workDir)
	}
	return WSLLaunchPreflight{Identity: identity, ResolvedWorkDir: path}, nil
}

// firstNLines 返回前 n 行（不足 n 行返回全部；按 \n 切分再拼回）。
func firstNLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}
