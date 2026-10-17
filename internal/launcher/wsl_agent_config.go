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
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"amagi-codebox/internal/platform"
	"gopkg.in/yaml.v3"
)

// WSL 探测接缝：生产实现指向 platform 包，单测可在 launcher 包内替换
// （platform 的探测 var 未导出，跨包不可注入）。探测接缝一律为 ctx 形态
// （G0：ctx 必须贯通到真实 exec.CommandContext；旧无 ctx 入口以
// context.Background() 包装，语义不变）；wslUserHomeFn/wslToUNCFn 仅服务
// 旧 resolveWSLAgentTarget（cachedHome）路径，A2 的 Context 入口不再使用。
// wslStatModeFn 是敏感提交的隔离验证探测（SA-06，wsl-goal-closure）。
var (
	wslUserHomeFn              = platform.WSLUserHome
	wslToUNCFn                 = platform.WSLToUNC
	wslChmodFn                 = platform.WSLChmodContext
	wslStatModeFn              = platform.WSLFileModeContext
	wslTargetIdentityContextFn = platform.WSLTargetIdentityContext
)

// 宿主本地私有化接缝（G0）：生产指向 platform.EnsurePrivateDir/File
// （真实 0700/0600 语义，Windows 为 owner-only DACL）；测试可注入故障
// 形态（如目录不可写、私有化不可验证）。
var (
	hostEnsurePrivateDirFn  = platform.EnsurePrivateDir
	hostEnsurePrivateFileFn = platform.EnsurePrivateFile
)

// wslAgentConfigMu 串行化 WSL 侧 merge→write→chmod 全序列（diting m1）：
// 同一进程内并发启动两个会话（如同 distro 的不同 provider）时，无锁的
// merge（读旧）→ write（覆盖新）会互相丢失对方刚写入的 provider 条目。
// 锁住整个序列保证 read-modify-write 原子性；Windows 侧非 WSL 路径的
// 同类竞态为预存行为，不在本切片扩大范围。
var wslAgentConfigMu sync.Mutex

// WSLAgentTarget 描述一个 WSL distro 内的 agent 目录落点。
type WSLAgentTarget struct {
	Distro    string // wsl.exe -d 目标（如 Ubuntu）
	Home      string // distro 默认用户 $HOME（Linux 路径，如 /home/u）
	LinuxDir  string // agent 根（Linux 路径，如 /home/u/.pi/agent）
	UNCDir    string // agent 根（Windows UNC，如 \\wsl.localhost\Ubuntu\home\u\.pi\agent）
	UID       string // distro 默认用户 uid（A2 目标身份复核用；同步时必须与当次探测一致）
	MachineID string // /etc/machine-id（可为空＝发行版无此文件；非空时必须与当次探测一致）
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

// wslStagingDirName 是 WSL 侧私有 staging 目录名（位于 agent 根下）。
const wslStagingDirName = ".amagi-staging"

// wslEnsurePrivateStagingContext 建立/验证 agent 根下的私有 staging 目录：
// MkdirAll → distro 内 chmod 700 → StatMode 验证（UNC/宿主 stat 的 9P 翻译
// 不可信），每个同步 I/O 后检查 ctx。验证不可用或不等于 700 都拒绝
// （SA-06：隔离未验证 = 未隔离，fail closed——任何秘密字节都不得写入未验证
// 隔离的目录）。返回 UNC 与 Linux 路径。
func wslEnsurePrivateStagingContext(ctx context.Context, target WSLAgentTarget) (uncDir, linuxDir string, err error) {
	uncDir = filepath.Join(target.UNCDir, wslStagingDirName)
	linuxDir = target.LinuxDir + "/" + wslStagingDirName
	if err := os.MkdirAll(uncDir, 0o700); err != nil {
		return "", "", fmt.Errorf("mkdir wsl staging dir: %w", err)
	}
	if cerr := ctx.Err(); cerr != nil {
		return "", "", fmt.Errorf("canceled after mkdir wsl staging dir: %w", cerr)
	}
	if err := wslChmodFn(ctx, target.Distro, "700", linuxDir); err != nil {
		return "", "", fmt.Errorf("chmod 700 wsl staging dir in distro: %w", err)
	}
	if cerr := ctx.Err(); cerr != nil {
		return "", "", fmt.Errorf("canceled after chmod 700 wsl staging dir: %w", cerr)
	}
	mode, ok := wslStatModeFn(ctx, target.Distro, linuxDir)
	if cerr := ctx.Err(); cerr != nil {
		return "", "", fmt.Errorf("canceled after staging isolation probe: %w", cerr)
	}
	if !ok || mode != "700" {
		return "", "", fmt.Errorf("wsl staging isolation unverified: mode=%q ok=%v (expected 700); refusing to write secrets", mode, ok)
	}
	return uncDir, linuxDir, nil
}

// wslEnsurePrivateStaging（旧无 ctx 入口）以 Background 包装 ctx 核心。
func wslEnsurePrivateStaging(target WSLAgentTarget) (uncDir, linuxDir string, err error) {
	return wslEnsurePrivateStagingContext(context.Background(), target)
}

// writeWSLAgentFileStaged 是 WSL 侧敏感配置文件的提交原语（SA-06/07，
// wsl-goal-closure）：
//  1. agent 根先在 distro 内收紧 0700（失败即中止，未写任何字节）；
//  2. 秘密字节只写入已验证 0700 的 .amagi-staging 内唯一临时名；
//  3. distro 内 chmod 600 并验证后才 rename 到最终路径——最终目录即使
//     0755，文件也从未以宽权限可见（旧实现 rename 后才 chmod，9P 临时段
//     是 0644，存在泄漏窗口）；
//  4. 任何阶段失败都发生在 rename 之前：既有文件（含用户自有 provider 等
//     混合内容）原样保留，不再以删整文件充当 fail-closed（SA-07）。
//
// writeWSLAgentFileStagedContext 是 WSL 侧敏感配置文件的 ctx 提交原语
// （SA-06/07，wsl-goal-closure；G0 攻坚把 ctx 贯通到实际 rename 前）：
//  1. agent 根先在 distro 内收紧 0700（失败即中止，未写任何字节）；
//  2. 秘密字节只写入已验证 0700 的 .amagi-staging 内唯一临时名；
//  3. distro 内 chmod 600 并验证后才允许提交；
//  4. G0：rename 前读回候选字节比对（持续污染/半写污染不得进入 rename，
//     原目标内容不变）+ 最后一次 ctx 检查（取消时未提交侧原样，仅清理
//     本次 staging 临时文件）；
//  5. rename 后读回最终路径字节复验；
//  6. 任何阶段失败都发生在 rename 之前（除 rename 自身失败，该错误如实
//     上抛，由调用方重新分类落盘现状）：既有文件（含用户自有 provider 等
//     混合内容）原样保留，不再以删整文件充当 fail-closed（SA-07）。
func writeWSLAgentFileStagedContext(ctx context.Context, target WSLAgentTarget, fileName string, content []byte) error {
	if err := os.MkdirAll(target.UNCDir, 0o700); err != nil {
		return fmt.Errorf("mkdir wsl agent dir: %w", err)
	}
	if cerr := ctx.Err(); cerr != nil {
		return fmt.Errorf("canceled before tightening wsl agent dir: %w", cerr)
	}
	if err := wslChmodFn(ctx, target.Distro, "700", target.LinuxDir); err != nil {
		return fmt.Errorf("chmod 700 wsl agent dir in distro: %w", err)
	}
	if cerr := ctx.Err(); cerr != nil {
		return fmt.Errorf("canceled after chmod 700 wsl agent dir: %w", cerr)
	}
	stagingUNC, stagingLinux, err := wslEnsurePrivateStagingContext(ctx, target)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(stagingUNC, ".amagi-agent-*")
	if err != nil {
		return fmt.Errorf("create wsl staging file: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() {
		if _, statErr := os.Stat(tmpPath); statErr == nil {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write wsl staging file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close wsl staging file: %w", err)
	}
	if cerr := ctx.Err(); cerr != nil {
		return fmt.Errorf("canceled after writing wsl staging file: %w", cerr)
	}
	_ = os.Chmod(tmpPath, 0o600) // 宿主侧尽力；权威位由 distro 内 chmod 落实
	tmpLinux := stagingLinux + "/" + filepath.Base(tmpPath)
	if err := wslChmodFn(ctx, target.Distro, "600", tmpLinux); err != nil {
		return fmt.Errorf("chmod 600 wsl staging file in distro (target untouched): %w", err)
	}
	if cerr := ctx.Err(); cerr != nil {
		return fmt.Errorf("canceled after chmod 600 wsl staging file: %w", cerr)
	}
	if mode, ok := wslStatModeFn(ctx, target.Distro, tmpLinux); !ok || mode != "600" {
		return fmt.Errorf("verify wsl staging file mode: got %q ok=%v (expected 600; target untouched, no secret bytes committed)", mode, ok)
	}
	// G0：候选字节读回预验——staging 写入面持续污染时（半写/字节翻转），
	// 污染字节不得进入 rename，原 auth/config 原样保留。
	candidate, rerr := os.ReadFile(tmpPath)
	if rerr != nil {
		return fmt.Errorf("read back wsl staging candidate: %w", rerr)
	}
	if !bytes.Equal(candidate, content) {
		return fmt.Errorf("wsl staging candidate bytes mismatch after write (target untouched, no secret bytes committed)")
	}
	// 实际 rename 前最后一次 ctx 检查：取消时未提交侧原样。
	if cerr := ctx.Err(); cerr != nil {
		return fmt.Errorf("canceled before committing wsl agent file rename: %w", cerr)
	}
	finalUNC := filepath.Join(target.UNCDir, fileName)
	if err := os.Rename(tmpPath, finalUNC); err != nil {
		return fmt.Errorf("commit wsl agent file: %w", err)
	}
	got, err := os.ReadFile(finalUNC)
	if err != nil || !bytes.Equal(got, content) {
		return fmt.Errorf("verify wsl agent file after commit: read-back mismatch (err=%v)", err)
	}
	return nil
}

// writeWSLAgentFileStaged（旧无 ctx 入口）以 Background 包装 ctx 核心，供
// pi/omp/opencode 既有 API 复用同一提交原语。
func writeWSLAgentFileStaged(target WSLAgentTarget, fileName string, content []byte) error {
	return writeWSLAgentFileStagedContext(context.Background(), target, fileName, content)
}

// wslRetighten600Context 复核字节未变化的秘密文件权限（零写入路径的
// SA-06 补丁：已存在但权限被改宽也要安全修复或阻断；PR02：探测不可用
// （ok=false）不再视为安全——unknown 不等于安全，先收紧后复验，仍不可证
// 则返回错误交由调用方阻断消费）：探测到确切非 600 或探测不可用时统一
// 重收紧并复验。探测/收紧均经 ctx 接缝（取消时按未验证处理）。
func wslRetighten600Context(ctx context.Context, distro, linuxFile string) error {
	mode, ok := wslStatModeFn(ctx, distro, linuxFile)
	if cerr := ctx.Err(); cerr != nil {
		return fmt.Errorf("canceled during permission re-check: %w", cerr)
	}
	if ok && mode == "600" {
		return nil // 健康路径：零字节重写保持
	}
	if err := wslChmodFn(ctx, distro, "600", linuxFile); err != nil {
		return fmt.Errorf("re-tighten wsl secret file (%s, was mode=%q ok=%v): %w", linuxFile, mode, ok, err)
	}
	if cerr := ctx.Err(); cerr != nil {
		return fmt.Errorf("canceled after re-tighten chmod: %w", cerr)
	}
	if m2, ok2 := wslStatModeFn(ctx, distro, linuxFile); !ok2 || m2 != "600" {
		return fmt.Errorf("re-tighten wsl secret file (%s) unverified: mode=%q ok=%v", linuxFile, m2, ok2)
	}
	return nil
}

// wslRetighten600（旧无 ctx 入口）以 Background 包装 ctx 核心，供
// pi/omp/opencode 零写入路径复用。
func wslRetighten600(distro, linuxFile string) error {
	return wslRetighten600Context(context.Background(), distro, linuxFile)
}

// WriteWSLPiAgentConfig 把 pi models.json 合并写入 WSL 侧 agent root。
// 合并语义与 Windows 侧一致（MergePiAgentConfig：保留已有 providers 与
// 顶层字段，amagi-<name> 当次条目优先），原子写（WritePiAgentConfig 的
// tmp+Rename 范式在 UNC 上同样成立）后经 WSLChmod 补偿 0700/0600。
// 成功返回 WSL 内 Linux 路径的 agent 目录（供日志/诊断）。
//
// P3-S1 零写入路径：合并结果与既有文件字节一致时跳过落盘；但已存在副本
// 的权限被改宽时也要安全修复或阻断（SA-06：无变化路径不得绕过权限检查）。
// 敏感提交走 SA-06 staging 原语：0600 在 rename 前落位，chmod 失败发生在
// 任何既有混合内容被替换之前（不再删整文件，SA-07）。
func WriteWSLPiAgentConfig(distro string, cfg map[string]any) (string, error) {
	target, err := resolveWSLAgentTarget(distro, ".pi/agent")
	if err != nil {
		return "", err
	}
	wslAgentConfigMu.Lock()
	defer wslAgentConfigMu.Unlock()
	merged := MergePiAgentConfig(cfg, target.UNCDir)
	content, mErr := json.MarshalIndent(merged, "", "  ")
	if mErr != nil {
		return "", fmt.Errorf("marshal pi agent config: %w", mErr)
	}
	content = append(content, '\n')
	if existing, rErr := os.ReadFile(filepath.Join(target.UNCDir, "models.json")); rErr == nil && bytes.Equal(existing, content) {
		if err := wslRetighten600(target.Distro, target.LinuxDir+"/models.json"); err != nil {
			return "", err
		}
		return target.LinuxDir, nil // 零写入：内容一致且权限安全
	}
	if err := writeWSLAgentFileStaged(target, "models.json", content); err != nil {
		return "", err
	}
	return target.LinuxDir, nil
}

// WriteWSLOmpAgentConfig 是 WriteWSLPiAgentConfig 的 omp 对称实现
// （.omp/agent/models.yml，YAML 序列化与合并沿用 omp_config.go；提交走同一
// SA-06 staging 原语）。
func WriteWSLOmpAgentConfig(distro string, cfg map[string]any) (string, error) {
	target, err := resolveWSLAgentTarget(distro, ".omp/agent")
	if err != nil {
		return "", err
	}
	wslAgentConfigMu.Lock()
	defer wslAgentConfigMu.Unlock()
	merged := MergeOmpModelsConfig(cfg, target.UNCDir)
	content, mErr := yaml.Marshal(merged)
	if mErr != nil {
		return "", fmt.Errorf("marshal omp models.yml: %w", mErr)
	}
	if err := writeWSLAgentFileStaged(target, "models.yml", content); err != nil {
		return "", err
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
// WSLENV 清单中也不会再出现对应名字）。P4-A2 起有 per-key 放行变体
// StripWSLHostPathEnvWithAllow：显式标记的键豁免剥离并改走 /p 路径翻译。
func StripWSLHostPathEnv(env []string, extraForwardKeys ...string) ([]string, []string) {
	kept, stripped, _ := StripWSLHostPathEnvWithAllow(env, extraForwardKeys, nil)
	return kept, stripped
}

// StripWSLHostPathEnvWithAllow 是 StripWSLHostPathEnv 的 per-key 放行扩展
// （P4-A2 路径值放行门，主上已批）：allowPathKeys 中的键（envvars.json
// allowPathValue 标记，大小写不敏感；保留键除外）即便值为 Windows 盘符
// 路径也不再剥离——用户已显式同意该路径值跨界可见，转发侧
// （appendWSLENVForwardingWithPaths）会为其加 WSLENV /p 路径翻译标志
// （C:\... → /mnt/c/...）。第三个返回值是实际被豁免的键（在转发面 +
// 值为盘符路径 + 已标记），供调用方记日志「per-key 放行+路径翻译」；
// 未标记键的剥离行为与 StripWSLHostPathEnv 完全一致（默认防泄漏基线
// 不变）。保留键不适用放行门：它们不在转发面，既不会被剥离也谈不上豁免。
func StripWSLHostPathEnvWithAllow(env []string, extraForwardKeys []string, allowPathKeys []string) ([]string, []string, []string) {
	allow := make(map[string]struct{}, len(allowPathKeys))
	for _, k := range allowPathKeys {
		trimmed := strings.TrimSpace(k)
		if trimmed == "" || platform.IsWSLENVReservedKey(trimmed) {
			continue
		}
		allow[strings.ToUpper(trimmed)] = struct{}{}
	}
	out := make([]string, 0, len(env))
	stripped := []string{}
	allowed := []string{}
	for _, kv := range env {
		key, value := splitEnvKV(kv)
		if key != "" && platform.ShouldForwardToWSL(key, extraForwardKeys...) && isWindowsDrivePath(strings.TrimSpace(value)) {
			if _, ok := allow[strings.ToUpper(key)]; ok {
				allowed = append(allowed, key)
				out = append(out, kv)
				continue
			}
			stripped = append(stripped, key)
			continue
		}
		out = append(out, kv)
	}
	return out, stripped, allowed
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
// 原子写（SA-06 staging 原语：0700 staging 验证 + rename 前 0600 收紧，
// 9P 不透传 POSIX 位的问题在秘密字节落盘前解决）；零写入路径同样复核
// 权限（改宽则安全修复或阻断，SA-06）。
func WriteWSLOpenCodeAgentConfig(distro string, cfg map[string]any) (string, error) {
	target, err := resolveWSLAgentTarget(distro, ".config/opencode")
	if err != nil {
		return "", err
	}
	wslAgentConfigMu.Lock()
	defer wslAgentConfigMu.Unlock()
	b, mErr := json.MarshalIndent(cfg, "", "  ")
	if mErr != nil {
		return "", fmt.Errorf("marshal opencode config: %w", mErr)
	}
	b = append(b, '\n')
	linuxFile := target.LinuxDir + "/" + openCodeWSLConfigFile
	if existing, rErr := os.ReadFile(filepath.Join(target.UNCDir, openCodeWSLConfigFile)); rErr == nil && bytes.Equal(existing, b) {
		// 零写入：内容一致，但已存在副本权限被改宽时也要安全修复或阻断。
		if err := wslRetighten600(target.Distro, linuxFile); err != nil {
			return "", err
		}
		return linuxFile, nil
	}
	if err := writeWSLAgentFileStaged(target, openCodeWSLConfigFile, b); err != nil {
		return "", err
	}
	return linuxFile, nil
}
