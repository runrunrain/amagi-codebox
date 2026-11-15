package sharedmcp

// state.go — configDir 侧托管跟踪状态库 shared-mcp-state.json（spec 裁定一
// §1.2）。记录「每个条目名 × 每引擎」的用户勾选（Selected）与我方写入事实
//（Applied：引擎 → 写入内容的 canonical hash）。选择状态只存 configDir，
// 绝不写 ~/.agents 共享文件（spec 裁定三）。
//
// 模式照 app_wsl_supply_s4.go 的 wslOpenCodeMCPSelectionFile：schemaVersion
// + tmp 0600 + rename；损坏/缺失回零值（选择面回到「全未分发」，不因状态
// 库失败破坏启动）。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
)

// sharedMcpStateFile 是状态存储文件名（configDir 内）。
const sharedMcpStateFile = "shared-mcp-state.json"

// sharedMcpStateSchemaVersion 是状态文件当前 schema 版本。
const sharedMcpStateSchemaVersion = 1

// sharedMcpState 是 configDir/shared-mcp-state.json 的形态（仅应用读写）。
type sharedMcpState struct {
	SchemaVersion int                            `json:"schemaVersion"`
	Entries       map[string]sharedMcpEntryState `json:"entries"` // key=条目名
}

// sharedMcpEntryState 是单个条目名的跟踪状态。
type sharedMcpEntryState struct {
	// Selected：用户勾选意图（true=希望分发到该引擎）。key ∈ 引擎 ID。
	Selected map[string]bool `json:"selected,omitempty"`
	// Applied：最近一次写入成功的事实记录：引擎 → 写入内容 canonical hash。
	// hash 用于 drift 判定（vs 当前登记映射结果）与外部手改检测（vs 目标
	// 当前条目 hash，Undistribute 前比对，不匹配 → 拒删待确认）。
	Applied map[string]sharedMcpAppliedRecord `json:"applied,omitempty"`
}

// sharedMcpAppliedRecord 记录一次成功写入的事实。
type sharedMcpAppliedRecord struct {
	ContentHash string `json:"contentHash"`
}

// zeroState 返回干净初始态。
func zeroState() sharedMcpState {
	return sharedMcpState{SchemaVersion: sharedMcpStateSchemaVersion, Entries: map[string]sharedMcpEntryState{}}
}

// statePath 返回状态文件绝对路径。
func (s *Service) statePath() string {
	return filepath.Join(s.configDir, sharedMcpStateFile)
}

// loadState 读取状态库；缺失/损坏回零值（spec §1.2）。
func (s *Service) loadState() sharedMcpState {
	out := zeroState()
	if s.configDir == "" {
		return out
	}
	data, err := os.ReadFile(s.statePath())
	if err != nil {
		return out
	}
	var parsed sharedMcpState
	if json.Unmarshal(data, &parsed) != nil || parsed.Entries == nil {
		return out
	}
	return parsed
}

// saveState 原子保存状态库（tmp 0600 + rename，spec §1.2）。
func (s *Service) saveState(st sharedMcpState) error {
	st.SchemaVersion = sharedMcpStateSchemaVersion
	if st.Entries == nil {
		st.Entries = map[string]sharedMcpEntryState{}
	}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return NewErr(KindIO, "状态库序列化失败（%v）", err)
	}
	b = append(b, '\n')
	path := s.statePath()
	if err := os.MkdirAll(s.configDir, 0o700); err != nil {
		return NewErr(KindIO, "创建状态目录失败：%s（%v）", s.configDir, err)
	}
	if err := writeAtomicPrivate(path, b); err != nil {
		return err
	}
	return nil
}

// appliedEngines 返回某条目当前 Applied 覆盖的引擎 ID（排序稳定）。
func (st sharedMcpState) appliedEngines(name string) []string {
	es, ok := st.Entries[name]
	if !ok {
		return nil
	}
	out := make([]string, 0, len(es.Applied))
	for id := range es.Applied {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// forgetStateEntry 删除单个条目的全部状态（登记删除成功后调用，孤儿不残留，
// spec §1.2——同名重建不继承旧 Applied）。
func (s *Service) forgetStateEntry(name string) error {
	st := s.loadState()
	if _, ok := st.Entries[name]; !ok {
		return nil
	}
	delete(st.Entries, name)
	return s.saveState(st)
}
