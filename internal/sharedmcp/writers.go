package sharedmcp

// writers.go — 引擎写入器接口、Service 编排（Distribute/Undistribute/
// Overview，spec §2.2/§2.3）与 canonical JSON 比较工具。
//
// 值语义比较纪律（spec §1.5）：drift/外改判定一律用 canonical 比较（unmarshal
// → marshal，键序无关），不比字节——缩进/键序/TOML 形态差异不构成 drift。

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"net/url"
	"os"
	"sync"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"amagi-codebox/internal/logging"
)

// EngineWriter 是一个引擎的 MCP 配置写入边界（spec §2.2）。所有实现遵守：
// 读原文件 → 只改动 mcp 命名空间的登记条目 → 结构校验 → 原子私写。
type EngineWriter interface {
	// ID 返回引擎标识：claude / codex / opencode / pi。
	ID() string
	// Supports 返回（形态是否可分发到该引擎, 不可分发原因）。
	Supports(entry SharedMcpEntry) (bool, string)
	// Apply 写入/覆盖条目（仅当该名字属我方登记——冲突由编排层先查）；
	// 成功后返回该引擎映射形态的 canonical JSON（供编排层记 ContentHash）。
	Apply(entry SharedMcpEntry) (json.RawMessage, error)
	// Remove 删除条目；条目不存在 → 幂等成功。
	Remove(name string) error
	// Has 报告目标文件当前是否存在该名字（状态查询实测，供 §1.5）。
	Has(name string) (bool, error)
	// Read 把目标文件中该条目的当前内容按本引擎映射读回为 canonical JSON
	//（不存在 → nil,nil）——供 drift 判定与外部手改检测，值语义比较。
	Read(name string) (json.RawMessage, error)
	// Mapped 返回登记条目经本引擎映射后期望的 canonical JSON——与 Read
	// 结果 canonical 相等即为 distributed。
	Mapped(entry SharedMcpEntry) (json.RawMessage, error)
}

// Service 是共用 MCP 登记与分发的领域服务（无缓存，每次调用实测文件）。
type Service struct {
	userHome  string
	configDir string
	env       []string
	log       *logging.Service

	mu         sync.Mutex // 串化「读状态→写目标→落状态」的读改写窗口（单进程）
	registryMu sync.Mutex // serialize registry read-modify-write (including bulk imports)
	writers    []EngineWriter
}

// New 创建服务。userHome 解析 ~/.agents 与各引擎默认路径；configDir 是应用
// 配置目录（状态库所在）；env 供 CODEX_HOME/PI_CODING_AGENT_DIR 解析
// （platform.CodexHomeDir 直接复用；Pi 复刻 piconfig.agentDir 语义在本包内
// 实现）；log 允许为 nil（测试夹具）。
func New(userHome, configDir string, env []string, log *logging.Service) *Service {
	s := &Service{userHome: userHome, configDir: configDir, env: env, log: log}
	s.writers = []EngineWriter{
		&claudeWriter{userHome: userHome},
		&codexWriter{env: env, userHome: userHome},
		&opencodeWriter{userHome: userHome},
		&piWriter{env: env, userHome: userHome},
	}
	return s
}

// RegistryPath 返回登记文件路径（供门面/前端展示）。
func (s *Service) RegistryPath() string { return s.registryPath() }

// writerByID 按引擎 ID 取写入器；未知 ID → 明确错误。
func (s *Service) writerByID(engineID string) (EngineWriter, error) {
	for _, w := range s.writers {
		if w.ID() == engineID {
			return w, nil
		}
	}
	return nil, NewErr(KindInvalidEntry, "未知引擎 %q（支持：%s）", engineID, engineIDsCSV())
}

// parseURL 独立包装（便于测试替换/复用）。
func parseURL(raw string) (*url.URL, error) { return url.Parse(raw) }

// ---- canonical JSON 工具（值语义比较，spec §1.5） ----

// canonicalJSON 返回投影的规范形态：unmarshal → marshal（对象键按字典序、
// 紧凑无空白）。解析失败时退回 json.Compact（保持可用性，调用方比较两侧
// 用同一函数即一致）。
func canonicalJSON(raw []byte) []byte {
	if len(raw) == 0 {
		return raw
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		var buf bytes.Buffer
		if cerr := json.Compact(&buf, raw); cerr != nil {
			return append([]byte(nil), raw...)
		}
		return buf.Bytes()
	}
	out, err := json.Marshal(v)
	if err != nil {
		return append([]byte(nil), raw...)
	}
	return out
}

// equalJSONValue 语义比较两段 JSON 投影（canonical 后字节比较）。
func equalJSONValue(a, b []byte) bool {
	if len(a) == 0 || len(b) == 0 {
		return len(a) == len(b)
	}
	return bytes.Equal(canonicalJSON(a), canonicalJSON(b))
}

// hashCanonical 计算 canonical JSON 的 SHA-256 十六进制（ContentHash 语义，
// spec §1.2）。
func hashCanonical(raw []byte) string {
	sum := sha256.Sum256(canonicalJSON(raw))
	return hex.EncodeToString(sum[:])
}

// ---- JSON mcpServers 写入器（Claude/Pi）共用工具 ----

// readJSONConfigDefensively 防御式读取 JSON 配置文件（readStableFile 轻量
// 版：Lstat 拒符号链接/非普通文件，claudeaccounts 先例；本包自实现，不引
// 账号域包避免反向依赖，spec §2.2 Claude 节）。文件缺失 → (nil,false,nil)
// （视为 {} 走新建）；JSON 非法或根非对象 → ErrTargetCorrupt，绝不覆写。
func readJSONConfigDefensively(path, what string) ([]byte, bool, error) {
	fi, err := osLstat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, NewErr(KindIO, "读取%s失败：%s（%v）", what, path, err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return nil, false, NewErr(KindIO, "%s是符号链接，已拒绝操作：%s", what, path)
	}
	if !fi.Mode().IsRegular() {
		return nil, false, NewErr(KindIO, "%s不是普通文件，已拒绝操作：%s", what, path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false, NewErr(KindIO, "读取%s失败：%s（%v）", what, path, err)
	}
	if !gjson.ValidBytes(data) {
		return nil, false, NewErr(KindTargetCorrupt, "%s不是合法 JSON，已拒绝改动（请先手动修复）：%s", what, path)
	}
	if !gjson.ParseBytes(data).IsObject() {
		return nil, false, NewErr(KindTargetCorrupt, "%s根不是 JSON 对象，已拒绝改动（请先手动修复）：%s", what, path)
	}
	return data, true, nil
}

// setMcpServerEntry 把 raw 条目写入 data 的 mcpServers.<name>（顶层无
// mcpServers 键时先建 {}），返回候选字节。供 Claude/Pi（verbatim 域）共用。
func setMcpServerEntry(data []byte, name string, raw []byte) ([]byte, error) {
	next := data
	if !gjson.GetBytes(data, "mcpServers").Exists() {
		var err error
		if next, err = sjson.SetRawBytes(next, "mcpServers", []byte("{}")); err != nil {
			return nil, NewErr(KindIO, "初始化 mcpServers 键失败（%v）", err)
		}
	}
	var err error
	if next, err = sjson.SetRawBytes(next, entryPath(name), raw); err != nil {
		return nil, NewErr(KindIO, "写入条目 %s 失败（%v）", name, err)
	}
	return next, nil
}

// assertTopKeysAllowMcpServers 断言写后顶层键集 − 写前键集 ⊆ {mcpServers}
// （仅首建允许新增这一个键，spec §2.2 Claude 节硬约束）。
func assertTopKeysAllowMcpServers(before, after []byte) error {
	bk := topKeyNames(before)
	for _, k := range topKeyNames(after) {
		if k == "mcpServers" {
			continue
		}
		if !containsString(bk, k) {
			return NewErr(KindIO, "顶层键集合在写入后意外变化（新增 %s）", k)
		}
	}
	return nil
}

// supportsJSONServer 是 Claude/Pi 的形态判定：stdio/http 均合法（登记形态
// 与两 CLI 的 mcpServers 同构）；未知 type → 不支持。
func supportsJSONServer(entry SharedMcpEntry) (bool, string) {
	switch entry.Type {
	case "stdio", "http":
		return true, ""
	}
	return false, "条目形态未知（type 不是 stdio/http），无法分发"
}

// osLstat 指向 os.Lstat（防御式读取用；var 形式便于对称注入，生产即 os）。
var osLstat = os.Lstat

// ---- 分发编排（spec §2.3） ----

// Distribute 把登记条目写入目标引擎（reconcile-then-settle：Apply 成功才
// 落 Selected+Applied，envvars 先例）。幂等 resync：重复 Distribute 同一
// 条目 = 用当前登记内容重新 Upsert（登记 Update 后的重新分发路径，成功即
// 刷新 ContentHash、drift 消除）。
func (s *Service) Distribute(entry SharedMcpEntry, engineID string, force bool) error {
	w, err := s.writerByID(engineID)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	// ① Supports 预检：形态受限事实优先——即使该引擎 Applied 仍留有旧副本
	// 也照实拒绝（spec §2.3），reason 提示关闭开关可移除旧副本。
	if ok, reason := w.Supports(entry); !ok {
		hint := ""
		st := s.loadState()
		if _, applied := st.Entries[entry.Name].Applied[engineID]; applied {
			hint = "；该引擎仍留有旧版分发副本，关闭开关可移除"
		}
		return NewErr(KindUnsupported, "%s 不支持此形态：%s%s", engineDisplayName(engineID), reason, hint)
	}
	if !validName(entry.Name) {
		return NewErr(KindNameInvalid, "无法分发到 %s：%s", engineDisplayName(engineID), nameInvalidReason)
	}

	// ② 所有权/外改预检。
	st := s.loadState()
	es := st.Entries[entry.Name]
	has, herr := w.Has(entry.Name)
	if herr != nil {
		return herr
	}
	rec, wasApplied := es.Applied[engineID]
	if has && !wasApplied {
		return NewErr(KindConflict, "目标 %s 配置已存在同名条目 %s，非本应用写入，拒绝覆盖；请先手动处理",
			engineDisplayName(engineID), entry.Name)
	}
	if has && wasApplied {
		cur, rerr := w.Read(entry.Name)
		if rerr != nil {
			return rerr
		}
		// 目标当前 hash ≠ Applied 记录 hash = 外部改过目标 → force 才覆盖；
		// 匹配（drift 源于登记更新）→ 直接覆盖为我方最新内容。
		if cur != nil && hashCanonical(cur) != rec.ContentHash && !force {
			return NewErr(KindForeignModified, "%s 中的 %s 已被外部修改（与上次分发的哈希不一致），直接覆盖将丢失这些修改；如确认请在弹窗中强制执行",
				engineDisplayName(engineID), entry.Name)
		}
	}

	// ③ 写入 + 落状态（失败 → 状态不变）。
	canonical, aerr := w.Apply(entry)
	if aerr != nil {
		return aerr
	}
	if es.Selected == nil {
		es.Selected = map[string]bool{}
	}
	if es.Applied == nil {
		es.Applied = map[string]sharedMcpAppliedRecord{}
	}
	es.Selected[engineID] = true
	es.Applied[engineID] = sharedMcpAppliedRecord{ContentHash: hashCanonical(canonical)}
	st.Entries[entry.Name] = es
	if serr := s.saveState(st); serr != nil {
		return serr
	}
	if s.log != nil {
		s.log.Info("sharedmcp", "分发条目到引擎", "engine="+engineID, fmtEntry(entry))
	}
	return nil
}

// Undistribute 从目标引擎移除条目并清状态（spec §2.3）。Applied 不含该
// 引擎 → 直接清 Selected（幂等）；含 → 外部手改检测（hash 不符且未 force
// → ErrForeignModified）→ Remove 成功后才清 Applied+Selected。不做
// Supports 预检（关停永远合法——已分发的受限形态条目必须有关闭路径）。
func (s *Service) Undistribute(name, engineID string, force bool) error {
	w, err := s.writerByID(engineID)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	st := s.loadState()
	es := st.Entries[name]
	rec, wasApplied := es.Applied[engineID]
	if !wasApplied {
		if _, selected := es.Selected[engineID]; !selected {
			return nil
		}
		delete(es.Selected, engineID)
		st.Entries[name] = es
		return s.saveState(st)
	}
	cur, rerr := w.Read(name)
	if rerr != nil {
		return rerr
	}
	if cur != nil && hashCanonical(cur) != rec.ContentHash && !force {
		return NewErr(KindForeignModified, "%s 中的 %s 已被外部修改（与上次分发的哈希不一致），直接移除将丢失这些修改；如确认请在弹窗中强制执行",
			engineDisplayName(engineID), name)
	}
	if rerr := w.Remove(name); rerr != nil {
		return rerr
	}
	delete(es.Applied, engineID)
	delete(es.Selected, engineID)
	if len(es.Applied) == 0 && len(es.Selected) == 0 {
		delete(st.Entries, name)
	} else {
		st.Entries[name] = es
	}
	if serr := s.saveState(st); serr != nil {
		return serr
	}
	if s.log != nil {
		s.log.Info("sharedmcp", "从引擎移除条目", "engine="+engineID, "name="+name)
	}
	return nil
}

// SetSelected 是门面层的勾选入口：selected=true → Distribute（幂等 resync
// ——drift 格的「重新分发」即调本方法；目标被外部改过时同样返回
// ErrForeignModified，force 确认后才覆盖）；false → Undistribute。条目从
// 登记文件现行读取。
func (s *Service) SetSelected(name, engineID string, selected, force bool) error {
	if selected {
		entries, _, err := s.List()
		if err != nil {
			return err
		}
		for _, e := range entries {
			if e.Name == name {
				return s.Distribute(e, engineID, force)
			}
		}
		return NewErr(KindNotFound, "条目 %s 不存在（可能已被删除，请刷新列表）", name)
	}
	return s.Undistribute(name, engineID, force)
}

// AppliedEngines 返回某条目当前已分发（Applied）的引擎 ID 列表（门面
// Delete 编排「先逐引擎还原」用，spec §3）。
func (s *Service) AppliedEngines(name string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadState().appliedEngines(name)
}

// ForgetStateEntry 删除单个条目的全部跟踪状态（登记删除成功后调用）。
func (s *Service) ForgetStateEntry(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.forgetStateEntry(name)
}

// ---- 状态查询（spec §1.5/§2.3 Overview） ----

// EngineDistStatus 是每条目 × 每引擎一格的分发状态。
type EngineDistStatus string

const (
	EngineDistributed    EngineDistStatus = "distributed"     // 目标存在该名字、属我方 Applied 且内容 = 当前登记的映射结果
	EngineDrift          EngineDistStatus = "drift"           // 属我方 Applied 且目标存在，但内容 ≠ 当前登记映射结果——附「可重新分发」
	EngineNotDistributed EngineDistStatus = "not-distributed" // 目标无该名字（含 Selected=true 但目标被外部删掉的失配形态）
	EngineConflict       EngineDistStatus = "conflict"        // 目标有同名但不在 Applied 登记（手写/外部写入）——Apply 拒绝
	EngineUnsupported    EngineDistStatus = "unsupported"     // 当前登记形态该引擎无法表达（如 http+headers → codex）
	EngineExcluded       EngineDistStatus = "excluded"        // 仅 omp
)

// EngineCell 是矩阵一格的视图。
type EngineCell struct {
	Status   EngineDistStatus `json:"status"`
	Selected bool             `json:"selected"` // 用户勾选意图（state.Selected）
	Reason   string           `json:"reason,omitempty"`
}

// EntryOverview 是单个登记条目 × 各引擎的状态矩阵。
type EntryOverview struct {
	Entry SharedMcpEntry `json:"entry"`
	// NameDistributable：条目名是否符合分发字符集（false → 行级「名字不
	// 兼容分发」警示徽章，四引擎格统一 unsupported，spec §1.4）。
	NameDistributable bool                  `json:"nameDistributable"`
	Engines           map[string]EngineCell `json:"engines"` // key=引擎 ID（含 omp excluded 行）
}

// Overview 是面板首屏的全量状态视图。
type Overview struct {
	Entries        []EntryOverview `json:"entries"`
	RegistryExists bool            `json:"registryExists"`
	RegistryPath   string          `json:"registryPath"`
	EnginesMeta    []EngineMeta    `json:"enginesMeta"` // 四引擎 + omp excluded 行
}

// Overview 实测登记文件与各引擎目标文件，按 §1.5 算法求矩阵，并执行孤儿
// GC（state 中登记外名字被清除且不写目标文件）。用户可见文案只用
// 「已分发/未分发/有更新」，绝不用「已连接」（分发≠连通，不做握手探测）。
func (s *Service) Overview() (Overview, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entries, exists, err := s.List()
	if err != nil {
		return Overview{}, err
	}
	st := s.loadState()

	liveNames := make(map[string]bool, len(entries))
	for _, e := range entries {
		liveNames[e.Name] = true
	}
	// 孤儿 GC（§1.2）：state 含登记外名字 → 从内存态丢弃并回写（不继承到
	// 同名重建）；目标侧已分发副本成为不受管孤儿，如实保留（§8 R-9）。
	gcChanged := false
	for name := range st.Entries {
		if !liveNames[name] {
			delete(st.Entries, name)
			gcChanged = true
			if s.log != nil {
				s.log.Debug("sharedmcp", "状态库清理孤儿条目（登记文件中已不存在）", "name="+name)
			}
		}
	}
	if gcChanged {
		if err := s.saveState(st); err != nil && s.log != nil {
			s.log.Warn("sharedmcp", "状态库孤儿清理回写失败（下次刷新重试）", "path="+s.statePath(), "err="+err.Error())
		}
	}

	out := Overview{RegistryExists: exists, RegistryPath: s.registryPath()}
	managedCounts := map[string]int{}
	for _, e := range entries {
		eo := EntryOverview{Entry: e, NameDistributable: validName(e.Name), Engines: map[string]EngineCell{}}
		// omp：恒 excluded 只读行（§2.2 静态声明）。
		eo.Engines[engineIDOMP] = EngineCell{Status: EngineExcluded, Reason: ompExcludedDetail}
		es := st.Entries[e.Name]
		for id := range es.Applied {
			managedCounts[id]++
		}
		for _, w := range s.writers {
			cell, cerr := s.computeCell(w, e, es)
			if cerr != nil {
				return Overview{}, cerr
			}
			eo.Engines[w.ID()] = cell
		}
		out.Entries = append(out.Entries, eo)
	}
	out.EnginesMeta = buildEnginesMeta(s.writers, managedCounts)
	if out.Entries == nil {
		out.Entries = []EntryOverview{}
	}
	return out, nil
}

// computeCell 按优先级算法求一格状态（§1.5，首个命中）：
// ① !NameDistributable → unsupported（名字字符集不兼容，四引擎统一拒绝）
// ② !Supports → unsupported（形态受限事实优先于陈旧分发事实；Applied 仍
// 含该引擎时 reason 追加旧副本提示）
// ③ Has 且 Applied 不含 → conflict
// ④ Applied 含：!Has → not-distributed（目标条目缺失）；Has 且当前 hash ≠
// 期望 hash → drift；相等 → distributed
// ⑤ 其余 → not-distributed。
func (s *Service) computeCell(w EngineWriter, e SharedMcpEntry, es sharedMcpEntryState) (EngineCell, error) {
	name := e.Name
	cell := EngineCell{Selected: es.Selected[w.ID()]}
	_, applied := es.Applied[w.ID()]
	switch {
	case !validName(name):
		cell.Status = EngineUnsupported
		cell.Reason = nameInvalidReason
		if applied {
			cell.Reason += "；该引擎仍留有旧版分发副本，关闭开关可移除"
		}
		return cell, nil
	}
	if ok, reason := w.Supports(e); !ok {
		cell.Status = EngineUnsupported
		cell.Reason = reason
		if applied {
			cell.Reason += "；该引擎仍留有旧版分发副本，关闭开关可移除"
		}
		return cell, nil
	}
	has, err := w.Has(name)
	if err != nil {
		return EngineCell{}, err
	}
	if has && !applied {
		cell.Status = EngineConflict
		cell.Reason = "目标配置已存在同名条目（非本应用写入），开启将被拒绝；请先手动处理目标文件"
		return cell, nil
	}
	if applied {
		if !has {
			cell.Status = EngineNotDistributed
			cell.Reason = "目标条目缺失（可能被外部删除）；重新开启开关可再次分发"
			return cell, nil
		}
		cur, err := w.Read(name)
		if err != nil {
			return EngineCell{}, err
		}
		want, err := w.Mapped(e)
		if err != nil {
			return EngineCell{}, err
		}
		if !equalJSONValue(cur, want) {
			cell.Status = EngineDrift
			cell.Reason = "登记内容已更新，可重新分发"
			return cell, nil
		}
		cell.Status = EngineDistributed
		return cell, nil
	}
	cell.Status = EngineNotDistributed
	return cell, nil
}
