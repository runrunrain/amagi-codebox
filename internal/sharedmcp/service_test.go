package sharedmcp

// service_test.go — 分发编排全链测试（spec §2.4）：Distribute/Undistribute/
// Overview + conflict/unsupported/foreign-modified 分类断言 + 中文文案含路径
// 与条目名；drift 全链；unsupported 优先级；外改保护（force 二次确认）；
// 原子性（写入原语注入失败 → 目标文件字节不变）。

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func findCell(t *testing.T, ov Overview, name, engine string) EngineCell {
	t.Helper()
	for _, e := range ov.Entries {
		if e.Entry.Name == name {
			c, ok := e.Engines[engine]
			if !ok {
				t.Fatalf("条目 %s 无引擎 %s 格", name, engine)
			}
			return c
		}
	}
	t.Fatalf("Overview 缺条目 %s", name)
	return EngineCell{}
}

func TestService_ConflictRefusedWithReadableMessage(t *testing.T) {
	svc, home, _ := newRegistryTestService(t)
	writeRegistryFixture(t, home)
	// 预置 Claude 手写同名条目。
	cj := filepath.Join(home, ".claude.json")
	if err := os.WriteFile(cj, []byte("{\r\n  \"mcpServers\": {\r\n    \"zhipu-vision\": {\r\n      \"type\": \"stdio\",\r\n      \"command\": \"handwritten\"\r\n    }\r\n  }\r\n}\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(cj)

	err := svc.Distribute(stdioEntry("zhipu-vision"), "claude", false)
	if KindOf(err) != KindConflict {
		t.Fatalf("应 conflict: %v", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, "Claude") || !strings.Contains(msg, "zhipu-vision") || !strings.Contains(msg, "拒绝覆盖") {
		t.Fatalf("conflict 文案缺引擎/条目名: %s", msg)
	}
	after, _ := os.ReadFile(cj)
	if string(before) != string(after) {
		t.Fatal("手写条目被改动")
	}
	// force 也不能越过 conflict（所有权问题，非确认问题）。
	if err := svc.Distribute(stdioEntry("zhipu-vision"), "claude", true); KindOf(err) != KindConflict {
		t.Fatalf("force 不应越过 conflict: %v", err)
	}
	// Overview 该格 conflict。
	ov, _ := svc.Overview()
	if c := findCell(t, ov, "zhipu-vision", "claude"); c.Status != EngineConflict {
		t.Fatalf("状态应 conflict: %+v", c)
	}
}

func TestService_UnsupportedRefusedEvenWithStaleCopy(t *testing.T) {
	svc, home, _ := newRegistryTestService(t)
	writeRegistryFixture(t, home)
	// stdio → codex 成功。
	if err := svc.Distribute(stdioEntry("zhipu-vision"), "codex", false); err != nil {
		t.Fatalf("stdio→codex: %v", err)
	}
	// 登记条目改为 http+headers → 再分发被拒（形态受限事实优先）。
	updated := httpEntry("zhipu-vision")
	if err := svc.Update("zhipu-vision", updated); err != nil {
		t.Fatal(err)
	}
	err := svc.Distribute(updated, "codex", false)
	if KindOf(err) != KindUnsupported {
		t.Fatalf("应 unsupported: %v", err)
	}
	if !strings.Contains(err.Error(), "旧版分发副本") {
		t.Fatalf("reason 应提示旧副本: %s", err)
	}
	// Overview：unsupported 优先于陈旧 distributed + reason 提示。
	ov, _ := svc.Overview()
	c := findCell(t, ov, "zhipu-vision", "codex")
	if c.Status != EngineUnsupported || !strings.Contains(c.Reason, "旧版分发副本") {
		t.Fatalf("unsupported 优先级未生效: %+v", c)
	}
	// Undistribute 不做 Supports 预检 → 可移除旧副本。
	if err := svc.Undistribute("zhipu-vision", "codex", false); err != nil {
		t.Fatalf("关停应永远合法: %v", err)
	}
	tomlBytes, _ := os.ReadFile(filepath.Join(home, ".codex", "config.toml"))
	if strings.Contains(string(tomlBytes), "[mcp_servers.zhipu-vision") {
		t.Fatal("旧副本未移除")
	}
}

func TestService_DriftFullChainAndResync(t *testing.T) {
	svc, home, _ := newRegistryTestService(t)
	writeRegistryFixture(t, home)
	// 分发「登记文件解析出的真实条目」（SetSelected 同路径），确保
	// Overview 的 Mapped 基准与写入内容一致。
	if err := svc.SetSelected("zhipu-vision", "claude", true, false); err != nil {
		t.Fatal(err)
	}
	ov, _ := svc.Overview()
	if c := findCell(t, ov, "zhipu-vision", "claude"); c.Status != EngineDistributed || !c.Selected {
		t.Fatalf("初始应 distributed+selected: %+v", c)
	}

	// 登记条目 Update（改 env 值）→ Overview 该格 drift + reason。
	updated := stdioEntry("zhipu-vision")
	updated.Env = map[string]string{"API_KEY": "ROTATED"}
	if err := svc.Update("zhipu-vision", updated); err != nil {
		t.Fatal(err)
	}
	ov, _ = svc.Overview()
	c := findCell(t, ov, "zhipu-vision", "claude")
	if c.Status != EngineDrift || !strings.Contains(c.Reason, "重新分发") {
		t.Fatalf("应 drift: %+v", c)
	}

	// 再次 Distribute（幂等 resync）→ distributed 且目标内容已更新。
	if err := svc.SetSelected("zhipu-vision", "claude", true, false); err != nil {
		t.Fatalf("resync: %v", err)
	}
	ov, _ = svc.Overview()
	if c := findCell(t, ov, "zhipu-vision", "claude"); c.Status != EngineDistributed {
		t.Fatalf("resync 后应 distributed: %+v", c)
	}
	data, _ := os.ReadFile(filepath.Join(home, ".claude.json"))
	if gjson.GetBytes(data, "mcpServers.zhipu-vision.env.API_KEY").String() != "ROTATED" {
		t.Fatalf("目标内容未更新: %s", data)
	}
}

func TestService_ForeignModifiedProtectionOnUndistribute(t *testing.T) {
	svc, home, _ := newRegistryTestService(t)
	writeRegistryFixture(t, home)
	if err := svc.Distribute(stdioEntry("zhipu-vision"), "claude", false); err != nil {
		t.Fatal(err)
	}
	// 外部重写目标同名条目。
	cj := filepath.Join(home, ".claude.json")
	data, _ := os.ReadFile(cj)
	foreign, _ := sjson.SetRawBytes(data, "mcpServers.zhipu-vision", []byte(`{"type":"stdio","command":"user-edited","env":{"USER":"custom"}}`))
	if err := os.WriteFile(cj, foreign, 0o644); err != nil {
		t.Fatal(err)
	}

	// Undistribute 默认拒删且目标未动。
	err := svc.Undistribute("zhipu-vision", "claude", false)
	if KindOf(err) != KindForeignModified {
		t.Fatalf("应 foreign-modified: %v", err)
	}
	if !strings.Contains(err.Error(), "Claude") || !strings.Contains(err.Error(), "zhipu-vision") {
		t.Fatalf("文案缺引擎/条目名: %s", err)
	}
	cur, _ := os.ReadFile(cj)
	if gjson.GetBytes(cur, "mcpServers.zhipu-vision.command").String() != "user-edited" {
		t.Fatal("外部修改被覆盖")
	}
	// force=true 重试 → 移除成功。
	if err := svc.Undistribute("zhipu-vision", "claude", true); err != nil {
		t.Fatalf("force 移除: %v", err)
	}
	cur, _ = os.ReadFile(cj)
	if gjson.GetBytes(cur, "mcpServers.zhipu-vision").Exists() {
		t.Fatal("force 后未移除")
	}
}

func TestService_ForeignModifiedProtectionOnResync(t *testing.T) {
	svc, home, _ := newRegistryTestService(t)
	writeRegistryFixture(t, home)
	if err := svc.Distribute(stdioEntry("zhipu-vision"), "claude", false); err != nil {
		t.Fatal(err)
	}
	// 外部重写 + 登记也更新（drift 且外改）。
	cj := filepath.Join(home, ".claude.json")
	data, _ := os.ReadFile(cj)
	foreign, _ := sjson.SetRawBytes(data, "mcpServers.zhipu-vision", []byte(`{"type":"stdio","command":"user-edited"}`))
	if err := os.WriteFile(cj, foreign, 0o644); err != nil {
		t.Fatal(err)
	}
	// resync（SetSelected true）→ ErrForeignModified 且目标未动。
	if err := svc.SetSelected("zhipu-vision", "claude", true, false); KindOf(err) != KindForeignModified {
		t.Fatalf("resync 应 foreign-modified: %v", err)
	}
	cur, _ := os.ReadFile(cj)
	if gjson.GetBytes(cur, "mcpServers.zhipu-vision.command").String() != "user-edited" {
		t.Fatal("外部修改被静默覆盖")
	}
	// force=true 才覆盖为我方最新内容。
	if err := svc.SetSelected("zhipu-vision", "claude", true, true); err != nil {
		t.Fatalf("force resync: %v", err)
	}
	cur, _ = os.ReadFile(cj)
	if gjson.GetBytes(cur, "mcpServers.zhipu-vision.command").String() != "npx" {
		t.Fatalf("force 后内容不符: %s", cur)
	}
}

func TestService_UnknownEngineAndNameGuard(t *testing.T) {
	svc, home, _ := newRegistryTestService(t)
	writeRegistryFixture(t, home)
	if err := svc.Distribute(stdioEntry("x"), "omp", false); KindOf(err) != KindInvalidEntry {
		t.Fatalf("omp 无写入器应拒绝: %v", err)
	}
	if err := svc.Distribute(stdioEntry("x"), "nope", false); KindOf(err) != KindInvalidEntry {
		t.Fatalf("未知引擎应拒绝: %v", err)
	}
	// 不合规名字拒绝分发（登记内夹具名均合规，直接调 Distribute）。
	if err := svc.Distribute(SharedMcpEntry{Name: "bad.name", Type: "http", URL: "https://x.example.com/m"}, "claude", false); KindOf(err) != KindNameInvalid {
		t.Fatalf("不合规名字应拒绝: %v", err)
	}
}

func TestService_OverviewEnginesMetaAndCounts(t *testing.T) {
	svc, home, _ := newRegistryTestService(t)
	writeRegistryFixture(t, home)
	if err := svc.Distribute(stdioEntry("zhipu-vision"), "claude", false); err != nil {
		t.Fatal(err)
	}
	if err := svc.Distribute(stdioEntry("zhipu-vision"), "pi", false); err != nil {
		t.Fatal(err)
	}
	ov, err := svc.Overview()
	if err != nil {
		t.Fatal(err)
	}
	if !ov.RegistryExists || ov.RegistryPath != svc.registryPath() {
		t.Fatalf("registry 元数据不符: %+v", ov)
	}
	// 5 行元数据（四引擎 + omp excluded）。
	if len(ov.EnginesMeta) != 5 {
		t.Fatalf("EnginesMeta 应 5 行: %d", len(ov.EnginesMeta))
	}
	var omp *EngineMeta
	counts := map[string]int{}
	for i := range ov.EnginesMeta {
		m := ov.EnginesMeta[i]
		counts[m.ID] = m.ManagedCount
		if m.ID == "omp" {
			omp = &ov.EnginesMeta[i]
		}
	}
	if omp == nil || omp.Support != "excluded" || !strings.Contains(omp.Detail, "裁定") {
		t.Fatalf("omp excluded 行不符: %+v", omp)
	}
	if counts["claude"] != 1 || counts["pi"] != 1 || counts["codex"] != 0 || counts["opencode"] != 0 {
		t.Fatalf("托管计数不符: %+v", counts)
	}
	// 每条 engines 含 omp excluded 格。
	for _, e := range ov.Entries {
		if e.Engines["omp"].Status != EngineExcluded {
			t.Fatalf("%s 缺 omp excluded 格", e.Entry.Name)
		}
		if len(e.Engines) != 5 {
			t.Fatalf("%s 引擎格数不符: %d", e.Entry.Name, len(e.Engines))
		}
	}
	// 不合规名字条目：NameDistributable=false + 四引擎格 unsupported。
	if err := svc.Create(SharedMcpEntry{Name: "ok", Type: "http", URL: "https://ok.example.com/m"}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(svc.registryPath())
	withBad, _ := sjson.SetRawBytes(data, "mcpServers.bad\\.name", []byte(`{"type":"http","url":"https://bad.example.com/m"}`))
	if err := os.WriteFile(svc.registryPath(), withBad, 0o600); err != nil {
		t.Fatal(err)
	}
	ov, _ = svc.Overview()
	for _, e := range ov.Entries {
		if e.Entry.Name == "bad.name" {
			if e.NameDistributable {
				t.Fatal("bad.name 应标记不可分发")
			}
			for _, eng := range []string{"claude", "codex", "opencode", "pi"} {
				if c := e.Engines[eng]; c.Status != EngineUnsupported || !strings.Contains(c.Reason, "不兼容") {
					t.Fatalf("bad.name %s 格应 unsupported: %+v", eng, c)
				}
			}
		}
	}
}

func TestService_RegistryCorruptMessageContainsPath(t *testing.T) {
	svc, home, _ := newRegistryTestService(t)
	if err := os.MkdirAll(filepath.Join(home, ".agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(svc.registryPath(), []byte("NOT-JSON"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := svc.Overview()
	if KindOf(err) != KindRegistryCorrupt {
		t.Fatalf("应 registry-corrupt: %v", err)
	}
	if !strings.Contains(err.Error(), svc.registryPath()) {
		t.Fatalf("文案缺路径: %s", err)
	}
}

func TestService_PerEngineIndependence(t *testing.T) {
	svc, home, _ := newRegistryTestService(t)
	writeRegistryFixture(t, home)
	if err := svc.Distribute(stdioEntry("zhipu-vision"), "claude", false); err != nil {
		t.Fatal(err)
	}
	if err := svc.Distribute(stdioEntry("zhipu-vision"), "pi", false); err != nil {
		t.Fatal(err)
	}
	// pi 目标损坏 → pi Undistribute 失败，claude 不受牵连。
	piPath := filepath.Join(home, ".pi", "agent", "mcp.json")
	if err := os.WriteFile(piPath, []byte("{BROKEN"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := svc.Undistribute("zhipu-vision", "pi", false); KindOf(err) != KindTargetCorrupt {
		t.Fatalf("pi 损坏应 target-corrupt: %v", err)
	}
	if err := svc.Undistribute("zhipu-vision", "claude", false); err != nil {
		t.Fatalf("claude 不应受牵连: %v", err)
	}
}

func TestAtomicity_WriteFailureLeavesTargetUntouched(t *testing.T) {
	svc, home, _ := newRegistryTestService(t)
	writeRegistryFixture(t, home)
	// 预置全部目标文件。
	targets := []string{
		filepath.Join(home, ".claude.json"),
		filepath.Join(home, ".codex", "config.toml"),
		filepath.Join(home, ".config", "opencode", "opencode.json"),
		filepath.Join(home, ".pi", "agent", "mcp.json"),
	}
	for i, p := range targets {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		content := "{}"
		if strings.HasSuffix(p, "config.toml") {
			content = "# toml\r\n"
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		_ = i
	}
	before := map[string]string{}
	for _, p := range targets {
		b, _ := os.ReadFile(p)
		before[p] = string(b)
	}

	origRename, origWrite := atomicRename, atomicWriteFile
	atomicRename = func(oldPath, newPath string) error { return errors.New("注入失败") }
	atomicWriteFile = func(name string, data []byte, perm os.FileMode) error { return errors.New("注入失败") }
	defer func() { atomicRename, atomicWriteFile = origRename, origWrite }()

	// 登记写入失败 → 文件不变。
	if err := svc.Create(httpEntry("boom")); err == nil {
		t.Fatal("登记写入应失败")
	}
	// 四写入器 Apply 全部失败 → 目标字节不变。
	engines := []string{"claude", "codex", "opencode", "pi"}
	for _, eng := range engines {
		if err := svc.Distribute(stdioEntry("zhipu-vision"), eng, false); err == nil {
			t.Fatalf("%s 分发应失败", eng)
		}
	}
	for _, p := range targets {
		b, _ := os.ReadFile(p)
		if string(b) != before[p] {
			t.Fatalf("目标被半写: %s\nbefore=%q\nafter=%q", p, before[p], string(b))
		}
	}
	// 登记文件同样未动（未创建新条目）。
	reg, _ := os.ReadFile(svc.registryPath())
	if gjson.GetBytes(reg, "mcpServers.boom").Exists() {
		t.Fatal("半写落盘")
	}
}
