package sharedext

// service.go — Service 骨架：registry 读写（互斥锁 + 原子写）、引擎目标
// 路径解析（默认真实解析 home，支持注入覆盖以便测试）、聚合缓存状态。

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

	"amagi-codebox/internal/logging"
)

// registryFile 是 registry.json 的落盘结构。
type registryFile struct {
	Skills map[string]SharedSkill     `json:"skills"`
	MCP    map[string]SharedMCPServer `json:"mcp"`
}

// Service 提供公共 skill / 公共 MCP 的管理与分发。
// 所有公开方法内部经 s.mu 串行化（registry 与文件系统部署的一致性优先）。
type Service struct {
	mu       sync.Mutex
	baseDir  string         // ~/.amagi-codebox（仓库根的父目录）
	log      *logging.Service
	overrides map[Engine]string // 引擎基准根注入（测试/便携模式）；空 = 真实解析

	// 聚合缓存（30s TTL；registry 写操作失效）
	aggCache    []AggregatedSkill
	aggCachedAt time.Time
}

// NewService 创建共用扩展服务。baseDir 为 ~/.amagi-codebox（P2 注入），
// 单测传 t.TempDir()。log 可为 nil（nil 时静默）。
func NewService(baseDir string, log *logging.Service) *Service {
	return &Service{
		baseDir:   baseDir,
		log:       log,
		overrides: map[Engine]string{},
	}
}

func (s *Service) warn(message, detail string) {
	if s.log != nil {
		s.log.Warn("sharedext", message, detail)
	}
}

func (s *Service) info(message, detail string) {
	if s.log != nil {
		s.log.Info("sharedext", message, detail)
	}
}

// sharedRoot 返回共用仓库根（<baseDir>/shared）。
func (s *Service) sharedRoot() string { return filepath.Join(s.baseDir, "shared") }

// skillsRepoDir 返回 skill 仓库目录（shared/skills）。
func (s *Service) skillsRepoDir() string { return filepath.Join(s.sharedRoot(), "skills") }

// registryPath 返回 registry.json 路径。
func (s *Service) registryPath() string { return filepath.Join(s.sharedRoot(), "registry.json") }

// ---------------------------------------------------------------------------
// 引擎目标路径解析
// ---------------------------------------------------------------------------

// SetEngineBaseDir 注入某引擎的基准根目录（覆盖默认的 home 解析），
// 供测试（t.TempDir）与便携模式使用；传空恢复默认解析。
func (s *Service) SetEngineBaseDir(engine Engine, dir string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if dir == "" {
		delete(s.overrides, engine)
		return
	}
	s.overrides[engine] = dir
}

// engineBase 解析某引擎的基准根目录：
//   - claude:    home（skills → home/.claude/skills；mcp → home/.claude.json）
//   - codex:     home/.codex（skills → base/skills；mcp → base/config.toml）
//   - opencode:  home/.config/opencode（skills → base/skills；mcp → base/opencode.json）
//   - pi:        $PI_CODING_AGENT_DIR → ~/.pi/agent（复刻 internal/piconfig agentDir()）
//   - omp:       $PI_CODING_AGENT_DIR → ~/.omp/agent（复刻 internal/ompconfig agentDir()）
func (s *Service) engineBase(engine Engine) (string, error) {
	if dir, ok := s.overrides[engine]; ok && strings.TrimSpace(dir) != "" {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home for engine %s: %w", engine, err)
	}
	switch engine {
	case EngineClaude:
		return home, nil
	case EngineCodex:
		return filepath.Join(home, ".codex"), nil
	case EngineOpenCode:
		return filepath.Join(home, ".config", "opencode"), nil
	case EnginePi:
		return piAgentDir(home), nil
	case EngineOmp:
		return ompAgentDir(home), nil
	default:
		return "", fmt.Errorf("unknown engine %q", engine)
	}
}

// piAgentDir 复刻 internal/piconfig agentDir()：优先 $PI_CODING_AGENT_DIR。
func piAgentDir(home string) string {
	if env := strings.TrimSpace(os.Getenv("PI_CODING_AGENT_DIR")); env != "" {
		return env
	}
	if strings.TrimSpace(home) != "" {
		return filepath.Join(home, ".pi", "agent")
	}
	return filepath.Join(".", ".pi", "agent")
}

// ompAgentDir 复刻 internal/ompconfig agentDir()：优先 $PI_CODING_AGENT_DIR。
func ompAgentDir(home string) string {
	if env := strings.TrimSpace(os.Getenv("PI_CODING_AGENT_DIR")); env != "" {
		return env
	}
	if strings.TrimSpace(home) != "" {
		return filepath.Join(home, ".omp", "agent")
	}
	return filepath.Join(".", ".omp", "agent")
}

// engineSkillsDir 解析某引擎的 skills 目标目录。
func (s *Service) engineSkillsDir(engine Engine) (string, error) {
	base, err := s.engineBase(engine)
	if err != nil {
		return "", err
	}
	if engine == EngineClaude {
		return filepath.Join(base, ".claude", "skills"), nil
	}
	return filepath.Join(base, "skills"), nil
}

// mcpTarget 描述某引擎的 MCP 写入目标。
type mcpTarget struct {
	Path     string // 配置文件绝对路径
	Key      string // 顶层键名（mcpServers / mcp）
	Style    string // "json" | "toml"
	BackedUp bool   // 写入前是否做 .bak-sharedext-<ts> 备份（保留 3 份）
}

// engineMCPTarget 解析某引擎的 MCP 配置目标。
func (s *Service) engineMCPTarget(engine Engine) (mcpTarget, error) {
	base, err := s.engineBase(engine)
	if err != nil {
		return mcpTarget{}, err
	}
	switch engine {
	case EngineClaude:
		return mcpTarget{Path: filepath.Join(base, ".claude.json"), Key: "mcpServers", Style: "json", BackedUp: true}, nil
	case EngineCodex:
		return mcpTarget{Path: filepath.Join(base, "config.toml"), Style: "toml"}, nil
	case EngineOpenCode:
		return mcpTarget{Path: filepath.Join(base, "opencode.json"), Key: "mcp", Style: "json", BackedUp: true}, nil
	case EnginePi, EngineOmp:
		return mcpTarget{Path: filepath.Join(base, "mcp.json"), Key: "mcpServers", Style: "json"}, nil
	default:
		return mcpTarget{}, fmt.Errorf("unknown engine %q", engine)
	}
}

// ---------------------------------------------------------------------------
// registry 读写（调用方持锁）
// ---------------------------------------------------------------------------

// loadRegistryLocked 读取 registry.json；文件缺失返回空 registry。
func (s *Service) loadRegistryLocked() (*registryFile, error) {
	data, err := os.ReadFile(s.registryPath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &registryFile{Skills: map[string]SharedSkill{}, MCP: map[string]SharedMCPServer{}}, nil
		}
		return nil, fmt.Errorf("read registry: %w", err)
	}
	reg := &registryFile{Skills: map[string]SharedSkill{}, MCP: map[string]SharedMCPServer{}}
	if err := json.Unmarshal(data, reg); err != nil {
		return nil, fmt.Errorf("parse registry %s: %w", s.registryPath(), err)
	}
	if reg.Skills == nil {
		reg.Skills = map[string]SharedSkill{}
	}
	if reg.MCP == nil {
		reg.MCP = map[string]SharedMCPServer{}
	}
	return reg, nil
}

// saveRegistryLocked 原子写 registry.json（临时文件 + rename）。
func (s *Service) saveRegistryLocked(reg *registryFile) error {
	data, err := json.MarshalIndent(reg, "", "  ")
	if err != nil {
		return fmt.Errorf("encode registry: %w", err)
	}
	data = append(data, '\n')

	path := s.registryPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create registry dir: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write registry temp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		// rename 失败（如 Windows 目标被占用）回退直接覆盖并清理临时文件。
		_ = os.Remove(tmp)
		if werr := os.WriteFile(path, data, 0o600); werr != nil {
			return fmt.Errorf("replace registry %s: %w", path, err)
		}
	}
	s.invalidateAggCacheLocked()
	return nil
}

// ---------------------------------------------------------------------------
// 通用辅助
// ---------------------------------------------------------------------------

// normalizeEngines 去重、校验并按固定顺序排序引擎列表。
func normalizeEngines(engines []Engine) ([]Engine, error) {
	seen := map[Engine]bool{}
	out := make([]Engine, 0, len(engines))
	for _, e := range engines {
		if !ValidEngine(e) {
			return nil, fmt.Errorf("未知引擎标识 %q（支持 claude/codex/opencode/pi/omp）", string(e))
		}
		if seen[e] {
			continue
		}
		seen[e] = true
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}

// containsEngine 报告列表是否含引擎。
func containsEngine(list []Engine, e Engine) bool {
	for _, item := range list {
		if item == e {
			return true
		}
	}
	return false
}

// validSkillName 校验 skill/server 名可安全用作目录名与 JSON/TOML 键。
func validName(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." {
		return false
	}
	if strings.ContainsAny(name, "/\\\x00") {
		return false
	}
	return true
}

// nowRFC3339 返回当前时间的 RFC3339 表示。
func nowRFC3339() string { return time.Now().Format(time.RFC3339) }
