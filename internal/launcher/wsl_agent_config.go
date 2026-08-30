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

import (
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
func WriteWSLPiAgentConfig(distro string, cfg map[string]any) (string, error) {
	target, err := resolveWSLAgentTarget(distro, ".pi/agent")
	if err != nil {
		return "", err
	}
	wslAgentConfigMu.Lock()
	defer wslAgentConfigMu.Unlock()
	merged := MergePiAgentConfig(cfg, target.UNCDir)
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

// StripWSLHostPathPIEnv 移除值为 Windows 盘符路径的 PI_* 环境变量。
// WSLENV 转发规则（platform.wslENVForwardPrefixes）会把所有 PI_ 前缀变量
// 带进 WSL；其中携带 Windows 路径值的变量（用户系统环境或上游进程残留的
// PI_CODING_AGENT_DIR / PI_SESSION_FILE 等）在 Linux 侧必然非法。WSL 模式
// 启动前剥离，确保 Windows 值不再泄漏进 WSL；标量值（PI_OFFLINE=1 等）
// 不受影响。必须在 appendWSLENVForwarding 之前调用（剥离后变量缺席，
// WSLENV 清单中也不会再出现对应名字）。
func StripWSLHostPathPIEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		key, value := splitEnvKV(kv)
		if key != "" && strings.HasPrefix(strings.ToUpper(key), "PI_") && isWindowsDrivePath(strings.TrimSpace(value)) {
			continue
		}
		out = append(out, kv)
	}
	return out
}
