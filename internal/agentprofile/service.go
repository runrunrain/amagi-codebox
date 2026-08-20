// Package agentprofile 管理命名 agent 配置档（公司/家一键切换）：
// 把当前 live 的 amagi 配置（pi 的 ~/.pi/agent/amagi.json 与 omp 的
// ~/.omp/agent/amagi.json）快照为命名配置档，并一键应用回 live 文件。
//
// 存储：~/.amagi-codebox/agent-profiles.json（0600，临时文件 + rename
// 原子写入，目录 0700），形状：
//
//	{"version":1,"profiles":{"<name>":{"pi":"<amagi.json 全文>",
//	  "omp":"<amagi.json 全文，可为空串>","updatedAt":<epoch ms>}},
//	 "lastApplied":"<name 或空>"}
//
// agentDir 解析复刻 piconfig / ompconfig 现有语义：
// 优先 $PI_CODING_AGENT_DIR（pi 落 ~/.pi/agent，omp 落 ~/.omp/agent）。
package agentprofile

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// storeVersion 是配置档存储的当前版本；不兼容的未来版本应迁移而非混读。
const storeVersion = 1

// maxProfileNameLength 配置档名长度上限（字符，非字节）。
const maxProfileNameLength = 64

// profile 是单个命名配置档：pi / omp 两侧 amagi.json 的全文快照。
type profile struct {
	Pi        string `json:"pi"`
	Omp       string `json:"omp"`
	UpdatedAt int64  `json:"updatedAt"`
}

// profileStore 是 agent-profiles.json 的根结构。
type profileStore struct {
	Version     int                `json:"version"`
	Profiles    map[string]profile `json:"profiles"`
	LastApplied string             `json:"lastApplied"`
}

// Service 提供命名配置档的快照 / 保存 / 应用 / 删除访问。
// 无状态：每次调用时解析 agentDir 与存储路径，保证路径始终最新。
type Service struct{}

// NewService 创建 agent 配置档服务。
func NewService() *Service {
	return &Service{}
}

// piAgentDir 返回 pi 的配置根目录（复刻 piconfig agentDir 语义）。
func piAgentDir() string {
	if env := strings.TrimSpace(os.Getenv("PI_CODING_AGENT_DIR")); env != "" {
		return env
	}
	home, err := os.UserHomeDir()
	if err == nil && strings.TrimSpace(home) != "" {
		return filepath.Join(home, ".pi", "agent")
	}
	return filepath.Join(".", ".pi", "agent")
}

// ompAgentDir 返回 omp 的配置根目录（复刻 ompconfig agentDir 语义）。
func ompAgentDir() string {
	if env := strings.TrimSpace(os.Getenv("PI_CODING_AGENT_DIR")); env != "" {
		return env
	}
	home, err := os.UserHomeDir()
	if err == nil && strings.TrimSpace(home) != "" {
		return filepath.Join(home, ".omp", "agent")
	}
	return filepath.Join(".", ".omp", "agent")
}

// storeDir 返回配置档存储目录（~/.amagi-codebox）。
func storeDir() string {
	home, err := os.UserHomeDir()
	if err == nil && strings.TrimSpace(home) != "" {
		return filepath.Join(home, ".amagi-codebox")
	}
	return filepath.Join(".", ".amagi-codebox")
}

func storePath() string {
	return filepath.Join(storeDir(), "agent-profiles.json")
}

// validateProfileName 校验配置档名：去首尾空白后非空且 ≤64 字符，
// 返回规范化（trim 后）的名字。
func validateProfileName(name string) (string, error) {
	n := strings.TrimSpace(name)
	if n == "" {
		return "", errors.New("invalid profile name: must not be empty")
	}
	if len([]rune(n)) > maxProfileNameLength {
		return "", fmt.Errorf("invalid profile name: must be at most %d characters", maxProfileNameLength)
	}
	return n, nil
}

// emptyStore 返回全新空存储（version=1、空 profiles、lastApplied 为空）。
func emptyStore() profileStore {
	return profileStore{
		Version:     storeVersion,
		Profiles:    map[string]profile{},
		LastApplied: "",
	}
}

// loadStore 读取 agent-profiles.json。文件缺失时返回空存储；
// JSON 非法或版本不是当前版本时报错（fail closed，不猜测迁移）。
func loadStore() (profileStore, error) {
	store := emptyStore()
	data, err := os.ReadFile(storePath())
	if errors.Is(err, os.ErrNotExist) {
		return store, nil
	}
	if err != nil {
		return store, fmt.Errorf("read agent profiles store: %w", err)
	}
	if err := json.Unmarshal(data, &store); err != nil {
		return store, fmt.Errorf("parse agent profiles store: %w", err)
	}
	if store.Version != storeVersion {
		return store, fmt.Errorf("unsupported agent profiles store version %d (expected %d)", store.Version, storeVersion)
	}
	if store.Profiles == nil {
		store.Profiles = map[string]profile{}
	}
	return store, nil
}

// saveStore 序列化并原子写入 agent-profiles.json（0600，目录 0700）。
func saveStore(store profileStore) error {
	out, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return fmt.Errorf("encode agent profiles store: %w", err)
	}
	out = append(out, '\n')

	path := storePath()
	if err := ensureDir(path); err != nil {
		return err
	}
	return writePrivateFile(path, out)
}

// ensureDir creates the parent directory of the given path if it does not
// exist (0700：存储含 agent 配置全文，按私有对待)。
func ensureDir(path string) error {
	dir := filepath.Dir(path)
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("create directory %s: %w", dir, err)
		}
	} else if err != nil {
		return fmt.Errorf("stat directory %s: %w", dir, err)
	}
	return nil
}

// writePrivateFile 临时文件 + rename 原子写入，权限 0600；
// rename 失败（如 Windows 文件被占用）时回退直接覆盖。
func writePrivateFile(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write temp file %s: %w", tmp, err)
	}
	defer func() { _ = os.Remove(tmp) }()
	_ = os.Chmod(tmp, 0o600)

	if err := os.Rename(tmp, path); err == nil {
		_ = os.Chmod(path, 0o600)
		return nil
	} else if overwriteErr := os.WriteFile(path, data, 0o600); overwriteErr == nil {
		_ = os.Chmod(path, 0o600)
		return nil
	} else {
		return fmt.Errorf("replace config file %s: %w", path, err)
	}
}

// ListAgentProfiles 返回存储全文 JSON（前端解析展示）。文件缺失时返回
// 空存储骨架。
func (s *Service) ListAgentProfiles() (string, error) {
	store, err := loadStore()
	if err != nil {
		return "", err
	}
	out, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode agent profiles: %w", err)
	}
	return string(out), nil
}

// GetAgentProfile 返回单个配置档的 JSON（{pi,omp,updatedAt}，预览用）。
func (s *Service) GetAgentProfile(name string) (string, error) {
	n, err := validateProfileName(name)
	if err != nil {
		return "", err
	}
	store, err := loadStore()
	if err != nil {
		return "", err
	}
	p, ok := store.Profiles[n]
	if !ok {
		return "", fmt.Errorf("agent profile %q not found", n)
	}
	out, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode agent profile %q: %w", n, err)
	}
	return string(out), nil
}

// CaptureAgentProfile 把当前 live 配置快照为命名配置档（存在则覆盖）：
// pi 的 amagi.json 必须存在且可读；omp 的 amagi.json 缺失时记空串。
func (s *Service) CaptureAgentProfile(name string) error {
	n, err := validateProfileName(name)
	if err != nil {
		return err
	}

	piData, err := os.ReadFile(filepath.Join(piAgentDir(), "amagi.json"))
	if err != nil {
		return fmt.Errorf("read live pi amagi config: %w", err)
	}
	ompData, err := os.ReadFile(filepath.Join(ompAgentDir(), "amagi.json"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read live omp amagi config: %w", err)
	}

	store, err := loadStore()
	if err != nil {
		return err
	}
	store.Profiles[n] = profile{
		Pi:        string(piData),
		Omp:       string(ompData),
		UpdatedAt: time.Now().UnixMilli(),
	}
	return saveStore(store)
}

// SaveAgentProfile 显式内容保存（前端编辑后落档）。非空内容必须是合法
// JSON，非法报错不写。
func (s *Service) SaveAgentProfile(name, piContent, ompContent string) error {
	n, err := validateProfileName(name)
	if err != nil {
		return err
	}
	if err := validateJSONContent("pi", piContent); err != nil {
		return err
	}
	if err := validateJSONContent("omp", ompContent); err != nil {
		return err
	}

	store, err := loadStore()
	if err != nil {
		return err
	}
	store.Profiles[n] = profile{
		Pi:        piContent,
		Omp:       ompContent,
		UpdatedAt: time.Now().UnixMilli(),
	}
	return saveStore(store)
}

// validateJSONContent 校验非空内容是合法 JSON（空串表示“该侧不管理”，放行）。
func validateJSONContent(side, content string) error {
	if content == "" {
		return nil
	}
	if !json.Valid([]byte(content)) {
		return fmt.Errorf("invalid %s content: not valid JSON", side)
	}
	return nil
}

// ApplyAgentProfile 应用命名配置档到 live 文件：
//   - pi 内容非空则写入 ~/.pi/agent/amagi.json（原子写 + 0600）；
//   - omp 内容非空且 omp agentDir 存在时写入 ~/.omp/agent/amagi.json；
//   - 写前对每个被覆盖的已有 live 文件生成 <file>.bak-<epoch ms> 备份
//     （仅保留一份，新备份覆盖旧）；
//   - 成功后更新 lastApplied。
//
// 内容非法（非合法 JSON）时报错且不写任何文件。
func (s *Service) ApplyAgentProfile(name string) error {
	n, err := validateProfileName(name)
	if err != nil {
		return err
	}

	store, err := loadStore()
	if err != nil {
		return err
	}
	p, ok := store.Profiles[n]
	if !ok {
		return fmt.Errorf("agent profile %q not found", n)
	}
	if err := validateJSONContent("pi", p.Pi); err != nil {
		return err
	}
	if err := validateJSONContent("omp", p.Omp); err != nil {
		return err
	}

	nowMs := time.Now().UnixMilli()

	if p.Pi != "" {
		path := filepath.Join(piAgentDir(), "amagi.json")
		if err := backupLiveFile(path, nowMs); err != nil {
			return err
		}
		if err := ensureDir(path); err != nil {
			return err
		}
		if err := writePrivateFile(path, []byte(p.Pi)); err != nil {
			return err
		}
	}

	if p.Omp != "" {
		dir := ompAgentDir()
		if dirExists(dir) {
			path := filepath.Join(dir, "amagi.json")
			if err := backupLiveFile(path, nowMs); err != nil {
				return err
			}
			if err := writePrivateFile(path, []byte(p.Omp)); err != nil {
				return err
			}
		}
	}

	store.LastApplied = n
	return saveStore(store)
}

// DeleteAgentProfile 删除命名配置档；删除的是 lastApplied 时同步清空。
func (s *Service) DeleteAgentProfile(name string) error {
	n, err := validateProfileName(name)
	if err != nil {
		return err
	}
	store, err := loadStore()
	if err != nil {
		return err
	}
	if _, ok := store.Profiles[n]; !ok {
		return fmt.Errorf("agent profile %q not found", n)
	}
	delete(store.Profiles, n)
	if store.LastApplied == n {
		store.LastApplied = ""
	}
	return saveStore(store)
}

// dirExists 判断目录是否存在（错误视为不存在）。
func dirExists(dir string) bool {
	info, err := os.Stat(dir)
	return err == nil && info.IsDir()
}

// backupLiveFile 为将被覆盖的 live 文件生成 <file>.bak-<epoch ms> 备份；
// 仅保留一份：写入新备份后移除同前缀的旧备份。文件不存在时无需备份。
func backupLiveFile(path string, nowMs int64) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read live config for backup %s: %w", path, err)
	}
	bak := fmt.Sprintf("%s.bak-%d", path, nowMs)
	if err := os.WriteFile(bak, data, 0o600); err != nil {
		return fmt.Errorf("write backup %s: %w", bak, err)
	}
	_ = os.Chmod(bak, 0o600)

	matches, err := filepath.Glob(path + ".bak-*")
	if err != nil {
		return nil // glob 失败不影响主流程，旧备份留待下次清理
	}
	for _, m := range matches {
		if m != bak {
			_ = os.Remove(m)
		}
	}
	return nil
}
