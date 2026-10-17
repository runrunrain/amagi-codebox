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
// G0 攻坚（wsl-goal-closure attack-contract PR02/PR07）后的结构约束：
//   - 算法主体只产生 provisional result；全部可消费出口
//     （Copied/Fresh/DistroNewer/DistroTieKept/DistroHasCopy）统一经
//     finishA2Consumption 消费门收口——成功回写 Windows 不再绕过权限门；
//   - 门语义：ctx 取消一律 NoAuth（不按「目标存在」降为 HasCopy）；distro
//     侧 auth 必须可读常规文件；权限必须可证 600（unknown/宽权限先收紧后
//     复验，仍不可证 → NoAuth）；同步原因与权限原因 errors.Join 并存；
//   - Context 入口 SyncWSLPiLoginAuthContext 使用已解析 target（不重新用
//     cachedHome 选目标），并用当次真实身份探测核对传入 UID/Home/MachineID；
//     旧 SyncWSLPiLoginAuth(distro) 保留为兼容包装（同样过最终消费门）；
//   - ctx 贯通 identity/mtime/stat/chmod 真实 runner 与两个方向 writer 的
//     实际 rename 前；每个同步 I/O 后复查；已经发生的写入只记事实（提交后
//     取消如实 NoAuth，不回滚新 token）；取消前未提交侧原样；
//   - A2 不做普通旧 token 备份回滚链，不往状态/日志落 token 或 auth hash。
//
// 通道选型（写路径选 UNC 直写，与 WriteWSLPiAgentConfig 同族）：
// resolveWSLAgentTarget → UNC 路径上 MkdirAll → 唯一临时名 tmp+Rename 原子写
// （P3-S0 提交原语，并发供给不撞名）→ WSLChmod 600 补偿（Windows os.Chmod
// 经 9P 不落 POSIX mode 位，见 wsl_agent_config.go 证据）。distro 侧 mtime 经
// platform.WSLFileMTime（wsl.exe 内 stat -c %Y，ext4 时间戳权威读法）。
// 失败语义（调用方闸门消费）：同步失败不必然阻断启动——distro 侧已有
// （哪怕是陈旧的）auth.json 副本且权限可证 600 时降级可用；两侧都无凭据
// （或 distro 侧状态不可探明）时 fail closed，由调用方以中文报错拒绝启动；
// 宽松权限副本不可证 600 同样 fail closed（m-3，不得按降级放行）。
// Windows 侧 auth.json 缺失返回 ErrWSLPiLoginSourceMissing（登录失效语义，
// 维持 RequirePiLogin 的关闭路径，不复制任何东西）。
//
// 保密约束：auth.json 含 OAuth token，本文件只按字节读取/比对/复制，
// 永不解码字段、永不把内容写进日志或错误信息。

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	// SyncWSLPiLoginAuthDistroHasCopy：同步失败，但 distro 侧已有权限可证
	// 600 的 auth.json（可能是旧凭据）——调用方可降级放行并记 WARN。
	// G0 起 HasCopy 由统一消费门产生：副本存在但权限不可证时不再是 HasCopy。
	SyncWSLPiLoginAuthDistroHasCopy
	// SyncWSLPiLoginAuthDistroNoAuth：同步失败，且 distro 侧没有（或无法
	// 确认有 / 权限不可证 600 的）auth.json——启动后 Pi 必然无法认证或副本
	// 不安全，调用方应报错拒绝。
	SyncWSLPiLoginAuthDistroNoAuth
)

// authMTimeTolerance 是双侧 mtime 的新陈判定容差：文件系统时间戳精度
// （FAT 系 2 秒）与 9P/DrvFS 的时钟呈现差异内视为并列，按不可判处理
// （保守取 distro）。
const authMTimeTolerance = 2 * time.Second

// wslStatMTimeFn 是同步链的注入接缝（单测替换；生产指向 platform 的 ctx
// 变体——真实 exec.CommandContext，取消即停止后续探测）。distro 侧 mtime
// 经 wsl.exe 内 stat 取 ext4 权威值；staging 提交原语与权限验证接缝
// （wslChmodFn/wslStatModeFn）在 wsl_agent_config.go 统一声明。
var wslStatMTimeFn = platform.WSLFileMTimeContext

// errWSLAuthLooseLeftover 标记 m-3/PR02 语义：即将被消费（或降级消费）的
// distro 侧凭据副本权限无法证明 600——fail closed，不得降级放行宽松权限
// 凭据。G0 起由统一消费门产生（errors.Join 链中保留）。
var errWSLAuthLooseLeftover = errors.New("wsl pi auth.json left with unprovable permissions")

// a2CtxStepErr 在每个同步 I/O 之后复查 ctx：已取消时返回携带阶段说明的
// 错误（调用方一律按 NoAuth/失败处理，不开展后继业务）。
func a2CtxStepErr(ctx context.Context, step string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("a2 %s: %w", step, err)
	}
	return nil
}

// verifyA2Target 核对传入 target 的完整性与当前身份（G0 A2 必闭合 #2）：
// 不重新用 cachedHome 选目标——Home/LinuxDir/UNC 均来自已解析 target，这里
// 只做结构一致性检查，并以当次真实身份探测核对 UID/Home/MachineID（同名
// distro 重装/换默认用户/换 Home 时旧 target 不得继续作为同步目标）。
func verifyA2Target(ctx context.Context, target WSLAgentTarget) error {
	distro := strings.TrimSpace(target.Distro)
	if distro == "" {
		return fmt.Errorf("a2 target: distro is required")
	}
	if target.UID == "" || !strings.HasPrefix(target.Home, "/") {
		return fmt.Errorf("a2 target incomplete for distro %q: uid=%q home=%q (caller must pass a resolved target)", distro, target.UID, target.Home)
	}
	wantLinuxDir := strings.TrimRight(target.Home, "/") + "/.pi/agent"
	if target.LinuxDir != wantLinuxDir {
		return fmt.Errorf("a2 target linux dir %q does not match home-relative pi agent dir %q", target.LinuxDir, wantLinuxDir)
	}
	if mapped := wslToUNCFn(distro, target.LinuxDir); mapped == "" || mapped != target.UNCDir {
		return fmt.Errorf("a2 target UNC dir mismatch for distro %q (mapping=%q)", distro, mapped)
	}
	identity := wslTargetIdentityContextFn(ctx, distro)
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("a2 identity probe canceled: %w", err)
	}
	if !identity.OK {
		return fmt.Errorf("a2 identity probe failed for distro %q (uid/home unresolvable)", distro)
	}
	if identity.UID != target.UID || identity.Home != target.Home || identity.MachineID != target.MachineID {
		return fmt.Errorf("a2 target identity mismatch for distro %q: uid-changed=%v home-changed=%v machine-id-changed=%v (resolve the target again)",
			distro, identity.UID != target.UID, identity.Home != target.Home, identity.MachineID != target.MachineID)
	}
	return nil
}

// finishA2Consumption 是全部可消费出口的统一 finish 消费门（G0/PR02）：
// Copied/Fresh/DistroNewer/DistroTieKept/DistroHasCopy 的 provisional 结果
// 必须经本门放行；成功回写 Windows 后同样不得跳过。门检查顺序：
// ctx → distro auth 可读常规文件 → 权限可证 600（unknown/宽权限先收紧后
// 复验）。committedNote 非空时表示门检查前已有方向性实际写入（新 token
// 已落位），门失败时在错误链中如实报告且绝不回滚新 token；cause 是本次
// 同步的原始原因（可为 nil），与权限原因 errors.Join 并存、互不吞并。
// 门失败与取消一律返回 NoAuth——不得按「目标存在」降为 HasCopy。
func finishA2Consumption(ctx context.Context, target WSLAgentTarget, outcome SyncWSLPiLoginAuthOutcome, cause error, committedNote string) (SyncWSLPiLoginAuthOutcome, error) {
	linuxAuth := target.LinuxDir + "/auth.json"
	uncAuth := filepath.Join(target.UNCDir, "auth.json")
	joinCauses := func(gateErr error) (SyncWSLPiLoginAuthOutcome, error) {
		errs := make([]error, 0, 3)
		if cause != nil {
			errs = append(errs, cause)
		}
		if committedNote != "" {
			errs = append(errs, fmt.Errorf("a2 %s (new token preserved, not rolled back)", committedNote))
		}
		errs = append(errs, gateErr)
		return SyncWSLPiLoginAuthDistroNoAuth, errors.Join(errs...)
	}
	if err := ctx.Err(); err != nil {
		return joinCauses(fmt.Errorf("a2 consumption gate canceled: %w", err))
	}
	info, statErr := os.Lstat(uncAuth)
	if statErr != nil {
		return joinCauses(fmt.Errorf("a2 consumption gate: distro auth not present: %w", statErr))
	}
	if !info.Mode().IsRegular() {
		return joinCauses(fmt.Errorf("a2 consumption gate: distro auth is not a regular file"))
	}
	if _, readErr := os.ReadFile(uncAuth); readErr != nil {
		return joinCauses(fmt.Errorf("a2 consumption gate: distro auth unreadable: %w", readErr))
	}
	// 权限门核心：健康 600 零重写；unknown/宽权限收紧后复验；仍不可证 →
	// NoAuth（errWSLAuthLooseLeftover 语义，以 %w 保留底层原因——含取消）。
	if err := wslRetighten600Context(ctx, target.Distro, linuxAuth); err != nil {
		return joinCauses(fmt.Errorf("%w: %w", errWSLAuthLooseLeftover, err))
	}
	if err := ctx.Err(); err != nil {
		return joinCauses(fmt.Errorf("a2 consumption gate canceled after permission verification: %w", err))
	}
	return outcome, cause
}

// SyncWSLPiLoginAuthContext 以已解析 target 惰性同步两侧
// ~/.pi/agent/auth.json（M-1 双向新陈判定 + G0 统一消费门）。成功
// （Copied/Fresh/DistroNewer/DistroTieKept）时 err 为 nil 且已过权限门；
// 失败时 outcome 给出降级判定依据（DistroHasCopy/DistroNoAuth），err 携带
// 原始失败原因（不含凭据内容）。ctx 取消/超时一律 NoAuth；已经发生的写入
// 在错误链中如实报告方向（不回滚新 token）。内容比对 SHA-256；新陈比对
// mtime（Windows 侧本机 stat，distro 侧 wsl.exe 内 stat -c %Y）。
func SyncWSLPiLoginAuthContext(ctx context.Context, target WSLAgentTarget) (SyncWSLPiLoginAuthOutcome, error) {
	if err := verifyA2Target(ctx, target); err != nil {
		return SyncWSLPiLoginAuthDistroNoAuth, err
	}
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

	uncAuth := filepath.Join(target.UNCDir, "auth.json")
	dstData, readErr := os.ReadFile(uncAuth)
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		// 读不出来的故障形态（权限/共享断开）：无法确认 distro 侧状态。
		return SyncWSLPiLoginAuthDistroNoAuth, fmt.Errorf("read distro pi auth.json: %w", readErr)
	}
	if readErr == nil && sha256.Sum256(dstData) == sha256.Sum256(srcData) {
		// Fresh（零写入路径）：同样过统一消费门（PR02：成功/无写路径不得
		// 绕过权限复核；健康 600 保持零字节重写）。
		return finishA2Consumption(ctx, target, SyncWSLPiLoginAuthFresh, nil, "")
	}

	if readErr != nil {
		// distro 侧缺失（首次播种）：无新陈可言，前向复制。
		if werr := writeWSLAuthAtomicContext(ctx, target, srcData); werr != nil {
			if cerr := a2CtxStepErr(ctx, "canceled after forward write failure"); cerr != nil {
				return SyncWSLPiLoginAuthDistroNoAuth, errors.Join(werr, cerr)
			}
			out, perr := classifyWriteFailure(uncAuth, werr)
			if out == SyncWSLPiLoginAuthDistroNoAuth {
				return out, perr
			}
			return finishA2Consumption(ctx, target, out, perr, "")
		}
		return finishA2Consumption(ctx, target, SyncWSLPiLoginAuthCopied, nil, "forward copy to distro committed")
	}

	// 两侧都在且内容不同：按 mtime 新陈判定，新者为准（M-1）。
	dstMTime, ok := wslStatMTimeFn(ctx, target.Distro, target.LinuxDir+"/auth.json")
	if cerr := a2CtxStepErr(ctx, "canceled during distro mtime probe"); cerr != nil {
		return SyncWSLPiLoginAuthDistroNoAuth, cerr
	}
	if !ok {
		// distro 侧时间戳不可判：不动任何一侧（两侧副本各自保全），按
		// 「distro 已有副本」provisional 降级，经统一消费门核权限后才能
		// 以 HasCopy 放行。
		return finishA2Consumption(ctx, target, SyncWSLPiLoginAuthDistroHasCopy, errors.New("probe distro pi auth.json mtime: unavailable"), "")
	}
	srcMTime := srcStat.ModTime()
	dstTime := time.Unix(dstMTime, 0)
	switch {
	case srcMTime.After(dstTime.Add(authMTimeTolerance)):
		// Windows 新 → 前向复制。
		if werr := writeWSLAuthAtomicContext(ctx, target, srcData); werr != nil {
			if cerr := a2CtxStepErr(ctx, "canceled after forward write failure"); cerr != nil {
				return SyncWSLPiLoginAuthDistroNoAuth, errors.Join(werr, cerr)
			}
			out, perr := classifyWriteFailure(uncAuth, werr)
			if out == SyncWSLPiLoginAuthDistroNoAuth {
				return out, perr
			}
			return finishA2Consumption(ctx, target, out, perr, "")
		}
		return finishA2Consumption(ctx, target, SyncWSLPiLoginAuthCopied, nil, "forward copy to distro committed")
	case dstTime.After(srcMTime.Add(authMTimeTolerance)):
		// distro 新（如 WSL 内 pi 轮转 token）→ 反向同步回 Windows 侧。
		if werr := writeWindowsAuthAtomicContext(ctx, srcDir, dstData); werr != nil {
			if cerr := a2CtxStepErr(ctx, "canceled after windows writeback failure"); cerr != nil {
				return SyncWSLPiLoginAuthDistroNoAuth, errors.Join(werr, cerr)
			}
			// 回写失败不碰 distro 侧（会话即将消费它），provisional 降级
			// 经门核权限（PR02：旧副本不可证 600 时不得降级放行）。
			return finishA2Consumption(ctx, target, SyncWSLPiLoginAuthDistroHasCopy, werr, "")
		}
		// 回写成功同样过门：成功出口不再绕过权限复核（PR02 根因）。
		return finishA2Consumption(ctx, target, SyncWSLPiLoginAuthDistroNewer, nil, "windows-side writeback committed")
	default:
		// 并列/容差内：保守取 distro（会话即将消费侧），回同步使两侧收敛。
		if werr := writeWindowsAuthAtomicContext(ctx, srcDir, dstData); werr != nil {
			if cerr := a2CtxStepErr(ctx, "canceled after windows writeback failure"); cerr != nil {
				return SyncWSLPiLoginAuthDistroNoAuth, errors.Join(werr, cerr)
			}
			return finishA2Consumption(ctx, target, SyncWSLPiLoginAuthDistroHasCopy, werr, "")
		}
		return finishA2Consumption(ctx, target, SyncWSLPiLoginAuthDistroTieKept, nil, "windows-side writeback committed")
	}
}

// SyncWSLPiLoginAuth 是旧入口的兼容包装：以 cachedHome 解析（resolveWSL-
// AgentTarget）+ 当次身份探测补齐 UID/MachineID 后调用 Context 入口，同样
// 经过统一消费门（不保留旧权限旁路）。生产协调器应使用 Context 入口传入
// 已解析 target；本包装仅供旧调用方与测试适配。
func SyncWSLPiLoginAuth(distro string) (SyncWSLPiLoginAuthOutcome, error) {
	target, err := resolveWSLAgentTarget(distro, ".pi/agent")
	if err != nil {
		// distro/home/UNC 任一不可解析 ⇒ distro 侧凭据状态不可探明，fail closed。
		return SyncWSLPiLoginAuthDistroNoAuth, err
	}
	identity := wslTargetIdentityContextFn(context.Background(), target.Distro)
	if !identity.OK {
		return SyncWSLPiLoginAuthDistroNoAuth, fmt.Errorf("probe WSL identity for distro %q: uid/home unresolvable", target.Distro)
	}
	if identity.Home != target.Home {
		return SyncWSLPiLoginAuthDistroNoAuth, fmt.Errorf("WSL home changed for distro %q: cached %q vs current %q", target.Distro, target.Home, identity.Home)
	}
	target.UID = identity.UID
	target.MachineID = identity.MachineID
	return SyncWSLPiLoginAuthContext(context.Background(), target)
}

// classifyWriteFailure 按落盘现状把前向写失败分类为 provisional 结果：
// tmp/rename 阶段失败时旧副本仍在（DistroHasCopy，待消费门核权限后才可
// 降级）；chmod 补偿失败已清理（stat 落空 → NoAuth）。G0 起 cancellation
// 不经本函数（取消一律在调用点直接 NoAuth，不得按「存在」转 HasCopy）。
func classifyWriteFailure(uncAuth string, err error) (SyncWSLPiLoginAuthOutcome, error) {
	if errors.Is(err, errWSLAuthLooseLeftover) {
		return SyncWSLPiLoginAuthDistroNoAuth, err
	}
	if _, statErr := os.Stat(uncAuth); statErr == nil {
		return SyncWSLPiLoginAuthDistroHasCopy, err
	}
	return SyncWSLPiLoginAuthDistroNoAuth, err
}

// writeWSLAuthAtomicContext 经提交原语把 auth 字节原子写入 distro 侧 agent
// root（SA-06，wsl-goal-closure；G0 加 ctx 与候选字节预验）：先建立并验证
// distro 内 0700 私有 staging，秘密字节只写入 staging 内唯一临时名，distro
// 内 chmod 600 并验证、候选字节读回比对后才 rename（持续污染时不替换旧
// auth）；实际 rename 前最后一次 ctx 检查（取消时未提交侧原样）。
// 本函数是 wslsupply A2Auth 适配器的底层写入。
func writeWSLAuthAtomicContext(ctx context.Context, target WSLAgentTarget, auth []byte) error {
	return writeWSLAgentFileStagedContext(ctx, target, "auth.json", auth)
}

// writeWindowsAuthAtomicContext 把 distro 侧新凭据原子写回 Windows 侧 agent
// root（M-1 反向同步；G0 加 ctx 与私有化验证）。写序列：EnsurePrivateDir
// （宿主真实 0700/ACL 语义）→ 唯一临时名 → 候选字节读回比对 → 实际 rename
// 前 ctx 检查 → rename（Windows 侧旧 auth 被新 token 替换是本方向的预期
// 语义；A2 不做普通旧 token 备份回滚链）→ EnsurePrivateFile 验证 0600/ACL。
func writeWindowsAuthAtomicContext(ctx context.Context, agentDir string, auth []byte) error {
	if err := hostEnsurePrivateDirFn(agentDir); err != nil {
		return fmt.Errorf("ensure private windows pi agent dir: %w", err)
	}
	if cerr := a2CtxStepErr(ctx, "canceled before staging windows auth"); cerr != nil {
		return cerr
	}
	tmp, err := os.CreateTemp(agentDir, ".amagi-auth-win-*")
	if err != nil {
		return fmt.Errorf("create windows auth staging file: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() {
		if _, statErr := os.Stat(tmpPath); statErr == nil {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.Write(auth); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write windows pi auth.json tmp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close windows pi auth.json tmp: %w", err)
	}
	if cerr := a2CtxStepErr(ctx, "canceled after writing windows auth staging"); cerr != nil {
		return cerr
	}
	// 候选字节读回预验（G0）：持续污染不得进入 rename，旧 auth 原样保留。
	got, rerr := os.ReadFile(tmpPath)
	if rerr != nil {
		return fmt.Errorf("read back windows auth staging candidate: %w", rerr)
	}
	if !bytes.Equal(got, auth) {
		return fmt.Errorf("windows auth staging candidate bytes mismatch (old windows auth untouched)")
	}
	// 实际 rename 前最后一次 ctx 检查。
	if cerr := a2CtxStepErr(ctx, "canceled before committing windows auth rename"); cerr != nil {
		return cerr
	}
	dst := filepath.Join(agentDir, "auth.json")
	if err := os.Rename(tmpPath, dst); err != nil {
		return fmt.Errorf("replace windows pi auth.json: %w", err)
	}
	if err := hostEnsurePrivateFileFn(dst); err != nil {
		return fmt.Errorf("verify private windows pi auth.json: %w", err)
	}
	return nil
}
