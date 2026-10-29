package main

// claude_accounts_launch.go — Claude 订阅账号轮换的启动接线（design §6.3）。
//
// 职责：
//   - 池状态缓存（启动门禁判据）：absent（未启用，零行为变化）/ managed
//     （持有凭据 admission/lease）/ blocked（needs-reconcile、池损坏、启动
//     恢复未完成 → 受影响启动入口关闭）。
//   - 共享凭据 admission 的获取/复核/释放：认证读取与进程副作用之前取
//     admission，真正进程启动前复核 generation，成功转运行 lease。
//   - MutationGate / QuiescenceChecker 生产实现（注入 1a NewService）。
//   - 受管 Anthropic 登录的环境边界（design §6.3 R3）：最终 shell 后边界
//     清除 CLAUDE 身份/认证覆盖变量；canonical 身份路径不可证明时拒绝。
//
// 指纹 = canonical 路径标识 + activeGeneration，不含 token；活动 grant 刷新
// 不改变 activeGeneration（design §6.3）。

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"amagi-codebox/internal/claudeaccounts"
	"amagi-codebox/internal/launchplan"
	"amagi-codebox/internal/platform"
	"amagi-codebox/internal/remote"
)

// ---------------------------------------------------------------------------
// 池状态缓存与启动门禁
// ---------------------------------------------------------------------------

// claudeCredentialsLaunchPolicy 是受影响启动入口（Claude Code / 宿主根 Pi）
// 对账号池状态的门禁结论。
type claudeCredentialsLaunchPolicy int

const (
	// claudeCredentialsAbsent：未启用（无池）或平台不支持 → 不接 lease，全部
	// 启动路径零行为变化。
	claudeCredentialsAbsent claudeCredentialsLaunchPolicy = iota
	// claudeCredentialsManaged：池存在且无未结事务 → 启动持有凭据
	// admission/lease（不阻塞运行，只阻塞换号；design §6.3）。
	claudeCredentialsManaged
	// claudeCredentialsBlocked：needs-reconcile / 池损坏或锁定 / 启动恢复未
	// 完成 / enrollment 期间（design §4.1「暂停普通受影响启动」）→ 受影响
	// 启动入口关闭。
	claudeCredentialsBlocked
)

// claudeCredentialsPolicyName 返回策略的稳定标识（日志/测试用）。
func (p claudeCredentialsLaunchPolicy) String() string {
	switch p {
	case claudeCredentialsAbsent:
		return "absent"
	case claudeCredentialsManaged:
		return "managed"
	case claudeCredentialsBlocked:
		return "blocked"
	}
	return "unknown"
}

// claudeAccountsUnsupportedPlatform 报告当前平台是否无账号池后端（非 darwin
// cgo → UNSUPPORTED_PLATFORM，v1 macOS only；design §8）。
func (a *App) claudeAccountsUnsupportedPlatform() bool {
	return claudeaccounts.NewKeychainPoolStore().Kind() == "unsupported"
}

// claudeAccountsPoolMissingErr 返回「曾初始化的池已消失」的稳定错误（F10：
// 与从未创建区分，fail closed 不当未启用）。
func claudeAccountsPoolMissingErr() error {
	return claudeaccounts.NewErr(claudeaccounts.ReasonPoolMissing,
		"账号池曾被创建但现已缺失（可能被外部删除）；不会自动重建空池或覆盖原生登录，请到 Provider Center 恢复")
}

// claudeAccountsMarkerStatus 仅把 ENOENT 判为未启用，其他错误必须阻塞。
func (a *App) claudeAccountsMarkerStatus() (bool, error) {
	if a.claudeAccountsPoolMarkerPath == "" {
		return false, fmt.Errorf("池存在性标记路径不可用")
	}
	info, err := os.Stat(a.claudeAccountsPoolMarkerPath)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("池存在性标记不是普通文件")
	}
	return true, nil
}

// writeClaudeAccountsMarker 幂等写入「曾初始化」标记（池首次创建后调用；
// F10：用于区分「从未创建」与「已创建后被外部删除」）。
func (a *App) writeClaudeAccountsMarker() error {
	exists, err := a.claudeAccountsMarkerStatus()
	if err != nil || exists {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(a.claudeAccountsPoolMarkerPath), 0o700); err != nil {
		return err
	}
	// 内容无秘密。先同步文件再同步目录；失败不得宣布 managed。
	f, err := os.OpenFile(a.claudeAccountsPoolMarkerPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	_, writeErr := f.WriteString("claude-accounts-pool-initialized\n")
	syncErr := f.Sync()
	closeErr := f.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(a.claudeAccountsPoolMarkerPath))
	if err != nil {
		return err
	}
	return errors.Join(dir.Sync(), dir.Close())
}

// claudeCredentialsPolicy 汇总缓存状态得出启动门禁结论。
func (a *App) claudeCredentialsPolicy() claudeCredentialsLaunchPolicy {
	if a == nil || a.claudeAccounts == nil || a.sharedCoord == nil {
		return claudeCredentialsAbsent
	}
	if a.claudeAccountsUnsupported {
		return claudeCredentialsAbsent
	}
	a.claudeAccountsMu.RLock()
	defer a.claudeAccountsMu.RUnlock()
	if a.claudeAccountsLoadErr != nil {
		// 池被删除/损坏/拒绝访问：保持原生文件不动、停用轮换；受影响启动
		// 保守关闭（design §3.3：不能把未知状态当未启用）。
		return claudeCredentialsBlocked
	}
	if a.claudeAccountsPool == nil {
		return claudeCredentialsAbsent // 从未创建过池：正常未启用
	}
	if !a.claudeAccountsRecovered {
		return claudeCredentialsBlocked // 启动恢复未完成（design §4.5）
	}
	if a.claudeAccountsPool.Transaction != nil {
		// 未结 swap（needs-reconcile/恢复中）或 enrollment（暂停普通受影响
		// 启动，design §4.1）。
		return claudeCredentialsBlocked
	}
	return claudeCredentialsManaged
}

// claudeCredentialsBlockedReason 返回 blocked 状态的具体原因（不含敏感信息）。
func (a *App) claudeCredentialsBlockedReason() string {
	a.claudeAccountsMu.RLock()
	defer a.claudeAccountsMu.RUnlock()
	if a.claudeAccountsLoadErr != nil {
		if domain, ok := claudeaccounts.AsError(a.claudeAccountsLoadErr); ok {
			return "账号池不可用：" + string(domain.Reason)
		}
		return "账号池不可用"
	}
	if a.claudeAccountsPool == nil {
		return ""
	}
	if !a.claudeAccountsRecovered {
		return "启动恢复未完成"
	}
	if txn := a.claudeAccountsPool.Transaction; txn != nil {
		return "存在未结事务（" + string(txn.Kind) + "），需先完成恢复"
	}
	return ""
}

// errClaudeCredentialsLaunchBlocked 是受影响启动入口关闭时的稳定错误（前端
// 可按前缀识别；文案不含敏感信息）。
var errClaudeCredentialsLaunchBlocked = errors.New("NEEDS_RECONCILE: 账号池处于需恢复/未结事务状态，Claude/Pi 受影响启动暂不可用，请先在 Provider Center 完成账号恢复")

// errClaudeCredentialsGenerationChanged 是启动前代际变更的稳定错误：调用方
// 重新发起启动即重新规划（design §6.3）。
var errClaudeCredentialsGenerationChanged = errors.New("REVISION_CONFLICT: Claude 活动账号代际已变化，请重新发起启动")

// claudeCredentialsBlockedError 组装 blocked 错误（带具体原因）。
func (a *App) claudeCredentialsBlockedError() error {
	reason := a.claudeCredentialsBlockedReason()
	if reason == "" {
		return errClaudeCredentialsLaunchBlocked
	}
	return fmt.Errorf("%w（%s）", errClaudeCredentialsLaunchBlocked, reason)
}

// ---------------------------------------------------------------------------
// 指纹与 admission claim
// ---------------------------------------------------------------------------

// claudeCredentialsFingerprintFor 计算 canonical 凭据槽位组合 + activeGeneration
// 的指纹（design §6.3：canonical 路径标识 + activeGeneration，不含 token）。
func claudeCredentialsFingerprintFor(paths claudeaccounts.FileCredentialPaths, generation uint64) [32]byte {
	h := sha256.New()
	h.Write([]byte("claude-credentials-v1"))
	h.Write([]byte{0})
	h.Write([]byte(paths.ClaudeJSONPath))
	h.Write([]byte{0})
	h.Write([]byte(paths.PiAuthPath))
	h.Write([]byte{0})
	var gen [8]byte
	binary.BigEndian.PutUint64(gen[:], generation)
	h.Write(gen[:])
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

// claudeCredentialsFingerprint 以当前缓存池代际计算指纹。
func (a *App) claudeCredentialsFingerprint() ([32]byte, error) {
	paths, err := claudeaccounts.DefaultFileCredentialPaths()
	if err != nil {
		return [32]byte{}, fmt.Errorf("resolve canonical credential paths: %w", err)
	}
	a.claudeAccountsMu.RLock()
	var generation uint64
	if a.claudeAccountsPool != nil {
		generation = a.claudeAccountsPool.ActiveGeneration
	}
	a.claudeAccountsMu.RUnlock()
	return claudeCredentialsFingerprintFor(paths, generation), nil
}

// claudeCredentialsClaim 是一次启动持有的凭据 admission（pending → 成功后
// 由启动链原子提升为运行 lease）。
type claudeCredentialsClaim struct {
	admission   *remote.SharedLaunchAdmission
	fingerprint [32]byte
	released    bool
	// absent reservation 与 managed claim 共用 admission，但不改变身份环境。
	absent bool
}

// release 幂等释放未提升的 admission（提升成功后释放是 no-op，coordinator
// 已在提升时移除 pending claim）。
func (c *claudeCredentialsClaim) release(a *App) {
	if c == nil || c.released {
		return
	}
	c.released = true
	if a != nil && a.sharedCoord != nil {
		a.sharedCoord.ReleaseLaunchAdmission(c.admission)
	}
}

// beginClaudeCredentialsClaim 在认证读取/配置写入/进程副作用之前取得凭据
// admission（design §6.3）。无池也持有 reservation 直到启动/退出交接，但
// 不改身份环境；nil 仅表示无后端/平台不支持。blocked 返回稳定错误。
func (a *App) beginClaudeCredentialsClaim(why string) (*claudeCredentialsClaim, error) {
	if a == nil || a.claudeAccounts == nil || a.sharedCoord == nil || a.claudeAccountsUnsupported {
		return nil, nil
	}
	policy := a.claudeCredentialsPolicy()
	if policy == claudeCredentialsBlocked {
		return nil, a.claudeCredentialsBlockedError()
	}
	fingerprint, err := a.claudeCredentialsFingerprint()
	if err != nil {
		return nil, err
	}
	admission, err := a.sharedCoord.AcquireLaunchAdmissionForConfig(remote.SharedServiceClaudeCredentials, fingerprint)
	if err != nil {
		// swap/录入在飞（mutation admission 被持有）→ 竞态总有一方 busy
		//（design §9 Phase 3 验收），启动以稳定错误结束。
		return nil, fmt.Errorf("SESSIONS_ACTIVE: 共享凭据切换/录入进行中，请稍后重试（%w）", err)
	}
	// F01：取得 claim 后再次校验同一权威状态——mutation 可能在 policy 读取与
	// admission 获取之间完成（失败事务留 journal → blocked；代际通常不变，
	// 仅比代际不能发现）。mutation 在释放 admission 前已发布权威状态，此处
	// 重读必见最新结论；blocked 则释放 claim 拒绝启动，不放行混合认证。
	if current := a.claudeCredentialsPolicy(); current != policy {
		a.sharedCoord.ReleaseLaunchAdmission(admission)
		if current == claudeCredentialsBlocked {
			return nil, a.claudeCredentialsBlockedError()
		}
		return nil, errClaudeCredentialsGenerationChanged
	}
	if a.Log != nil {
		a.Log.Info("claude-accounts", "启动已取得共享凭据 admission", why)
	}
	return &claudeCredentialsClaim{admission: admission, fingerprint: fingerprint, absent: policy == claudeCredentialsAbsent}, nil
}

// recheckClaudeCredentialsClaim 在真正进程启动前复核 admission 指纹与当前池
// 代际一致（外部终端路径；embedded 路径的复核由 lease 提升时指纹重算承担）。
// F01：失败事务通常不增加代际，仅比指纹放不开混合认证——同时复核权威
// policy，blocked（needs-reconcile/journal 残留）一律拒绝。
func (a *App) recheckClaudeCredentialsClaim(claim *claudeCredentialsClaim) error {
	if claim == nil {
		return nil
	}
	if a.claudeCredentialsPolicy() == claudeCredentialsBlocked {
		return a.claudeCredentialsBlockedError()
	}
	fingerprint, err := a.claudeCredentialsFingerprint()
	if err != nil {
		return err
	}
	if fingerprint != claim.fingerprint {
		return errClaudeCredentialsGenerationChanged
	}
	return nil
}

// claudeCredentialsPlanCheck 是 remote adapter 的计划新鲜度接缝（design §6.3
// 「启动前代际变更 → 返回重新规划」）：计划内 admission 指纹必须与当前池
// 一致；blocked 状态下 remote create/restart 的受影响入口同样关闭。
func (a *App) claudeCredentialsPlanCheck(planFingerprint [32]byte) error {
	switch a.claudeCredentialsPolicy() {
	case claudeCredentialsAbsent:
		return nil
	case claudeCredentialsBlocked:
		return a.claudeCredentialsBlockedError()
	}
	fingerprint, err := a.claudeCredentialsFingerprint()
	if err != nil {
		return err
	}
	if fingerprint != planFingerprint {
		return errClaudeCredentialsGenerationChanged
	}
	return nil
}

// claudeCredentialsAbsenceCheck 是 remote adapter 的无凭据计划边界接缝（F01：
// absent 时构建的旧计划不含凭据 admission，首次 capture 后执行会绕过仅
// 存在于 admission 循环内的新鲜度检查）。计划未携带凭据 admission 时，
// 执行边界必须确认权威状态仍为 absent；managed/blocked → 拒绝执行。
func (a *App) claudeCredentialsAbsenceCheck() error {
	if a.claudeCredentialsPolicy() == claudeCredentialsAbsent {
		return nil
	}
	return errClaudeCredentialsGenerationChanged
}

// ---------------------------------------------------------------------------
// MutationGate / QuiescenceChecker 生产实现
// ---------------------------------------------------------------------------

// claudeCredentialsMutationGate 把共享协调器的凭据 mutation admission 包为
// 1a 服务的 MutationGate（design §2/§6.3：自动/手动/恢复同一门禁，不绕过
// lease）。Acquire 被拒 = 有 pending/run lease 或启动 admission 在飞。
type claudeCredentialsMutationGate struct{ app *App }

func (g claudeCredentialsMutationGate) Acquire(_ context.Context, _ string) (func(), error) {
	app := g.app
	if app == nil || app.sharedCoord == nil {
		return nil, remote.ErrSharedServiceInUse
	}
	admission, err := app.sharedCoord.AcquireMutationAdmission(remote.SharedServiceClaudeCredentials, remote.MutationStop)
	if err != nil {
		return nil, err
	}
	released := false
	return func() {
		if released {
			return
		}
		released = true
		// F01：在释放 mutation admission 之前发布权威 ready/blocked/generation。
		// 否则「Service 返回（可能已留 journal）→ 释放 admission → App 刷新缓存」
		// 之间存在交接窗口，桌面 launch/remote admission 检查可读到旧缓存放行
		// 半切换会话。注意：release 在 Service 持有串行锁期间破调，不能用
		// svc.LoadPool（非重入死锁），直接读 PoolStore。
		app.publishClaudeAccountsAuthoritativeState()
		app.sharedCoord.ReleaseMutationAdmission(admission)
	}, nil
}

// publishClaudeAccountsAuthoritativeState 直接从 PoolStore 重读并更新门禁
// 缓存（不经过 Service 串行锁；F01）。仅在「池已落盘、无并发写者」的边界
// 调用（mutation admission 仍持有时只可能是刚完成写入的 mutation）。
func (a *App) publishClaudeAccountsAuthoritativeState() {
	if a == nil || a.claudeAccountsPoolStore == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	a.refreshClaudeAccountsCache(ctx)
}

// claudeProcessSample 是进程观察器的一行样本（credential-free）。
type claudeProcessSample struct {
	PID  int
	PPID int
	Args string
}

// claudeProcessLister 枚举全系统进程（pid/ppid/args）；生产实现走 `ps`，
// 测试注入 fake 进程表（design §10.1：进程观察器测试注入 fake 进程表）。
type claudeProcessLister func() ([]claudeProcessSample, error)

// newProductionClaudeProcessLister 返回生产进程枚举器。v1 仅 macOS（账号池
// 平台门禁已排除其余平台）；ps 缺失/失败由调用方按 EXTERNAL_SESSION_UNCERTAIN
// 处理（design §4.3：扫描失败不自动切换）。
func newProductionClaudeProcessLister() claudeProcessLister {
	return func() ([]claudeProcessSample, error) {
		if runtime.GOOS != "darwin" {
			return nil, fmt.Errorf("process scan unsupported on %s", runtime.GOOS)
		}
		out, err := exec.Command("/bin/ps", "-axo", "pid=,ppid=,args=").Output()
		if err != nil {
			return nil, fmt.Errorf("ps: %w", err)
		}
		var samples []claudeProcessSample
		for _, line := range strings.Split(string(out), "\n") {
			fields := strings.Fields(strings.TrimSpace(line))
			if len(fields) < 3 {
				continue
			}
			pid, perr := strconv.Atoi(fields[0])
			ppid, pperr := strconv.Atoi(fields[1])
			if perr != nil || pperr != nil || pid <= 0 {
				continue
			}
			samples = append(samples, claudeProcessSample{PID: pid, PPID: ppid, Args: strings.Join(fields[2:], " ")})
		}
		return samples, nil
	}
}

// claudeCredentialProcessNames 是保守匹配的可执行名（含 .exe 变体；F12：
// 含官方 RPC 入口进程标题 pi-rpc——pi 安装源码 dist/rpc-entry.js 设置
// process.title = APP_NAME + '-rpc'，逐字段 basename 精确 pi/claude 会漏掉）。
var claudeCredentialProcessNames = map[string]bool{
	"claude": true, "claude.exe": true, // Claude Code
	"pi": true, "pi.exe": true, // Pi（canonical ~/.pi/agent）
	"pi-rpc": true, "pi-rpc.exe": true, // Pi 官方 RPC 入口（--mode rpc 改标题）
}

// claudeCredentialProcessArgFragments 是参数级路径片段（F12：node 直跑官方
// 包入口等启动形式可执行名不命中，按可验证包路径保守计入，宁可误报）。
var claudeCredentialProcessArgFragments = []string{
	"pi-coding-agent",
}

// claudeProcessArgsMatch 报告样本是否像 Claude Code / Pi 进程：可执行路径或
// 任一参数的 basename 精确命中，或任一字段含官方包路径片段（保守：宁可
// 误报阻塞切换，不漏报）。
func claudeProcessArgsMatch(args string) bool {
	fields := strings.Fields(args)
	if len(fields) == 0 {
		return false
	}
	for _, field := range fields {
		base := strings.TrimSuffix(filepath.Base(field), " ")
		if claudeCredentialProcessNames[strings.ToLower(base)] {
			return true
		}
		for _, fragment := range claudeCredentialProcessArgFragments {
			if strings.Contains(field, fragment) {
				return true
			}
		}
	}
	return false
}

// claudeProcessQuiescence 是 1a 服务的 QuiescenceChecker 生产实现（design
// §4.3/§6.3）：扫描 Claude Code/pi 进程；不可分类、不可探明所有者、存活
// 后代、扫描失败 → EXTERNAL_SESSION_UNCERTAIN（不自动切换）。进程扫描只
// 辅助：它不能阻止用户随后从外部启动。
type claudeProcessQuiescence struct {
	app         *App
	lister      claudeProcessLister
	attribution func() map[int]bool
}

func (q claudeProcessQuiescence) Check(_ context.Context) error {
	if q.lister == nil {
		return claudeaccounts.NewErr(claudeaccounts.ReasonExternalSessionUncertain, "进程观察器未接线")
	}
	samples, err := q.lister()
	if err != nil {
		return claudeaccounts.NewErr(claudeaccounts.ReasonExternalSessionUncertain, "进程扫描失败")
	}
	attributed := map[int]bool{}
	if q.attribution != nil {
		for pid, ok := range q.attribution() {
			if ok {
				attributed[pid] = true
			}
		}
	}
	// 存活后代：归因集合的后代闭包也视为 CodeBox 管理（子 agent 随宿主
	// 会话计）；归因外的匹配进程（含匹配进程的存活后代）一律 uncertain。
	children := map[int][]int{}
	for _, s := range samples {
		children[s.PPID] = append(children[s.PPID], s.PID)
	}
	queue := make([]int, 0, len(attributed))
	for pid := range attributed {
		queue = append(queue, pid)
	}
	for len(queue) > 0 {
		pid := queue[0]
		queue = queue[1:]
		for _, child := range children[pid] {
			if !attributed[child] {
				attributed[child] = true
				queue = append(queue, child)
			}
		}
	}
	external := 0
	for _, s := range samples {
		if !claudeProcessArgsMatch(s.Args) || attributed[s.PID] {
			continue
		}
		external++
	}
	if external > 0 {
		return claudeaccounts.NewErr(claudeaccounts.ReasonExternalSessionUncertain,
			"检测到 %d 个 CodeBox 之外的 Claude/Pi 进程，保守不切换（请退出后重试）", external)
	}
	return nil
}

// claudeQuiescenceAttribution 汇总「持有凭据 lease 的会话」PID 作为观察器
// 归因集合（F02：静止检查不能仅凭「属于 CodeBox」豁免——absent 状态下启动
// 的会话从未持有凭据 lease，首次启用前它们仍是活动消费者，必须计入静止
// 条件；启用后正常会话持有 SharedServiceClaudeCredentials lease，其进程树
// 由 coordinator 的 mutation 门保护，观察器跳过）。归因只用于跳过自己持
// lease 的会话，不扩大放行面（匹配仍要求 claude/pi 形态）。
func (a *App) claudeQuiescenceAttribution() map[int]bool {
	out := map[int]bool{}
	if a == nil {
		return out
	}
	holders := map[string]bool{}
	a.sharedLeaseMu.Lock()
	for key := range a.sharedLeases {
		if key.Kind == remote.SharedServiceClaudeCredentials {
			holders[string(key.SessionID)] = true
		}
	}
	a.sharedLeaseMu.Unlock()
	if len(holders) == 0 || a.Sessions == nil {
		return out
	}
	for _, s := range a.Sessions.List() {
		if s.PID > 0 && holders[s.ID] {
			out[s.PID] = true
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// 受管 Anthropic 登录的环境边界（design §6.3 R3）
// ---------------------------------------------------------------------------

// managedClaudeIdentityEnvKeys 是受管 Anthropic 登录启动在最终 shell 后边界
// 必须清除的身份/认证覆盖变量（design §6.3）：CLAUDE_CODE_DEVICE_ID、
// CLAUDE_CODE_ACCOUNT_UUID、导向其他根的 CLAUDE_CONFIG_DIR、CLAUDE_CODE_OAUTH_TOKEN
// 等认证覆盖入口。不删模型参数。
var managedClaudeIdentityEnvKeys = []string{
	"CLAUDE_CODE_DEVICE_ID",
	"CLAUDE_CODE_ACCOUNT_UUID",
	"CLAUDE_CODE_OAUTH_TOKEN",
	"CLAUDE_CONFIG_DIR",
}

// managedHomeEnvKeys 是能把 canonical 身份路径导向其他根的变量。
var managedHomeEnvKeys = []string{"HOME", "USERPROFILE"}

// managedHomeOverride 检查环境里是否把 HOME/USERPROFILE 覆盖为非 canonical
// 值；返回命中的变量名（空串 = 无覆盖）。canonical 以 CodeBox 进程自身的
// home 为准。
func managedHomeOverride(env []string) string {
	home := canonicalHomeForManagedLaunch()
	if home == "" {
		return ""
	}
	for _, kv := range env {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		for _, key := range managedHomeEnvKeys {
			if !strings.EqualFold(k, key) {
				continue
			}
			value := strings.TrimSpace(v)
			if value == "" || strings.EqualFold(filepath.Clean(value), filepath.Clean(home)) {
				break
			}
			return k
		}
	}
	return ""
}

// canonicalHomeForManagedLaunch 返回 CodeBox 进程视角的 home（失败返回空）。
func canonicalHomeForManagedLaunch() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return home
}

// stripEnvKeys 从 "K=V" 列表移除指定键（大小写不敏感）。
func stripEnvKeys(env []string, keys []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		k, _, ok := strings.Cut(kv, "=")
		drop := false
		if ok {
			for _, key := range keys {
				if strings.EqualFold(k, key) {
					drop = true
					break
				}
			}
		}
		if !drop {
			out = append(out, kv)
		}
	}
	return out
}

// claudeLoginManagedExtraKeys 返回受管登录启动额外剥离的键（nil/absent
// reservation 返回 nil，保持非受管身份环境不变；design §6.3 R3）。
func claudeLoginManagedExtraKeys(claim *claudeCredentialsClaim) []string {
	if claim == nil || claim.absent {
		return nil
	}
	return managedClaudeIdentityEnvKeys
}

// managedClaudeIdentityBoundarySpec 在最终执行边界清除身份/认证覆盖变量
// （design §6.3 R3）：direct/Windows shell 的 spec.Env 已生效，直接剥键；
// POSIX shell 的 startup 文件可能重导出，用 env -u 前缀在 shell 初始化后
// 重新剥离（与 piLoginReAnchorSpec 同一模式）。不删模型参数。
func managedClaudeIdentityBoundarySpec(spec platform.ResolvedLaunchSpec) platform.ResolvedLaunchSpec {
	if spec.BootstrapMode == platform.BootstrapWSL {
		return spec // WSL 目标不持有凭据 lease（distro 独立根），无此边界
	}
	spec.Env.Variables = stripEnvKeys(spec.Env.Variables, managedClaudeIdentityEnvKeys)
	if spec.BootstrapMode != platform.BootstrapShellInline && spec.BootstrapMode != platform.BootstrapShellAttach {
		return spec
	}
	if !piLoginPosixShell(spec.Shell) {
		return spec
	}
	parts := []string{"env"}
	for _, key := range managedClaudeIdentityEnvKeys {
		parts = append(parts, "-u", key)
	}
	prefix := strings.Join(parts, " ") + " "
	spec.StartupCommand = prefix + spec.StartupCommand
	return spec
}

// ---------------------------------------------------------------------------
// 缓存刷新与启动恢复
// ---------------------------------------------------------------------------

// refreshClaudeAccountsCache 重读池并更新启动门禁缓存（任何 service mutation
// 与 Startup 恢复后调用）。 UNSUPPORTED_PLATFORM 平台保持 absent。F10：直接
// 读 PoolStore（不经 Service 串行锁，与 publishClaudeAccountsAuthoritativeState
// 同源），并维护「曾初始化」标记——已有池缺失时 fail closed 为 POOL_MISSING，
// 不当从未启用。
func (a *App) refreshClaudeAccountsCache(ctx context.Context, recovered ...bool) {
	// 唯一读发布序列：Load 返回到发布之间仍持有此锁。mutation release
	// 也走此路径，故旧 Load 的发布只能在新 blocked 发布之前，不能倒灌。
	a.claudeAccountsPublishMu.Lock()
	defer a.claudeAccountsPublishMu.Unlock()
	var pool *claudeaccounts.AccountPool
	var err error
	if a.claudeAccountsPoolStore == nil {
		// 测试/未装配场景回退 Service 路径。
		if a.claudeAccounts == nil {
			return
		}
		pool, err = a.claudeAccounts.LoadPool(ctx)
	} else {
		pool, err = a.claudeAccountsPoolStore.Load(ctx)
	}
	if err == nil && a.claudeAccountsPoolStore != nil {
		if pool != nil {
			if markerErr := a.writeClaudeAccountsMarker(); markerErr != nil {
				err = claudeaccounts.NewErr(claudeaccounts.ReasonNeedsReconcile, "池存在性标记无法持久化，启动保持关闭")
			}
		} else if exists, markerErr := a.claudeAccountsMarkerStatus(); markerErr != nil {
			err = claudeaccounts.NewErr(claudeaccounts.ReasonNeedsReconcile, "池存在性标记无法读取，启动保持关闭")
		} else if exists {
			err = claudeAccountsPoolMissingErr()
		}
	}
	a.claudeAccountsMu.Lock()
	a.claudeAccountsPool = pool
	a.claudeAccountsLoadErr = err
	if len(recovered) > 0 {
		a.claudeAccountsRecovered = recovered[0]
	}
	a.claudeAccountsMu.Unlock()
}

// startupRecoverClaudeAccounts 是 §4.5 启动时序：Config/Keychain/池加载 →
// RecoverAtStartup → 才开放受影响启动入口。其他 API 工作流不因账号池故障
// 停用（失败只记警告）。
func (a *App) startupRecoverClaudeAccounts() {
	if a.claudeAccounts == nil || a.claudeAccountsUnsupported {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	a.refreshClaudeAccountsCache(ctx)
	a.claudeAccountsMu.RLock()
	pool := a.claudeAccountsPool
	a.claudeAccountsMu.RUnlock()
	if pool == nil {
		// 从未创建过池：正常未启用，无恢复面。
		a.refreshClaudeAccountsCache(ctx, true)
		return
	}
	outcome, err := a.claudeAccounts.RecoverAtStartup(ctx)
	if err != nil {
		if a.Log != nil {
			a.Log.Warn("claude-accounts", "启动恢复未完成，受影响启动入口保持关闭", err.Error())
		}
		a.addStartupWarning("Claude 订阅账号池启动恢复未完成：" + err.Error() + "；Claude/Pi 受影响启动暂不可用，请到 Provider Center 完成「检查与恢复」")
	} else if outcome.Action == claudeaccounts.ReconcileNeedsReconcile || outcome.Action == claudeaccounts.ReconcileBlocked {
		if a.Log != nil {
			a.Log.Warn("claude-accounts", "账号池存在需人工恢复的事务", string(outcome.Action))
		}
		a.addStartupWarning("Claude 订阅账号池存在未完成的切换事务（needs-reconcile）；请到 Provider Center 完成「检查与恢复」后再启动 Claude/Pi 会话")
	} else if a.Log != nil {
		a.Log.Info("claude-accounts", "启动恢复完成", fmt.Sprintf("action=%s generation=%d", outcome.Action, outcome.Generation))
	}
	// 恢复尝试完成后刷新缓存；recovered 置位由池是否可判定决定：恢复后仍
	// needs-reconcile（Transaction 残留）保持 blocked（policy 里判定），但不
	// 再阻止后续 refresh/重试。
	a.refreshClaudeAccountsCache(ctx, true)
}

// claudeAccountsProcessSamples 是观察器的动态枚举入口：优先测试注入的
// fake 进程表，缺省用生产 ps 枚举（design §10.1：进程观察器测试注入 fake）。
func (a *App) claudeAccountsProcessSamples() ([]claudeProcessSample, error) {
	if a.claudeAccountsQuiescenceLister != nil {
		return a.claudeAccountsQuiescenceLister()
	}
	return newProductionClaudeProcessLister()()
}

// ---------------------------------------------------------------------------
// 计划接缝（launch planner 的凭据 admission 描述）
// ---------------------------------------------------------------------------

// claudeCredentialsPlanAdmission 返回计划应携带的凭据 admission spec（纯读，
// design §6.3：BuildPlan 禁止 harvest/swap/refresh）。未启用返回零值 false；
// blocked 返回错误（计划构建失败 → 入口关闭）。
func (a *App) claudeCredentialsPlanAdmission() (launchplan.SharedAdmissionSpec, bool, error) {
	switch a.claudeCredentialsPolicy() {
	case claudeCredentialsAbsent:
		return launchplan.SharedAdmissionSpec{}, false, nil
	case claudeCredentialsBlocked:
		return launchplan.SharedAdmissionSpec{}, false, a.claudeCredentialsBlockedError()
	}
	fingerprint, err := a.claudeCredentialsFingerprint()
	if err != nil {
		return launchplan.SharedAdmissionSpec{}, false, err
	}
	return launchplan.SharedAdmissionSpec{
		Service:           launchplan.SharedClaudeCredentials,
		ConfigFingerprint: fingerprint,
	}, true, nil
}
