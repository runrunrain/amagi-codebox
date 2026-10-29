package main

// Phase 1 第三轮：真实 facade→Service→生产 gate 的确定性屏障。
// 凭据/进程均为 fixture；不访问真实 Keychain，不启动 CLI。
import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"amagi-codebox/internal/claudeaccounts"
	"amagi-codebox/internal/config"
)

type snapshotBarrierStore struct {
	claudeaccounts.PoolStore
	skip   int // 发布读序列内，仅跳过指定次数以定位 operation 返回后的 refresh。
	once   sync.Once
	loaded chan struct{}
	resume chan struct{}
}

func (s *snapshotBarrierStore) Load(ctx context.Context) (*claudeaccounts.AccountPool, error) {
	p, err := s.PoolStore.Load(ctx)
	if s.skip > 0 {
		s.skip--
		return p, err
	}
	s.once.Do(func() { close(s.loaded); <-s.resume })
	return p, err
}
func awaitBarrier(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("屏障超时")
	}
}

// R01：旧 Load 已形成快照→真实 BeginEnrollment 写 journal→旧刷新恢复。
// 发布锁跨越整个 Load→publish，因此 mutation 的权威 blocked 发布只能
// 排在旧快照发布之后，且整个等待期间 mutation admission 不释放。
func TestPublicationOldLoadCannotOverwriteNewBlocked(t *testing.T) {
	app, store, _, _ := newFacadeTestApp(t)
	barrier := &snapshotBarrierStore{PoolStore: store, loaded: make(chan struct{}), resume: make(chan struct{})}
	app.claudeAccountsPoolStore = barrier
	refreshed := make(chan struct{})
	go func() { app.refreshClaudeAccountsCache(context.Background()); close(refreshed) }()
	awaitBarrier(t, barrier.loaded)
	enrolled := make(chan struct{})
	var enrollErr error
	go func() { _, enrollErr = app.BeginClaudeAccountEnrollment(2); close(enrolled) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		p, _ := store.Load(context.Background())
		if p.Transaction != nil {
			break
		}
		if time.Now().After(deadline) {
			close(barrier.resume)
			t.Fatal("真实 enrollment 未写 journal")
		}
		runtime.Gosched()
	}
	if claim, err := app.beginClaudeCredentialsClaim("during-publication"); err == nil {
		claim.release(app)
		close(barrier.resume)
		t.Fatal("发布等待期间 mutation admission 不得释放")
	}
	close(barrier.resume)
	awaitBarrier(t, refreshed)
	awaitBarrier(t, enrolled)
	if enrollErr != nil {
		t.Fatal(enrollErr)
	}
	if app.claudeCredentialsPolicy() != claudeCredentialsBlocked {
		t.Fatal("旧快照倒灌 blocked")
	}
	dto, err := app.GetClaudeAccounts()
	if err != nil || dto.State != "enrolling" {
		t.Fatalf("真实 Get 刷新后状态=%+v err=%v", dto, err)
	}
}

// F01 absent：真实 LaunchSession 在 claim 后停住，首次 capture 必须 busy，
// 原生/池零写；失败退出后 reservation 释放，首次 capture 可以成功。
func TestAbsentDesktopLaunchReservationBlocksFirstCapture(t *testing.T) {
	app, store, _, _ := newFacadeTestApp(t)
	store.mu.Lock()
	store.pool = nil
	store.mu.Unlock()
	setClaudeAccountsCache(app, nil, nil, true)
	if err := app.Config.SaveProvider("reservation-provider", config.Provider{Type: "anthropic", BaseURL: "https://example.invalid", AuthKey: "ANTHROPIC_API_KEY", DefaultModel: "fixture"}); err != nil {
		t.Fatal(err)
	}
	if err := app.Secrets.SetAPIKey("reservation-provider", "synthetic-key"); err != nil {
		t.Fatal(err)
	}
	entered, resume, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	stop := errors.New("fixture: stop before process effects")
	app.claudeCredentialsLaunchCheckpoint = func() error { close(entered); <-resume; return stop }
	var launchErr error
	workdir := t.TempDir()
	go func() {
		_, launchErr = app.LaunchSession("reservation-provider", "", "terminal", workdir, false, "")
		close(finished)
	}()
	awaitBarrier(t, entered)
	opID, err := app.CaptureClaudeAccount("", "first", 0)
	if err != nil {
		t.Fatal(err)
	}
	op := waitClaudeOpDone(t, app, opID, 5*time.Second)
	if op.Code != string(claudeaccounts.ReasonSessionsActive) || op.State != "error" {
		t.Fatalf("reservation 未阻止 capture: %+v", op)
	}
	p, _ := store.Load(context.Background())
	if p != nil {
		t.Fatal("busy capture 不得创建池")
	}
	close(resume)
	awaitBarrier(t, finished)
	if !errors.Is(launchErr, stop) {
		t.Fatalf("未经过真实 launch checkpoint: %v", launchErr)
	}
	opID, err = app.CaptureClaudeAccount("", "first", 0)
	if err != nil {
		t.Fatal(err)
	}
	if op := waitClaudeOpDone(t, app, opID, 5*time.Second); op.State != "done" {
		t.Fatalf("退出后 reservation 泄漏: %+v", op)
	}
}

// F10：ENOENT 是 absent；ENOTDIR/不可写标记必须 blocked，绝不降级 absent。
func TestMarkerErrorsFailClosedAcrossAppRebuild(t *testing.T) {
	for _, mode := range []string{"stat-error", "write-error"} {
		t.Run(mode, func(t *testing.T) {
			app, store, _, _ := newFacadeTestApp(t)
			parent := t.TempDir()
			if mode == "stat-error" {
				parent = filepath.Join(parent, "not-a-directory")
				if err := os.WriteFile(parent, []byte("fixture"), 0600); err != nil {
					t.Fatal(err)
				}
				store.mu.Lock()
				store.pool = nil
				store.mu.Unlock()
			} else {
				if os.Geteuid() == 0 {
					t.Skip("权限反例需非 root 用户")
				}
				if err := os.Chmod(parent, 0500); err != nil {
					t.Fatal(err)
				}
				defer os.Chmod(parent, 0700)
			}
			app.claudeAccountsPoolMarkerPath = filepath.Join(parent, "marker")
			if mode == "write-error" {
				if exists, err := app.claudeAccountsMarkerStatus(); exists || err != nil {
					t.Fatalf("写失败反例前提必须是 Stat ENOENT: %v/%v", exists, err)
				}
				if err := app.writeClaudeAccountsMarker(); err == nil {
					t.Fatal("只读目录必须导致标记写失败")
				}
			}
			app.refreshClaudeAccountsCache(context.Background())
			if app.claudeCredentialsPolicy() != claudeCredentialsBlocked {
				t.Fatal("标记错误必须 blocked")
			}
			rebuilt, _, _, _ := newFacadeTestApp(t)
			rebuilt.claudeAccountsPoolStore = store
			rebuilt.claudeAccountsPoolMarkerPath = app.claudeAccountsPoolMarkerPath
			rebuilt.refreshClaudeAccountsCache(context.Background())
			if rebuilt.claudeCredentialsPolicy() != claudeCredentialsBlocked {
				t.Fatal("重建 App 不能靠本进程缓存保护")
			}
		})
	}
}

func TestOperationInflightLimitBusyAndIdempotent(t *testing.T) {
	app, _, _, _ := newFacadeTestApp(t)
	entered, resume := make(chan struct{}, claudeAccountOperationInflightLimit), make(chan struct{})
	work := func(context.Context, *claudeAccountOperation) error { entered <- struct{}{}; <-resume; return nil }
	ids := make([]string, claudeAccountOperationInflightLimit)
	for i := range ids {
		var err error
		ids[i], err = app.runClaudeAccountOperation("fixture", string(rune('a'+i)), "d", 4, work)
		if err != nil {
			t.Fatal(err)
		}
	}
	defer func() {
		close(resume)
		for _, id := range ids {
			app.claudeAccountsOpsMu.Lock()
			op := app.claudeAccountsOps[id]
			app.claudeAccountsOpsMu.Unlock()
			awaitBarrier(t, op.done)
		}
	}()
	for range ids {
		awaitBarrier(t, entered)
	}
	if _, err := app.runClaudeAccountOperation("fixture", "overflow", "d", 4, work); claudeaccounts.ReasonOf(err) != claudeaccounts.ReasonPoolLocked {
		t.Fatalf("超限必须稳定 busy: %v", err)
	}
	id, err := app.runClaudeAccountOperation("fixture", "a", "d", 4, work)
	if err != nil || id != ids[0] {
		t.Fatalf("满载仍须幂等复用: %s/%v", id, err)
	}
	for _, id := range ids {
		if _, err := app.GetClaudeAccountOperation(id); err != nil {
			t.Fatal(err)
		}
	}
}
