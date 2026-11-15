package sharedmcp

// state_test.go — 状态库测试（spec §2.4）：损坏/缺失回零值；
// reconcile-then-settle（注入写入失败 → Selected 不置位）；undistribute
// 失败保持状态；ContentHash 记录/刷新；孤儿 GC。

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// sjsonDeleteBytes 是 sjson.DeleteBytes 的薄包装（测试用）。
func sjsonDeleteBytes(data []byte, path string) ([]byte, error) {
	return sjson.DeleteBytes(data, path)
}

// withWritesDisabled 同时禁用 rename 与 Windows 锁定回退的直写，模拟完整
// 写入路径失败（仅禁 rename 时 Windows 回退会救活写入，注入不成立）。
func withWritesDisabled(t *testing.T, fn func()) {
	t.Helper()
	origRename, origWrite := atomicRename, atomicWriteFile
	atomicRename = func(oldPath, newPath string) error { return errors.New("注入的 rename 失败") }
	atomicWriteFile = func(name string, data []byte, perm os.FileMode) error {
		return errors.New("注入的直写失败")
	}
	defer func() { atomicRename, atomicWriteFile = origRename, origWrite }()
	fn()
}

func TestState_CorruptOrMissingLoadsZero(t *testing.T) {
	svc, _, cfgDir := newRegistryTestService(t)
	// 缺失 → 零值。
	st := svc.loadState()
	if st.SchemaVersion != 1 || st.Entries == nil || len(st.Entries) != 0 {
		t.Fatalf("缺失状态应回零值: %+v", st)
	}
	// 损坏 → 零值。
	if err := os.WriteFile(filepath.Join(cfgDir, sharedMcpStateFile), []byte("{BROKEN"), 0o600); err != nil {
		t.Fatal(err)
	}
	st = svc.loadState()
	if st.Entries == nil || len(st.Entries) != 0 {
		t.Fatalf("损坏状态应回零值: %+v", st)
	}
	// Overview 在状态损坏时仍可用（不因状态库失败破坏启动）。
	if _, err := svc.Overview(); err != nil {
		t.Fatalf("Overview: %v", err)
	}
}

func TestState_SettleOnlyAfterApplySuccess(t *testing.T) {
	svc, home, _ := newRegistryTestService(t)
	writeRegistryFixture(t, home)
	entry := stdioEntry("zhipu-vision")

	// 注入 rename 失败 → Apply 失败 → Selected/Applied 均不置位。
	withWritesDisabled(t, func() {
		if err := svc.Distribute(entry, "claude", false); err == nil {
			t.Fatal("注入失败未生效")
		}
	})
	st := svc.loadState()
	if len(st.Entries["zhipu-vision"].Selected) != 0 || len(st.Entries["zhipu-vision"].Applied) != 0 {
		t.Fatalf("失败后状态被置位: %+v", st.Entries["zhipu-vision"])
	}

	// 成功路径 → Selected+Applied 落位。
	if err := svc.Distribute(entry, "claude", false); err != nil {
		t.Fatalf("Distribute: %v", err)
	}
	st = svc.loadState()
	es := st.Entries["zhipu-vision"]
	if !es.Selected["claude"] || es.Applied["claude"].ContentHash == "" {
		t.Fatalf("成功后状态未落位: %+v", es)
	}
	// ContentHash = 当前映射 canonical hash。
	mapped, _ := (&claudeWriter{userHome: home}).Mapped(entry)
	if es.Applied["claude"].ContentHash != hashCanonical(mapped) {
		t.Fatal("ContentHash 与映射不符")
	}

	// undistribute 失败 → 状态保持。
	withWritesDisabled(t, func() {
		if err := svc.Undistribute("zhipu-vision", "claude", false); err == nil {
			t.Fatal("注入失败未生效")
		}
	})
	st = svc.loadState()
	if !st.Entries["zhipu-vision"].Selected["claude"] || st.Entries["zhipu-vision"].Applied["claude"].ContentHash == "" {
		t.Fatalf("undistribute 失败后状态被清除: %+v", st.Entries["zhipu-vision"])
	}

	// 成功 undistribute → 状态清除。
	if err := svc.Undistribute("zhipu-vision", "claude", false); err != nil {
		t.Fatalf("Undistribute: %v", err)
	}
	st = svc.loadState()
	if _, ok := st.Entries["zhipu-vision"]; ok {
		t.Fatalf("清除后残留: %+v", st.Entries)
	}
}

func TestState_ContentHashRefreshOnResync(t *testing.T) {
	svc, home, _ := newRegistryTestService(t)
	writeRegistryFixture(t, home)
	entry := stdioEntry("zhipu-vision")
	if err := svc.Distribute(entry, "pi", false); err != nil {
		t.Fatal(err)
	}
	h1 := svc.loadState().Entries["zhipu-vision"].Applied["pi"].ContentHash

	// 登记条目更新（env 值变化）后重新分发 → hash 刷新为新内容 hash。
	entry.Env = map[string]string{"API_KEY": "ROTATED"}
	if err := svc.Update("zhipu-vision", entry); err != nil {
		t.Fatal(err)
	}
	if err := svc.Distribute(entry, "pi", false); err != nil {
		t.Fatal(err)
	}
	es := svc.loadState().Entries["zhipu-vision"]
	mapped, _ := (&piWriter{env: testEnv(home), userHome: home}).Mapped(entry)
	if es.Applied["pi"].ContentHash == h1 || es.Applied["pi"].ContentHash != hashCanonical(mapped) {
		t.Fatalf("ContentHash 未刷新: h1=%s h2=%s want=%s", h1, es.Applied["pi"].ContentHash, hashCanonical(mapped))
	}
}

func TestState_OrphanGCCollectsAndKeepsTargetCopy(t *testing.T) {
	svc, home, _ := newRegistryTestService(t)
	writeRegistryFixture(t, home)
	if err := svc.Distribute(stdioEntry("zhipu-vision"), "claude", false); err != nil {
		t.Fatal(err)
	}
	// 外部手删登记条目（直接动登记文件模拟）。
	data, _ := os.ReadFile(svc.registryPath())
	next, derr := sjsonDeleteBytes(data, entryPath("zhipu-vision"))
	if derr != nil {
		t.Fatal(derr)
	}
	if err := os.WriteFile(svc.registryPath(), next, 0o600); err != nil {
		t.Fatal(err)
	}
	ov, err := svc.Overview()
	if err != nil {
		t.Fatal(err)
	}
	// GC 后 state 无孤儿。
	st := svc.loadState()
	if _, ok := st.Entries["zhipu-vision"]; ok {
		t.Fatalf("孤儿未被 GC: %+v", st.Entries)
	}
	for _, e := range ov.Entries {
		if e.Entry.Name == "zhipu-vision" {
			t.Fatal("被删条目仍出现在 Overview")
		}
	}
	// 目标侧已分发副本成为不受管孤儿——如实保留（spec §8 R-9）。
	cj, _ := os.ReadFile(filepath.Join(home, ".claude.json"))
	if !gjson.GetBytes(cj, "mcpServers.zhipu-vision").Exists() {
		t.Fatal("不受管孤儿副本被自动清理（应保留）")
	}
	// 同名重建不继承旧 Applied（GC 保证）。
	if err := svc.Create(stdioEntry("zhipu-vision")); err != nil {
		t.Fatal(err)
	}
	st = svc.loadState()
	if _, ok := st.Entries["zhipu-vision"]; ok {
		t.Fatal("同名重建继承了旧状态")
	}
}
