package paths

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// PathEntry 保存的启动路径
type PathEntry struct {
	Path  string `json:"path"`
	Label string `json:"label"`
}

// PathsConfig 路径配置文件结构
type PathsConfig struct {
	Paths       []PathEntry `json:"paths"`
	DefaultPath string      `json:"defaultPath"`
}

// PathsService 启动路径管理服务
type PathsService struct {
	configPath string
	config     *PathsConfig
	mu         sync.RWMutex
}

func NewPathsService(configDir string) *PathsService {
	return &PathsService{
		configPath: filepath.Join(configDir, "paths.json"),
		config:     &PathsConfig{Paths: []PathEntry{}},
	}
}

func (s *PathsService) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	b, err := os.ReadFile(s.configPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			s.config = &PathsConfig{Paths: []PathEntry{}}
			return nil
		}
		return fmt.Errorf("read paths config: %w", err)
	}

	var cfg PathsConfig
	if err := json.Unmarshal(b, &cfg); err != nil {
		return fmt.Errorf("parse paths json: %w", err)
	}
	if cfg.Paths == nil {
		cfg.Paths = []PathEntry{}
	}
	s.config = &cfg
	return nil
}

func (s *PathsService) Save() error {
	s.mu.RLock()
	cfg := s.config
	path := s.configPath
	s.mu.RUnlock()

	if cfg == nil {
		return errors.New("paths config not loaded")
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir paths dir: %w", err)
	}

	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal paths: %w", err)
	}
	b = append(b, '\n')

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return fmt.Errorf("write temp paths: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replace paths: %w", err)
	}
	return nil
}

func (s *PathsService) GetPaths() []PathEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.config == nil {
		return []PathEntry{}
	}
	out := make([]PathEntry, len(s.config.Paths))
	copy(out, s.config.Paths)
	return out
}

func (s *PathsService) GetDefaultPath() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.config == nil {
		return ""
	}
	return s.config.DefaultPath
}

func (s *PathsService) SetDefaultPath(path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.config == nil {
		return errors.New("paths config not loaded")
	}
	s.config.DefaultPath = path
	return nil
}

func (s *PathsService) AddPath(entry PathEntry) error {
	if entry.Path == "" {
		return errors.New("path is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.config == nil {
		return errors.New("paths config not loaded")
	}
	// 检查重复
	for _, p := range s.config.Paths {
		if p.Path == entry.Path {
			return fmt.Errorf("path already exists: %s", entry.Path)
		}
	}
	if entry.Label == "" {
		entry.Label = filepath.Base(entry.Path)
	}
	s.config.Paths = append(s.config.Paths, entry)
	return nil
}

func (s *PathsService) RemovePath(path string) error {
	if path == "" {
		return errors.New("path is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.config == nil {
		return errors.New("paths config not loaded")
	}
	for i, p := range s.config.Paths {
		if p.Path == path {
			s.config.Paths = append(s.config.Paths[:i], s.config.Paths[i+1:]...)
			if s.config.DefaultPath == path {
				s.config.DefaultPath = ""
			}
			return nil
		}
	}
	return fmt.Errorf("path not found: %s", path)
}

func (s *PathsService) UpdateLabel(path, label string) error {
	if path == "" {
		return errors.New("path is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.config == nil {
		return errors.New("paths config not loaded")
	}
	for i, p := range s.config.Paths {
		if p.Path == path {
			s.config.Paths[i].Label = label
			return nil
		}
	}
	return fmt.Errorf("path not found: %s", path)
}

// BrowseDirectory 打开目录选择对话框（需要在前端通过 Wails runtime 调用）
// 这里只用于验证目录是否存在
func (s *PathsService) ValidatePath(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}
