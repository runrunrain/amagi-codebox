package launcher

// WSL 侧 pi/omp agent 配置写入（Bug B 修复主路径）。
//
// 问题：CodeBox 是 Windows 进程，历史上把 models.json/models.yml 写到
// Windows 侧 ~/.pi/agent；WSL 终端模式下 pi/omp 在 distro 内运行，读的是
// WSL 侧 $HOME/.pi/agent——两个文件系统，配置从未到达 CLI，--provider
// amagi-<name> 命中 WSL 内旧配置 → 401。
//
// 修复：WSL 模式启动时把合并后的配置写到 WSL 侧 agent root：
//
//	distro 默认用户 $HOME（platform.WSLUserHome，wsl.exe -- sh -lc 探测+缓存）
//	  └─ .pi/agent/models.json（JSON，pi）/ .omp/agent/models.yml（YAML，omp）
//
// 写入方式选型（UNC 直写而非 wsl.exe 内联 base64）：
//   - 复用 WritePiAgentConfig / WriteOmpAgentConfig 的既有原子写范式
//     （MkdirAll -> Marshal -> tmp -> Rename），内容与 Windows 侧产物逐字节
//     一致（同一代码路径序列化）；
//   - 不受 CreateProcess ~32K 命令行长度限制（多 provider/多模型 preset 的
//     配置经 base64 膨胀 4/3 后可能超限）；
//   - merge 读回直接读 UNC 路径上的现有文件，与非 WSL 路径同一条
//     MergePiAgentConfig/MergeOmpModelsConfig 代码路径。
//   权限补偿：Windows os.Chmod 经 9P 不改 POSIX mode 位（本机实证落盘
//   0755/0644），写后经 platform.WSLChmod 在 distro 内 chmod 700 目录 /
//   chmod 600 文件，维持 P1-7 的 0600/0700 收紧契约；失败即整体视为写入
//   失败（fail closed，调用方回退内置 provider env 兜底），与 Windows 侧
//   WritePiAgentConfig 对 chmod 错误的处理一致。
//
// 同族的 WSL 侧 OpenCode 配置写入（证据 T4）：opencode 的 amagi provider
// 唯一载体 OPENCODE_CONFIG_CONTENT 环境变量不在 WSLENV 转发面，WSL 会话
// 开箱零 provider。修复走路径载体：runtime config 写入 distro 内
// ~/.config/opencode/amagi-codebox.json（CodeBox 命名空间私有文件，不碰
// 用户自己的全局 opencode.json），经转发面内的 OPENCODE_CONFIG（Linux 路径
// 值）指向它；opencode 把该文件作为附加配置层加载（实测 v1.17.18）。
// 环境防线 StripWSLHostPathEnv 亦在本文件：剥离所有「将跨界且值为 Windows
// 盘符路径」的变量（PI_* 事故的泛化，证据 B3/T6）。

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"amagi-codebox/internal/platform"
)

// WSL 探测接缝：生产实现指向 platform 包，单测可在 launcher 包内替换
// （platform 的探测 var 未导出，跨包不可注入）。
var (
	wslUserHomeFn = platform.WSLUserHome
	wslToUNCFn    = platform.WSLToUNC
	wslChmodFn    = platform.WSLChmod
)

// wslAgentConfigMu 串行化 WSL 侧 merge→write→chmod 全序列（diting m1）：
// 同一进程内并发启动两个会话（如同 distro 的不同 provider）时，无锁的
// merge（读旧）→ write（覆盖新）会互相丢失对方刚写入的 provider 条目。
// 锁住整个序列保证 read-modify-write 原子性；Windows 侧非 WSL 路径的
// 同类竞态为预存行为，不在本切片扩大范围。
var wslAgentConfigMu sync.Mutex

// WSLAgentTarget 描述一个 WSL distro 内的 agent 目录落点。
type WSLAgentTarget struct {
	Distro   string // wsl.exe -d 目标（如 Ubuntu）
	Home     string // distro 默认用户 $HOME（Linux 路径，如 /home/u）
	LinuxDir string // agent 根（Linux 路径，如 /home/u/.pi/agent）
	UNCDir   string // agent 根（Windows UNC，如 \\wsl.localhost\Ubuntu\home\u\.pi\agent）
}

// resolveWSLAgentTarget 解析 distro 内 <home>/<relAgentDir> 的落点。
// relAgentDir 取 ".pi/agent"（pi）或 ".omp/agent"（omp）。任一探测失败
// （无 distro / home 不可解析 / UNC share 不可达）返回错误，调用方回退
// 既有 Windows 侧行为。
func resolveWSLAgentTarget(distro string, relAgentDir string) (WSLAgentTarget, error) {
	distro = strings.TrimSpace(distro)
	if distro == "" {
		return WSLAgentTarget{}, fmt.Errorf("wsl distro is required")
	}
	home := wslUserHomeFn(distro)
	if home == "" {
		return WSLAgentTarget{}, fmt.Errorf("cannot resolve $HOME in WSL distro %q", distro)
	}
	linuxDir := strings.TrimRight(home, "/") + "/" + relAgentDir
	uncDir := wslToUNCFn(distro, linuxDir)
	if uncDir == "" {
		return WSLAgentTarget{}, fmt.Errorf("WSL UNC share unreachable for distro %q", distro)
	}
	return WSLAgentTarget{Distro: distro, Home: home, LinuxDir: linuxDir, UNCDir: uncDir}, nil
}

// WriteWSLPiAgentConfig 把 pi models.json 合并写入 WSL 侧 agent root。
// 合并语义与 Windows 侧一致（MergePiAgentConfig：保留已有 providers 与
// 顶层字段，amagi-<name> 当次条目优先），原子写（WritePiAgentConfig 的
// tmp+Rename 范式在 UNC 上同样成立）后经 WSLChmod 补偿 0700/0600。
// 成功返回 WSL 内 Linux 路径的 agent 目录（供日志/诊断）。
//
// P3-S1 零写入路径：合并结果与既有文件字节一致时跳过落盘与 chmod 补偿
// （无变化启动的第二次调用不再写目标；权限若被手动改宽，待下次内容变化时
// 随完整写入路径重新收紧）。
func WriteWSLPiAgentConfig(distro string, cfg map[string]any) (string, error) {
	target, err := resolveWSLAgentTarget(distro, ".pi/agent")
	if err != nil {
		return "", err
	}
	wslAgentConfigMu.Lock()
	defer wslAgentConfigMu.Unlock()
	merged := MergePiAgentConfig(cfg, target.UNCDir)
	if content, mErr := json.MarshalIndent(merged, "", "  "); mErr == nil {
		content = append(content, '\n')
		if existing, rErr := os.ReadFile(filepath.Join(target.UNCDir, "models.json")); rErr == nil && bytes.Equal(existing, content) {
			return target.LinuxDir, nil // 零写入：内容一致
		}
	}
	if err := WritePiAgentConfig(target.UNCDir, merged); err != nil {
		return "", err
	}
	if err := wslChmodFn(target.Distro, "700", target.LinuxDir); err != nil {
		// diting m2：chmod 补偿失败时 WSL 侧文件以 umask 默认 0644 留盘，
		// 携带明文 apiKey，视为写入失败并尽力清理已落盘内容（fail closed）。
		_ = os.Remove(filepath.Join(target.UNCDir, "models.json"))
		return "", fmt.Errorf("chmod 700 WSL pi agent dir: %w", err)
	}
	if err := wslChmodFn(target.Distro, "600", target.LinuxDir+"/models.json"); err != nil {
		_ = os.Remove(filepath.Join(target.UNCDir, "models.json"))
		return "", fmt.Errorf("chmod 600 WSL pi models.json: %w", err)
	}
	return target.LinuxDir, nil
}

// WriteWSLOmpAgentConfig 是 WriteWSLPiAgentConfig 的 omp 对称实现
// （.omp/agent/models.yml，YAML 序列化与合并沿用 omp_config.go）。
func WriteWSLOmpAgentConfig(distro string, cfg map[string]any) (string, error) {
	target, err := resolveWSLAgentTarget(distro, ".omp/agent")
	if err != nil {
		return "", err
	}
	wslAgentConfigMu.Lock()
	defer wslAgentConfigMu.Unlock()
	merged := MergeOmpModelsConfig(cfg, target.UNCDir)
	if err := WriteOmpAgentConfig(target.UNCDir, merged); err != nil {
		return "", err
	}
	if err := wslChmodFn(target.Distro, "700", target.LinuxDir); err != nil {
		// 同 WriteWSLPiAgentConfig（diting m2）：chmod 补偿失败即清理已落盘
		// 明文内容，fail closed。
		_ = os.Remove(filepath.Join(target.UNCDir, "models.yml"))
		return "", fmt.Errorf("chmod 700 WSL omp agent dir: %w", err)
	}
	if err := wslChmodFn(target.Distro, "600", target.LinuxDir+"/models.yml"); err != nil {
		_ = os.Remove(filepath.Join(target.UNCDir, "models.yml"))
		return "", fmt.Errorf("chmod 600 WSL omp models.yml: %w", err)
	}
	return target.LinuxDir, nil
}

// isWindowsDrivePath 报告 s 是否为 Windows 盘符绝对路径（C:\... 或 C:/...）。
// WSL 内该形态是非法 Linux 路径：经 WSLENV 转发后 pi 会把它当相对路径，
// 在 cwd 下创建垃圾目录（实战：PI_SESSION_FILE/PI_CODING_AGENT_DIR 泄漏）。
func isWindowsDrivePath(s string) bool {
	if len(s) < 3 {
		return false
	}
	c := s[0]
	if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')) {
		return false
	}
	return s[1] == ':' && (s[2] == '\\' || s[2] == '/')
}

// StripWSLHostPathEnv 移除「将经 WSLENV 转发面跨界、且值为 Windows 盘符
// 路径」的环境变量，返回剥离后的 env 与被剥离的键名列表（供调用方记日志
// 保持可观测）。
//
// 泛化自 1.3.59 的 StripWSLHostPathPIEnv（PI_SESSION_FILE/PI_CODING_AGENT_DIR
// 泄漏让 pi 在 WSL cwd 建垃圾目录的真实事故）：转发面不只 PI_ 前缀——
// ANTHROPIC_/OPENAI_/CLAUDE_/CODEX_ 前缀、精确键（代理、OPENCODE_CONFIG）与
// extraForwardKeys（envvars.json 自定义键）携带 Windows 盘符路径值时在
// Linux 侧同样非法（如 CODEX_HOME=C:\...，证据 B3/T6）。判据与
// appendWSLENVForwarding 拼清单完全一致（platform.ShouldForwardToWSL）。
// 标量值（PI_OFFLINE=1 等）与 Linux 路径值（OPENCODE_CONFIG 指向 distro 内
// 文件）不受影响；不在转发面的键即使值为 Windows 路径也不动（它们根本
// 不会跨界）。必须在 appendWSLENVForwarding 之前调用（剥离后变量缺席，
// WSLENV 清单中也不会再出现对应名字）。
func StripWSLHostPathEnv(env []string, extraForwardKeys ...string) ([]string, []string) {
	out := make([]string, 0, len(env))
	stripped := []string{}
	for _, kv := range env {
		key, value := splitEnvKV(kv)
		if key != "" && platform.ShouldForwardToWSL(key, extraForwardKeys...) && isWindowsDrivePath(strings.TrimSpace(value)) {
			stripped = append(stripped, key)
			continue
		}
		out = append(out, kv)
	}
	return out, stripped
}

// openCodeWSLConfigFile 是 WriteWSLOpenCodeAgentConfig 在 distro 内的目标
// 文件名（~/.config/opencode/ 下）。用 CodeBox 命名空间私有名而非
// opencode.json：后者是用户全局配置文件，覆盖写会顶掉用户自己的配置。
const openCodeWSLConfigFile = "amagi-codebox.json"

// WriteWSLOpenCodeAgentConfig 把 OpenCode runtime config（BuildOpenCodeRuntimeConfig
// / BuildOpenCodeRuntimeConfigFromPreset 的产物，内含 apiKey）写入 WSL 侧
// ~/.config/opencode/amagi-codebox.json，成功返回 WSL 内 Linux 文件路径
// （供 OPENCODE_CONFIG 指向；调用方负责把该键值注入启动 env 使其进入
// WSLENV 转发清单）。
//
// 与 pi/omp 写入的两点差异：
//   - 无 merge：目标文件是 CodeBox 命名空间私有的，每次启动整体覆盖即所需
//     终态；用户全局配置由 opencode 以独立配置层自行合并（OPENCODE_CONFIG
//     是附加层而非顶替层，实测 v1.17.18），不需要我们做读改写。
//   - 失败语义：同样 fail closed（chmod 补偿失败即清理已落盘明文并返回
//     错误），但调用方对写失败不阻断启动（回退现状＝无 provider 启动，
//     与 pi 的 WSL 写入失败策略一致），仅记日志。
//
// 原子写沿用 WritePiAgentConfig 的 MkdirAll -> MarshalIndent -> tmp -> Rename
// 范式；0700/0600 经 platform.WSLChmod 在 distro 内补偿（9P 不透传 POSIX 位）。
func WriteWSLOpenCodeAgentConfig(distro string, cfg map[string]any) (string, error) {
	target, err := resolveWSLAgentTarget(distro, ".config/opencode")
	if err != nil {
		return "", err
	}
	wslAgentConfigMu.Lock()
	defer wslAgentConfigMu.Unlock()
	// P3-S1 零写入路径：序列化结果与既有文件字节一致时跳过落盘与 chmod 补偿。
	if b, mErr := json.MarshalIndent(cfg, "", "  "); mErr == nil {
		b = append(b, '\n')
		if existing, rErr := os.ReadFile(filepath.Join(target.UNCDir, openCodeWSLConfigFile)); rErr == nil && bytes.Equal(existing, b) {
			return target.LinuxDir + "/" + openCodeWSLConfigFile, nil // 零写入：内容一致
		}
	}
	if err := writeOpenCodeConfigFile(target.UNCDir, cfg); err != nil {
		return "", err
	}
	if err := wslChmodFn(target.Distro, "700", target.LinuxDir); err != nil {
		_ = os.Remove(filepath.Join(target.UNCDir, openCodeWSLConfigFile))
		return "", fmt.Errorf("chmod 700 WSL opencode config dir: %w", err)
	}
	if err := wslChmodFn(target.Distro, "600", target.LinuxDir+"/"+openCodeWSLConfigFile); err != nil {
		_ = os.Remove(filepath.Join(target.UNCDir, openCodeWSLConfigFile))
		return "", fmt.Errorf("chmod 600 WSL opencode config: %w", err)
	}
	return target.LinuxDir + "/" + openCodeWSLConfigFile, nil
}

// writeOpenCodeConfigFile 以全库统一原子范式把 cfg 写入 dir/amagi-codebox.json。
// P3-S1 零写入路径：序列化结果与既有文件字节一致时跳过落盘（无变化启动的
// 第二次调用不再写目标）。
func writeOpenCodeConfigFile(dir string, cfg map[string]any) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("mkdir opencode config dir: %w", err)
	}
	// MkdirAll 不会收紧已存在目录的权限（.config 层级可能已由其他工具以
	// 0755 建立），显式 Chmod 覆盖升级场景（对齐 WritePiAgentConfig）。
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("chmod opencode config dir: %w", err)
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal opencode config: %w", err)
	}
	b = append(b, '\n')
	path := filepath.Join(dir, openCodeWSLConfigFile)
	if existing, rErr := os.ReadFile(path); rErr == nil && bytes.Equal(existing, b) {
		return nil // 零写入：内容一致
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("write opencode config tmp: %w", err)
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("chmod opencode config tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replace opencode config: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("chmod opencode config: %w", err)
	}
	return nil
}
