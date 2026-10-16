package wslsupply

// pi_config_test.go — Pi 配置键级投影（S2）的冲突表驱动测试：设计 §三全表
//（unmanaged-existing / 本地修改冻结 / 本地删除冻结 / 双侧同变 / 来源删除 /
// 解除冻结两操作 / 旧播种树无清单 / 未知字段 / 损坏文件）+ mcp 转换接入 +
// amagi 依赖诊断。全部经假探测面与桩源运行，不触达真实 WSL。

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"amagi-codebox/internal/launcher"
)

// stubPiConfigSource 是 PiConfigSource 桩（可变来源内容）。
type stubPiConfigSource struct {
	settings    []byte
	amagi       []byte
	mcp         []byte
	providerIDs []string
	err         bool
}

func (s *stubPiConfigSource) SettingsJSON() ([]byte, error) {
	if s.err {
		return nil, os.ErrPermission
	}
	return s.settings, nil
}
func (s *stubPiConfigSource) AmagiJSON() ([]byte, error) {
	if s.err {
		return nil, os.ErrPermission
	}
	return s.amagi, nil
}
func (s *stubPiConfigSource) MCPJSON() ([]byte, error) {
	if s.err {
		return nil, os.ErrPermission
	}
	return s.mcp, nil
}
func (s *stubPiConfigSource) KnownProviderIDs() ([]string, error) { return s.providerIDs, nil }

// stubFactsSource 是 FactsSource 桩（可变验证结果）。
type stubFactsSource struct {
	mu    atomic.Value // TransformFacts
	calls int32
	err   bool
}

func (s *stubFactsSource) TransformFacts(_ context.Context, _ TargetRef) (TransformFacts, error) {
	atomic.AddInt32(&s.calls, 1)
	if s.err {
		return TransformFacts{}, os.ErrDeadlineExceeded
	}
	return s.mu.Load().(TransformFacts), nil
}
func (s *stubFactsSource) set(f TransformFacts) { s.mu.Store(f) }

// newS2Coordinator 构造带三个 S2 单元的协调器（假探测面 + 桩源）。
func newS2Coordinator(t *testing.T) (*Coordinator, *fakeProbes, *stubPiConfigSource, *stubFactsSource) {
	t.Helper()
	fp := newFakeProbes(t)
	c := newCoordinatorWith(t, fp, &stubA2{outcome: launcher.SyncWSLPiLoginAuthFresh})
	src := &stubPiConfigSource{}
	facts := &stubFactsSource{}
	facts.set(TransformFacts{
		ResolvedCommands: map[string]string{"npx": "/usr/bin/npx"},
		PrefixMappings:   []PathPrefixRule{{WindowsPrefix: `C:\Users\u\.pi\agent`, LinuxPrefix: "/home/u/.pi/agent"}},
		HostHomeWindows:  `C:\Users\u`,
	})
	c.RegisterProjection(NewPiSettingsAdapter(src, facts))
	c.RegisterProjection(NewPiAmagiAdapter(src, facts))
	c.RegisterProjection(NewPiMCPAdapter(src, facts))
	// F-3（S2 diting 裁定）：三单元需 per-target 同意——生命周期测试默认已同意
	//（同意门行为由 TestPiConfigUnitsRequireConsent 单独钉住）。
	for _, unit := range []UnitID{UnitPiSettings, UnitPiAmagi, UnitPiMCP} {
		if err := c.GrantUnitConsent(context.Background(), "Ubuntu", CLIPi, unit); err != nil {
			t.Fatalf("grant consent for %s: %v", unit, err)
		}
	}
	return c, fp, src, facts
}

func piFile(fp *fakeProbes, name string) string {
	return filepath.Join(fp.uncRoot, filepath.FromSlash("home/u/.pi/agent/"+name))
}

func writePiFile(t *testing.T, fp *fakeProbes, name, content string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(piFile(fp, name)), 0o700)
	if err := os.WriteFile(piFile(fp, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readPiFile(t *testing.T, fp *fakeProbes, name string) string {
	t.Helper()
	data, err := os.ReadFile(piFile(fp, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func runPiUnit(t *testing.T, c *Coordinator, unit UnitID) UnitResult {
	t.Helper()
	res, err := c.RunUnits(context.Background(), "Ubuntu", CLIPi, ReasonSave, []UnitID{unit})
	if err != nil {
		t.Fatalf("RunUnits(%s): %v", unit, err)
	}
	return res.Results[0]
}

// ---------------------------------------------------------------------------
// 验收 1：面板修改进入未手改 WSL；手改后再保存必须保留 WSL（冻结核心语义）
// ---------------------------------------------------------------------------

func TestPiSettingsProjectionLifecycle(t *testing.T) {
	c, fp, src, _ := newS2Coordinator(t)
	src.settings = []byte(`{
  "packages": ["https://github.com/runrunrain/amagi-pi.git"],
  "retry": {"provider": {"timeoutMs": 300000}},
  "lastChangelogVersion": "1.2.0"
}`)

	// 首次：目标缺失 → 白名单字段建立；packages/机器字段不携带。
	res := runPiUnit(t, c, UnitPiSettings)
	if res.Outcome != UnitApplied {
		t.Fatalf("first projection must apply, got %+v", res)
	}
	got := readPiFile(t, fp, "settings.json")
	var doc map[string]any
	if json.Unmarshal([]byte(got), &doc) != nil {
		t.Fatalf("projected settings.json invalid: %s", got)
	}
	if _, ok := doc["retry"]; !ok {
		t.Fatalf("whitelisted field retry missing:\n%s", got)
	}
	if _, ok := doc["packages"]; ok {
		t.Fatalf("packages must never be projected (S5 replays plugins):\n%s", got)
	}
	if _, ok := doc["lastChangelogVersion"]; ok {
		t.Fatalf("machine state must not project:\n%s", got)
	}

	// 面板修改进入未手改 WSL：来源变化 → 更新。
	src.settings = []byte(`{"retry": {"provider": {"timeoutMs": 600000}}}`)
	res = runPiUnit(t, c, UnitPiSettings)
	if res.Outcome != UnitApplied {
		t.Fatalf("source change must update unmodified target, got %+v", res)
	}
	if !strings.Contains(readPiFile(t, fp, "settings.json"), "600000") {
		t.Fatalf("update missing:\n%s", readPiFile(t, fp, "settings.json"))
	}

	// 无变化 → fresh 零写入。
	chmodBefore := atomic.LoadInt32(&fp.chmodCalls)
	res = runPiUnit(t, c, UnitPiSettings)
	if res.Outcome != UnitFresh {
		t.Fatalf("unchanged must be fresh, got %+v", res)
	}
	if atomic.LoadInt32(&fp.chmodCalls) != chmodBefore {
		t.Fatalf("unchanged run must not write")
	}

	// WSL 手改 → 冻结；再保存 Windows 必须保留 WSL 值。
	hand := strings.Replace(readPiFile(t, fp, "settings.json"), "600000", "900000", 1)
	os.WriteFile(piFile(fp, "settings.json"), []byte(hand), 0o600)
	src.settings = []byte(`{"retry": {"provider": {"timeoutMs": 120000}}}`)
	res = runPiUnit(t, c, UnitPiSettings)
	if res.Outcome != UnitBlocked {
		t.Fatalf("hand-edited target must freeze the key, got %+v", res)
	}
	if !strings.Contains(readPiFile(t, fp, "settings.json"), "900000") {
		t.Fatalf("WSL hand edit must be preserved:\n%s", readPiFile(t, fp, "settings.json"))
	}
	// 手改 + 来源再保存 = 双侧均有修改：保留 WSL 现状并列报告冲突（§三）。
	if !strings.Contains(res.Detail, "both-changed-frozen") {
		t.Fatalf("conflict freeze reason missing: %s", res.Detail)
	}
}

func TestPiMCPServerLifecycle(t *testing.T) {
	c, fp, src, facts := newS2Coordinator(t)
	src.mcp = []byte(`{"mcpServers": {
  "web": {"type": "http", "url": "https://example.com/mcp"},
  "tool": {"type": "stdio", "command": "npx.cmd", "args": ["-y", "pkg"], "env": {"K": "v"}}
}}`)

	// 首次：全部 server 新增（npx.cmd 经已验证事实映射为 npx；HTTP 原样）。
	if runPiUnit(t, c, UnitPiMCP).Outcome != UnitApplied {
		t.Fatal("first projection must apply")
	}
	body := readPiFile(t, fp, "mcp.json")
	if !strings.Contains(body, `"command": "npx"`) {
		t.Fatalf("verified npx.cmd must map to native npx:\n%s", body)
	}
	if !strings.Contains(body, "https://example.com/mcp") {
		t.Fatalf("remote server URL must be kept:\n%s", body)
	}

	// WSL 侧已有用户自有 server（新名）+ 托管 server 手改 → 仅手改键冻结，
	// 用户新增键 unmanaged 保留；其它托管键仍可更新。
	var doc map[string]any
	json.Unmarshal([]byte(body), &doc)
	servers := doc["mcpServers"].(map[string]any)
	servers["user-own"] = map[string]any{"type": "stdio", "command": "uvx"}
	servers["web"] = map[string]any{"type": "http", "url": "https://changed.example/mcp"}
	out, _ := json.MarshalIndent(doc, "", "  ")
	os.WriteFile(piFile(fp, "mcp.json"), append(out, '\n'), 0o600)

	src.mcp = []byte(`{"mcpServers": {
  "web": {"type": "http", "url": "https://new.example/mcp"},
  "tool": {"type": "stdio", "command": "npx.cmd", "args": ["-y", "pkg2"]}
}}`)
	res := runPiUnit(t, c, UnitPiMCP)
	if res.Outcome != UnitApplied || !strings.HasPrefix(res.Detail, "partial") {
		t.Fatalf("mixed update must apply partially, got %+v", res)
	}
	body = readPiFile(t, fp, "mcp.json")
	if !strings.Contains(body, "pkg2") {
		t.Fatalf("unmodified managed server must update:\n%s", body)
	}
	if !strings.Contains(body, "changed.example") {
		t.Fatalf("hand-edited server must keep WSL content:\n%s", body)
	}
	if !strings.Contains(body, "user-own") || !strings.Contains(body, "uvx") {
		t.Fatalf("user-own server must be preserved untouched:\n%s", body)
	}

	_ = facts
}

func TestPiMCPConversionFailureKeepsUnitAtomic(t *testing.T) {
	c, fp, src, facts := newS2Coordinator(t)
	src.mcp = []byte(`{"mcpServers": {
  "bad": {"type": "stdio", "command": "powershell.exe", "args": ["-File", "x.ps1"]}
}}`)
	// 事实探测失败 → 转换不可确认 → 该 server 不新增（单元受阻、目标零写入）。
	facts.err = true
	res := runPiUnit(t, c, UnitPiMCP)
	if res.Outcome != UnitBlocked || !strings.Contains(res.Detail, "conversion-blocked") {
		t.Fatalf("conversion-blocked server must keep the unit out, got %+v", res)
	}
	if raw, rErr := os.ReadFile(piFile(fp, "mcp.json")); rErr == nil {
		var doc map[string]any
		json.Unmarshal(raw, &doc)
		if servers, ok := doc["mcpServers"].(map[string]any); ok && len(servers) > 0 {
			t.Fatalf("conversion-blocked server must not be written:\n%s", raw)
		}
	} // 文件缺失是更强的「零写入」证据

	// 已写入托管的 server（可转换形态）后，来源改为 Windows 专属形态 →
	// 转换失败 → 该 server 不更新，目标同名保持（§五原子单元）。
	facts.err = false
	writePiFile(t, fp, "mcp.json", `{}`)
	// 无记录+目标文件存在但 mcpServers 为空：建立登记需先经历一次可写投影。
	src.mcp = []byte(`{"mcpServers": {"bad": {"type": "stdio", "command": "uvx"}}}`)
	if res := runPiUnit(t, c, UnitPiMCP); res.Outcome != UnitApplied {
		t.Fatalf("seed managed state failed: %+v", res)
	}
	src.mcp = []byte(`{"mcpServers": {"bad": {"type": "stdio", "command": "powershell.exe"}}}`)
	res = runPiUnit(t, c, UnitPiMCP)
	// 单键单元：唯一键转换受阻 → 整单元受阻（多键时才为 partial）。
	if res.Outcome != UnitBlocked || !strings.Contains(res.Detail, "conversion-blocked") {
		t.Fatalf("conversion-blocked update must keep the unit out: %+v", res)
	}
	if !strings.Contains(readPiFile(t, fp, "mcp.json"), "uvx") {
		t.Fatalf("conversion-failed update must keep existing entry:\n%s", readPiFile(t, fp, "mcp.json"))
	}
}

// ---------------------------------------------------------------------------
// 验收 2：表驱动逐态（来源删除 / 目标删除 / 旧播种树无清单 / 未知字段 /
// 损坏文件 / 双侧同时修改）
// ---------------------------------------------------------------------------

func TestPiKeyedConflictTable(t *testing.T) {
	t.Run("table", func(t *testing.T) {
		// 用 amagi 单元走全表（键级引擎共用，状态覆盖对三个单元同构）。
		base := `{"$schema": "./x.json", "profile": "tiered", "agents": {"baize": {"model": "amagi-glm/glm-5.3"}}, "mcp": {"default": ["web"]}, "compaction": {"model": "amagi-glm/glm-5.3"}, "userExtra": {"keep": true}}`

		t.Run("source-deleted-unmodified", func(t *testing.T) {
			c, fp, src, _ := newS2Coordinator(t)
			src.amagi = []byte(base)
			src.providerIDs = []string{"amagi-glm"}
			src.mcp = []byte(`{"mcpServers": {"web": {}}}`)
			if runPiUnit(t, c, UnitPiAmagi).Outcome != UnitApplied {
				t.Fatal("seed failed")
			}
			// 来源删除 compaction（目标未手改）→ 移除。
			src.amagi = []byte(`{"$schema": "./x.json", "profile": "tiered", "agents": {"baize": {"model": "amagi-glm/glm-5.3"}}, "mcp": {"default": ["web"]}, "userExtra": {"keep": true}}`)
			res := runPiUnit(t, c, UnitPiAmagi)
			if res.Outcome != UnitApplied {
				t.Fatalf("managed unmodified deletion must apply, got %+v", res)
			}
			body := readPiFile(t, fp, "amagi.json")
			if strings.Contains(body, "compaction") {
				t.Fatalf("source-deleted managed key must be removed:\n%s", body)
			}
			for _, keep := range []string{"tiered", "baize", "web"} {
				if !strings.Contains(body, keep) {
					t.Fatalf("other managed keys must remain:\n%s", body)
				}
			}
		})

		t.Run("source-deleted-hand-modified-keeps", func(t *testing.T) {
			c, fp, src, _ := newS2Coordinator(t)
			src.amagi = []byte(base)
			src.providerIDs = []string{"amagi-glm"}
			src.mcp = []byte(`{"mcpServers": {"web": {}}}`)
			runPiUnit(t, c, UnitPiAmagi)
			// 手改 compaction 后来源删除 → 保留本地（冻结）。
			body := readPiFile(t, fp, "amagi.json")
			hand := strings.Replace(body, `"model": "amagi-glm/glm-5.3"`, `"model": "kimi/k3:high"`, 1)
			os.WriteFile(piFile(fp, "amagi.json"), []byte(hand), 0o600)
			src.amagi = []byte(`{"$schema": "./x.json", "profile": "tiered", "agents": {"baize": {"model": "amagi-glm/glm-5.3"}}, "mcp": {"default": ["web"]}}`)
			res := runPiUnit(t, c, UnitPiAmagi)
			if res.Outcome != UnitApplied || !strings.HasPrefix(res.Detail, "partial") {
				t.Fatalf("mixed (removal + frozen hand-edit) must apply partially, got %+v", res)
			}
			body = readPiFile(t, fp, "amagi.json")
			if !strings.Contains(body, "kimi/k3:high") {
				t.Fatalf("local hand edit must survive:\n%s", body)
			}
			if strings.Contains(body, "compaction") {
				t.Fatalf("unmodified managed key must still be removed on source deletion:\n%s", body)
			}
		})

		t.Run("target-key-deleted-frozen", func(t *testing.T) {
			c, fp, src, _ := newS2Coordinator(t)
			src.amagi = []byte(base)
			src.providerIDs = []string{"amagi-glm"}
			src.mcp = []byte(`{"mcpServers": {"web": {}}}`)
			runPiUnit(t, c, UnitPiAmagi)
			// WSL 删除托管键（profile）→ local-deletion frozen，不自动补回。
			var doc map[string]any
			json.Unmarshal([]byte(readPiFile(t, fp, "amagi.json")), &doc)
			delete(doc, "profile")
			out, _ := json.MarshalIndent(doc, "", "  ")
			os.WriteFile(piFile(fp, "amagi.json"), append(out, '\n'), 0o600)
			res := runPiUnit(t, c, UnitPiAmagi)
			if res.Outcome != UnitBlocked || !strings.Contains(res.Detail, "local-deletion-frozen") {
				t.Fatalf("local deletion must freeze with reason, got %+v", res)
			}
			if strings.Contains(readPiFile(t, fp, "amagi.json"), "tiered") {
				t.Fatalf("user deletion must not be auto-restored")
			}
		})

		t.Run("both-changed-keeps-wsl-and-reports-conflict", func(t *testing.T) {
			c, fp, src, _ := newS2Coordinator(t)
			src.amagi = []byte(base)
			src.providerIDs = []string{"amagi-glm"}
			src.mcp = []byte(`{"mcpServers": {"web": {}}}`)
			runPiUnit(t, c, UnitPiAmagi)
			body := readPiFile(t, fp, "amagi.json")
			hand := strings.Replace(body, `"profile": "tiered"`, `"profile": "local"`, 1)
			os.WriteFile(piFile(fp, "amagi.json"), []byte(hand), 0o600)
			src.amagi = []byte(strings.Replace(base, `"profile": "tiered"`, `"profile": "windows-new"`, 1))
			res := runPiUnit(t, c, UnitPiAmagi)
			if res.Outcome != UnitBlocked {
				t.Fatalf("both-changed must keep WSL, got %+v", res)
			}
			if !strings.Contains(res.Detail, "both-changed-frozen") && !strings.Contains(res.Detail, "双侧均有修改") {
				t.Fatalf("conflict must be listed: %s", res.Detail)
			}
			if !strings.Contains(readPiFile(t, fp, "amagi.json"), `"profile": "local"`) {
				t.Fatalf("WSL current must win over mtime/windows recency")
			}
		})

		t.Run("old-seeded-tree-without-manifest-is-unmanaged", func(t *testing.T) {
			// S0 停用前 cp -r 播种的旧副本：无清单记录 → unmanaged-existing，
			// 整文件零写入（即使内容与来源相同也不接管）。
			c, fp, src, _ := newS2Coordinator(t)
			src.amagi = []byte(base)
			src.providerIDs = []string{"amagi-glm"}
			src.mcp = []byte(`{"mcpServers": {"web": {}}}`)
			seeded := `{"$schema": "./x.json", "profile": "tiered", "agents": {"baize": {"model": "amagi-glm/glm-5.3"}}, "mcp": {"default": ["web"]}, "compaction": {"model": "amagi-glm/glm-5.3"}}`
			writePiFile(t, fp, "amagi.json", seeded)
			before := readPiFile(t, fp, "amagi.json")
			chmodBefore := atomic.LoadInt32(&fp.chmodCalls)
			res := runPiUnit(t, c, UnitPiAmagi)
			if res.Outcome != UnitBlocked || !strings.Contains(res.Detail, "unmanaged-existing") {
				t.Fatalf("old seeded tree must be unmanaged-existing, got %+v", res)
			}
			if readPiFile(t, fp, "amagi.json") != before {
				t.Fatalf("unmanaged file must be byte-identical")
			}
			if atomic.LoadInt32(&fp.chmodCalls) != chmodBefore {
				t.Fatalf("unmanaged file must not be written")
			}
		})

		t.Run("unknown-source-fields-not-carried", func(t *testing.T) {
			c, fp, src, _ := newS2Coordinator(t)
			src.amagi = []byte(`{"$schema": "./x.json", "profile": "tiered", "futureField": {"x": 1}}`)
			src.providerIDs = []string{"amagi-glm"}
			src.mcp = []byte(`{}`)
			if runPiUnit(t, c, UnitPiAmagi).Outcome != UnitApplied {
				t.Fatal("projection failed")
			}
			if strings.Contains(readPiFile(t, fp, "amagi.json"), "futureField") {
				t.Fatalf("unknown source field must not be carried")
			}
		})

		t.Run("corrupt-target-refused-not-overwritten", func(t *testing.T) {
			c, fp, src, _ := newS2Coordinator(t)
			src.amagi = []byte(base)
			src.providerIDs = []string{"amagi-glm"}
			src.mcp = []byte(`{}`)
			writePiFile(t, fp, "amagi.json", `{"profile": "brok`)
			res := runPiUnit(t, c, UnitPiAmagi)
			if res.Outcome != UnitBlocked || res.Detail != "target-corrupt" {
				t.Fatalf("corrupt target must refuse with target-corrupt, got %+v", res)
			}
			if readPiFile(t, fp, "amagi.json") != `{"profile": "brok` {
				t.Fatalf("corrupt file must be untouched")
			}
		})

		t.Run("corrupt-source-blocked", func(t *testing.T) {
			c, _, src, _ := newS2Coordinator(t)
			src.amagi = []byte(`{invalid`)
			res := runPiUnit(t, c, UnitPiAmagi)
			if res.Outcome != UnitBlocked || res.Detail != "source-corrupt" {
				t.Fatalf("corrupt source must block, got %+v", res)
			}
		})
	})
}

// ---------------------------------------------------------------------------
// 验收 4：amagi 引用缺 provider/server 的明确诊断
// ---------------------------------------------------------------------------

func TestPiAmagiDependencyDiagnostics(t *testing.T) {
	c, _, src, _ := newS2Coordinator(t)
	src.providerIDs = []string{"amagi-glm"}         // 缺 kimi-coding
	src.mcp = []byte(`{"mcpServers": {"web": {}}}`) // 缺 zread
	src.amagi = []byte(`{
  "$schema": "./x.json",
  "agents": {"baize": {"model": "kimi-coding/k3:high"}},
  "compaction": {"model": "kimi-coding/k3"},
  "mcp": {"default": ["web", "zread"]}
}`)
	res := runPiUnit(t, c, UnitPiAmagi)
	if res.Outcome != UnitApplied {
		t.Fatalf("diagnostics must not block projection, got %+v", res)
	}
	for _, want := range []string{
		`provider "kimi-coding" 未在 Windows 侧模型目录中找到`,
		`server "zread" 未在 mcp.json 中找到`,
	} {
		if !strings.Contains(res.Detail, want) {
			t.Fatalf("missing diagnostic %q in detail: %s", want, res.Detail)
		}
	}
}

// ---------------------------------------------------------------------------
// 动作面：预览 / 保留本地 / 预览后接管（含旧预览 CAS 拒绝）
// ---------------------------------------------------------------------------

func TestPiKeyedPreviewKeepLocalTakeOver(t *testing.T) {
	c, fp, src, _ := newS2Coordinator(t)
	src.mcp = []byte(`{"mcpServers": {"web": {"type": "http", "url": "https://example.com/mcp"}}}`)
	// 旧播种树：无清单。
	writePiFile(t, fp, "mcp.json", `{"mcpServers": {"web": {"type": "http", "url": "https://old.example/mcp"}, "user": {"command": "uvx"}}}`)

	// 预览：逐键动作 + 目标 hash 令牌。
	previews, err := c.PreviewUnits(context.Background(), "Ubuntu", CLIPi, []UnitID{UnitPiMCP})
	if err != nil {
		t.Fatal(err)
	}
	pv := previews[0]
	if pv.BlockedReason != "unmanaged-existing" {
		t.Fatalf("preview must surface unmanaged-existing, got %+v", pv)
	}
	if len(pv.Keys) == 0 {
		t.Fatalf("preview must carry per-key rows")
	}
	token := pv.TargetHash
	if token == "" {
		t.Fatal("preview must carry target hash token")
	}

	// 保留本地（停跟随）：再次运行零动作。
	if err := c.DropUnitRegistration("Ubuntu", CLIPi, UnitPiMCP); err != nil {
		t.Fatal(err)
	}
	res := runPiUnit(t, c, UnitPiMCP)
	if res.Outcome != UnitFresh {
		t.Fatalf("detached unit must be a no-op (fresh), got %+v", res)
	}
	if !strings.Contains(readPiFile(t, fp, "mcp.json"), "old.example") {
		t.Fatalf("keep-local must not touch target")
	}

	// 旧预览 CAS：目标已变（接管前先被外部修改）→ 拒绝，不覆盖新编辑。
	os.WriteFile(piFile(fp, "mcp.json"), []byte(`{"mcpServers": {"web": {"type": "http", "url": "https://newer.example/mcp"}}}`), 0o600)
	take, err := c.TakeOverUnit(context.Background(), "Ubuntu", CLIPi, UnitPiMCP, token)
	if err != nil {
		t.Fatal(err)
	}
	if take.Outcome != UnitBlocked || take.Detail != "preview-expired" {
		t.Fatalf("stale preview must be rejected, got %+v", take)
	}

	// 重新预览 → 接管成功：来源覆写托管键，重建基准。
	previews, _ = c.PreviewUnits(context.Background(), "Ubuntu", CLIPi, []UnitID{UnitPiMCP})
	fresh := previews[0].TargetHash
	take, err = c.TakeOverUnit(context.Background(), "Ubuntu", CLIPi, UnitPiMCP, fresh)
	if err != nil {
		t.Fatal(err)
	}
	if take.Outcome != UnitApplied {
		t.Fatalf("takeover with fresh hash must apply, got %+v", take)
	}
	body := readPiFile(t, fp, "mcp.json")
	if !strings.Contains(body, "https://example.com/mcp") {
		t.Fatalf("takeover must adopt current source:\n%s", body)
	}
	// 接管后：来源变化恢复流动（不再 unmanaged）。
	src.mcp = []byte(`{"mcpServers": {"web": {"type": "http", "url": "https://v2.example/mcp"}}}`)
	res = runPiUnit(t, c, UnitPiMCP)
	if res.Outcome != UnitApplied {
		t.Fatalf("post-takeover source change must flow, got %+v", res)
	}
	if !strings.Contains(readPiFile(t, fp, "mcp.json"), "v2.example") {
		t.Fatalf("post-takeover update missing")
	}
}

// ---------------------------------------------------------------------------
// 状态登记：detached / partial 可见
// ---------------------------------------------------------------------------

func TestPiKeyedStatusSurfacesDetachedAndPartial(t *testing.T) {
	c, fp, src, _ := newS2Coordinator(t)
	src.mcp = []byte(`{"mcpServers": {"a": {"command": "uvx"}, "b": {"command": "uvx"}}}`)
	runPiUnit(t, c, UnitPiMCP)
	// 手改一个键 → partial 状态登记。
	body := readPiFile(t, fp, "mcp.json")
	os.WriteFile(piFile(fp, "mcp.json"), []byte(strings.Replace(body, `"a"`, `"a"`, 1)), 0o600)
	var doc map[string]any
	json.Unmarshal([]byte(body), &doc)
	doc["mcpServers"].(map[string]any)["a"] = map[string]any{"command": "custom"}
	out, _ := json.MarshalIndent(doc, "", "  ")
	os.WriteFile(piFile(fp, "mcp.json"), append(out, '\n'), 0o600)
	src.mcp = []byte(`{"mcpServers": {"a": {"command": "uvx2"}, "b": {"command": "uvx2"}}}`)
	runPiUnit(t, c, UnitPiMCP)
	st := c.GetStatus("Ubuntu", CLIPi)
	for _, u := range st.Units {
		if u.Unit == UnitPiMCP && u.LastOutcome != "partial" {
			t.Fatalf("partial outcome must be recorded, got %q", u.LastOutcome)
		}
	}
	// 保留本地 → detached 可见。
	if err := c.DropUnitRegistration("Ubuntu", CLIPi, UnitPiMCP); err != nil {
		t.Fatal(err)
	}
	st = c.GetStatus("Ubuntu", CLIPi)
	for _, u := range st.Units {
		if u.Unit == UnitPiMCP && !u.Detached {
			t.Fatalf("detached must be visible in status")
		}
	}
}

// ---------------------------------------------------------------------------
// F-3（S2 diting 裁定）：三键级单元的 per-target 同意门
// ---------------------------------------------------------------------------

func TestPiConfigUnitsRequireConsent(t *testing.T) {
	mk := func(t *testing.T) (*Coordinator, *fakeProbes, *stubPiConfigSource, *stubFactsSource) {
		// 不经 newS2Coordinator（其默认已同意）：此处保持未同意初态。
		fp := newFakeProbes(t)
		c := newCoordinatorWith(t, fp, &stubA2{outcome: launcher.SyncWSLPiLoginAuthFresh})
		src := &stubPiConfigSource{}
		facts := &stubFactsSource{}
		facts.set(TransformFacts{ResolvedCommands: map[string]string{"npx": "/usr/bin/npx"}})
		c.RegisterProjection(NewPiSettingsAdapter(src, facts))
		c.RegisterProjection(NewPiMCPAdapter(src, facts))
		return c, fp, src, facts
	}

	t.Run("unconsented first launch is read-only (no file created)", func(t *testing.T) {
		c, fp, src, _ := mk(t)
		src.settings = []byte(`{"retry": {"provider": {"timeoutMs": 1}}}`)
		src.mcp = []byte(`{"mcpServers": {"web": {"url": "https://x.example/mcp"}}}`)
		for _, unit := range []UnitID{UnitPiSettings, UnitPiMCP} {
			res := runPiUnit(t, c, unit)
			if res.Outcome != UnitBlocked || res.Detail != "awaiting-consent" {
				t.Fatalf("%s must be blocked awaiting-consent, got %+v", unit, res)
			}
		}
		if _, err := os.Stat(piFile(fp, "settings.json")); !os.IsNotExist(err) {
			t.Fatalf("unconsented run must not create settings.json")
		}
		if _, err := os.Stat(piFile(fp, "mcp.json")); !os.IsNotExist(err) {
			t.Fatalf("unconsented run must not create mcp.json")
		}
		// 同意前接管同样拒绝（显式写入不得绕过同意门）。
		take, err := c.TakeOverUnit(context.Background(), "Ubuntu", CLIPi, UnitPiMCP, "")
		if err != nil {
			t.Fatal(err)
		}
		if take.Outcome != UnitBlocked || take.Detail != "awaiting-consent" {
			t.Fatalf("takeover before consent must be blocked, got %+v", take)
		}
	})

	t.Run("consent unlocks first publish", func(t *testing.T) {
		c, fp, src, _ := mk(t)
		src.settings = []byte(`{"retry": {"provider": {"timeoutMs": 1}}}`)
		if err := c.GrantUnitConsent(context.Background(), "Ubuntu", CLIPi, UnitPiSettings); err != nil {
			t.Fatal(err)
		}
		res := runPiUnit(t, c, UnitPiSettings)
		if res.Outcome != UnitApplied {
			t.Fatalf("post-consent first publish must apply, got %+v", res)
		}
		if !strings.Contains(readPiFile(t, fp, "settings.json"), "timeoutMs") {
			t.Fatalf("post-consent publish missing content")
		}
	})

	t.Run("cross-generation consent does not inherit hash baselines", func(t *testing.T) {
		c, fp, src, _ := mk(t)
		src.mcp = []byte(`{"mcpServers": {"web": {"url": "https://x.example/mcp"}}}`)
		if err := c.GrantUnitConsent(context.Background(), "Ubuntu", CLIPi, UnitPiMCP); err != nil {
			t.Fatal(err)
		}
		if runPiUnit(t, c, UnitPiMCP).Outcome != UnitApplied {
			t.Fatal("gen1 publish failed")
		}
		// distro 重装（machine-id 变化）：旧授权失效（代际不匹配视为无登记），
		// 未重新同意前保持只读。
		fp.identity.MachineID = "machine-reinstalled"
		res := runPiUnit(t, c, UnitPiMCP)
		if res.Outcome != UnitBlocked || res.Detail != "awaiting-consent" {
			t.Fatalf("new generation must require fresh consent, got %+v", res)
		}
		// 重装后的新 home 无旧文件；重新同意后首发布必须 Applied——若跨代继承
		// 了 gen1 的 lastWrittenHash 会误判 local-deletion-frozen（F3-S1 语义）。
		if err := os.Remove(piFile(fp, "mcp.json")); err != nil {
			t.Fatal(err)
		}
		if err := c.GrantUnitConsent(context.Background(), "Ubuntu", CLIPi, UnitPiMCP); err != nil {
			t.Fatal(err)
		}
		res = runPiUnit(t, c, UnitPiMCP)
		if res.Outcome != UnitApplied {
			t.Fatalf("post-reconsent first publish must apply (no inherited baseline), got %+v", res)
		}
	})
}

// ---------------------------------------------------------------------------
// F-1（S2 diting）：冻结事件不得摧毁键级登记——跨冻结周期健康键仍流动
// ---------------------------------------------------------------------------

func TestFrozenKeyKeepsRegistrationAcrossCycles(t *testing.T) {
	c, fp, src, _ := newS2Coordinator(t)
	base := `{
  "$schema": "./x.json",
  "profile": "tiered",
  "agents": {"baize": {"model": "amagi-glm/a"}, "diting": {"model": "amagi-glm/b"}},
  "compaction": {"model": "amagi-glm/c"}
}`
	src.amagi = []byte(base)
	src.providerIDs = []string{"amagi-glm"}
	src.mcp = []byte(`{}`)
	if runPiUnit(t, c, UnitPiAmagi).Outcome != UnitApplied {
		t.Fatal("seed failed")
	}

	// 手改一个键（agents/baize）→ 该键冻结（本轮整单元无可写键时的受阻登记
	// 也不得清空 Subs）。
	body := readPiFile(t, fp, "amagi.json")
	hand := strings.Replace(body, `"model": "amagi-glm/a"`, `"model": "kimi/hand"`, 1)
	os.WriteFile(piFile(fp, "amagi.json"), []byte(hand), 0o600)
	src.amagi = []byte(strings.Replace(base, `"model": "amagi-glm/b"`, `"model": "amagi-glm/b2"`, 1))
	res := runPiUnit(t, c, UnitPiAmagi)
	if res.Outcome != UnitApplied || !strings.HasPrefix(res.Detail, "partial") {
		t.Fatalf("healthy key update must apply alongside frozen key, got %+v", res)
	}
	after := readPiFile(t, fp, "amagi.json")
	if !strings.Contains(after, "kimi/hand") {
		t.Fatalf("frozen key must keep WSL hand edit")
	}
	if !strings.Contains(after, "amagi-glm/b2") {
		t.Fatalf("healthy key must follow source change across the freeze cycle:\n%s", after)
	}

	// 下一轮：健康键（compaction）来源删除 → 仍按「托管且未手改」移除
	//（登记存续的第二个可观察面）。
	src.amagi = []byte(`{
  "$schema": "./x.json",
  "profile": "tiered",
  "agents": {"baize": {"model": "amagi-glm/a"}, "diting": {"model": "amagi-glm/b"}}
}`)
	res = runPiUnit(t, c, UnitPiAmagi)
	if res.Outcome != UnitApplied {
		t.Fatalf("source-deletion round must apply, got %+v", res)
	}
	final := readPiFile(t, fp, "amagi.json")
	if strings.Contains(final, "amagi-glm/c") {
		t.Fatalf("unmodified managed key must be removed on source deletion (registration survived freeze):\n%s", final)
	}
	if !strings.Contains(final, "kimi/hand") {
		t.Fatalf("frozen hand-edited key must still be preserved:\n%s", final)
	}
	// 冻结原因不漂移为 unmanaged-existing。
	st := c.GetStatus("Ubuntu", CLIPi)
	for _, u := range st.Units {
		if u.Unit == UnitPiAmagi && u.Frozen && strings.Contains(u.FrozenReason, "unmanaged") {
			t.Fatalf("freeze reason drifted to unmanaged-existing: %s", u.FrozenReason)
		}
	}
}
