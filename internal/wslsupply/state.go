package wslsupply

// state.go — 供给状态清单（design §七「状态落点与提交」）：保存在 CodeBox
// 配置目录的 wsl-config-supply.json，经项目私有原子写（tmp 0600 + rename），
// 不由前端解析。敏感 hash 仅私有本地存储且不通过状态 API 返回；A2 凭据单元
// 永不持久化任何 hash（凭据 hash 不落盘）。目标身份不能只用 distro 名称：
// generation 变化时旧登记失效（不自动迁移授权，退回未托管/重新确认）。
//
// SA-02（wsl-goal-closure）：登记按「完整身份 generation（含 distro/uid/home/
// machine-id）+ unit」多分区保存——A→B→A 交替不覆盖，克隆 distro（同
// machine-id）互不继承；unknown 代不参与持久授权匹配（无法验证身份的目标
// 不得复用任何历史授权）；v1 旧清单（generation 无法归属到新身份指纹）保守
// 隔离为 legacy——保留在盘上供诊断，但不再被任何新目标匹配（重新同意后继续，
// 不自动扩大旧授权，direction.md 迁移裁定）。

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// stateSchemaVersion 是清单 schema 版本。v2：身份多分区（Targets 索引 +
// per-entry generation 隔离 + v1 记录隔离）。v3（攻坚契约）：写前持久
// OperationIntent（独立 pending 目录）+ SettledOperations 终结墓碑 +
// StateEntry.LastOperationID + LegacyEvidence（v1/v2 线索保真，不盲信）+
// StoreHealth（读错误不得被空默认值覆盖）。
const stateSchemaVersion = 3

// StateFileName 是清单文件名（落在 CodeBox 配置目录）。
const StateFileName = "wsl-config-supply.json"

// legacyGenerationPrefix 是 v1 清单隔离前缀：v1 的 generation 指纹不含
// distro，无法归属到 v2 指纹——隔离后永不与新代匹配，但保留原文供诊断。
const legacyGenerationPrefix = "legacy:"

// StateEntry 是单单元登记信息。SourceHash/LastWrittenHash 仅供普通托管单元
// （TrackedProjection 等）使用；A2 单元恒为空。
type StateEntry struct {
	Unit             UnitID `json:"unit"`
	Policy           Policy `json:"policy"`
	Generation       string `json:"generation,omitempty"`
	SourceRevision   uint64 `json:"sourceRevision,omitempty"`
	SourceHash       string `json:"sourceHash,omitempty"`
	LastWrittenHash  string `json:"lastWrittenHash,omitempty"`
	TransformVersion string `json:"transformVersion,omitempty"`
	// Subs 是键级基准（S2 键级 TrackedProjection：settings 字段 / amagi 扁平
	// 路径 / mcp server）。文件级 SourceHash/LastWrittenHash 仍作为整体可观测
	// 字段保留；逐键判定只读 Subs。
	Subs map[string]SubHash `json:"subs,omitempty"`
	// SelectedKeys 是 S4 选择集（叶名 → 选中）：「仅用户选择的单元导入」的
	// 持久面（claude/mcp server、codex server/profile）。按 generation 生命
	// 周期存续；取消选择 = 来源删除语义（移除仍等于上次写入的托管条目）。
	SelectedKeys map[string]bool `json:"selectedKeys,omitempty"`
	// Detached：用户已选择「保留本地（停跟随）」——后续供给零动作，直到
	// 显式接管解除（design §三解除冻结两操作之一）。
	Detached bool `json:"detached,omitempty"`
	// Consented：per-target 明确同意标记（design §四——视觉清单密钥跨界授权；
	// 按 generation 生命周期存续，distro 重装后自动失效）。
	Consented     bool      `json:"consented,omitempty"`
	Frozen        bool      `json:"frozen,omitempty"`
	FrozenReason  string    `json:"frozenReason,omitempty"`
	LastSuccess   time.Time `json:"lastSuccess,omitempty"`
	LastErrorCode string    `json:"lastErrorCode,omitempty"`
	LastOutcome   string    `json:"lastOutcome,omitempty"`
	// LastOperationID（v3）关联最近确认的操作（仅状态关联，不是终结凭据）。
	LastOperationID string `json:"lastOperationId,omitempty"`
}

// SubHash 是单键登记基准：Source 是来源侧原始值 hash，Written 是上次实际
// 写入目标侧的值 hash（均为规范化 JSON hash，空白/键序差异不算修改）。
type SubHash struct {
	Source  string `json:"source,omitempty"`
	Written string `json:"written,omitempty"`
}

// TargetRecord 是 Targets 索引的单条目标身份记录（SA-02：generation 指纹 →
// distro/uid/home/最近接触时间）。GetStatus 据此把清单中的分区归属到请求的
// distro（不探测；同一 distro 多代共存时取最近接触的一代展示）。
type TargetRecord struct {
	Distro      string            `json:"distro"`
	UID         string            `json:"uid,omitempty"`
	Home        string            `json:"home,omitempty"`
	// LegacyGeneration 是该目标同一次解析导出的旧公式指纹（v1 逐目标核验
	// 用；resolve 时记录，不重新探测）。
	LegacyGeneration string            `json:"legacyGeneration,omitempty"`
	LastSeen    time.Time         `json:"lastSeen"`
	InspectedAt map[CLI]time.Time `json:"inspectedAt,omitempty"`
}

// StateFile 是清单整体。PendingSources 记录保存事件发布的待同步来源与
// revision（MarkDirty 落盘，重启可补漏；S0 不调度）。Targets 是身份索引
// （SA-02）；Units 允许同一 unit 多代分区共存。LegacySensitiveRisk（PR01）
// 是 v1→v2 迁移遗留的敏感副本待确认记录：旧授权不继承，但曾发布的密钥
// 副本不因迁移消失——按 v1 清单顶层 Distro（唯一身份线索）按 distro 归属。
type StateFile struct {
	SchemaVersion   int       `json:"schemaVersion"`
	Generation      string    `json:"generation,omitempty"`
	Distro          string    `json:"distro,omitempty"`
	Home            string    `json:"home,omitempty"`
	LastInspectedAt time.Time `json:"lastInspectedAt,omitempty"`
	// Targets 按 generation 指纹索引目标身份（SA-02）。
	Targets map[string]TargetRecord `json:"targets,omitempty"`
	// LegacySensitiveRisk（PR01）：distro → 曾发布且未确认撤回的敏感单元集。
	// 新代成功写入（副本重新受管）或确认撤回（副本已移除）时清除。
	LegacySensitiveRisk map[string]map[UnitID]bool `json:"legacySensitiveRisk,omitempty"`
	// Plugins 是 P3-S5 插件重放登记（source 键控；重放成功且复检通过才
	// Installed=true）。与 Units 分离：插件操作与文件同步分开授权与状态
	//（design §四），不经 Plan/Apply 单元词汇表——启动链无法触达（结构性
	// 保证「启动不自动安装」）。
	Plugins []PluginStateEntry `json:"plugins,omitempty"`
	// SettledOperations（v3）是已确认操作结论墓碑（OpID → 结论）；与对应
	// StateEntry 更新同次保存。材料清理完成后删除；活跃撤销租用期间保留。
	SettledOperations map[string]SettledOperation `json:"settledOperations,omitempty"`
	// LegacyEvidence（v3）是 v1/v2 迁移线索的保真留存：原 entry 与顶层线索
	// 原文保存，绝不转成 Consented；逐目标核验处置（ResolvedTarget）后解除。
	// v2 的 LegacySensitiveRisk map 原值保留作低可信来源证据，不继续盲信。
	LegacyEvidence []LegacyEvidenceEntry `json:"legacyEvidence,omitempty"`
	// LastInspectedAtByCLI 按 CLI 作用域记录检查时间（F4：跨 CLI 共享单值会
	// 把 pi 作用域的探测时间错配到 shared 面板；v2 起读取改走 Targets 内的
	// per-target per-CLI 时间，本表仅兼容写入）。
	LastInspectedAtByCLI map[CLI]time.Time `json:"lastInspectedAtByCLI,omitempty"`
	DesiredRevision      uint64            `json:"desiredRevision,omitempty"`
	PendingSources       map[string]uint64 `json:"pendingSources,omitempty"`
	// PendingProgress 按 pending 来源累计「标记后已成功」的单元集（F2：来源
	// 的滋养单元跨 RunUnits 作用域，单次运行不足以判定清除；MarkDirty 重置）。
	PendingProgress map[string][]UnitID `json:"pendingProgress,omitempty"`
	Units           []StateEntry        `json:"units"`
}

// PluginRefKind 是重放锚定的 ref 形态（登记中的诚实标注）。
type PluginRefKind string

const (
	// PluginRefNpmVersion：npm 确切语义化版本。
	PluginRefNpmVersion PluginRefKind = "npm-version"
	// PluginRefGitCommit：git 完整 commit SHA。
	PluginRefGitCommit PluginRefKind = "git-commit"
	// PluginRefSymbolic：未能固定为 commit 的符号 ref（tag/分支）。
	PluginRefSymbolic PluginRefKind = "symbolic"
)

// PluginStateEntry 是 P3-S5 插件重放登记（design §四：重放记录 source、
// 确切版本/ref、资源过滤项与操作 ID；重放成功才写「已安装」登记）。
type PluginStateEntry struct {
	// Source 是重放登记使用的源串（remove/update 重放用它调 pi CLI）。
	Source string `json:"source"`
	// Identity 是归一身份键（npm name 或 git host/path）：同包不同 ref 变体
	// 视为同一插件（版本切换语义）。
	Identity   string `json:"identity"`
	SourceType string `json:"sourceType"` // npm / git
	Generation string `json:"generation,omitempty"`
	// PinnedRef / RefKind 是重放锚定的确切版本或 commit（含形态诚实标注）。
	PinnedRef string        `json:"pinnedRef,omitempty"`
	RefKind   PluginRefKind `json:"refKind,omitempty"`
	// Filters 是 Windows 侧登记的子资源过滤清单快照（仅登记；本批不在
	// WSL settings 重建过滤对象）。
	Filters PluginFilters `json:"filters,omitempty"`
	// OpID 是最近一次操作 ID。
	OpID string `json:"opId,omitempty"`
	// LastOp 是最近操作类型（install/update/remove）。
	LastOp string `json:"lastOp,omitempty"`
	// Installed：重放成功且复检（settings 登记 + 实体）通过才为 true；
	// 失败绝不写已安装状态。
	Installed bool `json:"installed,omitempty"`
	// EntityVerified 是最近一次复检的实体结论（Installed 的证据位）。
	EntityVerified bool      `json:"entityVerified,omitempty"`
	LastSuccess    time.Time `json:"lastSuccess,omitempty"`
	// LastErrorCode / LastDetail 是最近失败的结构化摘要（不含凭据/完整输出）。
	LastErrorCode string `json:"lastErrorCode,omitempty"`
	LastDetail    string `json:"lastDetail,omitempty"`
	Frozen        bool   `json:"frozen,omitempty"`
	FrozenReason  string `json:"frozenReason,omitempty"`
}

// PluginFilters 是登记在案的子资源过滤清单（三态表达，SA12 补全）：字段
// nil = 该类型未声明过滤（加载全部）；非 nil 空切片 = 显式空数组 []
// （一项都不加载）。JSON 标签不用 omitempty——空切片必须序列化为 [] 而非
// 省略（省略会与 nil 混淆，落盘后丢三态）；nil 序列化为 null，读回仍为 nil。
type PluginFilters struct {
	Extensions []string `json:"extensions"`
	Skills     []string `json:"skills"`
	Prompts    []string `json:"prompts"`
	Themes     []string `json:"themes"`
}

// SameIdentity 报告两个登记是否同一插件（身份键相等）。
func (p PluginStateEntry) SameIdentity(identity string) bool { return p.Identity == identity }

// LegacyEvidenceEntry 是 v1/v2 迁移线索的保真记录（PR01 攻坚重做）：原
// schema 的 entry（generation/hash/outcome/consented/detached）与顶层线索
// （distro/generation/home）逐字保留；Source 标注来源（v1 = 原始清单、
// v2-derived = v2 迁移派生字段，低可信）。Fingerprint 是旧公式指纹
//（sha256(machine-id|uid|home)，无 distro）——逐目标核验时与
// TargetRef.LegacyGeneration 匹配的候选才是风险归属候选；不匹配的目标
// 不继承风险也不继承授权。ResolvedTarget 非空表示该完整身份已核验处置
//（用户确认 absent 等），不再产生新风险。
type LegacyEvidenceEntry struct {
	ID            string    `json:"id"`
	Source        string    `json:"source"` // "v1" | "v2-derived"
	Unit          UnitID    `json:"unit"`
	SchemaVersion int       `json:"schemaVersion"`
	// 原 entry 线索（保真）。
	EntryGeneration  string `json:"entryGeneration,omitempty"`
	EntryLastHash    string `json:"entryLastWrittenHash,omitempty"`
	EntryOutcome     string `json:"entryOutcome,omitempty"`
	EntryConsented   bool   `json:"entryConsented,omitempty"`
	EntryDetached    bool   `json:"entryDetached,omitempty"`
	Fingerprint      string `json:"fingerprint,omitempty"` // 旧公式指纹（来自 entry generation 原文）
	// 原顶层线索（保真；只算「最近检查」，不算 owner）。
	TopDistro     string `json:"topDistro,omitempty"`
	TopGeneration string `json:"topGeneration,omitempty"`
	TopHome       string `json:"topHome,omitempty"`
	// LowTrust：v2 LegacySensitiveRisk 派生（可能误归属），只作核验线索。
	LowTrust bool `json:"lowTrust,omitempty"`
	// 逐目标处置：完整身份键（generation）→ 处置结论（absent-confirmed 等）。
	Resolved map[string]string `json:"resolved,omitempty"`
}

// TerminalLegacyEvidence 报告该证据是否已是终态（不再重建受管风险）：
// detached（副本归用户）、确切 withdrawn/withdrawn-already-absent（已确认
// 撤回）。withdrawn 前缀匹配不吞并 unpersisted 之类的中间态——必须整串
// 相等才算终态。
func (e LegacyEvidenceEntry) TerminalRiskCleared() bool {
	if e.EntryDetached {
		return true
	}
	switch e.EntryOutcome {
	case "withdrawn", "withdrawn-already-absent", "detached":
		return true
	}
	return false
}

// StoreHealthKind 是存储面的健康分类。
type StoreHealthKind string

const (
	StoreFresh           StoreHealthKind = "fresh"           // main 不存在（首次安装）
	StoreOK              StoreHealthKind = "ok"              // 正常读写
	StoreMissingAfterUse StoreHealthKind = "missing-after-use" // 曾加载后消失（外部删除）
	StoreUnreadable      StoreHealthKind = "unreadable"      // 读错误（权限/I-O）
	StoreCorrupt         StoreHealthKind = "corrupt"         // 解析失败
	StoreUnsupported     StoreHealthKind = "unsupported"     // 更高 schema（禁止降级读取）
)

// StoreHealth 是 main/pending 存储面的显式健康状态（load 显式返回；GetStatus
// 展示，不触发 WSL 探测）。
type StoreHealth struct {
	Main    StoreHealthKind `json:"main"`
	MainErr string          `json:"mainErr,omitempty"`
	Pending StoreHealthKind `json:"pending"` // pending 目录（无 intent 且可读 = ok）
	PendingErr string      `json:"pendingErr,omitempty"`
}

// stateStore 持有清单的内存副本与落盘路径，串行化所有读改写。
type stateStore struct {
	mu      sync.Mutex
	path    string
	configDir string
	current StateFile
	loaded  bool
	health  StoreHealth
}

func newStateStore(configDir string) *stateStore {
	return &stateStore{
		path:      filepath.Join(configDir, StateFileName),
		configDir: configDir,
		health:    StoreHealth{Main: StoreFresh, Pending: StoreOK},
	}
}

// legacySensitiveUnits 是迁移遗留风险需要跟踪的敏感单元集（PR01）：当前
// 只有视觉清单导出（内嵌 api_key 且 per-target 授权）。
var legacySensitiveUnits = map[UnitID]bool{UnitSharedVisionExport: true}

// load 读取清单（不存在时视为空清单；损坏时退回空清单并保留损坏文件不动——
// 「登记信息损坏/丢失时退回未托管」，不因状态库丢失重置 WSL 配置，design §三）。
// PR06：返回锁内深复制的一致快照——调用方在锁外持有/修改副本都不会与
// 并发 mutate 共享任何 slice/map 底层（浅拷贝会让 Plan 的 entryFor 读与
// Apply 的 upsertEntry 写发生真实数据竞争）。
func (s *stateStore) load() StateFile {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadLocked()
}

func (s *stateStore) loadLocked() StateFile {
	if s.loaded {
		return cloneStateFile(s.current)
	}
	s.loaded = true
	s.health = StoreHealth{Main: StoreFresh, Pending: s.health.Pending}
	data, err := os.ReadFile(s.path)
	switch {
	case err != nil && errors.Is(err, os.ErrNotExist):
		// main 不存在：首次安装可识别 fresh（pending 有 intent 时绝非 fresh，
		// 由 pending 扫描面表达；这里只记录 main 状态）。
		return cloneStateFile(s.current)
	case err != nil:
		s.health.Main = StoreUnreadable
		s.health.MainErr = err.Error()
		return cloneStateFile(s.current) // 未初始化的零值清单（不得覆盖原文件，见 mutate）
	}
	var f StateFile
	if jerr := json.Unmarshal(data, &f); jerr != nil {
		s.health.Main = StoreCorrupt
		s.health.MainErr = jerr.Error()
		return cloneStateFile(s.current)
	}
	if f.SchemaVersion > stateSchemaVersion {
		// 旧二进制把更高 schema 当空——禁止降级读取（契约「迁移与回滚」）。
		s.health.Main = StoreUnsupported
		s.health.MainErr = fmt.Sprintf("schema %d > supported %d", f.SchemaVersion, stateSchemaVersion)
		return cloneStateFile(s.current)
	}
	s.health.Main = StoreOK
	s.health.MainErr = ""
	if f.SchemaVersion < stateSchemaVersion {
		migrateLegacyState(&f)
	}
	if f.Units == nil {
		f.Units = []StateEntry{}
	}
	if f.LastInspectedAtByCLI == nil {
		f.LastInspectedAtByCLI = map[CLI]time.Time{}
	}
	if f.PendingProgress == nil {
		f.PendingProgress = map[string][]UnitID{}
	}
	if f.PendingSources == nil {
		f.PendingSources = map[string]uint64{}
	}
	if f.Targets == nil {
		f.Targets = map[string]TargetRecord{}
	}
	if f.SettledOperations == nil {
		f.SettledOperations = map[string]SettledOperation{}
	}
	s.current = f
	return cloneStateFile(f)
}

// Health 返回存储面健康的深复制快照（GetStatus 展示面）。
func (s *stateStore) Health() StoreHealth {
	s.mu.Lock()
	defer s.mu.Unlock()
	h := s.health
	if h.Main == "" {
		h.Main = StoreFresh
	}
	if h.Pending == "" {
		h.Pending = StoreOK
	}
	return h
}

// migrateLegacyState 把 v1/v2 清单保守升级到 v3（内存视图；下次落盘写 v3）。
// v1（schema 1）的 generation 指纹不含 distro 名，无法归属到 v2/v3 身份指纹
// ——全部隔离为 legacy 前缀（永不与新代匹配，保留原文供诊断，不删除任何记
// 录）。顶层 Generation 同步隔离，避免它充当「当前目标」匹配链。插件登记同
// 样隔离（v1 代无法验证属于哪个目标；重新重放/重授权后自然重建）。
//
// PR01 攻坚重做：v1 原 entry 与顶层线索保真保存为 LegacyEvidence（不再把
// 风险盲绑到最后检查的顶层 Distro）。entry 的旧 generation 就是从前代指纹
// 原文（旧公式 sha256(machine-id|uid|home)）——作为 Fingerprint 线索逐
// 目标核验；顶层 Distro/Home 只算「最近检查」线索，不算 owner。终态
// （detached/确切 withdrawn/从未授权且从未发布）不重建受管风险。v2 的
// LegacySensitiveRisk map 是旧修复派生字段（可能误归属）——转低可信证据
// 保留，不继续盲信，也不直接丢弃。
//
// 一致性修正（v2-migration）：v2（schema 2）的 Units/Targets 已用新公式指纹
// （sha256(distro|machine-id|uid|home)，与 v3 同构）逐条登记并可经 Targets 索
// 引归属——已受管记录迁移后必须继续受管，不得隔离成 legacy、不得遗忘成
// user-owned/never-managed。v2 只转换派生风险面；v1 才走整体隔离。
func migrateLegacyState(f *StateFile) {
	if f.SchemaVersion >= 2 {
		migrateV2ToV3(f)
		return
	}
	for i := range f.Units {
		if g := f.Units[i].Generation; g != "" && !strings.HasPrefix(g, legacyGenerationPrefix) {
			f.Units[i].Generation = legacyGenerationPrefix + g
		}
	}
	for i := range f.Plugins {
		if g := f.Plugins[i].Generation; g != "" && !strings.HasPrefix(g, legacyGenerationPrefix) {
			f.Plugins[i].Generation = legacyGenerationPrefix + g
		}
	}
	// v1 敏感 entry → LegacyEvidence（保真 + 隔离授权；不盲绑 distro）。
	for i := range f.Units {
		u := f.Units[i].Unit
		if !legacySensitiveUnits[u] {
			continue
		}
		e := f.Units[i]
		if !e.Consented && e.LastWrittenHash == "" {
			continue // 从未授权且从未发布：无副本风险，不建证据
		}
		f.LegacyEvidence = appendLegacyEvidence(f.LegacyEvidence, LegacyEvidenceEntry{
			ID: legacyEvidenceID("v1", u, e.Generation), Source: "v1", Unit: u,
			SchemaVersion: f.SchemaVersion,
			EntryGeneration: e.Generation, EntryLastHash: e.LastWrittenHash,
			EntryOutcome: e.LastOutcome, EntryConsented: e.Consented, EntryDetached: e.Detached,
			Fingerprint:  strings.TrimPrefix(e.Generation, legacyGenerationPrefix),
			TopDistro:     strings.TrimSpace(f.Distro),
			TopGeneration: strings.TrimPrefix(f.Generation, legacyGenerationPrefix),
			TopHome:       f.Home,
		})
	}
	// v2 派生 map → 低可信证据（原 map 原值保留不丢；逐目标核验前不授写权、
	// 不全局误阻所有 WSL）。
	for distro, units := range f.LegacySensitiveRisk {
		for u := range units {
			f.LegacyEvidence = appendLegacyEvidence(f.LegacyEvidence, LegacyEvidenceEntry{
				ID: legacyEvidenceID("v2", u, distro), Source: "v2-derived", Unit: u,
				SchemaVersion: f.SchemaVersion, LowTrust: true,
				TopDistro: strings.TrimSpace(distro),
			})
		}
	}
	if f.Generation != "" {
		f.Generation = legacyGenerationPrefix + f.Generation
	}
	f.SchemaVersion = stateSchemaVersion
}

// migrateV2ToV3 把 v2 清单升级到 v3：Units/Targets 保持原样（新公式指纹可归
// 属，entryFor 继续命中）；v2 从 v1 升级时已隔离的 legacy:- 前缀敏感 entry 提
// 取 v1 指纹证据（保真）；LegacySensitiveRisk 派生 map 转低可信证据。
func migrateV2ToV3(f *StateFile) {
	for i := range f.Units {
		u := f.Units[i].Unit
		if !legacySensitiveUnits[u] {
			continue
		}
		e := f.Units[i]
		if !strings.HasPrefix(e.Generation, legacyGenerationPrefix) {
			continue // 可归属的受管记录：保持受管，不建证据
		}
		if !e.Consented && e.LastWrittenHash == "" {
			continue
		}
		f.LegacyEvidence = appendLegacyEvidence(f.LegacyEvidence, LegacyEvidenceEntry{
			ID: legacyEvidenceID("v1", u, e.Generation), Source: "v1", Unit: u,
			SchemaVersion: 1,
			EntryGeneration: e.Generation, EntryLastHash: e.LastWrittenHash,
			EntryOutcome: e.LastOutcome, EntryConsented: e.Consented, EntryDetached: e.Detached,
			Fingerprint:  strings.TrimPrefix(e.Generation, legacyGenerationPrefix),
			TopDistro:     strings.TrimSpace(f.Distro),
			TopGeneration: strings.TrimPrefix(f.Generation, legacyGenerationPrefix),
			TopHome:       f.Home,
		})
	}
	for distro, units := range f.LegacySensitiveRisk {
		for u := range units {
			f.LegacyEvidence = appendLegacyEvidence(f.LegacyEvidence, LegacyEvidenceEntry{
				ID: legacyEvidenceID("v2", u, distro), Source: "v2-derived", Unit: u,
				SchemaVersion: f.SchemaVersion, LowTrust: true,
				TopDistro: strings.TrimSpace(distro),
			})
		}
	}
	f.SchemaVersion = stateSchemaVersion
}
// appendLegacyEvidence 幂等追加（同 ID 不重复）。
func appendLegacyEvidence(list []LegacyEvidenceEntry, e LegacyEvidenceEntry) []LegacyEvidenceEntry {
	for i := range list {
		if list[i].ID == e.ID {
			return list
		}
	}
	return append(list, e)
}

// legacyEvidenceID 构造稳定证据 ID。
func legacyEvidenceID(source string, u UnitID, key string) string {
	return source + ":" + string(u) + ":" + key
}

// clearLegacySensitiveRisk 清除某 distro 上一个敏感单元的迁移遗留风险标记
// （新代成功写入=副本重新受管；确认撤回=副本已移除——两者都让当前状态面
// 可以如实判定，不再需要 v1 线索兜底）。
func clearLegacySensitiveRisk(st *StateFile, distro string, unit UnitID) {
	if st.LegacySensitiveRisk == nil {
		return
	}
	units, ok := st.LegacySensitiveRisk[distro]
	if !ok {
		return
	}
	delete(units, unit)
	if len(units) == 0 {
		delete(st.LegacySensitiveRisk, distro)
	}
	if len(st.LegacySensitiveRisk) == 0 {
		st.LegacySensitiveRisk = nil
	}
}

// saveLocked 持久化指定清单（私有原子写：MkdirAll → tmp 0600 → rename →
// chmod 0600）。调用方必须持有 s.mu。
func (s *stateStore) saveLocked(st StateFile) error {
	st.SchemaVersion = stateSchemaVersion
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal wsl supply state: %w", err)
	}
	b = append(b, '\n')
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("mkdir wsl supply state dir: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".wsl-supply-state-*")
	if err != nil {
		return fmt.Errorf("create wsl supply state tmp: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write wsl supply state tmp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close wsl supply state tmp: %w", err)
	}
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("chmod wsl supply state tmp: %w", err)
	}
	if err := os.Rename(tmpPath, s.path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("replace wsl supply state: %w", err)
	}
	_ = os.Chmod(s.path, 0o600)
	return nil
}

// entry 返回「当前目标」（清单记录的当前代，st.Generation）的单元登记：
// 同一 unit 可能存在多个身份分区，只取属于当前代的那一条；无匹配返回零值
// （SA-02：跨目标的登记互不串读——包括 legacy 隔离区与 unknown 槽）。
func (s *StateFile) entry(unit UnitID) (StateEntry, bool) {
	if s.Generation == "" {
		return StateEntry{}, false
	}
	for i := range s.Units {
		if s.Units[i].Unit == unit && s.Units[i].Generation == s.Generation {
			return s.Units[i], true
		}
	}
	return StateEntry{}, false
}

// entryFor 返回请求代（完整身份指纹）的单元登记。匹配规则（SA-02）：
//   - 请求代为空或 unknown：永不匹配持久登记（无法验证身份的目标不复用任何
//     历史授权/基准；敏感授权在 GrantUnitConsent 处直接拒绝持久化）；
//   - legacy 隔离代：永不匹配（v1 记录无法归属，重新同意后继续）；
//   - 其余：unit+generation 同时相等才命中——不同 distro（克隆）或不同代
//     （重装/换用户）各自独立分区。
func (s *StateFile) entryFor(unit UnitID, generation string) StateEntry {
	if generation == "" || generation == GenerationUnknown || strings.HasPrefix(generation, legacyGenerationPrefix) {
		return StateEntry{}
	}
	for i := range s.Units {
		if s.Units[i].Unit == unit && s.Units[i].Generation == generation {
			return s.Units[i]
		}
	}
	return StateEntry{}
}

// upsertEntry 按身份分区写入单元登记：同 unit 同代原位替换；同 unit 异代
// 追加为独立分区（不清除其它目标的登记——A→B→A 交替各自保全，SA-02）。
// 写入同时把清单的「当前代」指向本次目标（entry() 的匹配锚）。unknown 代
// 单槽复用（状态展示面；entryFor 永不匹配它，授权面无风险）。
func (s *StateFile) upsertEntry(entry StateEntry, generation string) {
	if generation != "" {
		s.Generation = generation
	}
	for i := range s.Units {
		if s.Units[i].Unit == entry.Unit && s.Units[i].Generation == entry.Generation {
			s.Units[i] = entry
			return
		}
	}
	s.Units = append(s.Units, entry)
}

// mutate 在清单锁内完成一次读改写：fn 拿到当前清单的**深复制**指针，修改
// 后原子落盘。PR05/PR06 语义：落盘失败时内存态保持修改前状态（内存不得
// 冒充已持久化——GetStatus/重启不可误判归属），并返回错误由调用方把结果
// 区分为 content-committed/state-unpersisted；成功时才把副本发布为当前态。
// 攻坚契约：main 有读取错误（unreadable/corrupt/unsupported）时不得把空
// 默认值保存覆盖原文件——mutate 直接失败；修复存储后重新读入再操作。
// 所有清单变更必须经 mutate，禁止在 load 返回的副本上修改后漏存。
func (s *stateStore) mutate(fn func(*StateFile)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.loadLocked() // 深复制：fn 的修改与并发读者完全隔离
	if h := s.health.Main; h == StoreUnreadable || h == StoreCorrupt || h == StoreUnsupported {
		return fmt.Errorf("wsl supply state store unhealthy (%s: %s); refusing to overwrite the original file with an empty manifest", h, s.health.MainErr)
	}
	fn(&st)
	if err := s.saveLocked(st); err != nil {
		return err // s.current 保持修改前状态
	}
	s.current = st
	return nil
}

// cloneStateFile 深复制清单（PR06）：Units/Targets/Plugins/Pending* 等全部
// slice 与 map（含嵌套 map/slice）都复制底层数据，锁外读快照与锁内写互不
// 别名。清单规模为个位数到几十条单元，复制成本可忽略。
func cloneStateFile(f StateFile) StateFile {
	out := f // 值字段（时间戳/标量）直接复制
	if f.Targets != nil {
		out.Targets = make(map[string]TargetRecord, len(f.Targets))
		for gen, rec := range f.Targets {
			if rec.InspectedAt != nil {
				inspected := make(map[CLI]time.Time, len(rec.InspectedAt))
				for cli, at := range rec.InspectedAt {
					inspected[cli] = at
				}
				rec.InspectedAt = inspected
			}
			out.Targets[gen] = rec
		}
	}
	if f.LegacySensitiveRisk != nil {
		out.LegacySensitiveRisk = make(map[string]map[UnitID]bool, len(f.LegacySensitiveRisk))
		for distro, units := range f.LegacySensitiveRisk {
			cp := make(map[UnitID]bool, len(units))
			for u, v := range units {
				cp[u] = v
			}
			out.LegacySensitiveRisk[distro] = cp
		}
	}
	if f.Plugins != nil {
		out.Plugins = make([]PluginStateEntry, len(f.Plugins))
		for i, e := range f.Plugins {
			// 三态保全：nil 与空切片必须区分（空数组 = 一项都不加载），
			// 用非 nil 底座 append 避免空切片折叠回 nil。
			if e.Filters.Extensions != nil {
				e.Filters.Extensions = append([]string{}, e.Filters.Extensions...)
			}
			if e.Filters.Skills != nil {
				e.Filters.Skills = append([]string{}, e.Filters.Skills...)
			}
			if e.Filters.Prompts != nil {
				e.Filters.Prompts = append([]string{}, e.Filters.Prompts...)
			}
			if e.Filters.Themes != nil {
				e.Filters.Themes = append([]string{}, e.Filters.Themes...)
			}
			out.Plugins[i] = e
		}
	}
	if f.LastInspectedAtByCLI != nil {
		out.LastInspectedAtByCLI = make(map[CLI]time.Time, len(f.LastInspectedAtByCLI))
		for cli, at := range f.LastInspectedAtByCLI {
			out.LastInspectedAtByCLI[cli] = at
		}
	}
	if f.SettledOperations != nil {
		out.SettledOperations = make(map[string]SettledOperation, len(f.SettledOperations))
		for id, op := range f.SettledOperations {
			if op.RelatedOpIDs != nil {
				op.RelatedOpIDs = append([]string(nil), op.RelatedOpIDs...)
			}
			out.SettledOperations[id] = op
		}
	}
	if f.LegacyEvidence != nil {
		out.LegacyEvidence = make([]LegacyEvidenceEntry, len(f.LegacyEvidence))
		for i, e := range f.LegacyEvidence {
			if e.Resolved != nil {
				resolved := make(map[string]string, len(e.Resolved))
				for k, v := range e.Resolved {
					resolved[k] = v
				}
				e.Resolved = resolved
			}
			out.LegacyEvidence[i] = e
		}
	}
	if f.PendingSources != nil {
		out.PendingSources = make(map[string]uint64, len(f.PendingSources))
		for k, v := range f.PendingSources {
			out.PendingSources[k] = v
		}
	}
	if f.PendingProgress != nil {
		out.PendingProgress = make(map[string][]UnitID, len(f.PendingProgress))
		for k, units := range f.PendingProgress {
			out.PendingProgress[k] = append([]UnitID(nil), units...)
		}
	}
	if f.Units != nil {
		out.Units = make([]StateEntry, len(f.Units))
		for i, e := range f.Units {
			if e.Subs != nil {
				subs := make(map[string]SubHash, len(e.Subs))
				for k, v := range e.Subs {
					subs[k] = v
				}
				e.Subs = subs
			}
			if e.SelectedKeys != nil {
				selected := make(map[string]bool, len(e.SelectedKeys))
				for k, v := range e.SelectedKeys {
					selected[k] = v
				}
				e.SelectedKeys = selected
			}
			out.Units[i] = e
		}
	}
	return out
}

// resolveDistroGeneration 从身份索引解析「该 distro 唯一可归属的一代」
// （PR04）：目标性 mutate（停跟随/选择集）必须作用于明确目标 generation，
// 不能取全局最近代（st.Generation 指向最后一次接触的目标——A→B→操作A 时
// 会误改 B）。停机目标可用：只读索引不探测。同名 distro 多代并存（重装后
// 新旧代同在）无法唯一归属 → 拒绝（歧义不改别人）；legacy/unknown 永不
// 命中。顶层 Distro/Generation 作为索引缺失时的兼容线索（v2 早期清单）。
func resolveDistroGeneration(st *StateFile, distro string) (string, error) {
	valid := func(gen string) bool {
		return gen != "" && gen != GenerationUnknown && !strings.HasPrefix(gen, legacyGenerationPrefix)
	}
	var gens []string
	for gen, rec := range st.Targets {
		if rec.Distro == distro && valid(gen) {
			gens = append(gens, gen)
		}
	}
	if len(gens) == 0 && st.Distro == distro && valid(st.Generation) {
		gens = []string{st.Generation}
	}
	sort.Strings(gens)
	switch len(gens) {
	case 1:
		return gens[0], nil
	case 0:
		return "", fmt.Errorf("WSL 目标 %q 没有可归属的供给登记（先检查/供给该目标后再操作）", distro)
	default:
		return "", fmt.Errorf("WSL 目标 %q 存在多代身份登记（%d 代），无法唯一归属；请先对该目标执行一次检查收敛后代际", distro, len(gens))
	}
}
