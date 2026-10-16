package launcher

// wsl_pi_login_sync.go — A2 凭据新鲜度：WSL Pi 登录会话的启动时惰性同步。
//
// 背景（主上裁定 2026-10-16）：Pi OAuth 登录预设原契约「WSL 场景拒绝，而不
// 是复制 token」被推翻——WSL 会话须能消费订阅登录模型。历史上首装播种
// （wslsetup ensurePiConfig，P3-S0 起已停用整树播种）只覆盖首次；此后两侧
// 副本都可能成为新者：Windows 侧 pi 刷新 token，或 WSL 会话内 pi 原生轮转
// token 写 distro 侧 auth.json（OAuth refresh token 轮转通常使旧 token 失效）。
//
// 同步方向（qreview M-1 裁定：双向新陈判定，新者为准）：比较两侧
// auth.json 的 SHA-256 与 mtime——内容一致 → Fresh；Windows 新 → 前向复制
// 进 distro；distro 新 → 反向同步回 Windows 侧（唯一有效凭据可能在任一侧，
// 单向覆盖会把 distro 侧轮转后的新凭据摧毁）；两侧都有、不同且 mtime
// 不可判（并列/容差内）→ 保守取 distro（会话即将消费侧）回同步；mtime
// 探测失败 → 不动任何一侧，按 DistroHasCopy 降级。仅同步 auth.json 单文件
// （不碰 models.json / sessions 等机器本地状态）。
//
// 通道选型（写路径选 UNC 直写，与 WriteWSLPiAgentConfig 同族）：
// resolveWSLAgentTarget → UNC 路径上 MkdirAll → 唯一临时名 tmp+Rename 原子写
// （P3-S0 提交原语，并发供给不撞名）→ WSLChmod 600 补偿（Windows os.Chmod
// 经 9P 不落 POSIX mode 位，见 wsl_agent_config.go 证据）。distro 侧 mtime 经
// platform.WSLFileMTime（wsl.exe 内 stat -c %Y，ext4 时间戳权威读法）。
// P3-S0 起桌面闸门与 remote/v1 effect 经 internal/wslsupply 协调器统一接入
// 本函数（A2Auth 策略适配器，判定语义零变化）；登录载荷内的 cmp/cp 同步
// 片段已退役，同一算法只此一份实现。
// 失败语义（调用方闸门消费）：同步失败不必然阻断启动——distro 侧已有
// （哪怕是陈旧的）auth.json 副本时降级可用；两侧都无凭据（或 distro 侧
// 状态不可探明）时 fail closed，由调用方以中文报错拒绝启动；chmod+清理
// 双失败留下的宽松权限副本同样 fail closed（m-3，不得按降级放行）。
// Windows 侧 auth.json 缺失返回 ErrWSLPiLoginSourceMissing（登录失效语义，
// 维持 RequirePiLogin 的关闭路径，不复制任何东西）。
//
// 保密约束：auth.json 含 OAuth token，本文件只按字节读取/比对/复制，
// 永不解码字段、永不把内容写进日志或错误信息。

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"amagi-codebox/internal/piconfig"
	"amagi-codebox/internal/platform"
)

// ErrWSLPiLoginSourceMissing 表示 Windows 侧 ~/.pi/agent/auth.json 不存在
// （未登录 / 已退出）。调用方应按「登录失效」处理，而不是当作同步故障。
var ErrWSLPiLoginSourceMissing = errors.New("windows-side pi auth.json is missing (not logged in)")

// SyncWSLPiLoginAuthOutcome 分类一次同步尝试的结果，供启动闸门决定
// 放行 / 降级（WARN） / 报错。
type SyncWSLPiLoginAuthOutcome int

const (
	// SyncWSLPiLoginAuthCopied：Windows 侧新于 distro 侧（或 distro 侧缺失
	// 首次播种），已前向复制进 distro。
	SyncWSLPiLoginAuthCopied SyncWSLPiLoginAuthOutcome = iota
	// SyncWSLPiLoginAuthFresh：两侧内容一致，无需复制。
	SyncWSLPiLoginAuthFresh
	// SyncWSLPiLoginAuthDistroNewer：distro 侧新于 Windows 侧（如 WSL 会话内
	// pi 轮转了 token），已反向同步回 Windows 侧（M-1）。
	SyncWSLPiLoginAuthDistroNewer
	// SyncWSLPiLoginAuthDistroTieKept：两侧都在且内容不同，但 mtime 并列/
	// 容差内不可判——保守取 distro（会话即将消费侧）并回同步（M-1）。
	SyncWSLPiLoginAuthDistroTieKept
	// SyncWSLPiLoginAuthDistroHasCopy：同步失败，但 distro 侧已有 auth.json
	// （可能是旧凭据）——调用方可降级放行并记 WARN。
	SyncWSLPiLoginAuthDistroHasCopy
	// SyncWSLPiLoginAuthDistroNoAuth：同步失败，且 distro 侧没有（或无法
	// 确认有）auth.json——启动后 Pi 必然无法认证，调用方应报错拒绝。
	SyncWSLPiLoginAuthDistroNoAuth
)

// authMTimeTolerance 是双侧 mtime 的新陈判定容差：文件系统时间戳精度
// （FAT 系 2 秒）与 9P/DrvFS 的时钟呈现差异内视为并列，按不可判处理
// （保守取 distro）。
const authMTimeTolerance = 2 * time.Second

// wslStatMTimeFn / wslAuthRemoveFn / wslAuthCreateTempFn 是同步链的注入接缝
// （单测替换；生产指向 platform 与 os）。distro 侧 mtime 经 wsl.exe 内 stat 取
// ext4 权威值；staging 文件创建接缝用于注入 rename 前写入故障（唯一临时名
// 后无法再用固定名碰撞注入）。
var (
	wslStatMTimeFn      = platform.WSLFileMTime
	wslAuthRemoveFn     = os.Remove
	wslAuthCreateTempFn = os.CreateTemp
)

// errWSLAuthLooseLeftover 标记 m-3 场景：chmod 600 补偿失败且清理也失败，
// distro 侧留下了非 600 权限的凭据副本——调用方必须 fail closed，不得按
// DistroHasCopy 降级放行宽松权限凭据。
var errWSLAuthLooseLeftover = errors.New("wsl pi auth.json left with loose permissions")

// SyncWSLPiLoginAuth 惰性同步两侧 ~/.pi/agent/auth.json（M-1：双向新陈
// 判定，新者为准）。成功（Copied/Fresh/DistroNewer/DistroTieKept）时 err
// 为 nil；失败时 outcome 给出降级判定依据（DistroHasCopy/DistroNoAuth），
// err 携带原始失败原因（不含凭据内容）。
//
// 内容比对 SHA-256；新陈比对 mtime（Windows 侧本机 stat，distro 侧
// wsl.exe 内 stat -c %Y）。并发安全：tmp+Rename 原子替换使并发调用收敛；
// 与 WriteWSLPiAgentConfig 操作的是不同文件（auth.json vs models.json），
// 无 merge 竞态。
func SyncWSLPiLoginAuth(distro string) (SyncWSLPiLoginAuthOutcome, error) {
	srcDir, err := piconfig.StandardAgentDir()
	if err != nil {
		return SyncWSLPiLoginAuthDistroNoAuth, fmt.Errorf("resolve windows pi agent dir: %w", err)
	}
	srcPath := filepath.Join(srcDir, "auth.json")
	srcData, err := os.ReadFile(srcPath)
	if errors.Is(err, os.ErrNotExist) {
		// Windows 侧未登录：登录失效语义，交由调用方走既有失效错误路径。
		return SyncWSLPiLoginAuthDistroNoAuth, ErrWSLPiLoginSourceMissing
	}
	if err != nil {
		return SyncWSLPiLoginAuthDistroNoAuth, fmt.Errorf("read windows pi auth.json: %w", err)
	}
	srcStat, err := os.Stat(srcPath)
	if err != nil {
		return SyncWSLPiLoginAuthDistroNoAuth, fmt.Errorf("stat windows pi auth.json: %w", err)
	}

	target, err := resolveWSLAgentTarget(distro, ".pi/agent")
	if err != nil {
		// distro/home/UNC 任一不可解析 ⇒ distro 侧凭据状态不可探明，fail closed。
		return SyncWSLPiLoginAuthDistroNoAuth, err
	}
	uncAuth := filepath.Join(target.UNCDir, "auth.json")

	dstData, readErr := os.ReadFile(uncAuth)
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		// 读不出来的故障形态（权限/共享断开）：无法确认 distro 侧状态。
		return SyncWSLPiLoginAuthDistroNoAuth, fmt.Errorf("read distro pi auth.json: %w", readErr)
	}
	if readErr == nil && sha256.Sum256(dstData) == sha256.Sum256(srcData) {
		return SyncWSLPiLoginAuthFresh, nil
	}

	if readErr != nil {
		// distro 侧缺失（首次播种）：无新陈可言，前向复制。
		if err := writeWSLAuthAtomic(target, srcData); err != nil {
			return classifyWriteFailure(uncAuth, err)
		}
		return SyncWSLPiLoginAuthCopied, nil
	}

	// 两侧都在且内容不同：按 mtime 新陈判定，新者为准（M-1）。
	dstMTime, ok := wslStatMTimeFn(target.Distro, target.LinuxDir+"/auth.json")
	if !ok {
		// distro 侧时间戳不可判：不动任何一侧（两侧副本各自保全），
		// 按「distro 已有副本」降级，会话消费 distro 现状。
		return SyncWSLPiLoginAuthDistroHasCopy, errors.New("probe distro pi auth.json mtime: unavailable")
	}
	srcMTime := srcStat.ModTime()
	dstTime := time.Unix(dstMTime, 0)
	switch {
	case srcMTime.After(dstTime.Add(authMTimeTolerance)):
		// Windows 新 → 前向复制。
		if err := writeWSLAuthAtomic(target, srcData); err != nil {
			return classifyWriteFailure(uncAuth, err)
		}
		return SyncWSLPiLoginAuthCopied, nil
	case dstTime.After(srcMTime.Add(authMTimeTolerance)):
		// distro 新（如 WSL 内 pi 轮转 token）→ 反向同步回 Windows 侧。
		if err := writeWindowsAuthAtomic(srcDir, dstData); err != nil {
			// 回写失败不碰 distro 侧（会话即将消费它），降级放行。
			return SyncWSLPiLoginAuthDistroHasCopy, err
		}
		return SyncWSLPiLoginAuthDistroNewer, nil
	default:
		// 并列/容差内：保守取 distro（会话即将消费侧），回同步使两侧收敛。
		if err := writeWindowsAuthAtomic(srcDir, dstData); err != nil {
			return SyncWSLPiLoginAuthDistroHasCopy, err
		}
		return SyncWSLPiLoginAuthDistroTieKept, nil
	}
}

// classifyWriteFailure 按落盘现状分类前向写失败：tmp/rename 阶段失败时旧
// 副本仍在（DistroHasCopy，降级可用）；chmod 补偿失败已清理（stat 落空 →
// NoAuth）；chmod+清理双失败（errWSLAuthLooseLeftover）留下宽松权限副本
// ——fail closed，绝不得按降级放行（m-3）。
func classifyWriteFailure(uncAuth string, err error) (SyncWSLPiLoginAuthOutcome, error) {
	if errors.Is(err, errWSLAuthLooseLeftover) {
		return SyncWSLPiLoginAuthDistroNoAuth, err
	}
	if _, statErr := os.Stat(uncAuth); statErr == nil {
		return SyncWSLPiLoginAuthDistroHasCopy, err
	}
	return SyncWSLPiLoginAuthDistroNoAuth, err
}

// writeWSLAuthAtomic 经 UNC 通道把 auth 字节原子写入 distro 侧 agent root：
// MkdirAll（首装播种缺失 / 目录被清理的场景）→ 唯一临时名 staging（P3-S0
// 提交原语：并发供给不撞名）→ 权限先收紧（0600）再 Rename → WSLChmod 600
// 补偿 POSIX 权限。chmod 补偿失败视为写入失败并清理已落盘内容（fail
// closed，与 WriteWSLPiAgentConfig 的 diting m2 契约一致：auth.json 不允许
// 以 0644 之类宽松权限留盘）。本函数是 wslsupply A2Auth 适配器的底层写入，
// A2 判定语义在 P3-S0 保持零变化（统一的是执行边界不是算法）。
func writeWSLAuthAtomic(target WSLAgentTarget, auth []byte) error {
	if err := os.MkdirAll(target.UNCDir, 0o700); err != nil {
		return fmt.Errorf("mkdir wsl pi agent dir: %w", err)
	}
	uncAuth := filepath.Join(target.UNCDir, "auth.json")
	tmp, err := wslAuthCreateTempFn(target.UNCDir, ".amagi-auth-*")
	if err != nil {
		return fmt.Errorf("create wsl auth staging file: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(auth); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath) // m-2：与 Rename 分支对称，不留部分凭据字节的 tmp 残留
		return fmt.Errorf("write wsl auth.json tmp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close wsl auth.json tmp: %w", err)
	}
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("chmod wsl auth.json tmp: %w", err)
	}
	if err := os.Rename(tmpPath, uncAuth); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("replace wsl auth.json: %w", err)
	}
	if err := wslChmodFn(target.Distro, "600", target.LinuxDir+"/auth.json"); err != nil {
		if rmErr := wslAuthRemoveFn(uncAuth); rmErr != nil && !os.IsNotExist(rmErr) {
			// m-3：chmod 失败且清理失败——宽松权限凭据留在盘上，必须 fail
			// closed（调用方不得按 DistroHasCopy 降级放行）。
			return fmt.Errorf("chmod 600 wsl pi auth.json: %v (cleanup failed: %v): %w", err, rmErr, errWSLAuthLooseLeftover)
		}
		return fmt.Errorf("chmod 600 wsl pi auth.json: %w", err)
	}
	return nil
}

// writeWindowsAuthAtomic 把 distro 侧新凭据原子写回 Windows 侧 agent root
// （M-1 反向同步）。写序列与 WritePiAgentConfig 同构（MkdirAll+Chmod 0700
// 目录 → 唯一临时名 0600 → Chmod tmp → Rename，P3-S0 起与其他供给提交原语
// 一致使用唯一临时名），chmod 语义对齐 Windows 侧产物。
func writeWindowsAuthAtomic(agentDir string, auth []byte) error {
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		return fmt.Errorf("mkdir windows pi agent dir: %w", err)
	}
	dst := filepath.Join(agentDir, "auth.json")
	tmp, err := os.CreateTemp(agentDir, ".amagi-auth-win-*")
	if err != nil {
		return fmt.Errorf("create windows auth staging file: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(auth); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write windows pi auth.json tmp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close windows pi auth.json tmp: %w", err)
	}
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("chmod windows pi auth.json tmp: %w", err)
	}
	if err := os.Rename(tmpPath, dst); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("replace windows pi auth.json: %w", err)
	}
	return nil
}
