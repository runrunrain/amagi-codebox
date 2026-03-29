package secrets

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/billgraziano/dpapi"
)

type SecretsService struct {
	secretsPath string
	cache       map[string]string // provider -> apikey
	mu          sync.RWMutex
}

func NewSecretsService(configDir string) *SecretsService {
	return &SecretsService{
		secretsPath: filepath.Join(configDir, "secrets.enc"),
		cache:       map[string]string{},
	}
}

func (s *SecretsService) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	b, err := os.ReadFile(s.secretsPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			s.cache = map[string]string{}
			return nil
		}
		return fmt.Errorf("read secrets: %w", err)
	}

	if len(b) == 0 {
		s.cache = map[string]string{}
		return nil
	}

	plaintext, err := dpapi.Decrypt(string(b))
	if err != nil {
		return fmt.Errorf("dpapi decrypt: %w", err)
	}

	var m map[string]string
	if err := json.Unmarshal([]byte(plaintext), &m); err != nil {
		return fmt.Errorf("parse secrets json: %w", err)
	}
	if m == nil {
		m = map[string]string{}
	}
	s.cache = m
	return nil
}

func (s *SecretsService) Save() error {
	s.mu.RLock()
	m := make(map[string]string, len(s.cache))
	for k, v := range s.cache {
		m[k] = v
	}
	path := s.secretsPath
	s.mu.RUnlock()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir secrets dir: %w", err)
	}

	plaintext, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("marshal secrets: %w", err)
	}

	ciphertext, err := dpapi.Encrypt(string(plaintext))
	if err != nil {
		return fmt.Errorf("dpapi encrypt: %w", err)
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(ciphertext), 0o600); err != nil {
		return fmt.Errorf("write temp secrets: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replace secrets: %w", err)
	}
	return nil
}

func (s *SecretsService) GetAPIKey(provider string) (string, error) {
	if provider == "" {
		return "", errors.New("provider is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.getAPIKeyLocked(provider)
}

// getAPIKeyLocked 内部方法，调用方必须持有读锁或写锁。
func (s *SecretsService) getAPIKeyLocked(provider string) (string, error) {
	if s.cache == nil {
		return "", errors.New("secrets not loaded")
	}
	return s.cache[provider], nil
}

func (s *SecretsService) SetAPIKey(provider, apiKey string) error {
	if provider == "" {
		return errors.New("provider is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cache == nil {
		s.cache = map[string]string{}
	}
	s.cache[provider] = strings.TrimSpace(apiKey)
	return nil
}

func (s *SecretsService) DeleteAPIKey(provider string) error {
	if provider == "" {
		return errors.New("provider is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cache == nil {
		s.cache = map[string]string{}
	}
	delete(s.cache, provider)
	return nil
}

func (s *SecretsService) HasAPIKey(provider string) bool {
	key, err := s.GetAPIKey(provider)
	return err == nil && key != ""
}

func (s *SecretsService) GetAllProviders() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.cache == nil {
		return []string{}
	}
	providers := make([]string, 0, len(s.cache))
	for k := range s.cache {
		providers = append(providers, k)
	}
	sort.Strings(providers)
	return providers
}

// GetAPIKeyWithFallback 先查存储，再查环境变量。
// 返回 (apiKey, source)，source 为 "stored" 或命中的环境变量名；都未命中则返回 ("", "").
func (s *SecretsService) GetAPIKeyWithFallback(provider string) (string, string) {
	s.mu.RLock()
	key, err := s.getAPIKeyLocked(provider)
	s.mu.RUnlock()
	if err == nil && key != "" {
		return strings.TrimSpace(key), "stored"
	}
	envVars := getProviderEnvVars(provider)
	for _, envVar := range envVars {
		if val := os.Getenv(envVar); val != "" {
			return strings.TrimSpace(val), envVar
		}
	}
	return "", ""
}

func getProviderEnvVars(provider string) []string {
	switch provider {
	case "openai":
		return []string{"OPENAI_API_KEY"}
	case "glm":
		return []string{"GLM46_API_KEY", "GLM_API_KEY", "ZHIPU_API_KEY", "BIGMODEL_API_KEY"}
	case "minimax":
		return []string{"MINIMAX_API_KEY", "MM_API_KEY"}
	case "deepseek":
		return []string{"DEEPSEEK_API_KEY"}
	case "bailian":
		return []string{"BAILIAN_API_KEY", "DASHSCOPE_API_KEY", "ALIBABA_API_KEY", "ZAI_API_KEY"}
	default:
		return nil
	}
}

// GetZhipuAPIKey 读取智谱 API Key。
func (s *SecretsService) GetZhipuAPIKey() string {
	key, _ := s.GetAPIKey("zhipu")
	return key
}

// SetZhipuAPIKey 存储智谱 API Key。
func (s *SecretsService) SetZhipuAPIKey(key string) error {
	return s.SetAPIKey("zhipu", key)
}

// GetMinimaxAPIKey 读取 MiniMax API Key。
func (s *SecretsService) GetMinimaxAPIKey() string {
	key, _ := s.GetAPIKey("minimax_codex")
	return key
}

// SetMinimaxAPIKey 存储 MiniMax API Key。
func (s *SecretsService) SetMinimaxAPIKey(key string) error {
	return s.SetAPIKey("minimax_codex", key)
}

// MaskKey 返回密钥的掩码版本，保留前4后4，中间用*替代。
func MaskKey(key string) string {
	if len(key) <= 8 {
		return "***"
	}
	return key[:4] + "..." + key[len(key)-4:]
}

// GetKeyDiagnostics 返回所有提供商的密钥来源诊断信息。
func (s *SecretsService) GetKeyDiagnostics(providerNames []string) map[string]map[string]string {
	result := make(map[string]map[string]string, len(providerNames))
	for _, name := range providerNames {
		info := map[string]string{}
		key, source := s.GetAPIKeyWithFallback(name)
		info["source"] = source
		info["masked_key"] = MaskKey(key)
		info["key_length"] = fmt.Sprintf("%d", len(key))

		// 检查存储的密钥
		s.mu.RLock()
		storedKey, _ := s.getAPIKeyLocked(name)
		s.mu.RUnlock()
		if storedKey != "" {
			info["has_stored"] = "true"
			info["stored_masked"] = MaskKey(storedKey)
		} else {
			info["has_stored"] = "false"
		}

		// 检查环境变量
		envVars := getProviderEnvVars(name)
		for _, ev := range envVars {
			if val := os.Getenv(ev); val != "" {
				info["env_"+ev] = MaskKey(val)
			}
		}

		result[name] = info
	}
	return result
}
