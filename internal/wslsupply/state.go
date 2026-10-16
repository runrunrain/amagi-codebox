package wslsupply

// state.go — 供给状态清单（design §七「状态落点与提交」）：保存在 CodeBox
// 配置目录的 wsl-config-supply.json，经项目私有原子写（tmp 0600 + rename），
// 不由前端解析。敏感 hash 仅私有本地存储且不通过状态 API 返回；A2 凭据单元
// 永不持久化任何 hash（凭据 hash 不落盘）。目标身份不能只用 distro 名称：
// generation 变化时旧登记失效（不自动迁移授权，退回未托管/重新确认）。

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// stateSchemaVersion 是清单 schema 版本（S0 = 1）。
const stateSchemaVersion = 1

// StateFileName 是清单文件名（落在 CodeBox 配置目录）。
const StateFileName = "wsl-config-supply.json"

// StateEntry 是单单元登记信息。SourceHash/LastWrittenHash 仅供普通托管单元
// （TrackedProjection 等）使用；A2 单元恒为空。
type StateEntry struct {
	Unit             UnitID    `json:"unit"`
	Policy           Policy    `json:"policy"`
	Generation       string    `json:"generation,omitempty"`
	SourceRevision   uint64    `json:"sourceRevision,omitempty"`
	SourceHash       string    `json:"sourceHash,omitempty"`
	LastWrittenHash  string    `json:"lastWrittenHash,omitempty"`
	TransformVersion string    `json:"transformVersion,omitempty"`
	Frozen           bool      `json:"frozen,omitempty"`
	FrozenReason     string    `json:"frozenReason,omitempty"`
	LastSuccess      time.Time `json:"lastSuccess,omitempty"`
	LastErrorCode    string    `json:"lastErrorCode,omitempty"`
	LastOutcome      string    `json:"lastOutcome,omitempty"`
}

// StateFile 是清单整体。PendingSources 记录保存事件发布的待同步来源与
// revision（MarkDirty 落盘，重启可补漏；S0 不调度）。
type StateFile struct {
	SchemaVersion   int               `json:"schemaVersion"`
	Generation      string            `json:"generation,omitempty"`
	Distro          string            `json:"distro,omitempty"`
	Home            string            `json:"home,omitempty"`
	LastInspectedAt time.Time         `json:"lastInspectedAt,omitempty"`
	DesiredRevision uint64            `json:"desiredRevision,omitempty"`
	PendingSources  map[string]uint64 `json:"pendingSources,omitempty"`
	Units           []StateEntry      `json:"units"`
}

// stateStore 持有清单的内存副本与落盘路径，串行化所有读改写。
type stateStore struct {
	mu      sync.Mutex
	path    string
	current StateFile
	loaded  bool
}

func newStateStore(configDir string) *stateStore {
	return &stateStore{path: filepath.Join(configDir, StateFileName)}
}

// load 读取清单（不存在时视为空清单；损坏时退回空清单并保留损坏文件不动——
// 「登记信息损坏/丢失时退回未托管」，不因状态库丢失重置 WSL 配置，design §三）。
func (s *stateStore) load() StateFile {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadLocked()
}

func (s *stateStore) loadLocked() StateFile {
	if s.loaded {
		return s.current
	}
	s.loaded = true
	data, err := os.ReadFile(s.path)
	if err != nil {
		return s.current // 未初始化的零值清单（schemaVersion 落盘时补）
	}
	var f StateFile
	if json.Unmarshal(data, &f) != nil || f.SchemaVersion > stateSchemaVersion {
		return s.current
	}
	if f.Units == nil {
		f.Units = []StateEntry{}
	}
	if f.PendingSources == nil {
		f.PendingSources = map[string]uint64{}
	}
	s.current = f
	return s.current
}

// saveLocked 持久化当前清单（私有原子写：MkdirAll → tmp 0600 → rename →
// chmod 0600）。调用方必须持有 s.mu。
func (s *stateStore) saveLocked() error {
	s.current.SchemaVersion = stateSchemaVersion
	b, err := json.MarshalIndent(s.current, "", "  ")
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

// entry 返回单元登记（无登记返回零值与 false）。
func (s *StateFile) entry(unit UnitID) (StateEntry, bool) {
	for i := range s.Units {
		if s.Units[i].Unit == unit {
			return s.Units[i], true
		}
	}
	return StateEntry{}, false
}

// upsertEntry 以目标 generation 为界更新单元登记；generation 变化（非 unknown→
// 同值）时先作废旧登记再写入新值（旧授权不自动扩散）。
func (s *StateFile) upsertEntry(entry StateEntry, generation string) {
	if existing, ok := s.entry(entry.Unit); ok && existing.Generation != "" &&
		generation != "" && existing.Generation != generation {
		// 目标代际变化：旧登记整体失效，按新代重记（不带任何旧 hash 继承）。
		for i := range s.Units {
			if s.Units[i].Unit == entry.Unit {
				s.Units[i] = entry
				s.Generation = generation
				return
			}
		}
	}
	for i := range s.Units {
		if s.Units[i].Unit == entry.Unit {
			s.Units[i] = entry
			return
		}
	}
	s.Units = append(s.Units, entry)
}

// mutate 在清单锁内完成一次读改写：fn 拿到当前清单指针，修改后原子落盘
// （失败时内存态保留修改、返回错误，由调用方决定是否告警——下次写盘会带上
// 同样的状态，不会丢失）。所有清单变更必须经 mutate，禁止在 load 返回的
// 副本上修改后漏存。
func (s *stateStore) mutate(fn func(*StateFile)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.loadLocked()
	fn(&st)
	s.current = st
	return s.saveLocked()
}
