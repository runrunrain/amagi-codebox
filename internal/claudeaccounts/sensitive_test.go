package claudeaccounts

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// 敏感边界（design §1.3-5/§3.3）：token、完整身份快照、凭据 hash、HTTP
// 原文不进日志、错误消息、DTO。

// sensitiveTokens 是全流程使用的合成敏感值集合。
var sensitiveTokens = []string{
	"cc-a-1", "ccr-cc-a-1", "pi-a-1", "pir-pi-a-1",
	"cc-b-1", "ccr-cc-b-1", "pi-b-1", "pir-pi-b-1",
	"pi-a-2", "pi-a-3", "pi-a-4", "acc-new", "ref-new", "pir-old",
	"sk-synthetic", "g-synthetic",
	"a@example.com", "b@example.com", // 完整邮箱也不进日志（遮罩后可以）
	syntheticUUIDA, syntheticUUIDB, // 服务端 UUID 不进日志（本机 ID 可以）
}

// TestLogsAndEventsSensitiveScan：完整 A/B 录入+切换+harvest 流程后，
// 日志与事件不含任何敏感值。
func TestLogsAndEventsSensitiveScan(t *testing.T) {
	env := newTestEnv(t)
	idA, idB := env.enrollAccountB()
	pool, _ := env.svc.LoadPool(context.Background())
	if _, err := env.svc.Swap(context.Background(), SwapRequest{TargetID: idA, Reason: SwapReasonManual, ExpectedGeneration: pool.ActiveGeneration}); err != nil {
		t.Fatalf("swap: %v", err)
	}
	pool, _ = env.svc.LoadPool(context.Background())
	if _, err := env.svc.Swap(context.Background(), SwapRequest{TargetID: idB, Reason: SwapReasonManual, ExpectedGeneration: pool.ActiveGeneration}); err != nil {
		t.Fatalf("swap back: %v", err)
	}
	pool, _ = env.svc.LoadPool(context.Background())
	if _, err := env.svc.HarvestActive(context.Background(), pool.Revision); err != nil {
		t.Fatalf("harvest: %v", err)
	}

	for _, log := range env.allLogs() {
		for _, secret := range sensitiveTokens {
			if strings.Contains(log, secret) {
				t.Fatalf("日志泄漏敏感值 %q：%s", secret, log)
			}
		}
	}
	for _, ev := range env.events {
		data, _ := json.Marshal(ev)
		for _, secret := range sensitiveTokens {
			if strings.Contains(string(data), secret) {
				t.Fatalf("事件泄漏敏感值 %q：%s", secret, data)
			}
		}
	}
}

// TestDTOHasNoSensitiveFields：DTO 序列化不含 token/服务端身份/邮箱原文。
func TestDTOHasNoSensitiveFields(t *testing.T) {
	env := newTestEnv(t)
	dtos, err := env.svc.ListAccounts(context.Background())
	if err != nil || len(dtos) != 0 {
		// 空池也应正常。
		if err != nil {
			t.Fatal(err)
		}
	}
	env.enrollAccountB()
	dtos, err = env.svc.ListAccounts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(dtos) != 2 {
		t.Fatalf("期望两个账号 DTO，实际 %d", len(dtos))
	}
	for _, dto := range dtos {
		data, _ := json.Marshal(dto)
		text := string(data)
		for _, secret := range sensitiveTokens {
			if strings.Contains(text, secret) {
				t.Fatalf("DTO 泄漏敏感值 %q：%s", secret, text)
			}
		}
		if dto.EmailMasked != "" && !strings.Contains(dto.EmailMasked, "***") {
			t.Fatalf("DTO 邮箱未遮罩：%s", dto.EmailMasked)
		}
	}
}

// TestErrorMessageNoRawContent：错误路径的错误消息不含 HTTP 原文与凭据。
func TestErrorMessageNoRawContent(t *testing.T) {
	env := newTestEnv(t)
	env.identityFake.mu.Lock()
	delete(env.identityFake.responses, "pi-a-1")
	env.identityFake.mu.Unlock()
	_, err := env.svc.CaptureFirstAccount(context.Background(), "a", 0)
	if err == nil {
		t.Fatal("期望身份失败")
	}
	msg := err.Error()
	for _, secret := range sensitiveTokens {
		if strings.Contains(msg, secret) {
			t.Fatalf("错误消息泄漏 %q：%s", secret, msg)
		}
	}
	assertReason(t, err, ReasonIdentityUnverified)
}

// TestDefaultConstructorsDontTouchRealKeychain：默认构造器只在生产平台
// 返回后端句柄，测试内绝不调用其 Load/Save（design §10.1：不可通过默认
// 构造器碰真实 Keychain）。这里仅验证句柄可创建且 Kind 有值，不做 IO。
func TestDefaultConstructorsDontTouchRealKeychain(t *testing.T) {
	store := NewKeychainPoolStore()
	if store.Kind() == "" {
		t.Fatal("Kind 不能为空")
	}
	kc := NewClaudeKeychainAccess()
	_ = kc // 不调用其任何 IO 方法
	paths, err := DefaultFileCredentialPaths()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(paths.ClaudeJSONPath, ".claude.json") || !strings.Contains(paths.PiAuthPath, ".pi") {
		t.Fatalf("canonical 路径不符: %+v", paths)
	}
}
