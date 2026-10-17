package wslsupply

// plugins.go — P3-S5 插件操作目标化与 WSL 原生重放作用域。
//
// 设计依据（p3-config-supply §四「插件实体：复制还是重放」六条硬规则）：
//  1. 插件操作必须有显式目标（Windows / 当前 WSL distro / 同时应用——目标
//     选择在 App 编排层；本作用域只承接 WSL 侧）；每目标独立结果，Windows
//     成功不冒充 WSL 成功。
//  2. WSL 侧走目标原生 CLI 命令重放（distro 内 `pi install/remove`），绝不
//     复制 node_modules/git 实体；npm 源带确切版本、git 源尽量固定 commit。
//  3. 重放记录 source/确切版本/ref/过滤项/操作 ID 入状态清单；重放成功且
//     复检（distro settings 登记 + 实体目录同时有效）通过才写「已安装」。
//  4. 更新/删除只作用于本系统登记管理的插件；WSL 本地手动增删的插件显示为
//     本地项不动；git 实体有未提交修改时 update 冻结（绝不自动 reset/clean）。
//  5. 来源不可解析（npm 包消失/git ref 失效）冻结该插件操作并诊断，不影响
//     其他插件。
//  6. 离线/超时/取消为结构化错误、单次尝试不重试；同版本重复 install 为
//     AlreadyOK 语义。
//
// 本作用域与文件同步单元（Plan/Apply）分离授权与状态：插件单元不进 RunUnits
// 词汇表，启动链（ReasonLaunch）结构性无法触达插件安装（启动只经状态面提示
// 缺失，不自动联网安装、不执行 postinstall）。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"amagi-codebox/internal/platform"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// ---------------------------------------------------------------------------
// 操作与结果类型
// ---------------------------------------------------------------------------

// 插件操作类型。
const (
	PluginOpInstall = "install"
	PluginOpUpdate  = "update"
	PluginOpRemove  = "remove"
)

// 操作结果 outcome 枚举。
const (
	// PluginOutcomeInstalled / Updated / Removed：重放成功且复检通过。
	PluginOutcomeInstalled = "installed"
	PluginOutcomeUpdated   = "updated"
	PluginOutcomeRemoved   = "removed"
	// PluginOutcomeAlreadyOK：幂等命中（同版本 install / 同 ref update /
	// 登记已不存在的 remove），未执行任何重放命令。
	PluginOutcomeAlreadyOK = "already-ok"
	// PluginOutcomeBlocked：结构化阻断（不冻结目标内容）：目标不可解析、
	// CLI 缺失、本地源不支持、来源不可解析、脏仓库、非本系统管理、
	// unmanaged-existing、无宿主锚点等。
	PluginOutcomeBlocked = "blocked"
	// PluginOutcomeFailed：重放命令执行失败或复检未通过（失败绝不写已安装）。
	PluginOutcomeFailed = "failed"
)

// 结构化错误码（离线/超时/取消不重试风暴；诊断面，非可重试信号）。
const (
	PluginErrTargetUnresolved      = "target-unresolved"
	PluginErrCLIMissing            = "cli-missing"
	PluginErrLocalSource           = "local-source-unsupported"
	PluginErrSourceUnresolvable    = "source-unresolvable"
	PluginErrDirtyRepo             = "dirty-repo"
	PluginErrGitStatusUnavailable  = "git-status-unavailable"
	PluginErrNotManaged            = "not-managed"
	PluginErrUnmanagedExisting     = "unmanaged-existing"
	PluginErrNoHostAnchor          = "windows-anchor-missing"
	PluginErrTimeout               = "timeout"
	PluginErrCanceled              = "canceled"
	PluginErrReplayFailed          = "replay-failed"
	PluginErrVerifyFailed          = "verify-failed"
	PluginErrRegistrationMismatch  = "registration-mismatch"
	PluginErrParserUnavailable     = "parser-unavailable"
	PluginErrInvalidSource         = "invalid-source"
	PluginErrRegistrationUnchanged = "registration-unchanged"
	// PluginErrFilterApplyFailed：过滤清单按 Pi 原生语义写入 distro settings
	// 失败（SA12：应用与复检是硬门槛，失败绝不报已安装）。
	PluginErrFilterApplyFailed = "filter-apply-failed"
)

// PluginOperation 是一次 WSL 侧插件重放请求（design §七 RunPluginOperation：
// 显式目标、来源/ref、预期登记状态）。
type PluginOperation struct {
	// Source 是面板原始源串。
	Source string
	// Kind: install / update / remove。
	Kind string
	// AnchorRef 是宿主侧已解析的确切版本（npm）或 commit（git）——「同时
	// 应用」模式的重放锚点；空串表示在 distro 内独立解析。
	AnchorRef string
	// Filters 是登记在案的子资源过滤清单快照（SA12：重放时按 Pi 原生
	// packages 语义写入 distro settings 过滤对象并复检）。PR08 三态指针：
	// nil = 调用方未提供选择（沿用既有登记 reg.Filters——旧调用兼容）；
	// 非 nil（含全字段 nil）= 显式完整期望快照——全 nil 表示「全部加载」
	//（纯字符串登记形态），可把旧过滤集重置回全量，不再被解释为沿用。
	Filters *PluginFilters
}

// PluginOpResult 是一次重放的结构化结果（脱敏：不含完整命令输出正文，只有
// 截断摘要）。
type PluginOpResult struct {
	Source string `json:"source"`
	Kind   string `json:"kind"`
	OK     bool   `json:"ok"`
	// Outcome: installed / updated / removed / already-ok / blocked / failed。
	Outcome string `json:"outcome"`
	// ErrCode 是结构化错误码（成功为空）。
	ErrCode string `json:"errCode,omitempty"`
	// Detail 是中文短说明（冻结原因/失败阶段）。
	Detail string `json:"detail,omitempty"`
	OpID   string `json:"opId,omitempty"`
	// PinnedRef / RefKind 是重放实际锚定的确切版本/commit 与形态。
	PinnedRef string `json:"pinnedRef,omitempty"`
	RefKind   string `json:"refKind,omitempty"`
	// EntityVerified：settings 登记与 distro 内实体同时有效。
	EntityVerified bool `json:"entityVerified"`
}

// WSLPluginItem 是 WSL 侧插件列表的一项（Probe 产物；每侧实体各反映自己的
// 状态，Windows 成功不冒充 WSL 成功）。
type WSLPluginItem struct {
	Source string `json:"source"`
	// Identity 是归一身份键（npm name / git host/path）。
	Identity   string `json:"identity"`
	SourceType string `json:"sourceType"`
	Name       string `json:"name,omitempty"`
	// Version 是实体 package.json 的 version（npm）。
	Version string `json:"version,omitempty"`
	// Commit 是实体 git HEAD（只读 .git 解析；不可得为空）。
	Commit string `json:"commit,omitempty"`
	// Managed：本系统登记管理（update/remove 可作用）；false = WSL 本地项。
	Managed bool `json:"managed"`
	// State: installed / mismatch / local-item / registered-missing / frozen。
	State  string `json:"state"`
	Detail string `json:"detail,omitempty"`
	// EntityPresent / SettingsPresent 是复检两面（同时有效才算 installed）。
	EntityPresent   bool   `json:"entityPresent"`
	SettingsPresent bool   `json:"settingsPresent"`
	EntityPath      string `json:"entityPath,omitempty"` // Linux 路径
	PinnedRef       string `json:"pinnedRef,omitempty"`
	RefKind         string `json:"refKind,omitempty"`
	Frozen          bool   `json:"frozen,omitempty"`
	FrozenReason    string `json:"frozenReason,omitempty"`
	LastOp          string `json:"lastOp,omitempty"`
	LastErrorCode   string `json:"lastErrorCode,omitempty"`
}

// WSLPluginItem State 枚举。
const (
	PluginItemInstalled        = "installed"
	PluginItemMismatch         = "mismatch"
	PluginItemLocal            = "local-item"
	PluginItemRegisteredMissed = "registered-missing"
	PluginItemFrozen           = "frozen"
)

// WSLPluginSnapshot 是一次 Probe 的聚合快照（缓存后供面板只读展示）。
type WSLPluginSnapshot struct {
	Distro     string          `json:"distro"`
	Available  bool            `json:"available"`
	Reason     string          `json:"reason,omitempty"`
	Checked    bool            `json:"checked"`
	CheckedAt  string          `json:"checkedAt,omitempty"`
	PIResolve  string          `json:"piResolve,omitempty"` // distro 内 pi 的绝对路径（空=未解析）
	Items      []WSLPluginItem `json:"items"`
	Managed    int             `json:"managed"`
	Installed  int             `json:"installed"`
	Mismatched int             `json:"mismatched"`
	Local      int             `json:"local"`
}

// PluginRegistrationSummary 是状态页插件面（读缓存派生，不唤醒 distro）。
type PluginRegistrationSummary struct {
	Managed   int `json:"managed"`
	Installed int `json:"installed"`
	Failed    int `json:"failed"`
	Frozen    int `json:"frozen"`
	Missing   int `json:"missing"` // 登记 Installed=false（含启动提示缺失面）
	// PendingOps（攻坚契约）：未决插件操作摘要数（native 副作用可能已发生，
	// 待显式复检；优先于旧 installed 展示——不能说原生副作用已回滚）。
	PendingOps int       `json:"pendingOps"`
	LastOpAt   time.Time `json:"-"`
	LastOpAtRC string    `json:"lastOpAt,omitempty"`
}

// ---------------------------------------------------------------------------
// 接缝
// ---------------------------------------------------------------------------

// PluginSourceSpec 是重放级源描述（由 piplugin 注入解析实现；wslsupply 不
// 依赖 piplugin 包）。
type PluginSourceSpec struct {
	Type      string // npm / git / local
	Name      string // npm 包名（含 scope，不含版本）
	Ref       string // npm 版本或 git ref（可空）
	Host      string // git host
	Path      string // git user/project
	LocalPath string
}

// PluginSourceParser 把源串解析为重放级描述（生产实现 piplugin.ParseSourceSpec）。
type PluginSourceParser func(raw string) PluginSourceSpec

// PluginRefChecker 判定 ref 形态（生产实现 piplugin.IsExactNpmVersion /
// IsFullCommitSHA 的组合面）。
type PluginRefChecker struct {
	// ExactNpmVersion 报告 ref 是否为 npm 精确语义化版本。
	ExactNpmVersion func(ref string) bool
	// FullCommitSHA 报告 ref 是否为完整 commit SHA。
	FullCommitSHA func(ref string) bool
}

// PluginCommandRunner 在 distro 默认用户登录 shell 内执行脚本并返回合并
// 输出（ctx 控制超时/取消；生产实现经 wsl.exe --exec bash -lc）。
type PluginCommandRunner = func(ctx context.Context, distro, script string) (string, error)

// pluginUserBinPathPrefix 与 wslsetup 同源：非交互登录 shell 不加载 .bashrc
// 的交互段，npm-global shim（wslsetup `npm i -g` 的落点）必须显式前置。
const pluginUserBinPathPrefix = `export PATH="$HOME/.npm-global/bin:/usr/bin:/usr/local/bin:$PATH"; `

// defaultPluginCommandRunner 生产执行面：wsl.exe --exec bash -lc（--exec 逐字
// 传参，无中间 shell 二次解析）；SuppressConsoleWindow 抑制 GUI 闪窗；命令
// 绑定 ctx（exec.CommandContext）。ctx 已取消/到期在构造命令前短路——已取
// 消的请求不查 wsl.exe、不启动任何进程（SA16/SA18：exec.CommandContext 对
// 已取消 ctx 的 Start 仍可能先触发 LookPath，平台 PATH 差异不得掩盖取消语
// 义）。终止 wsl.exe 只结束 Windows 侧进程；distro 内由它启动的 Linux 子进程
// 是否随之退出未经 Windows 真机验证——超时/取消结果按此边界如实报告，不宣
// 称 Linux 侧进程已清理。非 Windows 返回错误（插件重放仅 Windows 宿主可用）。
func defaultPluginCommandRunner(ctx context.Context, distro, script string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, "wsl.exe", "-d", distro, "--exec", "bash", "-lc", pluginUserBinPathPrefix+script)
	platform.SuppressConsoleWindow(cmd)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// pluginShQuote 是 POSIX 单引号转义（与 platform.wslShSingleQuote 同式；
// 本包自带副本避免依赖私有符号）。
func pluginShQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// ---------------------------------------------------------------------------
// PluginScope
// ---------------------------------------------------------------------------

// PluginScope 承载插件重放的全部状态与执行面：runner/parser 接缝、探测快照
// 缓存（进程内，不持久化实体清单）、登记读写（经共享 stateStore）。并发安全：
// opMu 串行化操作（同目标的重放命令不交错），snapMu 保护快照缓存。
type PluginScope struct {
	state    *stateStore
	runner   PluginCommandRunner
	parser   PluginSourceParser
	refCheck PluginRefChecker
	// commitFileFn 是统一文件提交/恢复原语的插件内部回调（攻坚契约 PR07：
	// filters 的最终发布复用同一提交回调，禁止另造 tmp+无条件 rename 旁路）。
	// 由协调器装配（NewCoordinator 注入 commitPluginFile）。
	commitFileFn PluginCommitFn

	opMu   sync.Mutex
	snapMu sync.Mutex
	snaps  map[string]*WSLPluginSnapshot
}

// PluginCommitFn 是插件侧的内部提交回调签名：expectedBeforeHash 是构建基准
// （发布前重读必须仍等于该值），pluginMeta 是脱敏摘要元数据。
type PluginCommitFn = func(ctx context.Context, target TargetRef, relPath, expectedBeforeHash string, content []byte, pluginMeta map[string]string) *CommitReceipt

// NewPluginScope 构造插件作用域。parser/refCheck 为空时进入降级态（一切
// 操作 blocked parser-unavailable——裸协调器不装配生产接缝时的诚实行为）。
func NewPluginScope(state *stateStore, runner PluginCommandRunner, parser PluginSourceParser, refCheck PluginRefChecker) *PluginScope {
	if runner == nil {
		runner = defaultPluginCommandRunner
	}
	return &PluginScope{
		state: state, runner: runner, parser: parser, refCheck: refCheck,
		snaps: map[string]*WSLPluginSnapshot{},
	}
}

// SetSourceParser 注入生产解析面（App 装配时调用；测试注入假实现）。
func (p *PluginScope) SetSourceParser(parser PluginSourceParser, refCheck PluginRefChecker) {
	p.parser = parser
	p.refCheck = refCheck
}

// parseSpec 解析并校验源串。
func (p *PluginScope) parseSpec(source string) (PluginSourceSpec, error) {
	source = strings.TrimSpace(source)
	if source == "" || len(source) > 2048 || strings.HasPrefix(source, "-") ||
		strings.ContainsAny(source, "\x00\r\n") {
		return PluginSourceSpec{}, errors.New(PluginErrInvalidSource)
	}
	if p.parser == nil {
		return PluginSourceSpec{}, errors.New(PluginErrParserUnavailable)
	}
	spec := p.parser(source)
	if spec.Type != "npm" && spec.Type != "git" && spec.Type != "local" {
		return PluginSourceSpec{}, errors.New(PluginErrInvalidSource)
	}
	return spec, nil
}

// pluginIdentity 归一身份键：npm → name；git → host/path（host 小写）；local
// → 绝对路径原样（本批不支持重放，仅用于一致展示）。
func pluginIdentity(spec PluginSourceSpec) string {
	switch spec.Type {
	case "npm":
		return "npm:" + spec.Name
	case "git":
		return "git:" + strings.ToLower(spec.Host) + "/" + spec.Path
	default:
		return "local:" + spec.LocalPath
	}
}

// entityRelPath 返回实体目录相对 agent 根的路径（npm/node_modules/<name> 或
// git/<host>/<path>）。
func entityRelPath(spec PluginSourceSpec) string {
	switch spec.Type {
	case "npm":
		if spec.Name == "" {
			return ""
		}
		return "npm/node_modules/" + spec.Name
	case "git":
		if spec.Host == "" || spec.Path == "" {
			return ""
		}
		return "git/" + strings.ToLower(spec.Host) + "/" + spec.Path
	default:
		return ""
	}
}

// pinnedSourceString 构造重放命令使用的源串（npm: 带确切版本 / git: 固定
// commit 的规范形态）。
func pinnedSourceString(spec PluginSourceSpec, ref string) string {
	switch spec.Type {
	case "npm":
		if ref == "" {
			return "npm:" + spec.Name
		}
		return "npm:" + spec.Name + "@" + ref
	case "git":
		repo := strings.ToLower(spec.Host) + "/" + spec.Path
		if ref == "" {
			return "git:" + repo
		}
		return "git:" + repo + "@" + ref
	default:
		return spec.LocalPath
	}
}

// newPluginOpID 生成操作 ID（kind + 纳秒时标；登记与结果可对账）。
func newPluginOpID(kind string) string {
	short := kind
	if len(short) > 4 {
		short = short[:4]
	}
	return fmt.Sprintf("s5-%s-%d", short, time.Now().UnixNano())
}

// truncateDetail 截断输出摘要（诊断面，不携带完整命令输出正文）。
func truncateDetail(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	return s
}

// classifyRunnerError 把 runner 错误分类为结构化错误码。SA16：ctx 已到期/
// 取消时优先按 ctx 归因——运行中被 CommandContext 终止的 wsl.exe 常以
// ExitError 形态返回、不携带 ctx 错误链，runner 错误形态本身不能推翻取消/
// 超时事实；只有 ctx 未结束时才按错误链（假 runner 直接回 ctx.Err）归类。
func classifyRunnerError(ctx context.Context, err error) string {
	if ctx != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			if errors.Is(ctxErr, context.DeadlineExceeded) {
				return PluginErrTimeout
			}
			return PluginErrCanceled
		}
	}
	if err == nil {
		return ""
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return PluginErrTimeout
	}
	if errors.Is(err, context.Canceled) {
		return PluginErrCanceled
	}
	return PluginErrReplayFailed
}

// runScript 执行一段 distro 内登录 shell 脚本（带 opMu 串行与 ctx 透传）。
// ctx 已取消/到期时不排队等 opMu、不启动任何 distro 命令（SA16：取消的请求
// 不得在锁等待或执行面继续产生跨界副作用）。
func (p *PluginScope) runScript(ctx context.Context, distro, script string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	p.opMu.Lock()
	defer p.opMu.Unlock()
	// 攻坚契约：取得 opMu 后再查 ctx——等锁期间取消的请求不执行命令。
	if err := ctx.Err(); err != nil {
		return "", err
	}
	out, err := p.runner(ctx, distro, script)
	if err == nil {
		// runner 返回 nil 但 ctx 已结束 → 取消（后继 remove/install/filter
		// 不执行；外部命令可能已有副作用，由调用方如实归因）。
		if cerr := ctx.Err(); cerr != nil {
			return out, cerr
		}
	}
	return out, err
}

// ---------------------------------------------------------------------------
// distro 侧只读事实（UNC 读，不 spawn）
// ---------------------------------------------------------------------------

// distroPluginEntry 是 distro settings.json packages[] 一项的只读解析。
type distroPluginEntry struct {
	source string
	spec   PluginSourceSpec
	// raw 是元素的原始 JSON（字符串或对象形态）；isObject 报告形态。
	raw      string
	isObject bool
	// filters 是对象形态携带的过滤清单（字符串形态为零值）。
	filters PluginFilters
}

// settingsReadState 区分 settings.json 的读取三态：可读 / 缺失（agent 根
// 尚未初始化，不算错误）/ 不可读或损坏（无法确认登记状态，操作必须阻断，
// 不得把「读不到」冒充「登记已消失」）。
type settingsReadState int

const (
	settingsReadable settingsReadState = iota
	settingsMissing
	settingsUnreadable
)

// readDistroSettingsPackages 经 UNC 读 distro settings.json 的 packages[]
// （元素为字符串源或 {source,...} 过滤对象；与 piplugin 同语义的只读子集，
// 同时携带过滤清单供 SA12 复检，并保留三态：键省略 → nil，显式空数组 []
// → 非 nil 空切片——写入面不产生 null，手写 null 值按键省略处理）。文件缺失
// 返回空清单与 settingsMissing（尚未初始化的 agent 根不算错误）；其他读取
// 失败或 JSON 损坏返回 settingsUnreadable（调用方阻断，不猜测）。
func (p *PluginScope) readDistroSettingsPackages(target TargetRef) ([]distroPluginEntry, settingsReadState) {
	path := filepath.Join(target.UNCRoot, "settings.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, settingsMissing
		}
		return nil, settingsUnreadable
	}
	var root struct {
		Packages []json.RawMessage `json:"packages"`
	}
	if json.Unmarshal(data, &root) != nil {
		return nil, settingsUnreadable
	}
	out := make([]distroPluginEntry, 0, len(root.Packages))
	for _, raw := range root.Packages {
		trimmed := strings.TrimSpace(string(raw))
		if trimmed == "" {
			continue
		}
		entry := distroPluginEntry{raw: string(raw)}
		var source string
		if trimmed[0] == '"' {
			if err := json.Unmarshal(raw, &source); err != nil {
				continue
			}
		} else {
			var obj struct {
				Source     string   `json:"source"`
				Extensions []string `json:"extensions"`
				Skills     []string `json:"skills"`
				Prompts    []string `json:"prompts"`
				Themes     []string `json:"themes"`
			}
			if json.Unmarshal(raw, &obj) != nil || strings.TrimSpace(obj.Source) == "" {
				continue
			}
			source = obj.Source
			entry.isObject = true
			entry.filters = PluginFilters{
				Extensions: obj.Extensions, Skills: obj.Skills,
				Prompts: obj.Prompts, Themes: obj.Themes,
			}
		}
		source = strings.TrimSpace(source)
		entry.source = source
		if p.parser != nil {
			entry.spec = p.parser(source)
		}
		out = append(out, entry)
	}
	return out, settingsReadable
}

// distroEntryForIdentity 返回 distro settings 中命中身份键的登记（未命中
// 返回 nil）。
func distroEntryForIdentity(entries []distroPluginEntry, identity string) *distroPluginEntry {
	for i := range entries {
		if pluginIdentity(entries[i].spec) == identity {
			return &entries[i]
		}
	}
	return nil
}

// entityFacts 是 distro 内实体的只读事实。
type entityFacts struct {
	present bool
	version string // npm 实体 package.json version
	commit  string // git 实体 HEAD（.git/HEAD 解析；不可得为空）
}

// probeEntity 经 UNC 探测实体目录与元数据（不 spawn、不写）。
func (p *PluginScope) probeEntity(target TargetRef, spec PluginSourceSpec) entityFacts {
	rel := entityRelPath(spec)
	if rel == "" {
		return entityFacts{}
	}
	unc := filepath.Join(target.UNCRoot, filepath.FromSlash(rel))
	info, err := os.Stat(unc)
	if err != nil || !info.IsDir() {
		return entityFacts{}
	}
	facts := entityFacts{present: true}
	switch spec.Type {
	case "npm":
		if data, err := os.ReadFile(filepath.Join(unc, "package.json")); err == nil {
			facts.version = strings.TrimSpace(gjsonGet(data, "version"))
		}
	case "git":
		facts.commit = readDotGitHead(filepath.Join(unc, ".git"))
	}
	return facts
}

// gjsonGet 是 settings/package.json 单字段读取（gjson 依赖已在包内；集中一
// 处避免多处直接引 gjson）。
func gjsonGet(data []byte, path string) string {
	return gjson.GetBytes(data, path).String()
}

// readDotGitHead 只读解析 .git/HEAD → commit SHA（detached 或 symbolic ref +
// loose/packed refs）。解析失败返回空串（诚实：不可得不猜测）。
func readDotGitHead(dotGit string) string {
	data, err := os.ReadFile(filepath.Join(dotGit, "HEAD"))
	if err != nil {
		return ""
	}
	head := strings.TrimSpace(string(data))
	if sha, ok := pluginTrimSHA(head); ok {
		return sha
	}
	const refPrefix = "ref: "
	if !strings.HasPrefix(head, refPrefix) {
		return ""
	}
	refName := strings.TrimSpace(strings.TrimPrefix(head, refPrefix))
	if refName == "" {
		return ""
	}
	if loose, err := os.ReadFile(filepath.Join(dotGit, filepath.FromSlash(refName))); err == nil {
		if sha, ok := pluginTrimSHA(strings.TrimSpace(string(loose))); ok {
			return sha
		}
	}
	packed, err := os.ReadFile(filepath.Join(dotGit, "packed-refs"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(packed), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") {
			continue
		}
		sha, rest, ok := strings.Cut(line, " ")
		if ok && strings.TrimSpace(rest) == refName {
			if v, ok := pluginTrimSHA(sha); ok {
				return v
			}
		}
	}
	return ""
}

var pluginSHARe = regexp.MustCompile(`^[0-9a-fA-F]{40}$|^[0-9a-fA-F]{64}$`)

func pluginTrimSHA(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if !pluginSHARe.MatchString(s) {
		return "", false
	}
	return strings.ToLower(s), true
}

// ---------------------------------------------------------------------------
// distro 内网络解析（确切版本 / commit 固定）
// ---------------------------------------------------------------------------

// resolveNpmVersion 在 distro 内解析包的确切最新版本（npm view name version）。
// 失败（离线/包消失）返回结构化错误。
func (p *PluginScope) resolveNpmVersion(ctx context.Context, target TargetRef, name string) (string, error) {
	out, err := p.runScript(ctx, target.Distro, "npm view "+pluginShQuote(name)+" version 2>/dev/null | tail -1")
	if err != nil {
		if code := classifyRunnerError(ctx, err); code != PluginErrReplayFailed {
			// ctx 到期/取消优先于「离线」文案（复审 F2：超时不得误导诊断为离线）。
			return "", errors.New(code)
		}
		return "", fmt.Errorf("npm view 失败（离线或 npm 不可用）: %s", truncateDetail(out))
	}
	ver := strings.TrimSpace(strings.Trim(strings.TrimSpace(out), "v"))
	if ver == "" || strings.ContainsAny(ver, " \t") {
		return "", errors.New("npm view 未返回确切版本（包可能已消失）")
	}
	return ver, nil
}

// resolveGitCommit 在 distro 内把 git 源解析为 commit（git ls-remote）。ref
// 为空解析 HEAD。失败返回错误（ref 失效/离线）。
func (p *PluginScope) resolveGitCommit(ctx context.Context, target TargetRef, spec PluginSourceSpec) (string, error) {
	repo := spec.Host + "/" + spec.Path
	refArg := "HEAD"
	if spec.Ref != "" {
		refArg = spec.Ref
	}
	out, err := p.runScript(ctx, target.Distro, "git ls-remote "+pluginShQuote("https://"+repo)+" "+pluginShQuote(refArg)+" 2>/dev/null | head -1 | cut -f1")
	if err != nil {
		if code := classifyRunnerError(ctx, err); code != PluginErrReplayFailed {
			return "", errors.New(code)
		}
		return "", fmt.Errorf("git ls-remote 失败（离线或 ref 失效）: %s", truncateDetail(out))
	}
	sha, _ := pluginTrimSHA(strings.SplitN(strings.TrimSpace(out), "\n", 2)[0])
	if sha == "" {
		return "", fmt.Errorf("git ref %q 无法解析为 commit（ref 失效）", refArg)
	}
	return sha, nil
}

// piAvailable 探测 distro 内 pi CLI 可解析性（登录 shell command -v）。
func (p *PluginScope) piAvailable(ctx context.Context, target TargetRef) (string, error) {
	out, err := p.runScript(ctx, target.Distro, "command -v pi")
	if err != nil {
		if code := classifyRunnerError(ctx, err); code != PluginErrReplayFailed {
			return "", errors.New(code)
		}
		return "", fmt.Errorf("distro 探测失败: %s", truncateDetail(out))
	}
	path := strings.TrimSpace(strings.SplitN(strings.TrimSpace(out), "\n", 2)[0])
	if !strings.HasPrefix(path, "/") {
		return "", errors.New("distro 内未解析到原生 pi CLI（需先在 WSL 中安装 pi）")
	}
	return path, nil
}

// gitDirtyCheck 检查 git 实体工作区是否干净（git status --porcelain）。
// 返回 (dirty, summary, err)：err = git 不可用/仓库不可读（保守冻结面）。
func (p *PluginScope) gitDirtyCheck(ctx context.Context, target TargetRef, spec PluginSourceSpec) (bool, string, error) {
	rel := entityRelPath(spec)
	linuxPath := strings.TrimRight(target.LinuxRoot, "/") + "/" + rel
	out, err := p.runScript(ctx, target.Distro, "git -C "+pluginShQuote(linuxPath)+" status --porcelain 2>&1 | head -5")
	if err != nil {
		if code := classifyRunnerError(ctx, err); code != PluginErrReplayFailed {
			return false, "", errors.New(code)
		}
		return false, "", fmt.Errorf("git status 不可用: %s", truncateDetail(out))
	}
	trimmed := strings.TrimSpace(out)
	if trimmed == "" {
		return false, "", nil
	}
	if strings.Contains(trimmed, "not a git repository") || strings.Contains(trimmed, "No such file or directory") {
		return false, "", errors.New("git 实体不可读（仓库缺失）")
	}
	return true, strings.SplitN(trimmed, "\n", 2)[0], nil
}

// ---------------------------------------------------------------------------
// 登记读写（stateStore；重放成功才写 Installed）
// ---------------------------------------------------------------------------

// pluginEntryByIdentity 返回当前目标可归属的登记。SA-02 插件面：登记必须
// 能唯一归属到当前目标——双方 generation 都已知且相等才匹配；目标代际未知
// （GenerationUnknown/空）或登记代际缺失/未知时不复用持久登记（无法证明登记
// 属于这个 distro 时按未登记处理，宁可退回本地项语义也不跨目标继承管理权；
// 无 machine-id 的最小 distro 上 update/remove 会诚实 not-managed，重复
// install 会诚实 unmanaged-existing）。多目标并存（A/B 交替同包不互相覆盖
// 单槽）经 identity+known generation 联合键实现：登记列表按代际分区保存/
// 删除（见 savePluginEntry/dropPluginEntry），本函数是读侧同一键的匹配。
func (p *PluginScope) pluginEntryByIdentity(st *StateFile, identity, generation string) (PluginStateEntry, bool) {
	if !generationBindable(generation) {
		return PluginStateEntry{}, false
	}
	for i := range st.Plugins {
		e := st.Plugins[i]
		if e.Identity != identity {
			continue
		}
		if generationBindable(e.Generation) && e.Generation == generation {
			return e, true
		}
	}
	return PluginStateEntry{}, false
}

// generationBindable 报告身份代是否可用于归属持久登记（空与 unknown 不可）。
func generationBindable(generation string) bool {
	return generation != "" && generation != GenerationUnknown
}

// savePluginEntry 写入登记（mutate 原子落盘；联合键 identity+known
// generation 命中替换否则追加——调用方保证 entry 携带完整身份与 Generation，
// 绝不以空 Identity 落盘）。SA-02 多目标并存：同包在不同代际目标上各自登记
// 一行（A→B→A 交替不互相覆盖单槽——B 的保存不丢 A 的登记，回 A 时旧登记
// 仍可归属）；同代际同身份才替换。身份代未知的登记无法归属目标、读侧永不
// 匹配——不落死数据；该次安装/失败结果仍如实返回给调用方（不冒充已持久化
// 管理）。
func (p *PluginScope) savePluginEntry(entry PluginStateEntry) error {
	if !generationBindable(entry.Generation) {
		return nil
	}
	return p.state.mutate(func(st *StateFile) {
		for i := range st.Plugins {
			if st.Plugins[i].Identity == entry.Identity && st.Plugins[i].Generation == entry.Generation {
				st.Plugins[i] = entry
				return
			}
		}
		st.Plugins = append(st.Plugins, entry)
	})
}

// dropPluginEntry 删除当前目标代际的登记（remove 成功后；SA-02 多目标并存：
// 只删 identity+generation 联合键命中的行——B 移除同包不动 A 的登记；其它
// 代际的旧登记留盘，换回 A 时仍可归属）。generation 必须可绑定（调用方经
// pluginEntryByIdentity 的 hasReg 路径保证）。
func (p *PluginScope) dropPluginEntry(identity, generation string) error {
	if !generationBindable(generation) {
		return nil
	}
	return p.state.mutate(func(st *StateFile) {
		out := st.Plugins[:0]
		for _, e := range st.Plugins {
			if e.Identity != identity || e.Generation != generation {
				out = append(out, e)
			}
		}
		st.Plugins = out
	})
}

// ---------------------------------------------------------------------------
// 操作主流程
// ---------------------------------------------------------------------------

// RunPluginOperation 执行一次 WSL 侧插件重放（显式目标、单次尝试不重试）。
// 全部结构化结果（含阻断），错误面只承载编程性失误（非法 kind/源）。
func (p *PluginScope) RunPluginOperation(ctx context.Context, target TargetRef, op PluginOperation) PluginOpResult {
	opID := newPluginOpID(op.Kind)
	if err := ctx.Err(); err != nil {
		// SA16：已取消/到期的请求在入口立即按 ctx 归因返回——不进入探测/
		// 解析/命令面，避免把取消误报为 cli-missing 等无关阶段。
		code := classifyRunnerError(ctx, err)
		return PluginOpResult{Source: op.Source, Kind: op.Kind, Outcome: PluginOutcomeFailed,
			ErrCode: code, Detail: "请求开始前 ctx 已结束，未执行任何 distro 命令（" + mapLifecycleCodeDetail(code) + "）", OpID: opID}
	}
	blocked := func(errCode, detail string) PluginOpResult {
		return PluginOpResult{Source: op.Source, Kind: op.Kind, Outcome: PluginOutcomeBlocked,
			ErrCode: errCode, Detail: detail, OpID: opID}
	}

	spec, perr := p.parseSpec(op.Source)
	if perr != nil {
		return blocked(perr.Error(), "源字符串不可用于重放")
	}
	if spec.Type == "local" {
		return blocked(PluginErrLocalSource, "本地路径源不支持 WSL 重放（目标需原生 npm/git 源）")
	}
	switch op.Kind {
	case PluginOpInstall, PluginOpUpdate, PluginOpRemove:
	default:
		return PluginOpResult{Source: op.Source, Kind: op.Kind, Outcome: PluginOutcomeBlocked,
			ErrCode: PluginErrInvalidSource, Detail: "未知操作类型: " + op.Kind, OpID: opID}
	}

	// 攻坚契约：首个改变状态的命令前保存插件 pending 摘要（完整目标、
	// source/ref/filters/OpID，不含秘密）；摘要写失败不开始命令。首次失败也
	// 保留可能副作用事实（重启不自动重放，待显式复检）。
	if err := p.savePluginPending(target, opID, op, spec); err != nil {
		return PluginOpResult{Source: op.Source, Kind: op.Kind, Outcome: PluginOutcomeFailed,
			ErrCode: "prepare-state-failed",
			Detail:  "插件 pending 摘要持久化失败，未执行任何 distro 命令: " + err.Error(), OpID: opID}
	}
	identity := pluginIdentity(spec)
	st := p.state.load()
	regEntry, hasReg := p.pluginEntryByIdentity(&st, identity, target.Generation)
	entries, settingsState := p.readDistroSettingsPackages(target)
	if settingsState == settingsUnreadable {
		// 不可读/损坏：无法确认 distro 侧登记状态——一切操作阻断（尤其
		// remove 不得把「读不到」冒充「已消失」的 already-ok）。
		return blocked(PluginErrRegistrationMismatch,
			"WSL 侧 settings.json 不可读或已损坏，无法确认登记状态；插件操作已阻断（请先检查 distro 内文件）")
	}
	distroEntry := distroEntryForIdentity(entries, identity)

	var result PluginOpResult
	switch op.Kind {
	case PluginOpInstall:
		result = p.runInstall(ctx, target, op, spec, identity, regEntry, hasReg, distroEntry, settingsState, opID)
	case PluginOpUpdate:
		result = p.runUpdate(ctx, target, op, spec, identity, regEntry, hasReg, distroEntry, settingsState, opID)
	default:
		result = p.runRemove(ctx, target, op, spec, identity, regEntry, hasReg, distroEntry, settingsState, opID)
	}
	p.finishPluginPending(target, opID, op, spec, result)
	return result
}

// savePluginPending 保存插件操作摘要（脱敏：source/ref/过滤形态；无秘密）。
func (p *PluginScope) savePluginPending(target TargetRef, opID string, op PluginOperation, spec PluginSourceSpec) error {
	return p.state.saveIntent(OperationIntent{
		OpID: opID, Kind: OpKindPluginOp, TargetKey: targetKeyOf(target),
		TargetDistro: target.Distro, TargetHome: target.Home,
		TargetCLI: string(target.CLI),
		PluginMeta: pluginPendingMeta(op, spec), CreatedAt: time.Now(),
	})
}

// pluginPendingMeta 构造摘要元数据（无秘密：源串本身是用户输入的包引用；
// identity 入摘要供成功收敛时按同插件身份结清——不能同目标全清）。
func pluginPendingMeta(op PluginOperation, spec PluginSourceSpec) map[string]string {
	meta := map[string]string{"kind": op.Kind, "source": op.Source, "sourceType": spec.Type, "identity": pluginIdentity(spec)}
	if op.AnchorRef != "" {
		meta["anchorRef"] = op.AnchorRef
	}
	if op.Filters != nil {
		meta["filters"] = "explicit"
	} else {
		meta["filters"] = "unspecified"
	}
	return meta
}

// finishPluginPending 结束插件摘要：成功 → settled + 清理；失败/取消 → 保留
// （native 副作用可能已发生，不自动重放；RegistrationSummary 优先展示）。
// spec 携带本次操作的归一身份：同身份旧摘要收敛，跨源摘要保留。
func (p *PluginScope) finishPluginPending(target TargetRef, opID string, op PluginOperation, spec PluginSourceSpec, result PluginOpResult) {
	disposition := "not-applied"
	if result.OK {
		disposition = "committed"
	}
	if !result.OK {
		// 失败/取消：摘要保留为 pending（native 副作用可能已发生，重启不自动
		// 重放；RegistrationSummary 优先展示待确认面）。
		return
	}
	err := p.state.mutate(func(st *StateFile) {
		if st.SettledOperations == nil {
			st.SettledOperations = map[string]SettledOperation{}
		}
		st.SettledOperations[opID] = SettledOperation{
			TargetKey: targetKeyOf(target), Disposition: disposition, SettledAt: time.Now(),
		}
	})
	if err != nil {
		// settled 保存失败：摘要保留为 pending（事实不丢）。
		return
	}
	_ = p.state.deleteIntent(opID)
	_ = p.state.mutate(func(st *StateFile) {
		delete(st.SettledOperations, opID)
	})
	// 同目标旧 pending 插件摘要只能按「同一插件身份」收敛清理（一致性修正
	// plugin-cross-source-cleanup：B 源的成功不结清 A 源的未确认副作用——只
	// 有同一 identity（同包不同 ref 是版本切换语义）的先前失败摘要才被本次
	// 成功复检覆盖；跨源/无法归一身份的摘要一律保留可见）。
	if intents, ierr := p.state.loadIntents(); ierr == nil {
		key := targetKeyOf(target)
		identity := pluginIdentity(spec)
		for id := range intents {
			if id == opID || intents[id].Kind != OpKindPluginOp || intents[id].TargetKey != key {
				continue
			}
			oldIdentity := intents[id].PluginMeta["identity"]
			if oldIdentity == "" {
				// 旧格式摘要无身份标注：从 source 串归一；不可归一则不结清。
				if raw := intents[id].PluginMeta["source"]; raw != "" {
					if oldSpec, perr := p.parseSpec(raw); perr == nil {
						oldIdentity = pluginIdentity(oldSpec)
					}
				}
			}
			if oldIdentity == "" || oldIdentity != identity {
				continue // 跨源未确认副作用：保留（RegistrationSummary 仍可见）
			}
			_ = p.state.deleteIntent(id)
			_ = p.state.mutate(func(st *StateFile) {
				if st.SettledOperations != nil {
					delete(st.SettledOperations, id)
				}
			})
		}
	}
}

// verifyInstalled 复检：distro settings 登记与实体同时有效才算 installed。
func (p *PluginScope) verifyInstalled(target TargetRef, spec PluginSourceSpec, identity string) (settingsHit bool, facts entityFacts) {
	entries, state := p.readDistroSettingsPackages(target)
	if state != settingsReadable {
		return false, entityFacts{}
	}
	entry := distroEntryForIdentity(entries, identity)
	facts = p.probeEntity(target, spec)
	return entry != nil, facts
}

// ---------------------------------------------------------------------------
// 子资源过滤清单的应用与复检（SA12）
// ---------------------------------------------------------------------------

// stringSlicesEqual 报告两个过滤字段语义相等（三态严格：nil 与空切片不等
// ——nil = 键省略全部加载，空切片 = 显式空数组一项都不加载；元素级相等）。
func stringSlicesEqual(a, b []string) bool {
	if (a == nil) != (b == nil) {
		return false
	}
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// pluginFiltersEqual 逐字段比较两份过滤清单（三态严格：任一字段 nil 与
// 空切片的区别视为不一致——复检面据此把「期望空数组仍是纯字符串/省略」
// 判为形态不符，重放射敛而非冒充等价）。
func pluginFiltersEqual(a, b PluginFilters) bool {
	return stringSlicesEqual(a.Extensions, b.Extensions) &&
		stringSlicesEqual(a.Skills, b.Skills) &&
		stringSlicesEqual(a.Prompts, b.Prompts) &&
		stringSlicesEqual(a.Themes, b.Themes)
}

// filtersAppliedForm 报告登记的过滤形态是否与期望语义等价：期望带过滤项
// → 登记必须是携带同清单的过滤对象；期望无过滤项 → 登记不带任何过滤项
// （纯字符串与无过滤键的对象形态都等价于「全部加载」）。
func filtersAppliedForm(entry *distroPluginEntry, want PluginFilters) bool {
	if entry == nil {
		return false
	}
	if hasFilters(want) {
		return entry.isObject && pluginFiltersEqual(entry.filters, want)
	}
	return !hasFilters(entry.filters)
}

// canonicalPackageEntryJSON 构造 packages[] 元素的规范 JSON（过滤对象或纯
// 字符串源）。手工构造而非 omitempty 结构体序列化：nil 字段键省略（该类型
// 全部加载），非 nil 字段一律写键（含空数组 []——一项都不加载）。
func canonicalPackageEntryJSON(source string, filters PluginFilters) string {
	if !hasFilters(filters) {
		b, _ := json.Marshal(source)
		return string(b)
	}
	src, _ := json.Marshal(source)
	var b strings.Builder
	b.WriteString(`{"source":`)
	b.Write(src)
	writeFilterField(&b, "extensions", filters.Extensions)
	writeFilterField(&b, "skills", filters.Skills)
	writeFilterField(&b, "prompts", filters.Prompts)
	writeFilterField(&b, "themes", filters.Themes)
	b.WriteString("}")
	return b.String()
}

// writeFilterField 追加一个过滤键：vals 为 nil 时省略键（全部加载）；非 nil
// 时写数组（含空数组 []——一项都不加载，不得折叠回省略）。
func writeFilterField(b *strings.Builder, name string, vals []string) {
	if vals == nil {
		return
	}
	b.WriteString(`,"` + name + `":[`)
	for i, v := range vals {
		if i > 0 {
			b.WriteString(",")
		}
		enc, _ := json.Marshal(v)
		b.Write(enc)
	}
	b.WriteString("]")
}

// opFiltersOrZero 解引用操作过滤快照（nil = 未提供 → 零值全量形态；install
// 语义：安装即定义终态，无「沿用」一说）。
func opFiltersOrZero(op PluginOperation) PluginFilters {
	if op.Filters == nil {
		return PluginFilters{}
	}
	return *op.Filters
}

// applyDistroFilters 把 distro settings.json 中 identity 对应的登记改写为与
// filters 等价的 Pi 原生形态（SA12）。攻坚契约 PR07：在读后、解析后、tmp 写
// 前、tmp 完成后、最终移动/发布前检查 ctx——取消清本次 tmp，不改原 settings；
// 若已提交才取消，返回 filter-committed/verification-pending（由调用方映射），
// 不声称零写。最终发布使用统一文件提交/恢复原语（插件传入内部提交回调，
// 禁止另造 tmp+无条件 rename 旁路）。
func (p *PluginScope) applyDistroFilters(ctx context.Context, target TargetRef, identity, source string, filters PluginFilters) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("filter apply canceled: %w", err)
	}
	path := filepath.Join(target.UNCRoot, "settings.json")
	data, err := os.ReadFile(path) // 读后检查
	if err != nil {
		return fmt.Errorf("settings.json 读取失败: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("filter apply canceled after read: %w", err)
	}
	items := gjson.GetBytes(data, "packages").Array()
	idx := -1
	for i, e := range items {
		var src string
		if e.Type == gjson.String {
			src = strings.TrimSpace(e.String())
		} else if e.IsObject() {
			src = strings.TrimSpace(e.Get("source").String())
		}
		if src == "" {
			continue
		}
		spec := PluginSourceSpec{}
		if p.parser != nil {
			spec = p.parser(src)
		}
		if pluginIdentity(spec) == identity {
			idx = i
			break
		}
	}
	if idx < 0 {
		return errors.New("登记不存在，无法应用过滤形态")
	}
	want := canonicalPackageEntryJSON(source, filters)
	if items[idx].Raw == want {
		return nil
	}
	next, err := sjson.SetRawBytes(data, "packages."+strconv.Itoa(idx), []byte(want))
	if err != nil {
		return fmt.Errorf("settings.json 过滤对象写入失败: %w", err)
	}
	if err := ctx.Err(); err != nil { // 解析后检查
		return fmt.Errorf("filter apply canceled after parse: %w", err)
	}
	// 统一提交回调：settings.json 是普通（非敏感）文件；CAS 基准 = 本次读取
	// 的原始字节（发布前重读仍须等于构建基准；packages 外字段保留）。
	if p.commitFileFn == nil {
		return errors.New("filter commit callback unavailable")
	}
	rcpt := p.commitFileFn(ctx, target, "settings.json", hexHash(data), next, filtersMeta(source, filters))
	switch {
	case rcpt == nil:
		return errors.New("filter commit produced no receipt")
	case rcpt.Err == nil:
		return nil
	case rcpt.ErrDetail == "filter-committed-verification-pending":
		return rcpt.Err
	default:
		return fmt.Errorf("settings.json 提交失败: %w", rcpt.Err)
	}
}

// filtersMeta 构造插件操作摘要的脱敏元数据。
func filtersMeta(source string, filters PluginFilters) map[string]string {
	meta := map[string]string{"source": source}
	if filters.Extensions != nil {
		meta["filters.extensions"] = "[]" // 只记形态，不展开清单
	}
	if filters.Skills != nil {
		meta["filters.skills"] = "[]"
	}
	if filters.Prompts != nil {
		meta["filters.prompts"] = "[]"
	}
	if filters.Themes != nil {
		meta["filters.themes"] = "[]"
	}
	return meta
}

// verifyDistroFilters 复检 identity 对应登记的过滤形态与期望一致（SA12：
// 复检必须覆盖过滤集——settings 里是纯字符串全量形态而期望带过滤时不算通过）。
func (p *PluginScope) verifyDistroFilters(target TargetRef, identity string, want PluginFilters) (bool, string) {
	entries, state := p.readDistroSettingsPackages(target)
	if state != settingsReadable {
		return false, "settings.json 不可读，无法复检过滤形态"
	}
	entry := distroEntryForIdentity(entries, identity)
	if filtersAppliedForm(entry, want) {
		return true, ""
	}
	if hasFilters(want) {
		return false, "登记过滤对象与期望清单不一致（或仍为全量字符串形态）"
	}
	return false, "登记仍带过滤对象，未回到全量形态"
}

// replay 执行 pi CLI 子命令并分类结果。子命令是本包内固定字面量（install/
// remove/update，不转义），包名/源串全部单引号转义。
func (p *PluginScope) replay(ctx context.Context, distro string, args ...string) (string, string) {
	if len(args) == 0 {
		return PluginErrReplayFailed, "pi replay: missing subcommand"
	}
	script := "pi " + args[0]
	for _, a := range args[1:] {
		script += " " + pluginShQuote(a)
	}
	out, err := p.runScript(ctx, distro, script)
	if err != nil {
		code := classifyRunnerError(ctx, err)
		if code == PluginErrReplayFailed {
			return code, "pi " + args[0] + " 失败: " + truncateDetail(out)
		}
		return code, "pi " + args[0] + " " + mapLifecycleCodeDetail(code)
	}
	return "", out
}

func mapLifecycleCodeDetail(code string) string {
	switch code {
	case PluginErrTimeout:
		// SA16：如实报告进程边界——终止的是 Windows 侧 wsl.exe；distro 内
		// 子进程是否随之退出未经真机验证，不宣称已清理。
		return "执行超时：Windows 侧命令已终止（distro 内子进程是否随之退出未验证；单次尝试，不自动重试）"
	case PluginErrCanceled:
		return "已取消：Windows 侧命令已终止（distro 内子进程是否随之退出未验证）"
	default:
		return "失败"
	}
}

// runInstall 安装重放：
//   - distro 已有本地登记（非本系统）→ unmanaged-existing 阻断（不自动接管）；
//   - 已由本系统登记且复检通过、锚点一致 → already-ok（幂等，零命令）；
//   - 锚点（宿主解析或 distro 内解析）→ 固定版本/commit 重放 `pi install`；
//   - 成功且复检（settings+实体）通过才写 Installed 登记；失败绝不写已安装。
func (p *PluginScope) runInstall(ctx context.Context, target TargetRef, op PluginOperation, spec PluginSourceSpec, identity string, reg PluginStateEntry, hasReg bool, distroEntry *distroPluginEntry, settingsState settingsReadState, opID string) PluginOpResult {
	blocked := func(errCode, detail string) PluginOpResult {
		return PluginOpResult{Source: op.Source, Kind: PluginOpInstall, Outcome: PluginOutcomeBlocked, ErrCode: errCode, Detail: detail, OpID: opID}
	}
	fail := func(errCode, detail string) PluginOpResult {
		// 从未登记的失败绝不落登记——否则 mutateEntry 会以空 Identity/Source
		// 追加幽灵登记行（Probe 漂移环 + Summary 计数污染，且无法清除）。
		if hasReg {
			_ = p.savePluginEntry(p.mutateEntry(reg, func(e *PluginStateEntry) {
				e.OpID, e.LastOp, e.Installed, e.EntityVerified = opID, PluginOpInstall, false, false
				e.LastErrorCode, e.LastDetail = errCode, truncateDetail(detail)
			}))
		}
		return PluginOpResult{Source: op.Source, Kind: PluginOpInstall, Outcome: PluginOutcomeFailed, ErrCode: errCode, Detail: detail, OpID: opID}
	}

	if distroEntry != nil && !hasReg {
		return blocked(PluginErrUnmanagedExisting,
			"WSL 侧已有该插件的本地登记（非本系统管理），不自动接管；如需接管请先在 WSL 内处理")
	}
	if settingsState == settingsMissing && distroEntry == nil && hasReg && reg.Installed {
		// settings 缺失但本系统登记称已安装：单向漂移，按登记/实体不一致阻断
		//（不可读/损坏已在 RunPluginOperation 入口统一阻断）。
		return blocked(PluginErrRegistrationMismatch, "WSL 侧 settings.json 缺失，无法复检既有登记（登记/实体不一致）")
	}

	// pi CLI 可用性（重放前置；显式操作允许唤醒 distro）。
	if _, err := p.piAvailable(ctx, target); err != nil {
		return blocked(PluginErrCLIMissing, err.Error())
	}

	// 锚点解析：宿主锚点优先；否则 distro 内解析（npm view / git ls-remote）。
	pinned, refKind := op.AnchorRef, ""
	resolveNote := ""
	if pinned == "" {
		resolved, rerr := p.resolveRefForInstall(ctx, target, spec)
		if rerr != nil {
			return blocked(PluginErrSourceUnresolvable, "来源不可解析（"+rerr.Error()+"）；该插件操作已冻结诊断，不影响其他插件")
		}
		pinned, refKind, resolveNote = resolved.ref, resolved.kind, resolved.note
	} else {
		refKind = refKindFor(spec, op.AnchorRef)
	}
	replaySource := pinnedSourceString(spec, pinned)

	// 幂等：已登记安装且复检通过且锚点与过滤形态一致 → already-ok（不执行
	// 任何命令）。SA12：过滤形态纳入幂等条件——Windows 侧过滤清单变化时同版
	// 本重放不再 already-ok，而是走重放路径重建过滤对象后复检。
	if hasReg && reg.Installed && distroEntry != nil {
		facts := p.probeEntity(target, spec)
		if facts.present && p.refMatches(spec, facts, pinned) && reg.PinnedRef == pinned &&
			filtersAppliedForm(distroEntry, opFiltersOrZero(op)) {
			return PluginOpResult{Source: op.Source, Kind: PluginOpInstall, Outcome: PluginOutcomeAlreadyOK,
				Detail: "同版本已安装（幂等命中，未执行重放命令）", OpID: opID,
				PinnedRef: pinned, RefKind: refKind, EntityVerified: true, OK: true}
		}
	}

	// 版本切换（同身份不同源串且旧登记属于本系统）：先移除旧登记串，防双引用。
	// git 实体与 runUpdate 同受脏仓库冻结保护——切换路径（remove+install）
	// 不得旁路 gitDirtyCheck。
	if hasReg && distroEntry != nil && distroEntry.source != replaySource {
		if spec.Type == "git" {
			dirty, summary, derr := p.gitDirtyCheck(ctx, target, spec)
			if derr != nil {
				_ = p.savePluginEntry(p.mutateEntry(reg, func(e *PluginStateEntry) {
					e.OpID, e.LastOp = opID, PluginOpInstall
					e.Frozen, e.FrozenReason = true, PluginErrGitStatusUnavailable
				}))
				return blocked(PluginErrGitStatusUnavailable, "无法确认 git 实体工作区状态（"+derr.Error()+"）；版本切换已冻结")
			}
			if dirty {
				_ = p.savePluginEntry(p.mutateEntry(reg, func(e *PluginStateEntry) {
					e.OpID, e.LastOp = opID, PluginOpInstall
					e.Frozen, e.FrozenReason = true, PluginErrDirtyRepo
				}))
				return blocked(PluginErrDirtyRepo,
					"WSL 侧 git 插件仓库有未提交修改（"+summary+" 等）；版本切换已冻结，绝不自动 reset/clean")
			}
		}
		if code, detail := p.replay(ctx, target.Distro, "remove", reg.Source); code != "" {
			return fail(code, "切换版本前移除旧登记失败: "+detail)
		}
	}

	if code, detail := p.replay(ctx, target.Distro, "install", replaySource); code != "" {
		return fail(code, detail)
	}

	// SA12：按 Pi 原生 packages 语义应用过滤清单（过滤对象/纯字符串形态），
	// 应用或复检失败绝不报已安装——实体已落盘时如实报告失败阶段。
	if err := p.applyDistroFilters(ctx, target, identity, replaySource, opFiltersOrZero(op)); err != nil {
		return fail(PluginErrFilterApplyFailed, "过滤清单应用失败: "+err.Error())
	}

	// 复检：settings 登记 + 实体同时有效（失败绝不写已安装）。
	settingsHit, facts := p.verifyInstalled(target, spec, identity)
	if !settingsHit || !facts.present {
		return fail(PluginErrVerifyFailed, "重放命令成功但复检未通过（settings 登记或实体缺失）")
	}
	if ok, why := p.verifyDistroFilters(target, identity, opFiltersOrZero(op)); !ok {
		return fail(PluginErrVerifyFailed, "重放命令成功但过滤清单复检未通过（"+why+"）")
	}
	if spec.Type == "npm" && facts.version != "" && facts.version != pinned {
		return fail(PluginErrVerifyFailed, fmt.Sprintf("实体版本 %q 与锚定版本 %q 不一致", facts.version, pinned))
	}
	if spec.Type == "git" && refKind == string(PluginRefGitCommit) && facts.commit != "" && facts.commit != pinned {
		return fail(PluginErrVerifyFailed, fmt.Sprintf("实体 HEAD 与锚定 commit 不一致"))
	}
	entry := PluginStateEntry{
		Source: replaySource, Identity: identity, SourceType: spec.Type, Generation: target.Generation,
		PinnedRef: pinned, RefKind: PluginRefKind(refKind), Filters: opFiltersOrZero(op),
		OpID: opID, LastOp: PluginOpInstall, Installed: true, EntityVerified: true,
		LastSuccess: time.Now(),
	}
	if resolveNote != "" {
		entry.LastDetail = truncateDetail(resolveNote)
	}
	if err := p.savePluginEntry(entry); err != nil {
		return PluginOpResult{Source: op.Source, Kind: PluginOpInstall, Outcome: PluginOutcomeFailed,
			ErrCode: PluginErrRegistrationUnchanged, Detail: "重放成功但登记落盘失败（下次操作将重新复检）", OpID: opID,
			PinnedRef: pinned, RefKind: refKind, EntityVerified: true}
	}
	p.invalidateSnapshot(target.Distro)
	detail := "已按固定 " + refKindLabel(refKind) + " " + pinned + " 原生安装并复检通过"
	if hasFilters(opFiltersOrZero(op)) {
		detail += "（含子资源过滤对象，已按 Pi 原生语义写入并复检）"
	}
	if !generationBindable(target.Generation) {
		// SA-02 插件面：身份代未知时不落持久登记（下次操作按本地项对待）。
		detail += "；目标身份代未知，登记未持久化"
	}
	if resolveNote != "" {
		detail += "（" + resolveNote + "）"
	}
	return PluginOpResult{Source: op.Source, Kind: PluginOpInstall, Outcome: PluginOutcomeInstalled,
		Detail: detail, OpID: opID, PinnedRef: pinned, RefKind: refKind, EntityVerified: true, OK: true}
}

// refKindFor 判定锚点 ref 的形态标签。
func refKindFor(spec PluginSourceSpec, ref string) string {
	if spec.Type == "npm" {
		return string(PluginRefNpmVersion)
	}
	if isFullCommitSHALocal(ref) {
		return string(PluginRefGitCommit)
	}
	return string(PluginRefSymbolic)
}

// isFullCommitSHA 本地判定（接缝缺省时的兜底；生产面经 refCheck 注入）。
func isFullCommitSHALocal(ref string) bool { return pluginSHARe.MatchString(strings.TrimSpace(ref)) }

type resolvedRef struct {
	ref  string
	kind string
	note string
}

// resolveRefForInstall 在 distro 内解析重放锚点：npm → npm view 确切版本；
// git → ls-remote 固定 commit（解析失败时降级符号 ref 并如实标注）。
func (p *PluginScope) resolveRefForInstall(ctx context.Context, target TargetRef, spec PluginSourceSpec) (resolvedRef, error) {
	switch spec.Type {
	case "npm":
		if p.refCheck.ExactNpmVersion != nil && p.refCheck.ExactNpmVersion(spec.Ref) {
			return resolvedRef{ref: spec.Ref, kind: string(PluginRefNpmVersion)}, nil
		}
		ver, err := p.resolveNpmVersion(ctx, target, spec.Name)
		if err != nil {
			return resolvedRef{}, err
		}
		return resolvedRef{ref: ver, kind: string(PluginRefNpmVersion),
			note: "源未带版本，已在 distro 内解析确切版本 " + ver}, nil
	case "git":
		if isFullCommitSHALocal(spec.Ref) || (p.refCheck.FullCommitSHA != nil && p.refCheck.FullCommitSHA(spec.Ref)) {
			return resolvedRef{ref: strings.ToLower(spec.Ref), kind: string(PluginRefGitCommit)}, nil
		}
		sha, err := p.resolveGitCommit(ctx, target, spec)
		if err != nil {
			if spec.Ref == "" {
				return resolvedRef{}, err
			}
			// 符号 ref 解析失败：降级为符号 ref 重放（如实标注非 commit 固定）。
			return resolvedRef{ref: spec.Ref, kind: string(PluginRefSymbolic),
				note: "commit 解析失败，按符号 ref 重放（" + err.Error() + "）"}, nil
		}
		return resolvedRef{ref: sha, kind: string(PluginRefGitCommit),
			note: "已固定 commit " + sha}, nil
	default:
		return resolvedRef{}, errors.New("本地源不支持")
	}
}

// refMatches 复检锚点一致性（npm 比版本、git 比 HEAD）。
func (p *PluginScope) refMatches(spec PluginSourceSpec, facts entityFacts, pinned string) bool {
	switch spec.Type {
	case "npm":
		return facts.version == pinned
	case "git":
		if isFullCommitSHALocal(pinned) {
			return facts.commit == pinned
		}
		return true // 符号 ref：实体在即视为命中（形态如实标注）
	default:
		return false
	}
}

// mutateEntry 在既有登记上执行字段级修改（保留身份/来源历史），返回新登记。
// 仅用于已登记（hasReg）路径——从未登记的操作失败不落登记（防幽灵行）。
func (p *PluginScope) mutateEntry(prev PluginStateEntry, fn func(*PluginStateEntry)) PluginStateEntry {
	entry := prev
	fn(&entry)
	return entry
}

func refKindLabel(kind string) string {
	switch PluginRefKind(kind) {
	case PluginRefNpmVersion:
		return "npm 版本"
	case PluginRefGitCommit:
		return "git commit"
	default:
		return "符号 ref"
	}
}

// runUpdate 更新重放（只作用于本系统登记管理的插件）：
//   - 无本系统登记 → not-managed 阻断（WSL 本地手动项不动）；
//   - distro 登记已丢失（用户在 WSL 内移除）→ 登记漂移阻断（不擅自重装）；
//   - git 实体脏（未提交修改）→ 冻结报告，绝不 reset/clean；
//   - 锚点（宿主解析优先，否则 distro 内解析）与当前一致 → already-ok；
//   - 不一致 → remove 旧登记串 + install 固定锚点（宿主一次 update 不等价
//     为目标再次追 latest：重放始终携带确切版本/ref）。
func (p *PluginScope) runUpdate(ctx context.Context, target TargetRef, op PluginOperation, spec PluginSourceSpec, identity string, reg PluginStateEntry, hasReg bool, distroEntry *distroPluginEntry, settingsState settingsReadState, opID string) PluginOpResult {
	blocked := func(errCode, detail string) PluginOpResult {
		return PluginOpResult{Source: op.Source, Kind: PluginOpUpdate, Outcome: PluginOutcomeBlocked, ErrCode: errCode, Detail: detail, OpID: opID}
	}
	if !hasReg {
		return blocked(PluginErrNotManaged, "仅本系统登记管理的插件可更新；WSL 本地手动项不动")
	}
	if distroEntry == nil {
		return blocked(PluginErrRegistrationMismatch,
			"WSL 侧 settings 登记已不存在（可能在 WSL 内被手动移除）；登记/实体不一致，请先检查")
	}

	// 冻结与保护（git 实体脏仓库；git status 不可用按保守冻结）。
	if spec.Type == "git" {
		dirty, summary, derr := p.gitDirtyCheck(ctx, target, spec)
		if derr != nil {
			_ = p.savePluginEntry(p.mutateEntry(reg, func(e *PluginStateEntry) {
				e.OpID, e.LastOp = opID, PluginOpUpdate
				e.Frozen, e.FrozenReason = true, PluginErrGitStatusUnavailable
			}))
			return blocked(PluginErrGitStatusUnavailable, "无法确认 git 实体工作区状态（"+derr.Error()+"）；update 已冻结")
		}
		if dirty {
			_ = p.savePluginEntry(p.mutateEntry(reg, func(e *PluginStateEntry) {
				e.OpID, e.LastOp = opID, PluginOpUpdate
				e.Frozen, e.FrozenReason = true, PluginErrDirtyRepo
			}))
			return blocked(PluginErrDirtyRepo,
				"WSL 侧 git 插件仓库有未提交修改（"+summary+" 等）；update 已冻结，绝不自动 reset/clean")
		}
	}

	if _, err := p.piAvailable(ctx, target); err != nil {
		return blocked(PluginErrCLIMissing, err.Error())
	}

	// 锚点：宿主解析优先（同时应用模式）；否则 distro 内解析。
	pinned, refKind := op.AnchorRef, ""
	if pinned == "" {
		resolved, rerr := p.resolveRefForInstall(ctx, target, spec)
		if rerr != nil {
			return blocked(PluginErrSourceUnresolvable, "来源不可解析（"+rerr.Error()+"）；该插件操作已冻结诊断，不影响其他插件")
		}
		pinned, refKind = resolved.ref, resolved.kind
	} else {
		refKind = refKindFor(spec, op.AnchorRef)
	}

	// 生效过滤清单（PR08）：op.Filters 为 nil（未提供）时沿用既有登记；
	// 非 nil（含全字段 nil）是显式完整期望——可把旧过滤集重置回全量加载，
	// 与 Windows 当前登记期望一致。
	filters := reg.Filters
	if op.Filters != nil {
		filters = *op.Filters
	}

	// 幂等：锚点与登记/实体一致且过滤形态一致 → already-ok（SA12：过滤形态
	// 纳入幂等条件，清单变化时重放重建过滤对象）。
	facts := p.probeEntity(target, spec)
	if reg.Installed && facts.present && p.refMatches(spec, facts, pinned) && reg.PinnedRef == pinned &&
		filtersAppliedForm(distroEntry, filters) {
		return PluginOpResult{Source: op.Source, Kind: PluginOpUpdate, Outcome: PluginOutcomeAlreadyOK,
			Detail: "已处于锚定 " + pinned + "（幂等命中，未执行重放命令）", OpID: opID,
			PinnedRef: pinned, RefKind: refKind, EntityVerified: true, OK: true}
	}

	replaySource := pinnedSourceString(spec, pinned)
	if distroEntry.source != replaySource {
		if code, detail := p.replay(ctx, target.Distro, "remove", distroEntry.source); code != "" {
			_ = p.savePluginEntry(p.mutateEntry(reg, func(e *PluginStateEntry) {
				e.OpID, e.LastOp, e.Installed, e.EntityVerified = opID, PluginOpUpdate, false, false
				e.LastErrorCode, e.LastDetail = code, truncateDetail("更新前移除旧登记失败: "+detail)
			}))
			return PluginOpResult{Source: op.Source, Kind: PluginOpUpdate, Outcome: PluginOutcomeFailed,
				ErrCode: code, Detail: "更新前移除旧登记失败: " + detail, OpID: opID}
		}
	}
	if code, detail := p.replay(ctx, target.Distro, "install", replaySource); code != "" {
		_ = p.savePluginEntry(p.mutateEntry(reg, func(e *PluginStateEntry) {
			e.OpID, e.LastOp, e.Installed, e.EntityVerified = opID, PluginOpUpdate, false, false
			e.LastErrorCode, e.LastDetail = code, truncateDetail(detail)
		}))
		return PluginOpResult{Source: op.Source, Kind: PluginOpUpdate, Outcome: PluginOutcomeFailed,
			ErrCode: code, Detail: detail + "（实体保留；重试将重新安装）", OpID: opID}
	}

	// SA12：按 Pi 原生语义应用生效过滤清单并复检（失败绝不写已安装）。
	if err := p.applyDistroFilters(ctx, target, identity, replaySource, filters); err != nil {
		_ = p.savePluginEntry(p.mutateEntry(reg, func(e *PluginStateEntry) {
			e.OpID, e.LastOp, e.Installed, e.EntityVerified = opID, PluginOpUpdate, false, false
			e.LastErrorCode, e.LastDetail = PluginErrFilterApplyFailed, truncateDetail("过滤清单应用失败: "+err.Error())
		}))
		return PluginOpResult{Source: op.Source, Kind: PluginOpUpdate, Outcome: PluginOutcomeFailed,
			ErrCode: PluginErrFilterApplyFailed, Detail: "过滤清单应用失败: " + err.Error() + "（实体已更新；重试将重新应用）", OpID: opID}
	}

	settingsHit, facts2 := p.verifyInstalled(target, spec, identity)
	if !settingsHit || !facts2.present {
		_ = p.savePluginEntry(p.mutateEntry(reg, func(e *PluginStateEntry) {
			e.OpID, e.LastOp, e.Installed, e.EntityVerified = opID, PluginOpUpdate, false, false
			e.LastErrorCode, e.LastDetail = PluginErrVerifyFailed, "复检未通过"
		}))
		return PluginOpResult{Source: op.Source, Kind: PluginOpUpdate, Outcome: PluginOutcomeFailed,
			ErrCode: PluginErrVerifyFailed, Detail: "重放命令成功但复检未通过（settings 登记或实体缺失）", OpID: opID}
	}
	if spec.Type == "npm" && facts2.version != "" && facts2.version != pinned {
		_ = p.savePluginEntry(p.mutateEntry(reg, func(e *PluginStateEntry) {
			e.OpID, e.LastOp, e.Installed, e.EntityVerified = opID, PluginOpUpdate, false, false
			e.LastErrorCode, e.LastDetail = PluginErrVerifyFailed, "实体版本与锚定不一致"
		}))
		return PluginOpResult{Source: op.Source, Kind: PluginOpUpdate, Outcome: PluginOutcomeFailed,
			ErrCode: PluginErrVerifyFailed, Detail: fmt.Sprintf("实体版本 %q 与锚定版本 %q 不一致", facts2.version, pinned), OpID: opID}
	}
	if ok, why := p.verifyDistroFilters(target, identity, filters); !ok {
		_ = p.savePluginEntry(p.mutateEntry(reg, func(e *PluginStateEntry) {
			e.OpID, e.LastOp, e.Installed, e.EntityVerified = opID, PluginOpUpdate, false, false
			e.LastErrorCode, e.LastDetail = PluginErrVerifyFailed, "过滤清单复检未通过"
		}))
		return PluginOpResult{Source: op.Source, Kind: PluginOpUpdate, Outcome: PluginOutcomeFailed,
			ErrCode: PluginErrVerifyFailed, Detail: "重放命令成功但过滤清单复检未通过（" + why + "）", OpID: opID}
	}
	entry := PluginStateEntry{
		Source: replaySource, Identity: identity, SourceType: spec.Type, Generation: target.Generation,
		PinnedRef: pinned, RefKind: PluginRefKind(refKind), Filters: filters,
		OpID: opID, LastOp: PluginOpUpdate, Installed: true, EntityVerified: true,
		LastSuccess: time.Now(),
	}
	if err := p.savePluginEntry(entry); err != nil {
		return PluginOpResult{Source: op.Source, Kind: PluginOpUpdate, Outcome: PluginOutcomeFailed,
			ErrCode: PluginErrRegistrationUnchanged, Detail: "重放成功但登记落盘失败（下次操作将重新复检）",
			OpID: opID, PinnedRef: pinned, RefKind: refKind, EntityVerified: true}
	}
	p.invalidateSnapshot(target.Distro)
	detail := "已更新到固定 " + refKindLabel(refKind) + " " + pinned + " 并复检通过"
	if hasFilters(filters) {
		detail += "（含子资源过滤对象，已按 Pi 原生语义写入并复检）"
	}
	return PluginOpResult{Source: op.Source, Kind: PluginOpUpdate, Outcome: PluginOutcomeUpdated,
		Detail: detail, OpID: opID,
		PinnedRef: pinned, RefKind: refKind, EntityVerified: true, OK: true}
}

func hasFilters(f PluginFilters) bool {
	// 三态：任一字段非 nil（含空切片——显式空数组也是过滤形态）即携带
	// 过滤；全 nil 才是「无过滤」（纯字符串全量形态）。
	return f.Extensions != nil || f.Skills != nil || f.Prompts != nil || f.Themes != nil
}

// runRemove 移除重放（只作用于本系统登记管理的插件；本地手动项不动）：
// distro 登记已不存在 → 清登记 already-ok；否则 `pi remove` 后复检并删登记。
// 实体目录按 pi 语义保留（重装可用），不删除实体。
func (p *PluginScope) runRemove(ctx context.Context, target TargetRef, op PluginOperation, spec PluginSourceSpec, identity string, reg PluginStateEntry, hasReg bool, distroEntry *distroPluginEntry, settingsState settingsReadState, opID string) PluginOpResult {
	blocked := func(errCode, detail string) PluginOpResult {
		return PluginOpResult{Source: op.Source, Kind: PluginOpRemove, Outcome: PluginOutcomeBlocked, ErrCode: errCode, Detail: detail, OpID: opID}
	}
	if !hasReg {
		return blocked(PluginErrNotManaged, "仅本系统登记管理的插件可移除；WSL 本地手动项不动")
	}
	if distroEntry == nil {
		if err := p.dropPluginEntry(identity, target.Generation); err != nil {
			return PluginOpResult{Source: op.Source, Kind: PluginOpRemove, Outcome: PluginOutcomeFailed,
				ErrCode: PluginErrRegistrationUnchanged, Detail: "登记已不存在但本地登记清理失败", OpID: opID}
		}
		p.invalidateSnapshot(target.Distro)
		return PluginOpResult{Source: op.Source, Kind: PluginOpRemove, Outcome: PluginOutcomeAlreadyOK,
			Detail: "WSL 侧登记已不存在（已清理本系统登记；实体目录按 pi 语义保留）", OpID: opID, OK: true}
	}
	if _, err := p.piAvailable(ctx, target); err != nil {
		return blocked(PluginErrCLIMissing, err.Error())
	}
	if code, detail := p.replay(ctx, target.Distro, "remove", reg.Source); code != "" {
		_ = p.savePluginEntry(p.mutateEntry(reg, func(e *PluginStateEntry) {
			e.OpID, e.LastOp = opID, PluginOpRemove
			e.LastErrorCode, e.LastDetail = code, truncateDetail(detail)
		}))
		return PluginOpResult{Source: op.Source, Kind: PluginOpRemove, Outcome: PluginOutcomeFailed,
			ErrCode: code, Detail: detail, OpID: opID}
	}
	// 复检：settings 登记应已消失（实体保留）。
	entries, _ := p.readDistroSettingsPackages(target)
	if distroEntryForIdentity(entries, identity) != nil {
		return PluginOpResult{Source: op.Source, Kind: PluginOpRemove, Outcome: PluginOutcomeFailed,
			ErrCode: PluginErrVerifyFailed, Detail: "移除命令成功但 settings 登记仍存在", OpID: opID}
	}
	if err := p.dropPluginEntry(identity, target.Generation); err != nil {
		return PluginOpResult{Source: op.Source, Kind: PluginOpRemove, Outcome: PluginOutcomeFailed,
			ErrCode: PluginErrRegistrationUnchanged, Detail: "移除成功但本地登记清理失败", OpID: opID}
	}
	p.invalidateSnapshot(target.Distro)
	return PluginOpResult{Source: op.Source, Kind: PluginOpRemove, Outcome: PluginOutcomeRemoved,
		Detail: "已从 WSL settings 移除登记（实体目录按 pi 语义保留，重装可用）", OpID: opID, OK: true}
}

// ---------------------------------------------------------------------------
// 探测快照（显式检查允许唤醒；缓存面只读不唤醒）
// ---------------------------------------------------------------------------

// invalidateSnapshot 使缓存快照失效（操作成功后调用；下次 Probe 重建）。
func (p *PluginScope) invalidateSnapshot(distro string) {
	p.snapMu.Lock()
	delete(p.snaps, distro)
	p.snapMu.Unlock()
}

// Probe 显式检查：解析目标 → pi 可用性 → 读 distro settings + 实体（UNC）
// → 汇总登记面 → 缓存快照。允许唤醒当前目标（显式动作）；只读，不写任何
// 目标内容。
func (p *PluginScope) Probe(ctx context.Context, target TargetRef) WSLPluginSnapshot {
	if err := ctx.Err(); err != nil {
		// SA16：已取消的探测不进入命令面，如实标注取消原因。
		code := classifyRunnerError(ctx, err)
		return WSLPluginSnapshot{Distro: target.Distro, Available: false,
			Reason: mapLifecycleCodeDetail(code), Items: []WSLPluginItem{}}
	}
	snap := WSLPluginSnapshot{Distro: target.Distro, Available: true, Items: []WSLPluginItem{}}
	piResolve, piErr := p.piAvailable(ctx, target)
	if piErr != nil {
		snap.PIResolve = ""
		snap.Reason = piErr.Error()
	}
	snap.PIResolve = piResolve
	entries, settingsState := p.readDistroSettingsPackages(target)
	switch settingsState {
	case settingsMissing:
		snap.Reason = joinReason(snap.Reason, "WSL 侧 settings.json 尚不存在（agent 根可能尚未初始化）")
	case settingsUnreadable:
		snap.Reason = joinReason(snap.Reason, "WSL 侧 settings.json 不可读或已损坏")
	}
	seenIdentity := map[string]bool{}
	for i := range entries {
		e := &entries[i]
		identity := pluginIdentity(e.spec)
		// 本地路径/不可解析项（含降级 parser 零值）不参与重放语义，跳过。
		if identity == "local:" || identity == "" {
			continue
		}
		if seenIdentity[identity] {
			continue
		}
		seenIdentity[identity] = true
		st := p.state.load()
		reg, hasReg := p.pluginEntryByIdentity(&st, identity, target.Generation)
		facts := p.probeEntity(target, e.spec)
		item := WSLPluginItem{
			Source: e.source, Identity: identity, SourceType: e.spec.Type,
			Version: facts.version, Commit: facts.commit,
			SettingsPresent: true, EntityPresent: facts.present,
			EntityPath: entityLinuxPath(target, e.spec),
			PinnedRef:  reg.PinnedRef, RefKind: string(reg.RefKind),
			Frozen: reg.Frozen, FrozenReason: reg.FrozenReason,
			LastOp: reg.LastOp, LastErrorCode: reg.LastErrorCode,
		}
		switch {
		case !hasReg:
			item.Managed, item.State = false, PluginItemLocal
			item.Detail = "WSL 本地项（非本系统登记管理，操作不动它）"
		case reg.Frozen:
			item.Managed, item.State = true, PluginItemFrozen
			item.Detail = "已冻结（" + frozenReasonText(reg.FrozenReason) + "）"
		case !facts.present:
			item.Managed, item.State = true, PluginItemMismatch
			item.Detail = "登记/实体不一致：settings 有登记但实体目录缺失"
		default:
			item.Managed, item.State = true, PluginItemInstalled
			item.Detail = "已安装（settings 登记与实体同时有效）"
		}
		snap.Items = append(snap.Items, item)
	}
	// 登记存在但 distro settings 已无对应项（单向漂移）。settings 不可读时
	// 无法确认「已消失」——跳过 reg-only 环（复审 F1：读不到≠已消失，展示腿与
	// R4 操作腿同一原则；快照 Reason 已标注不可读）。SA-02：登记必须可归属到
	// 当前目标（双方代际已知且相等）才展示为登记面漂移，身份代未知不跨目标
	// 继承登记。
	st := p.state.load()
	if settingsState != settingsUnreadable {
		for _, reg := range st.Plugins {
			if !generationBindable(reg.Generation) || reg.Generation != target.Generation {
				continue
			}
			if seenIdentity[reg.Identity] {
				continue
			}
			spec := PluginSourceSpec{}
			if p.parser != nil {
				spec = p.parser(reg.Source)
			}
			snap.Items = append(snap.Items, WSLPluginItem{
				Source: reg.Source, Identity: reg.Identity, SourceType: reg.SourceType,
				Managed: true, State: PluginItemRegisteredMissed,
				Detail:     "本系统登记存在，但 WSL settings 已无此项（登记/实体不一致）",
				EntityPath: entityLinuxPath(target, spec),
				PinnedRef:  reg.PinnedRef, RefKind: string(reg.RefKind),
				Frozen: reg.Frozen, FrozenReason: reg.FrozenReason,
				LastOp: reg.LastOp, LastErrorCode: reg.LastErrorCode,
			})
		}
	}
	sort.Slice(snap.Items, func(i, j int) bool { return snap.Items[i].Identity < snap.Items[j].Identity })
	for _, it := range snap.Items {
		if it.Managed {
			snap.Managed++
		} else {
			snap.Local++
		}
		switch it.State {
		case PluginItemInstalled:
			snap.Installed++
		case PluginItemMismatch, PluginItemRegisteredMissed:
			snap.Mismatched++
		}
	}
	snap.Checked = true
	snap.CheckedAt = time.Now().Format(time.RFC3339)
	p.snapMu.Lock()
	p.snaps[target.Distro] = &snap
	p.snapMu.Unlock()
	return snap
}

func joinReason(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	default:
		return a + "；" + b
	}
}

func entityLinuxPath(target TargetRef, spec PluginSourceSpec) string {
	rel := entityRelPath(spec)
	if rel == "" {
		return ""
	}
	return strings.TrimRight(target.LinuxRoot, "/") + "/" + rel
}

func frozenReasonText(reason string) string {
	switch reason {
	case PluginErrDirtyRepo:
		return "git 仓库有未提交修改，update 冻结（绝不 reset/clean）"
	case PluginErrGitStatusUnavailable:
		return "git 状态不可确认，update 保守冻结"
	default:
		return reason
	}
}

// CachedSnapshot 返回缓存快照（不唤醒 distro；从未检查时 Checked=false）。
func (p *PluginScope) CachedSnapshot(distro string) WSLPluginSnapshot {
	p.snapMu.Lock()
	defer p.snapMu.Unlock()
	if s, ok := p.snaps[distro]; ok && s != nil {
		return *s
	}
	return WSLPluginSnapshot{Distro: distro, Items: []WSLPluginItem{}}
}

// RegistrationSummary 从状态清单派生登记摘要（只读缓存，不唤醒；启动路径的
// 「缺失插件」warning 数据源——只提示，不自动安装）。SA-02：只统计可归属到
// 当前代际的登记（目标/登记代际未知时不跨目标继承计数，诚实归零）。
func (p *PluginScope) RegistrationSummary(generation string) PluginRegistrationSummary {
	st := p.state.load()
	out := PluginRegistrationSummary{}
	// 未决插件摘要优先面（不依赖 generation 绑定）：native 副作用可能已发生
	//（重启不自动重放）。
	if intents, err := p.state.loadIntents(); err == nil {
		for id := range intents {
			op := intents[id]
			if op.Kind != OpKindPluginOp {
				continue
			}
			if _, settled := st.SettledOperations[id]; settled {
				continue
			}
			out.PendingOps++
		}
	}
	if !generationBindable(generation) {
		return out
	}
	for _, reg := range st.Plugins {
		if !generationBindable(reg.Generation) || reg.Generation != generation {
			continue
		}
		out.Managed++
		if reg.Installed && reg.EntityVerified {
			out.Installed++
		} else {
			out.Missing++
		}
		if reg.LastErrorCode != "" {
			out.Failed++
		}
		if reg.Frozen {
			out.Frozen++
		}
		if reg.LastSuccess.After(out.LastOpAt) {
			out.LastOpAt = reg.LastSuccess
		}
	}
	if !out.LastOpAt.IsZero() {
		out.LastOpAtRC = out.LastOpAt.Format(time.RFC3339)
	}
	return out
}
