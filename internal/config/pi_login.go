package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

// PiLoginNamespace 是登录引用的首段保留字。裸段 "pi-login" 仍是合法的普通
// provider 名；登录引用必须同时满足结构规则（见 ParsePiLoginProviderID /
// ParsePiLoginPresetKey），仅前缀匹配不能判定为登录引用——否则普通 provider
// "pi-login" 的公共预设 stable key（如 "pi-login/Daily"）会被劫持进登录通道。
const PiLoginNamespace = "pi-login"

// PiLoginProviderID 生成登录来源 id：pi-login/<base64url(provider)>。
// 编码段不含 "/"，与公共 provider 名（禁 "/"）在结构上可区分。
func PiLoginProviderID(provider string) string {
	return PiLoginNamespace + "/" + base64.RawURLEncoding.EncodeToString([]byte(provider))
}

// PiLoginPresetKey 生成登录预设 key：pi-login/<base64url(provider)>/<base64url(name)>。
func PiLoginPresetKey(provider, name string) string {
	return PiLoginProviderID(provider) + "/" + base64.RawURLEncoding.EncodeToString([]byte(name))
}

// ParsePiLoginProviderID 解析登录来源 id（恰好两段，第二段为非空 base64url）。
// 裸 "pi-login"（普通 provider 名）与任意公共 stable key 均不匹配。
func ParsePiLoginProviderID(id string) (string, bool) {
	parts := strings.Split(id, "/")
	if len(parts) != 2 || parts[0] != PiLoginNamespace {
		return "", false
	}
	return decodeLoginSegment(parts[1])
}

// ParsePiLoginPresetKey 解析登录预设 key（恰好三段，后两段为非空 base64url）。
// 公共 stable key "pi-login/Daily"（两段）不匹配；公共 preset 名本身含 "/"
// 的三段 key 结构上可能同形，但登录判定以专用存储的精确 key 匹配为准
// （见 ConfigService.ResolvePiLoginLaunch），不凭结构单独判定。
func ParsePiLoginPresetKey(key string) (provider, name string, ok bool) {
	parts := strings.Split(key, "/")
	if len(parts) != 3 || parts[0] != PiLoginNamespace {
		return "", "", false
	}
	provider, ok = decodeLoginSegment(parts[1])
	if !ok {
		return "", "", false
	}
	name, ok = decodeLoginSegment(parts[2])
	if !ok {
		return "", "", false
	}
	return provider, name, true
}

// decodeLoginSegment 校验并解码单个 base64url 段（非空、无 padding、可解码）。
func decodeLoginSegment(seg string) (string, bool) {
	if seg == "" || strings.ContainsAny(seg, "=+/") {
		return "", false
	}
	decoded, err := base64.RawURLEncoding.DecodeString(seg)
	if err != nil || len(decoded) == 0 {
		return "", false
	}
	return string(decoded), true
}

// PiLoginPreset contains only native Pi launch arguments, never API configuration.
type PiLoginPreset struct {
	Name     string `json:"name"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Thinking string `json:"thinking,omitempty"`
}

func (p PiLoginPreset) Validate() error {
	if strings.TrimSpace(p.Name) == "" || strings.TrimSpace(p.Provider) == "" || strings.TrimSpace(p.Model) == "" {
		return errors.New("login preset requires name, provider and model")
	}
	switch p.Thinking {
	case "", "off", "minimal", "low", "medium", "high", "xhigh", "max":
	default:
		return errors.New("invalid Pi thinking level")
	}
	return nil
}

func (s *ConfigService) GetPiLoginPresets() (map[string]PiLoginPreset, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.config == nil {
		return nil, errors.New("config not loaded")
	}
	out := make(map[string]PiLoginPreset, len(s.config.PiLoginPresets))
	for k, p := range s.config.PiLoginPresets {
		out[k] = p
	}
	return out, nil
}

func (s *ConfigService) SavePiLoginPreset(p PiLoginPreset) error {
	p.Name = strings.TrimSpace(p.Name)
	p.Provider = strings.TrimSpace(p.Provider)
	p.Model = strings.TrimSpace(p.Model)
	if err := p.Validate(); err != nil {
		return err
	}
	key := PiLoginPresetKey(p.Provider, p.Name)
	// 与公共 stable key 的互斥：同字符串 key 不允许同时存在两个语义，
	// 否则导入/旧数据路径（SetAllTerminalPresets 不走 Save 校验）会产生歧义。
	if collision := s.publicPresetKeyExists(key); collision {
		return fmt.Errorf("login preset key %q collides with an existing public preset", key)
	}
	return s.changePiLoginPreset(key, &p)
}

func (s *ConfigService) DeletePiLoginPreset(key string) error {
	if _, _, ok := ParsePiLoginPresetKey(key); !ok {
		return errors.New("invalid login preset key")
	}
	return s.changePiLoginPreset(key, nil)
}

func (s *ConfigService) changePiLoginPreset(key string, preset *PiLoginPreset) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.config == nil {
		return errors.New("config not loaded")
	}
	next := *s.config
	next.PiLoginPresets = make(map[string]PiLoginPreset, len(s.config.PiLoginPresets)+1)
	for k, p := range s.config.PiLoginPresets {
		next.PiLoginPresets[k] = p
	}
	if preset == nil {
		delete(next.PiLoginPresets, key)
	} else {
		next.PiLoginPresets[key] = *preset
	}
	if err := s.saveLockedConfig(&next); err != nil {
		return err
	}
	s.config = &next
	return nil
}

func (s *ConfigService) ResolvePiLoginPreset(key string) (*PiLoginPreset, error) {
	presets, err := s.GetPiLoginPresets()
	if err != nil {
		return nil, err
	}
	p, ok := presets[key]
	if !ok || PiLoginPresetKey(p.Provider, p.Name) != key {
		return nil, errors.New("Pi login preset not found")
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return &p, nil
}

// ResolvePiLoginLaunch 是启动链的统一登录分类器（desktop / v1 / restart 共用）。
//
// 判定规则（与公共 stable key 真正不相交）：
//  1. modelRef 精确命中专用登录存储中的 key → 登录启动（isLogin=true, preset 非空）；
//     providerRef 必须为空或等于该登录来源 id，否则返回 mismatch 错误。
//  2. modelRef 未命中存储，但 providerRef 结构上是登录来源 id（普通 provider 名
//     不含 "/"，不可能撞上该形态）→ 登录选择已失效（isLogin=true, err 非 nil，
//     固定"不可再用"语义）。调用方必须失败关闭，不得回退普通路径。
//  3. 其余（含裸 "pi-login" provider、公共 "pi-login/Daily"、乃至结构同形的
//     公共 "pi-login/X/Y"）→ 普通路径（isLogin=false, err=nil）。
func (s *ConfigService) ResolvePiLoginLaunch(modelRef, providerRef string) (preset *PiLoginPreset, isLogin bool, err error) {
	// 存储不可读（如测试未 Load）时仅保留结构化判定：登录来源 id 形态的
	// providerRef 仍按登录失效失败关闭；精确 key 匹配降级为未命中，维持既有
	// 非 Pi 入口对未加载配置的行为（如全 workdir 失效 → FailureWorkdir）。
	loginPresets, _ := s.GetPiLoginPresets()
	if p, ok := loginPresets[modelRef]; ok && PiLoginPresetKey(p.Provider, p.Name) == modelRef {
		if providerRef != "" && providerRef != PiLoginProviderID(p.Provider) {
			return nil, true, errors.New("Pi login provider does not match preset")
		}
		if verr := p.Validate(); verr != nil {
			return nil, true, verr
		}
		return &p, true, nil
	}
	if _, isLoginProvider := ParsePiLoginProviderID(providerRef); isLoginProvider {
		return nil, true, errors.New("Pi login preset is no longer available; refresh providers and log in with Pi")
	}
	return nil, false, nil
}

// RejectPiLoginLaunchRefs 供非 Pi CLI 入口拒绝登录引用。首项是 providerRef，
// 只有该位置可按结构判定登录来源 id；其余项是预设/模型引用，仅按专用存储
// 精确匹配，避免将公共 "pi-login/Zm9v" 等可解码 stable key 误判为来源 id。
func (s *ConfigService) RejectPiLoginLaunchRefs(refs ...string) error {
	// 与 ResolvePiLoginLaunch 同策略：存储不可读时仅结构化判定，不改变既有
	// 未加载配置路径的行为。
	loginPresets, _ := s.GetPiLoginPresets()
	for i, ref := range refs {
		if i == 0 {
			if _, isLoginProvider := ParsePiLoginProviderID(ref); isLoginProvider {
				return errors.New("login authentication is only available for Pi")
			}
		}
		if p, ok := loginPresets[ref]; ok && PiLoginPresetKey(p.Provider, p.Name) == ref {
			return errors.New("login authentication is only available for Pi")
		}
	}
	return nil
}

// loginPresetKeyExists 报告给定 key 是否精确命中已存登录预设（读锁）。
func (s *ConfigService) loginPresetKeyExists(key string) bool {
	presets, err := s.GetPiLoginPresets()
	if err != nil {
		return false
	}
	p, ok := presets[key]
	return ok && PiLoginPresetKey(p.Provider, p.Name) == key
}

// publicPresetKeyExists 报告给定 key 是否已是公共预设（anthropic/openai 桶）的
// stable key。持锁调用（changePiLoginPreset 内）。
func (s *ConfigService) publicPresetKeyExists(key string) bool {
	if s.config == nil || s.config.TerminalPresets == nil {
		return false
	}
	for _, bucket := range []map[string]TerminalPreset{
		s.config.TerminalPresets.GetMap(TerminalPresetAnthropic),
		s.config.TerminalPresets.GetMap(TerminalPresetOpenAI),
	} {
		if _, ok := bucket[key]; ok {
			return true
		}
	}
	return false
}
