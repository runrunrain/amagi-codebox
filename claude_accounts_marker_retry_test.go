package main

// F10 第四轮：真实 marker 创建后 Sync 出错，不得被下一次 Stat 绕过。
import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestMarkerPersistenceFailureRetriesUntilConfirmed(t *testing.T) {
	for _, stage := range []string{"file-sync", "directory-sync"} {
		t.Run(stage, func(t *testing.T) {
			app, store, _, _ := newFacadeTestApp(t)
			markerPath := app.claudeAccountsPoolMarkerPath
			fail := true
			fileSyncs, directorySyncs := 0, 0
			syncMarker := func(f *os.File) error {
				// 保留真实 Sync；额外注入其失败返回，绝不伪造持久化成功。
				if err := f.Sync(); err != nil {
					return err
				}
				isDirectory := f.Name() == filepath.Dir(markerPath)
				if isDirectory {
					directorySyncs++
				} else {
					fileSyncs++
				}
				if fail && ((stage == "directory-sync") == isDirectory) {
					return syscall.EIO
				}
				return nil
			}
			app.claudeAccountsMarkerSync = syncMarker
			assertBlocked := func(a *App) {
				t.Helper()
				dto, err := a.GetClaudeAccounts() // 真实 refresh→发布→安全 DTO。
				if err != nil {
					t.Fatal(err)
				}
				if dto.State != "blocked" || a.claudeCredentialsPolicy() != claudeCredentialsBlocked {
					t.Fatalf("同步失败必须持续 blocked，不能仅凭 Stat 重新 managed: %+v", dto)
				}
				if info, err := os.Stat(markerPath); err != nil || !info.Mode().IsRegular() {
					t.Fatalf("反例必须发生在 regular marker 创建后: %v", err)
				}
			}
			for i := 0; i < 3; i++ {
				assertBlocked(app)
			}
			// 无内存失败标志也必须重做确认：重建 App，复用同一真实文件。
			rebuilt, _, _, _ := newFacadeTestApp(t)
			rebuilt.claudeAccountsPoolStore = store
			rebuilt.claudeAccountsPoolMarkerPath = markerPath
			rebuilt.claudeAccountsMarkerSync = syncMarker
			assertBlocked(rebuilt)
			if fileSyncs != 4 {
				t.Fatalf("每轮必须尝试 file Sync，实际=%d", fileSyncs)
			}
			if stage == "directory-sync" && directorySyncs != 4 {
				t.Fatalf("目录 Sync 未逐次重试: %d", directorySyncs)
			}
			fail = false
			for _, a := range []*App{app, rebuilt} {
				beforeFile, beforeDirectory := fileSyncs, directorySyncs
				dto, err := a.GetClaudeAccounts()
				if err != nil || dto.State != "enabled" || a.claudeCredentialsPolicy() != claudeCredentialsManaged {
					t.Fatalf("确认恢复后应放行 managed: dto=%+v err=%v", dto, err)
				}
				if fileSyncs != beforeFile+1 || directorySyncs != beforeDirectory+1 {
					t.Fatal("已有 marker 放行前必须同时完成文件与目录 Sync")
				}
			}
		})
	}
}
