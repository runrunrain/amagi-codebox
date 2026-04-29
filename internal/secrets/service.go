package secrets

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

type SecretsService struct {
	secretsPath string
	store       SecretStore
	cache       map[string]string // provider -> apikey
	mu          sync.RWMutex
}

func NewSecretsService(configDir string) *SecretsService {
	return &SecretsService{
		secretsPath: filepath.Join(configDir, "secrets.enc"),
		store:       NewSecretStore(),
		cache:       map[string]string{},
	}
}

// NewSecretsServiceWithStore creates a SecretsService with an injected
// SecretStore implementation. This is intended for tests that need to
// avoid hitting the real OS keychain.
func NewSecretsServiceWithStore(configDir string, store SecretStore) *SecretsService {
	return &SecretsService{
		secretsPath: filepath.Join(configDir, "secrets.enc"),
		store:       store,
		cache:       map[string]string{},
	}
}

func (s *SecretsService) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	loaded, err := s.store.Load(s.secretsPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			s.cache = map[string]string{}
			return nil
		}
		return err
	}
	if loaded == nil {
		s.cache = map[string]string{}
		return nil
	}
	s.cache = loaded
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

	err := s.store.Save(path, m)
	if err != nil && errors.Is(err, ErrSecretStoreNotReady) && !hasNonEmptySecrets(m) {
		return nil
	}
	return err
}

func hasNonEmptySecrets(values map[string]string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return true
		}
	}
	return false
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
		info["secure_store_kind"] = s.store.Kind()
		if legacyPath := s.store.LegacyImportPath(s.secretsPath); legacyPath != "" {
			info["legacy_store_path"] = legacyPath
		}
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
