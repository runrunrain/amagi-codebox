// hosts_test.go — 登记簿测试：增删改查、JSON 原子写（tmp+rename 无残留）、
// hostPort 白名单、探活分类（reachable/revoked/unreachable）、凭据经
// internal/secrets 适配器往返 + secret 不落盘断言（D-T04）。
package remoteclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"amagi-codebox/internal/remote/contract"
	"amagi-codebox/internal/secrets"
)

// memCredentialStore 是 CredentialStore 的内存实现（测试注入）。
type memCredentialStore struct {
	mu    sync.Mutex
	items map[string]string
}

func newMemCredentialStore() *memCredentialStore {
	return &memCredentialStore{items: map[string]string{}}
}

func (m *memCredentialStore) Put(name, secret string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.items[name] = secret
	return nil
}

func (m *memCredentialStore) Get(name string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.items[name], nil
}

func (m *memCredentialStore) Delete(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.items, name)
	return nil
}

func (m *memCredentialStore) snapshot() map[string]string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]string{}
	for k, v := range m.items {
		out[k] = v
	}
	return out
}

func newTestRegistry(t *testing.T) (*HostRegistry, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "remote-hosts.json")
	r, err := LoadHostRegistry(path)
	if err != nil {
		t.Fatalf("LoadHostRegistry: %v", err)
	}
	return r, path
}

func TestHostRegistryCRUDAndPersistence(t *testing.T) {
	r, path := newTestRegistry(t)

	// 新增 + 规范化（大写 host/IPv6）。
	e1, err := r.Add("我的台式机", "Local.Host:8787")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if e1.HostPort != "local.host:8787" {
		t.Errorf("HostPort not normalized: %q", e1.HostPort)
	}
	if e1.Health != HealthProbing || e1.DeviceID != "" {
		t.Errorf("new entry must be unpaired+probing: %+v", e1)
	}
	e2, err := r.Add("", "[::1]:9000")
	if err != nil {
		t.Fatalf("Add IPv6: %v", err)
	}
	if e2.HostPort != "[::1]:9000" {
		t.Errorf("IPv6 HostPort = %q", e2.HostPort)
	}
	if e2.DisplayName != e2.HostPort {
		t.Errorf("default DisplayName = %q, want hostPort", e2.DisplayName)
	}

	// Get/List。
	if got, ok := r.Get(e1.ID); !ok || got.ID != e1.ID {
		t.Fatalf("Get miss after Add")
	}
	if len(r.List()) != 2 {
		t.Fatalf("List = %d entries, want 2", len(r.List()))
	}

	// Rename。
	if err := r.Rename(e1.ID, "客厅主机"); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if got, _ := r.Get(e1.ID); got.DisplayName != "客厅主机" {
		t.Errorf("Rename not applied: %+v", got)
	}
	if err := r.Rename("nope", "x"); err == nil {
		t.Error("Rename unknown id must fail")
	}

	// UpdateHostPort 重置配对态。
	if err := r.SetHealth(e1.ID, HealthReachable, time.Now()); err != nil {
		t.Fatalf("SetHealth: %v", err)
	}
	if err := r.UpdateHostPort(e1.ID, "newhost.example:1234"); err != nil {
		t.Fatalf("UpdateHostPort: %v", err)
	}
	got, _ := r.Get(e1.ID)
	if got.HostPort != "newhost.example:1234" || got.Health != HealthProbing || !got.LastSeen.IsZero() {
		t.Errorf("UpdateHostPort must reset pairing state: %+v", got)
	}

	// Remove。
	if err := r.Remove(e2.ID); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, ok := r.Get(e2.ID); ok {
		t.Error("removed entry still present")
	}
	if err := r.Remove(e2.ID); err == nil {
		t.Error("double Remove must fail")
	}

	// 持久化往返：重新装载后状态一致。
	r2, err := LoadHostRegistry(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	entries := r2.List()
	if len(entries) != 1 || entries[0].ID != e1.ID || entries[0].DisplayName != "客厅主机" {
		t.Fatalf("reload mismatch: %+v", entries)
	}
	if entries[0].HostPort != "newhost.example:1234" {
		t.Errorf("reload HostPort mismatch: %+v", entries[0])
	}
}

// TestHostRegistryAtomicWriteNoLeftovers：tmp+rename 后目录里只有登记簿文件，
// 无 tmp 残留；文件权限 0600。
func TestHostRegistryAtomicWriteNoLeftovers(t *testing.T) {
	r, path := newTestRegistry(t)
	for i := 0; i < 5; i++ {
		if _, err := r.Add("", "host.example:1000"); err != nil {
			t.Fatalf("Add %d: %v", i, err)
		}
	}
	dir := filepath.Dir(path)
	items, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(items) != 1 || items[0].Name() != filepath.Base(path) {
		names := []string{}
		for _, it := range items {
			names = append(names, it.Name())
		}
		t.Fatalf("registry dir has leftovers: %v", names)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("registry file mode = %v, want 0600", info.Mode().Perm())
	}
}

func TestValidateHostPort(t *testing.T) {
	ok := map[string]string{
		"localhost:8080":     "localhost:8080",
		"Example.COM:1":      "example.com:1",
		"host-1.a-b.c:65535": "host-1.a-b.c:65535",
		"[::1]:9000":         "[::1]:9000",
		"192.168.1.10:30001": "192.168.1.10:30001",
		"  localhost:8080  ": "localhost:8080",
	}
	for in, want := range ok {
		got, err := ValidateHostPort(in)
		if err != nil || got != want {
			t.Errorf("ValidateHostPort(%q) = (%q, %v), want (%q, nil)", in, got, err, want)
		}
	}
	bad := []string{
		"", "localhost", ":8080", "localhost:",
		"localhost:0", "localhost:65536", "localhost:99999999999", "localhost:80a", "localhost:-1",
		"user@host:8080", "host/with/path:8080", "host:8080?q=1",
		"::1:9000", "[fe80::1%25eth0]:8080", // 未括号 IPv6 / zone
		".lead:8080", "host..dot:8080", "-start:8080", "end-:8080",
	}
	for _, in := range bad {
		if _, err := ValidateHostPort(in); err == nil {
			t.Errorf("ValidateHostPort(%q) accepted, want rejection", in)
		}
	}
}

// TestProbeHostClassification：探活四态分类（200→reachable、撤销→revoked、
// 契约错误体（未配对 401）→reachable（宿主存活）、网络失败→unreachable）。
func TestProbeHostClassification(t *testing.T) {
	f := newFakeHost(t)
	creds := newMemCredentialStore()
	ctx := context.Background()

	// 未配对条目：401 auth.unpaired 契约体 → 宿主存活。
	unpaired := HostEntry{ID: "h-unpaired", HostPort: f.hostPort()}
	res, seen := ProbeHost(ctx, unpaired, creds)
	if res.State != HealthReachable {
		t.Fatalf("unpaired probe state = %q, want reachable", res.State)
	}
	if seen.IsZero() {
		t.Error("probe lastSeen not stamped")
	}

	// 已配对且凭据有效：200 → reachable + summary。
	deviceID, secret := deviceWireID(7), deviceWireSecret(11)
	creds.Put(credentialEntryName(deviceID), secret) //nolint:errcheck // 测试恒成功
	paired := HostEntry{ID: "h-paired", HostPort: f.hostPort(), DeviceID: deviceID}
	res, _ = ProbeHost(ctx, paired, creds)
	if res.State != HealthReachable || res.Summary == nil || res.Summary.APIVersion != contract.APIVersionV1 {
		t.Fatalf("paired probe = %+v, want reachable+summary", res)
	}

	// 已撤销：401 auth.revoked → revoked（fail-closed 投影）。
	f.revokeDevice(deviceID)
	res, _ = ProbeHost(ctx, paired, creds)
	if res.State != HealthRevoked {
		t.Fatalf("revoked probe state = %q, want revoked", res.State)
	}

	// 网络失败 → unreachable。
	dead := HostEntry{ID: "h-dead", HostPort: "127.0.0.1:1"}
	res, _ = ProbeHost(ctx, dead, creds)
	if res.State != HealthUnreachable {
		t.Fatalf("dead probe state = %q, want unreachable", res.State)
	}

	// M-3 回归：代理 502 + 非 JSON 体（自产 fallback net.unreachable，带 StatusCode）
	// → 不得判为宿主存活。
	f.setOverride(http.StatusBadGateway, "<html>Bad Gateway</html>")
	res, _ = ProbeHost(ctx, unpaired, creds)
	if res.State != HealthUnreachable {
		t.Fatalf("proxy-502 probe state = %q, want unreachable (M-3)", res.State)
	}
	f.clearOverride()

	// Registry.Probe 把投影写回登记簿。
	r, _ := newTestRegistry(t)
	e, err := r.Add("", f.hostPort())
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	res, err = r.Probe(ctx, e.ID, creds)
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if res.State != HealthReachable {
		t.Fatalf("registry probe state = %q", res.State)
	}
	got, _ := r.Get(e.ID)
	if got.Health != HealthReachable || got.LastSeen.IsZero() {
		t.Errorf("probe result not persisted: %+v", got)
	}
}

// stubSecretStore 让 internal/secrets 在测试中不触碰 OS keychain（文件即
// “密文”占位——断言对象是登记簿不得含 secret，而非该 stub 的保密性）。
type stubSecretStore struct{}

func (stubSecretStore) Load(path string) (map[string]string, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	var m map[string]string
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	return m, nil
}

func (stubSecretStore) Save(path string, values map[string]string) error {
	raw, err := json.Marshal(values)
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o600)
}

func (stubSecretStore) Kind() string                     { return "stub" }
func (stubSecretStore) LegacyImportPath(p string) string { return p }

// TestSecretsCredentialStoreRoundTrip：经 internal/secrets 服务（注入 stub
// store）的 Put/Get/Delete 往返；条目名格式 codebox-remoteclient/<DeviceID>。
func TestSecretsCredentialStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	svc := secrets.NewSecretsServiceWithStore(dir, stubSecretStore{})
	if err := svc.Load(); err != nil {
		t.Fatalf("load secrets service: %v", err)
	}
	store, err := NewSecretsCredentialStoreWithService(svc)
	if err != nil {
		t.Fatalf("NewSecretsCredentialStoreWithService: %v", err)
	}
	name := credentialEntryName(deviceWireID(7))
	secret := deviceWireSecret(11)
	if err := store.Put(name, secret); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, err := store.Get(name)
	if err != nil || got != secret {
		t.Fatalf("Get = (%q, %v), want stored secret", got, err)
	}
	if err := store.Delete(name); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	got, err = store.Get(name)
	if err != nil || got != "" {
		t.Fatalf("Get after delete = (%q, %v), want empty", got, err)
	}
	if _, err := NewSecretsCredentialStoreWithService(nil); err == nil {
		t.Error("nil service must be rejected")
	}
}

// TestPairingSecretNeverOnDisk：完整配对后，登记簿 JSON 与整个 config 目录
// （除 secrets 存储文件外）都不得出现 secret 明文；登记簿也不含 secret 字段。
func TestPairingSecretNeverOnDisk(t *testing.T) {
	f := newFakeHost(t)
	dir := t.TempDir()
	registryPath := filepath.Join(dir, "remote-hosts.json")
	registry, err := LoadHostRegistry(registryPath)
	if err != nil {
		t.Fatalf("LoadHostRegistry: %v", err)
	}
	svc := secrets.NewSecretsServiceWithStore(dir, stubSecretStore{})
	if err := svc.Load(); err != nil {
		t.Fatalf("load secrets service: %v", err)
	}
	creds, err := NewSecretsCredentialStoreWithService(svc)
	if err != nil {
		t.Fatalf("credential store: %v", err)
	}
	pairing, err := NewPairingService(registry, creds)
	if err != nil {
		t.Fatalf("NewPairingService: %v", err)
	}
	res, cerr := pairing.CompletePairing(context.Background(), f.hostPort(), f.pairingCode, "测试设备")
	if cerr != nil {
		t.Fatalf("CompletePairing: %v", cerr)
	}

	// 登记簿文件：无 secret 明文、无 secret 命名字段。
	raw, err := os.ReadFile(registryPath)
	if err != nil {
		t.Fatalf("read registry: %v", err)
	}
	if strings.Contains(string(raw), res.DeviceID) == false {
		t.Errorf("registry should carry deviceId %q", res.DeviceID)
	}
	fdev := f.devSnapshot()
	if strings.Contains(string(raw), fdev.secret) {
		t.Errorf("registry JSON contains device secret plaintext")
	}
	if strings.Contains(strings.ToLower(string(raw)), "secret") {
		t.Errorf("registry JSON mentions a secret-named field:\n%s", raw)
	}

	// secret 确已入凭据库（条目名正确）。
	stored, err := creds.Get(credentialEntryName(res.DeviceID))
	if err != nil || stored != fdev.secret {
		t.Fatalf("credential store entry = (%q, %v), want issued secret", stored, err)
	}

	// 目录内其它文件（除 secrets.enc）不含 secret。
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() || e.Name() == "secrets.enc" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		if strings.Contains(string(data), fdev.secret) {
			t.Errorf("file %s contains device secret", e.Name())
		}
	}

	// ForgetHost：条目与凭据一并清理。
	if err := pairing.ForgetHost(res.EntryID); err != nil {
		t.Fatalf("ForgetHost: %v", err)
	}
	if len(registry.List()) != 0 {
		t.Error("registry not empty after ForgetHost")
	}
	if left, _ := creds.Get(credentialEntryName(res.DeviceID)); left != "" {
		t.Error("credential not deleted by ForgetHost")
	}
}

// devSnapshot 取假宿主最近一次签发的设备凭据。
func (f *fakeHost) devSnapshot() fakeDevice {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.dev == nil {
		f.t.Fatal("fakeHost has not issued a device yet")
	}
	return *f.dev
}
