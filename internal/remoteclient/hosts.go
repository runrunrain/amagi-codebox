// hosts.go 主机登记簿（蓝图 §3/§5/§8）：
//
// 本机持久化的已配对主机列表，JSON 原子写（tmp+rename），存放于 settings 目录
// 旁路（不进 models.json）；提供增删改查、健康探活（probe → host/summary）、
// 显示名管理。登记簿的写权威是本机；Health/LastSeen 为探活投影。凭据 secret
// 永不入簿（仅 Keychain，D-T04）。hostPort 输入校验：host:1-65535 白名单（§9）。
package remoteclient

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"amagi-codebox/internal/remote/contract"
	"amagi-codebox/internal/secrets"
)

// HealthState 是登记簿条目的健康投影（客户端本地状态，非服务端事实）。
type HealthState string

const (
	HealthProbing     HealthState = "probing"
	HealthReachable   HealthState = "reachable"
	HealthUnreachable HealthState = "unreachable"
	HealthRevoked     HealthState = "revoked"
)

// HostEntry 是登记簿条目（蓝图 §5 领域模型）。ID/DisplayName/HostPort 为本机
// 可编辑字段；DeviceID 由配对流填入。
type HostEntry struct {
	ID          string      `json:"id"`
	DisplayName string      `json:"displayName"`
	HostPort    string      `json:"hostPort"`
	DeviceID    string      `json:"deviceId"`
	Health      HealthState `json:"health"`
	LastSeen    time.Time   `json:"lastSeen"`
}

// entryIDPrefix 是登记簿条目 ID 前缀（本机随机生成，与远端无关）。
const entryIDPrefix = "host-"

// newEntryID 生成登记簿条目 ID：host- + 16 hex。
func newEntryID() (string, error) {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return entryIDPrefix + hex.EncodeToString(buf), nil
}

// ---------------------------------------------------------------------------
// hostPort 白名单校验（蓝图 §9）
// ---------------------------------------------------------------------------

// ValidateHostPort 校验 "host:1-65535" 形态：host 为 DNS 名/IPv4/IPv6（带
// 括号），拒绝 userinfo、路径、空 host 与越界端口。返回规范化后的
// host:port（IPv6 恢复方括号、host 小写）。
func ValidateHostPort(hostPort string) (string, error) {
	raw := strings.TrimSpace(hostPort)
	if raw == "" || strings.ContainsAny(raw, "@/?#") {
		return "", fmt.Errorf("invalid hostPort %q", hostPort)
	}
	host, portStr, err := net.SplitHostPort(raw)
	if err != nil {
		return "", fmt.Errorf("invalid hostPort %q: missing port", hostPort)
	}
	if host == "" {
		return "", fmt.Errorf("invalid hostPort %q: empty host", hostPort)
	}
	var port int
	if _, err := fmt.Sscanf(portStr, "%d", &port); err != nil || fmt.Sprint(port) != portStr {
		return "", fmt.Errorf("invalid hostPort %q: bad port", hostPort)
	}
	if port < 1 || port > 65535 {
		return "", fmt.Errorf("invalid hostPort %q: port out of range", hostPort)
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if strings.Contains(host, ":") {
		// IPv6 字面量：必须可解析且无 zone。
		if ip := net.ParseIP(host); ip == nil || strings.Contains(host, "%") {
			return "", fmt.Errorf("invalid hostPort %q: bad IPv6 literal", hostPort)
		}
		return net.JoinHostPort(host, portStr), nil
	}
	// DNS/IPv4 标签校验：字母/数字/连字符，标签 1..63，总长 ≤253。
	if len(host) > 253 {
		return "", fmt.Errorf("invalid hostPort %q: host too long", hostPort)
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) < 1 || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return "", fmt.Errorf("invalid hostPort %q: bad host label", hostPort)
		}
		for i := 0; i < len(label); i++ {
			c := label[i]
			if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-') {
				return "", fmt.Errorf("invalid hostPort %q: bad host character", hostPort)
			}
		}
	}
	return net.JoinHostPort(host, portStr), nil
}

// ---------------------------------------------------------------------------
// 凭据存取（Keychain 条目，D-T04：secret 只进 OS 凭据库，不落盘/落日志）
// ---------------------------------------------------------------------------

// CredentialStore 抽象设备凭据的持久化：生产实现走 internal/secrets（DPAPI/
// Keychain 加密的 secrets 存储），测试注入内存实现。条目名
// codebox-remoteclient/<DeviceID>。
type CredentialStore interface {
	// Put 保存（覆盖）一条设备 secret。
	Put(entryName, secret string) error
	// Get 读取；不存在时返回 ("", nil)。
	Get(entryName string) (string, error)
	// Delete 删除；条目不存在视为成功。
	Delete(entryName string) error
}

// credentialEntryName 构造 Keychain 条目名（D-T04 冻结格式）。
func credentialEntryName(deviceID string) string {
	return "codebox-remoteclient/" + deviceID
}

// CredentialEntryName 是 credentialEntryName 的导出形态：App 转发层在
// Connect 入口做凭据恢复（登记簿 DeviceID → Keychain 取 secret）时需要按
// 同一冻结格式构造条目名；导出薄包装保证格式仍只有一处定义。
func CredentialEntryName(deviceID string) string {
	return credentialEntryName(deviceID)
}

// SecretsCredentialStore 将 internal/secrets 的 provider-key 语义适配为设备
// 凭据存取：条目即 provider key，值经平台加密后落 secrets.enc（Keychain/DPAPI
// 保护），明文 secret 永不出现在其它文件。
type SecretsCredentialStore struct {
	svc *secrets.SecretsService
}

// NewSecretsCredentialStore 在 configDir 上构建凭据存储（生产入口）。
func NewSecretsCredentialStore(configDir string) (*SecretsCredentialStore, error) {
	svc := secrets.NewSecretsService(configDir)
	if err := svc.Load(); err != nil {
		return nil, fmt.Errorf("load credential store: %w", err)
	}
	return &SecretsCredentialStore{svc: svc}, nil
}

// NewSecretsCredentialStoreWithService 用已构造的 SecretsService 构建凭据存储
// （App 转发层复用既有服务实例 / 测试注入 stub SecretStore）。
func NewSecretsCredentialStoreWithService(svc *secrets.SecretsService) (*SecretsCredentialStore, error) {
	if svc == nil {
		return nil, errors.New("nil SecretsService")
	}
	if err := svc.Load(); err != nil {
		return nil, fmt.Errorf("load credential store: %w", err)
	}
	return &SecretsCredentialStore{svc: svc}, nil
}

// Put 保存并立即持久化。
func (s *SecretsCredentialStore) Put(entryName, secret string) error {
	if err := s.svc.SetAPIKey(entryName, secret); err != nil {
		return err
	}
	return s.svc.Save()
}

// Get 读取；不存在返回 ("", nil)。
func (s *SecretsCredentialStore) Get(entryName string) (string, error) {
	return s.svc.GetAPIKey(entryName)
}

// Delete 删除并立即持久化。
func (s *SecretsCredentialStore) Delete(entryName string) error {
	if err := s.svc.DeleteAPIKey(entryName); err != nil {
		return err
	}
	return s.svc.Save()
}

// ---------------------------------------------------------------------------
// 登记簿（JSON 原子写 tmp+rename，蓝图 §8）
// ---------------------------------------------------------------------------

// HostRegistry 是本机持久化的主机登记簿。写权威 = 本机；每次变更即时落盘
// （原子写），读方始终拿到一致快照。
type HostRegistry struct {
	mu      sync.Mutex
	path    string
	entries []HostEntry
}

// NewHostRegistry 构造指向 path 的空登记簿（不读盘）。App 转发层在装载
// 失败时用它降级：保留同一路径，下一次写盘即重建文件。
func NewHostRegistry(path string) *HostRegistry {
	return &HostRegistry{path: path}
}

// LoadHostRegistry 从 path 装载登记簿；文件不存在返回空登记簿。
func LoadHostRegistry(path string) (*HostRegistry, error) {
	r := &HostRegistry{path: path}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return r, nil
	}
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return r, nil
	}
	var entries []HostEntry
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, fmt.Errorf("parse host registry %s: %w", path, err)
	}
	for i, e := range entries {
		if e.ID == "" {
			return nil, fmt.Errorf("host registry %s: entry %d has empty id", path, i)
		}
		if _, verr := ValidateHostPort(e.HostPort); verr != nil {
			return nil, fmt.Errorf("host registry %s: entry %s: %w", path, e.ID, verr)
		}
	}
	r.entries = entries
	return r, nil
}

// save 原子落盘：同目录 tmp 文件（0600）→ fsync → rename（蓝图 §8）。
func (r *HostRegistry) save() error {
	raw, err := json.MarshalIndent(r.entries, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(r.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(r.path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) //nolint:errcheck // rename 成功后为 no-op
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close() //nolint:errcheck // 已进入失败路径
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close() //nolint:errcheck // 已进入失败路径
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close() //nolint:errcheck // 已进入失败路径
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, r.path)
}

// Add 新增主机条目（未配对态：Health=probing、DeviceID 空）。hostPort 走
// 白名单校验并规范化；DisplayName 为空时默认 hostPort。
func (r *HostRegistry) Add(displayName, hostPort string) (HostEntry, error) {
	hp, err := ValidateHostPort(hostPort)
	if err != nil {
		return HostEntry{}, err
	}
	id, err := newEntryID()
	if err != nil {
		return HostEntry{}, err
	}
	name := strings.TrimSpace(displayName)
	if name == "" {
		name = hp
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	entry := HostEntry{
		ID:          id,
		DisplayName: name,
		HostPort:    hp,
		Health:      HealthProbing,
	}
	r.entries = append(r.entries, entry)
	if err := r.save(); err != nil {
		r.entries = r.entries[:len(r.entries)-1]
		return HostEntry{}, err
	}
	return entry, nil
}

// Get 按 ID 返回条目副本。
func (r *HostRegistry) Get(id string) (HostEntry, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, e := range r.entries {
		if e.ID == id {
			return e, true
		}
	}
	return HostEntry{}, false
}

// List 返回全部条目副本（保持落盘顺序）。
func (r *HostRegistry) List() []HostEntry {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]HostEntry, len(r.entries))
	copy(out, r.entries)
	return out
}

// Rename 修改显示名。
func (r *HostRegistry) Rename(id, displayName string) error {
	name := strings.TrimSpace(displayName)
	if name == "" {
		return errors.New("displayName must be non-empty")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.entries {
		if r.entries[i].ID == id {
			r.entries[i].DisplayName = name
			return r.save()
		}
	}
	return fmt.Errorf("host %q not found", id)
}

// UpdateHostPort 修改目标地址（白名单校验；重置健康投影与配对态——地址变了
// 旧凭据不再可信，DeviceID 清空待重新配对）。
func (r *HostRegistry) UpdateHostPort(id, hostPort string) error {
	hp, err := ValidateHostPort(hostPort)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.entries {
		if r.entries[i].ID == id {
			r.entries[i].HostPort = hp
			r.entries[i].DeviceID = ""
			r.entries[i].Health = HealthProbing
			r.entries[i].LastSeen = time.Time{}
			return r.save()
		}
	}
	return fmt.Errorf("host %q not found", id)
}

// Remove 删除条目（不触碰 Keychain；凭据清理走 PairingService.ForgetHost）。
func (r *HostRegistry) Remove(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.entries {
		if r.entries[i].ID == id {
			r.entries = append(r.entries[:i], r.entries[i+1:]...)
			return r.save()
		}
	}
	return fmt.Errorf("host %q not found", id)
}

// SetHealth 更新健康投影与 LastSeen（探活结果落盘）。
func (r *HostRegistry) SetHealth(id string, health HealthState, lastSeen time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.entries {
		if r.entries[i].ID == id {
			r.entries[i].Health = health
			if !lastSeen.IsZero() {
				r.entries[i].LastSeen = lastSeen
			}
			return r.save()
		}
	}
	return fmt.Errorf("host %q not found", id)
}

// upsertPaired 由配对流回填：按 DeviceID 匹配既有条目，其次按 HostPort；
// 都无则新增。返回条目 ID。DisplayName 属本机可编辑字段，已有值不覆盖。
func (r *HostRegistry) upsertPaired(hostPort, deviceID, displayName string, health HealthState, lastSeen time.Time) (string, error) {
	hp, err := ValidateHostPort(hostPort)
	if err != nil {
		return "", err
	}
	name := strings.TrimSpace(displayName)
	if name == "" {
		name = hp
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.entries {
		if r.entries[i].DeviceID == deviceID || r.entries[i].HostPort == hp {
			r.entries[i].DeviceID = deviceID
			r.entries[i].HostPort = hp
			r.entries[i].Health = health
			r.entries[i].LastSeen = lastSeen
			return r.entries[i].ID, r.save()
		}
	}
	id, err := newEntryID()
	if err != nil {
		return "", err
	}
	r.entries = append(r.entries, HostEntry{
		ID:          id,
		DisplayName: name,
		HostPort:    hp,
		DeviceID:    deviceID,
		Health:      health,
		LastSeen:    lastSeen,
	})
	if err := r.save(); err != nil {
		return "", err
	}
	return id, nil
}

// ---------------------------------------------------------------------------
// 健康探活（probe → GET host/summary）
// ---------------------------------------------------------------------------

// ProbeResult 是一次探活的结论：健康投影 + （200 时的）宿主摘要。
type ProbeResult struct {
	State   HealthState
	Summary *contract.HostSummary // 仅 200 时非 nil
}

// ProbeHost 对登记簿条目探活：构建 Transport，若条目已配对则注入凭据，
// GET host/summary。分类规则：200 → reachable；契约错误体（HTTP 往返完成）
// → reachable（宿主存活、讲 v1 协议），其中 auth.revoked → revoked；
// 网络层失败/非契约响应 → unreachable。lastSeen 为结论时刻。
func ProbeHost(ctx context.Context, entry HostEntry, creds CredentialStore) (ProbeResult, time.Time) {
	now := time.Now()
	t, err := NewTransport("http://" + entry.HostPort)
	if err != nil {
		return ProbeResult{State: HealthUnreachable}, now
	}
	var (
		hs   contract.HostSummary
		cerr *ClientError
	)
	if entry.DeviceID != "" && creds != nil {
		if secret, gerr := creds.Get(credentialEntryName(entry.DeviceID)); gerr == nil && secret != "" {
			_ = t.SetCredential(entry.DeviceID, secret)
			hs, cerr = t.HostSummary(ctx)
		} else {
			// 已配对但凭据缺失：退化为无鉴权探活（凭据恢复属上层职责）。
			hs, cerr = t.HostSummaryUnauthenticated(ctx)
		}
	} else {
		hs, cerr = t.HostSummaryUnauthenticated(ctx)
	}
	if cerr == nil {
		return ProbeResult{State: HealthReachable, Summary: &hs}, now
	}
	if cerr.Code() == contract.ErrorCodeAuthRevoked {
		return ProbeResult{State: HealthRevoked}, now
	}
	if cerr.API != nil && cerr.StatusCode > 0 {
		// 契约形态错误（如未配对 401 auth.unpaired）：宿主可达。
		return ProbeResult{State: HealthReachable}, now
	}
	return ProbeResult{State: HealthUnreachable}, now
}

// Probe 探活指定条目并把健康投影写回登记簿。
func (r *HostRegistry) Probe(ctx context.Context, id string, creds CredentialStore) (ProbeResult, error) {
	entry, ok := r.Get(id)
	if !ok {
		return ProbeResult{}, fmt.Errorf("host %q not found", id)
	}
	res, seen := ProbeHost(ctx, entry, creds)
	return res, r.SetHealth(id, res.State, seen)
}
