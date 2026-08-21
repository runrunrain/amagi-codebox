// pairing_test.go — 配对流测试：全流程（探活→complete→Set-Cookie 解析→验证
// →Keychain+登记簿）与四失败族（码错/过期/已撤销/网络）映射 + 契约异常注入
// （缺 Cookie/脏 Cookie/设备不一致）+ 零残留断言。
package remoteclient

import (
	"context"
	"strings"
	"testing"

	"amagi-codebox/internal/remote/contract"
)

// newPairingFixture 组装配对测试三件套：假宿主、内存凭据库、临时目录登记簿。
func newPairingFixture(t *testing.T) (*fakeHost, *PairingService, *memCredentialStore, *HostRegistry) {
	t.Helper()
	f := newFakeHost(t)
	creds := newMemCredentialStore()
	registry, _ := newTestRegistry(t)
	pairing, err := NewPairingService(registry, creds)
	if err != nil {
		t.Fatalf("NewPairingService: %v", err)
	}
	return f, pairing, creds, registry
}

func TestPairingCompleteFullFlow(t *testing.T) {
	f, pairing, creds, registry := newPairingFixture(t)

	res, cerr := pairing.CompletePairing(context.Background(), f.hostPort(), f.pairingCode, "MacBook 桌面端")
	if cerr != nil {
		t.Fatalf("CompletePairing: %v", cerr)
	}

	// 结果字段。
	dev := f.devSnapshot()
	if res.DeviceID != dev.id {
		t.Errorf("DeviceID = %q, want %q", res.DeviceID, dev.id)
	}
	if res.HostPort != f.hostPort() {
		t.Errorf("HostPort = %q", res.HostPort)
	}
	if res.Summary.APIVersion != contract.APIVersionV1 {
		t.Errorf("verification summary missing: %+v", res.Summary)
	}

	// Keychain：条目名 + secret 原文。
	stored := creds.snapshot()
	wantName := credentialEntryName(dev.id)
	if got, ok := stored[wantName]; !ok || got != dev.secret {
		t.Fatalf("credential entry %q = (%q, %v)", wantName, got, ok)
	}
	if len(stored) != 1 {
		t.Errorf("credential store has extra entries: %v", stored)
	}

	// 登记簿：DeviceID/Health/LastSeen 回填。
	entry, ok := registry.Get(res.EntryID)
	if !ok {
		t.Fatalf("registry entry %q missing", res.EntryID)
	}
	if entry.DeviceID != dev.id || entry.Health != HealthReachable || entry.LastSeen.IsZero() {
		t.Errorf("registry entry not backfilled: %+v", entry)
	}
	if entry.DisplayName != f.hostPort() {
		t.Errorf("default DisplayName = %q, want hostPort", entry.DisplayName)
	}

	// 凭据立即可用：新 Transport 装载后可完成已鉴权调用。
	tr, err := NewTransport(f.baseURL())
	if err != nil {
		t.Fatalf("NewTransport: %v", err)
	}
	if err := tr.SetCredential(dev.id, dev.secret); err != nil {
		t.Fatalf("SetCredential: %v", err)
	}
	if _, cerr := tr.HostSummary(context.Background()); cerr != nil {
		t.Fatalf("post-pairing authenticated call: %v", cerr)
	}

	// 重复配对（同 hostPort）：upsert 不新增条目、覆盖凭据。
	f.mu.Lock()
	f.pairingCode = "PAIR-CODE-2"
	f.mu.Unlock()
	res2, cerr := pairing.CompletePairing(context.Background(), f.hostPort(), "PAIR-CODE-2", "MacBook 桌面端")
	if cerr != nil {
		t.Fatalf("re-pair: %v", cerr)
	}
	if res2.EntryID != res.EntryID {
		t.Errorf("re-pair must upsert same entry: %q vs %q", res2.EntryID, res.EntryID)
	}
	if len(registry.List()) != 1 {
		t.Errorf("registry entries = %d, want 1", len(registry.List()))
	}
	if got, _ := creds.Get(credentialEntryName(res2.DeviceID)); got == "" {
		t.Error("re-pair credential missing")
	}
}

// TestPairingFailureFamilies：四失败族映射 + 零残留（Keychain/登记簿均空）。
func TestPairingFailureFamilies(t *testing.T) {
	t.Run("wrong code → auth.unpaired", func(t *testing.T) {
		f, pairing, creds, registry := newPairingFixture(t)
		_, cerr := pairing.CompletePairing(context.Background(), f.hostPort(), "WRONG-CODE", "dev")
		if cerr == nil || cerr.Code() != contract.ErrorCodeAuthUnpaired {
			t.Fatalf("code = %v, want auth.unpaired", cerr)
		}
		assertZeroResidue(t, creds, registry)
	})

	t.Run("window expired → auth.window_expired", func(t *testing.T) {
		f, pairing, creds, registry := newPairingFixture(t)
		f.mu.Lock()
		f.windowActive = false
		f.mu.Unlock()
		_, cerr := pairing.CompletePairing(context.Background(), f.hostPort(), f.pairingCode, "dev")
		if cerr == nil || cerr.Code() != contract.ErrorCodeAuthWindowExpired {
			t.Fatalf("code = %v, want auth.window_expired", cerr)
		}
		assertZeroResidue(t, creds, registry)
	})

	t.Run("revoked at verification → auth.revoked", func(t *testing.T) {
		f, pairing, creds, registry := newPairingFixture(t)
		// 配对即撤销：complete 成功签发 Cookie，但验证请求时设备已进撤销名单。
		f.mu.Lock()
		f.revOnIssue = true
		f.mu.Unlock()
		_, cerr := pairing.CompletePairing(context.Background(), f.hostPort(), f.pairingCode, "dev")
		if cerr == nil || cerr.Code() != contract.ErrorCodeAuthRevoked {
			t.Fatalf("code = %v, want auth.revoked", cerr)
		}
		if !cerr.IsAuthRevoked() {
			t.Error("IsAuthRevoked false")
		}
		assertZeroResidue(t, creds, registry)
	})

	t.Run("network down → net.unreachable", func(t *testing.T) {
		_, pairing, creds, registry := newPairingFixture(t)
		_, cerr := pairing.CompletePairing(context.Background(), "127.0.0.1:1", "ANY", "dev")
		if cerr == nil || cerr.Code() != contract.ErrorCodeNetUnreachable {
			t.Fatalf("code = %v, want net.unreachable", cerr)
		}
		assertZeroResidue(t, creds, registry)
	})
}

// TestPairingContractAnomalies：宿主响应异常（缺 Set-Cookie / 脏 Cookie /
// 响应体与 Cookie 设备不一致）→ service.down，且零残留。
func TestPairingContractAnomalies(t *testing.T) {
	cases := []struct {
		name  string
		mutat func(f *fakeHost)
	}{
		{"missing set-cookie", func(f *fakeHost) { f.mu.Lock(); f.noCookie = true; f.mu.Unlock() }},
		{"malformed cookie", func(f *fakeHost) { f.mu.Lock(); f.cookieOverride = "v1.garbage.garbage"; f.mu.Unlock() }},
		{"device mismatch", func(f *fakeHost) { f.mu.Lock(); f.bodyDeviceID = deviceWireID(99); f.mu.Unlock() }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, pairing, creds, registry := newPairingFixture(t)
			tc.mutat(f)
			_, cerr := pairing.CompletePairing(context.Background(), f.hostPort(), f.pairingCode, "dev")
			if cerr == nil || cerr.Code() != contract.ErrorCodeServiceDown {
				t.Fatalf("code = %v, want service.down", cerr)
			}
			assertZeroResidue(t, creds, registry)
		})
	}
}

// TestPairingInputValidation：hostPort/配对码/设备名的本地白名单校验。
func TestPairingInputValidation(t *testing.T) {
	_, pairing, _, _ := newPairingFixture(t)
	ctx := context.Background()
	cases := []struct {
		name       string
		hostPort   string
		code       string
		deviceName string
	}{
		{"bad hostPort", "localhost", "CODE", "dev"},
		{"bad port", "localhost:0", "CODE", "dev"},
		{"empty code", "localhost:8080", "  ", "dev"},
		{"empty deviceName", "localhost:8080", "CODE", " "},
	}
	for _, tc := range cases {
		_, cerr := pairing.CompletePairing(ctx, tc.hostPort, tc.code, tc.deviceName)
		if cerr == nil || cerr.Code() != contract.ErrorCodeBadRequest {
			t.Errorf("%s: code = %v, want bad_request", tc.name, cerr)
		}
	}
	_, err := NewPairingService(nil, nil)
	if err == nil {
		t.Error("NewPairingService(nil,nil) must fail")
	}
}

func assertZeroResidue(t *testing.T, creds *memCredentialStore, registry *HostRegistry) {
	t.Helper()
	if items := creds.snapshot(); len(items) != 0 {
		t.Errorf("credential residue: %v", items)
	}
	if entries := registry.List(); len(entries) != 0 {
		t.Errorf("registry residue: %+v", entries)
	}
}

// TestPairingCodeNeverLeakedInErrors：错误链路不得携带配对码文本。
func TestPairingCodeNeverLeakedInErrors(t *testing.T) {
	f, pairing, _, _ := newPairingFixture(t)
	const secretCode = "TOPSECRET-CODE-42"
	_, cerr := pairing.CompletePairing(context.Background(), f.hostPort(), secretCode, "dev")
	if cerr == nil {
		t.Fatal("expected failure")
	}
	if msg := cerr.Error(); strings.Contains(msg, secretCode) {
		t.Errorf("ClientError.Error() leaks pairing code: %q", msg)
	}
}
