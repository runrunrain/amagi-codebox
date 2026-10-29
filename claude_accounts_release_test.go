package main

// Phase 1 第三轮：失败留 journal→Service 释放→普通 refresh 尚未发布窗口。
import (
	"amagi-codebox/internal/config"
	"context"
	"strings"
	"testing"
	"time"
)

func TestFailedMutationPublishesBlockedBeforeFacadeRefresh(t *testing.T) {
	app, store, native, _ := newFacadeTestApp(t)
	native.failPiPatch = true
	barrier := &snapshotBarrierStore{PoolStore: store, skip: 1, loaded: make(chan struct{}), resume: make(chan struct{})}
	app.claudeAccountsPoolStore = barrier
	if err := app.Config.SaveProvider("release-provider", config.Provider{Type: "anthropic", BaseURL: "https://example.invalid", AuthKey: "ANTHROPIC_API_KEY", DefaultModel: "fixture"}); err != nil {
		t.Fatal(err)
	}
	if err := app.Secrets.SetAPIKey("release-provider", "synthetic-key"); err != nil {
		t.Fatal(err)
	}
	opID, err := app.SwitchClaudeAccount("acc-b", 4, "failed-mutation-release")
	if err != nil {
		t.Fatal(err)
	}
	awaitBarrier(t, barrier.loaded) // 1=release 权威发布已完成；2=runner refresh 暂停。
	// 测试中先记录结果再放开屏障，保证断言失败时也不遗留后台任务。
	_, launchErr := app.LaunchSession("release-provider", "", "terminal", t.TempDir(), false, "")
	p, _ := store.Load(context.Background())
	close(barrier.resume)
	op := waitClaudeOpDone(t, app, opID, 5*time.Second)
	if op.State != "error" {
		t.Fatalf("部分写入注入必须失败: %+v", op)
	}
	if p.Transaction == nil || p.ActiveGeneration != 4 {
		t.Fatal("测试前提失败：必须保留 journal 且 generation 不变")
	}
	if launchErr == nil || !strings.Contains(launchErr.Error(), "NEEDS_RECONCILE") {
		t.Fatalf("Service release 后普通 refresh 前真实 launch 必须 blocked: %v", launchErr)
	}
}
