package wslsupply

// consistency_fixes_test.go — 一致性修正回归（G1 后独立审核中途观测转正）。
// 每条观测对应一个强期望断言：合法正向 + 失败/恢复反向。全部合成夹具
//（t.TempDir + fake probes），不读真实配置/凭据，不执行网络/SSH/真实 CLI。
// 观测来源：agent-outputs/workflow/wsl-goal-closure/attack-stage-review-*。

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"amagi-codebox/internal/launcher"
)

// cfWrite 写一个合成夹具文件（MkdirAll 父目录）。
func cfWrite(t *testing.T, path, content string, perm os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), perm); err != nil {
		t.Fatal(err)
	}
}

func cfExists(p string) bool { _, err := os.Lstat(p); return err == nil }

// ---------------------------------------------------------------------------
// 1. backup-path：chmod 目标与实际备份落盘路径同源
// ---------------------------------------------------------------------------

// TestConsistencyBackupPathSensitiveUpdateSucceeds：已存在文件的敏感更新必须
// 成功；持久前像落在 .amagi-staging/recovery-<opID>/before（真实路径），字节
// 等于旧内容，敏感 mode 600 在正确路径上可验证。此前 chmod 路径缺 recovery-
// 前缀导致正常更新误报 backup-unavailable。
func TestConsistencyBackupPathSensitiveUpdateSucceeds(t *testing.T) {
	oldBody := []byte(`{"user":"ORIGINAL"}`)
	e := newCommitTestEnv(t, true, oldBody)
	rcpt := e.c.commitFile(context.Background(), CommitRequest{
		Target: e.target, Unit: "pi/audit", RelPath: e.rel,
		ExpectedBefore: hexHash(oldBody), Candidate: []byte(`{"v":2}`),
		Sensitive: true, Kind: OpKindWrite,
	})
	if rcpt.Err != nil {
		t.Fatalf("sensitive update on existing file must succeed, got detail=%s err=%v", rcpt.ErrDetail, rcpt.Err)
	}
	if rcpt.BackupRel == "" {
		t.Fatal("preimage backup must be recorded")
	}
	want := ".amagi-staging/recovery-" + rcpt.OpID + "/before"
	if rcpt.BackupRel != want {
		t.Fatalf("backup rel must be %s (actual staging path), got %s", want, rcpt.BackupRel)
	}
	if rcpt.Recovery != OpRecoveryAvailable {
		t.Fatalf("verified preimage must be reported available, got %s", rcpt.Recovery)
	}
	// 成功提交后备份按终结顺序清理（不再断言在盘）；正确路径的证据是敏感
	// chmod/StatMode 落在真实备份路径上——fake 探测面按真实文件校验，错位
	// 路径（缺 recovery- 前缀）会 ENOENT 失败使提交失败，因此 mode 记录在
	// 正确路径上 == chmod 确实打在真实备份文件上。
	linuxBackup := strings.TrimSuffix(e.target.LinuxRoot, "/") + "/" + want
	e.fp.mu.Lock()
	mode, modeOK := e.fp.modes[linuxBackup]
	e.fp.mu.Unlock()
	if !modeOK || mode != "600" {
		t.Fatalf("sensitive chmod must target the real backup path %s, got mode=%q ok=%v", linuxBackup, mode, modeOK)
	}
	// 目标已是新内容（正向成功）。
	cur, _ := os.ReadFile(e.uncPath)
	if string(cur) != `{"v":2}` {
		t.Fatalf("target must hold candidate, got %q", cur)
	}
}

// ---------------------------------------------------------------------------
// 2. legacy-unknown-and-hash：指纹弱证据 × 内容正证据
// ---------------------------------------------------------------------------

// TestConsistencyLegacyUnknownFingerprintHashMatchKeepsRisk：当前文件内容与
// 历史记录 hash 一致但指纹 unknown/不匹配 → 待确认不得解除（unsafe+pending，
// reason=legacy-content-hash-match）；内容与指纹都不匹配的用户自有文件 →
// user-owned（不误阻）。B 目标（文件不在）不受 A 的线索影响（不全局误阻）。
func TestConsistencyLegacyUnknownFingerprintHashMatchKeepsRisk(t *testing.T) {
	for _, finger := range []string{GenerationUnknown, legacyGenerationOf("OTHER", "1000", "/home/u")} {
		e := newLegacyABEnv(t)
		e.writeLegacyV1TopB(t, "written", true)
		st := e.c.state.load()
		st.LegacyEvidence[0].Fingerprint = finger
		if err := e.c.state.mutate(func(s *StateFile) { s.LegacyEvidence = st.LegacyEvidence }); err != nil {
			t.Fatal(err)
		}
		cfWrite(t, e.visionFn("A"), legacyCloneCopy, 0o600)
		a := e.c.AssessWSLSensitiveConsumption(context.Background(), "A", CLICommon, UnitSharedVisionExport, nil)
		if a.Consumption != ConsumptionUnsafe || !a.RiskPending || a.Reason != "legacy-content-hash-match" {
			t.Fatalf("finger=%s: content-hash match must stay risk-pending (unsafe), got %+v", finger, a)
		}
		// B：文件不在 → 本目标放行（A 的内容线索不全局误阻其它发行版）。
		b := e.c.AssessWSLSensitiveConsumption(context.Background(), "B", CLICommon, UnitSharedVisionExport, nil)
		if b.Consumption != ConsumptionAbsent || b.RiskPending {
			t.Fatalf("finger=%s: B (copy absent) must stay released, got %+v", finger, b)
		}
		// 对照：内容与指纹都不匹配 → 用户自有文件，解除待确认。
		cfWrite(t, e.visionFn("A"), `{"user":"OWN"}`, 0o600)
		a2 := e.c.AssessWSLSensitiveConsumption(context.Background(), "A", CLICommon, UnitSharedVisionExport, nil)
		if a2.Consumption != ConsumptionUserOwned || a2.RiskPending {
			t.Fatalf("finger=%s: unrelated user file must be user-owned, got %+v", finger, a2)
		}
	}
}

// ---------------------------------------------------------------------------
// 3. v2-migration：真实 v2 生产形态迁移不遗忘受管记录
// ---------------------------------------------------------------------------

// TestConsistencyV2MigrationKeepsManagedRecords：真实 v2 生产形态（schema 2 +
// Targets 索引 + 新公式 generation 的受管 Units + v1 遗留 legacy: 隔离区 +
// LegacySensitiveRisk 派生 map）迁移到 v3：受管记录继续可归属（entryFor 命
// 中；副本在且 0600 → safe，不是 user-owned/never-managed）；v1 遗留隔离区转
// 证据保真。v1（schema 1）仍整体隔离。
func TestConsistencyV2MigrationKeepsManagedRecords(t *testing.T) {
	fp := newFakeProbes(t)
	dir := t.TempDir()
	c := NewCoordinator(dir, nil, fp.probes())
	tr, err := c.ResolveTarget(context.Background(), "Ubuntu", CLICommon)
	if err != nil {
		t.Fatal(err)
	}
	body := "SYNTHETIC V2 SECRET"
	cfWrite(t, filepath.Join(tr.UNCRoot, VisionExportFile), body, 0o600)
	// 副本权限经 fake 探测面的 mode 表显式表达（0600）：生产权限真值面是
	// distro 内 stat 探测；宿主 os.Stat 的 POSIX 位在 Windows 上不存在，
	// 不得作为夹具事实源（借用宿主位曾令 Windows 误报 perms-unverified）。
	fp.setMode(tr.LinuxRoot+"/"+VisionExportFile, "600")
	legacyGen := legacyGenerationOf("m-old", "1000", "/home/u")
	v2 := StateFile{
		SchemaVersion: 2, Generation: tr.Generation, Distro: "Ubuntu", Home: tr.Home,
		Targets: map[string]TargetRecord{tr.Generation: {Distro: "Ubuntu", UID: tr.UID, Home: tr.Home, LegacyGeneration: legacyGen}},
		Units: []StateEntry{
			// 受管视觉单元：新公式 generation（可归属），Consented + 已写。
			{Unit: UnitSharedVisionExport, Policy: PolicyManagedNamespace, Generation: tr.Generation,
				Consented: true, LastWrittenHash: hexHash([]byte(body)), LastOutcome: "written"},
			// v2 从 v1 升级时隔离的敏感 entry（legacy: 前缀）。
			{Unit: UnitSharedVisionExport, Policy: PolicyManagedNamespace, Generation: legacyGenerationPrefix + legacyGen,
				Consented: true, LastWrittenHash: hexHash([]byte("OLD-V1")), LastOutcome: "written"},
			// 普通受管单元同样不得被隔离。
			{Unit: UnitSharedModalityKB, Policy: PolicyManagedNamespace, Generation: tr.Generation, LastOutcome: "written"},
		},
		LegacySensitiveRisk: map[string]map[UnitID]bool{"Ubuntu": {UnitSharedVisionExport: true}},
	}
	b, err := json.Marshal(v2)
	if err != nil {
		t.Fatal(err)
	}
	cfWrite(t, filepath.Join(dir, StateFileName), string(b), 0o600)

	c2 := NewCoordinator(dir, nil, fp.probes())
	st := c2.state.load()
	// 受管记录仍在（未隔离、未遗忘）。
	if got := st.entryFor(UnitSharedVisionExport, tr.Generation); got.LastWrittenHash != hexHash([]byte(body)) || !got.Consented {
		t.Fatalf("v2 managed entry must stay attributable after migration, got %+v", got)
	}
	if got := st.entryFor(UnitSharedModalityKB, tr.Generation); got.LastOutcome != "written" {
		t.Fatalf("v2 non-sensitive managed entry must stay attributable, got %+v", got)
	}
	if strings.HasPrefix(st.entryFor(UnitSharedVisionExport, tr.Generation).Generation, legacyGenerationPrefix) {
		t.Fatal("attributable v2 entry must not be quarantined as legacy")
	}
	// 副本在且 0600 → safe（此前误判 user-owned/unrelated-file-user-owned 或
	// never-managed）。
	a := c2.AssessWSLSensitiveConsumption(context.Background(), "Ubuntu", CLICommon, UnitSharedVisionExport, nil)
	if a.Consumption != ConsumptionSafe || a.RiskPending {
		t.Fatalf("v2 migrated managed copy (present, 0600) must be safe, got %+v", a)
	}
	// v1 遗留隔离区 + v2 派生风险 map 都转证据保真。
	ev := c2.LegacyEvidenceSnapshot()
	var v1Keep, v2Derived bool
	for _, e := range ev {
		if e.Source == "v1" && e.Fingerprint == legacyGen {
			v1Keep = true
		}
		if e.Source == "v2-derived" && e.LowTrust && e.TopDistro == "Ubuntu" {
			v2Derived = true
		}
	}
	if !v1Keep || !v2Derived {
		t.Fatalf("legacy quarantine evidence must be preserved (v1=%v v2derived=%v): %+v", v1Keep, v2Derived, ev)
	}
}

// TestConsistencyV1MigrationStillQuarantines：v1（schema 1）不受 v2 修正影响
// ——整体隔离 + 证据保真（既有语义回归钉）。
func TestConsistencyV1MigrationStillQuarantines(t *testing.T) {
	e := newLegacyABEnv(t)
	e.writeLegacyV1TopB(t, "written", true)
	st := e.c.state.load()
	if st.SchemaVersion != stateSchemaVersion {
		t.Fatalf("migration must bump schema, got %d", st.SchemaVersion)
	}
	for _, u := range st.Units {
		if u.Unit == UnitSharedVisionExport && !strings.HasPrefix(u.Generation, legacyGenerationPrefix) {
			t.Fatalf("v1 entry must stay quarantined, got %+v", u)
		}
	}
	if len(st.LegacyEvidence) == 0 {
		t.Fatal("v1 evidence must exist")
	}
}

// ---------------------------------------------------------------------------
// 4. wrong-root-reconcile：shared intent 不得被 pi 目标根解释
// ---------------------------------------------------------------------------

// TestConsistencyWrongRootReconcileKeepsSharedPending：shared 与 pi 的
// targetKey 相同身份基础（同 distro/home）——pi TargetRef 的 reconcile 不得把
// shared 的未决 intent 判 not-applied 并清未决（文件仍在 shared 根）；shared
// 自己的 reconcile 正常按落点收敛（committed）。新 Coordinator 同样不误清。
func TestConsistencyWrongRootReconcileKeepsSharedPending(t *testing.T) {
	e := newCommitTestEnv(t, false, nil) // pi 目标
	shared, err := e.c.ResolveTarget(context.Background(), "Ubuntu", CLICommon)
	if err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(shared.UNCRoot, VisionExportFile)
	cfWrite(t, secret, "SYNTHETIC-SECRET", 0o600)
	id := newOpID()
	intent := OperationIntent{
		OpID: id, Kind: OpKindWrite, TargetKey: targetKeyOf(shared),
		TargetDistro: "Ubuntu", TargetHome: shared.Home, TargetCLI: string(CLICommon),
		Unit: UnitSharedVisionExport, RelPath: VisionExportFile,
		AfterHash: hexHash([]byte("SYNTHETIC-SECRET")),
	}
	if err := e.c.state.saveIntent(intent); err != nil {
		t.Fatal(err)
	}
	// pi 的 reconcile：不属本作用域 → 不处置（不猜 absent、不清未决）。
	c2 := NewCoordinator(filepath.Dir(e.c.state.path), nil, e.fp.probes())
	rep := c2.ReconcilePending(context.Background(), e.target)
	for _, d := range rep.Dispositions {
		if d.OpID == id {
			t.Fatalf("pi reconcile must not dispose shared intent, got %+v", d)
		}
	}
	if _, pending := c2.PendingSensitiveOperation("Ubuntu", UnitSharedVisionExport); !pending {
		t.Fatal("shared pending operation must stay visible after pi reconcile")
	}
	if _, ok, _ := c2.state.readIntent(id); !ok {
		t.Fatal("shared intent must be retained after pi reconcile")
	}
	if data, err := os.ReadFile(secret); err != nil || string(data) != "SYNTHETIC-SECRET" {
		t.Fatalf("secret must remain in shared root, got %q err=%v", data, err)
	}
	// shared 的 assessment：文件在 + 内容匹配 after → 不能报 absent。
	a := c2.AssessSensitiveConsumption(context.Background(), shared, UnitSharedVisionExport, nil)
	if a.Consumption == ConsumptionAbsent {
		t.Fatalf("shared assessment must not report absent while secret present, got %+v", a)
	}
	// shared 自己的 reconcile 正常收敛：target == after → committed（正向对照）。
	repShared := c2.ReconcilePending(context.Background(), shared)
	found := false
	for _, d := range repShared.Dispositions {
		if d.OpID == id && d.Disposition == ReconcileCommitted {
			found = true
		}
	}
	if !found {
		t.Fatalf("shared reconcile must settle its own intent as committed, got %+v", repShared.Dispositions)
	}
}

// ---------------------------------------------------------------------------
// 5. dir-cas-and-structure：不可逆移动前对构建基准复验
// ---------------------------------------------------------------------------

// cfSkillEnv 构造一个已同意的技能单元环境（host 源 demo/SKILL.md=# NEW；目标
// 已有 # OLD），返回协调器/探测/单元/目标目录。
func cfSkillEnv(t *testing.T) (*Coordinator, *fakeProbes, UnitID, string) {
	t.Helper()
	fp := newFakeProbes(t)
	c := newCoordinatorWith(t, fp, nil)
	host := t.TempDir()
	cfWrite(t, filepath.Join(host, "demo", "SKILL.md"), "# NEW", 0o644)
	scope := NewSkillsScope(host, DefaultSkillsScanLimits())
	scope.Scan()
	c.RegisterSkills(scope)
	u := SkillUnitID("demo")
	if err := c.GrantUnitConsent(context.Background(), "Ubuntu", CLICommon, u); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(fp.uncRoot, "home/u/.agents/skills/demo")
	cfWrite(t, filepath.Join(dst, "SKILL.md"), "# OLD", 0o644)
	return c, fp, u, dst
}

func cfTakeOver(t *testing.T, c *Coordinator, u UnitID, ctx context.Context) UnitResult {
	t.Helper()
	p, err := c.PreviewUnits(context.Background(), "Ubuntu", CLICommon, []UnitID{u})
	if err != nil {
		t.Fatal(err)
	}
	r, err := c.TakeOverUnit(ctx, "Ubuntu", CLICommon, u, p[0].TargetHash)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// TestConsistencyDirCASExternalEditDuringStagingRetained：candidate staging
// 后、move-aside 前注入外部文件 → 提交必须失败（target-changed-before-move），
// 外部文件在原路径保留，不得 applied 且外部文件消失（原缺陷：外部文件被搬走
// 的旧树带走并消失）。
func TestConsistencyDirCASExternalEditDuringStagingRetained(t *testing.T) {
	c, _, u, dst := cfSkillEnv(t)
	edited := false
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := boundaryCtx{Context: base, cancel: cancel, when: func() bool {
		// staging 已建且目标仍在（第一次 ctx 检查点=move 前）→ 外部编辑落进目标树。
		ps, _ := filepath.Glob(filepath.Join(filepath.Dir(dst), ".amagi-supply-skill-*"))
		if !edited && len(ps) > 0 && cfExists(dst) {
			edited = true
			cfWrite(t, filepath.Join(dst, "EXTERNAL"), "UNIQUE EDIT", 0o644)
		}
		return false
	}}
	r := cfTakeOver(t, c, u, ctx)
	if !edited {
		t.Fatal("fixture must inject the external edit before move-aside")
	}
	if r.Outcome == UnitApplied {
		t.Fatalf("commit must fail when target changed during staging (detail=%s)", r.Detail)
	}
	if !cfExists(filepath.Join(dst, "EXTERNAL")) {
		t.Fatal("external edit must be retained at the target dir (not moved away/destroyed)")
	}
	if data, err := os.ReadFile(filepath.Join(dst, "EXTERNAL")); err != nil || string(data) != "UNIQUE EDIT" {
		t.Fatalf("external bytes must be intact, got %q err=%v", data, err)
	}
	if data, err := os.ReadFile(filepath.Join(dst, "SKILL.md")); err != nil || string(data) != "# OLD" {
		t.Fatalf("original tree must be intact, got %q err=%v", data, err)
	}
	if r.Undo != nil {
		t.Fatal("failed dir commit must not carry undo")
	}
}

// TestConsistencyDirCASExternalNewTargetDuringPublishKeepsBoth：move-aside 后、
// no-replace 发布前外部在原路径新建目录 → publish-conflict：两处保留（外部版
// 本 + prev 旧树），不覆盖。
func TestConsistencyDirCASExternalNewTargetDuringPublishKeepsBoth(t *testing.T) {
	c, _, u, dst := cfSkillEnv(t)
	injected := false
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := boundaryCtx{Context: base, cancel: cancel, when: func() bool {
		// prev 已出现且目标已被移走（第二次 ctx 检查点=发布前）→ 外部新建目标。
		ps, _ := filepath.Glob(filepath.Join(filepath.Dir(dst), ".amagi-supply-prev-*"))
		if !injected && len(ps) > 0 && !cfExists(dst) {
			injected = true
			cfWrite(t, filepath.Join(dst, "EXT", "keep.txt"), "EXTERNAL TREE", 0o644)
		}
		return false
	}}
	r := cfTakeOver(t, c, u, ctx)
	if !injected {
		t.Fatal("fixture must inject the external target before publish")
	}
	if r.Outcome == UnitApplied {
		t.Fatalf("publish over external target must conflict, got %+v", r)
	}
	if !cfExists(filepath.Join(dst, "EXT", "keep.txt")) {
		t.Fatal("external new target must be retained (never overwritten)")
	}
	// prev（旧树）也必须保留（两处）。
	ps, _ := filepath.Glob(filepath.Join(filepath.Dir(dst), ".amagi-supply-prev-*"))
	kept := false
	for _, p := range ps {
		if data, err := os.ReadFile(filepath.Join(p, "SKILL.md")); err == nil && string(data) == "# OLD" {
			kept = true
		}
	}
	if !kept {
		t.Fatal("prev preimage must be retained alongside the external target")
	}
}

// ---------------------------------------------------------------------------
// 6. 目录 Abort 不丢空目录/嵌套结构
// ---------------------------------------------------------------------------

// TestConsistencyDirAbortPreservesFullTreeStructure：目录补偿必须保全完整树
// 结构——空目录/嵌套目录与文件字节一并恢复（不只 SKILL.md 字节）。
func TestConsistencyDirAbortPreservesFullTreeStructure(t *testing.T) {
	c, _, u, dst := cfSkillEnv(t)
	if err := os.MkdirAll(filepath.Join(dst, "empty", "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dst, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfWrite(t, filepath.Join(dst, "sub", "deep.txt"), "DEEP", 0o644)
	r := cfTakeOver(t, c, u, context.Background())
	if r.Undo == nil {
		t.Fatalf("takeover must commit with undo, got %+v", r)
	}
	out := c.Compensate(context.Background(), r.Undo)
	if len(out) != 1 || out[0].Disposition != CompensationConfirmed {
		t.Fatalf("dir abort must confirm, got %+v", out)
	}
	if !cfExists(filepath.Join(dst, "empty", "nested")) {
		t.Fatal("empty/nested dirs must survive dir abort")
	}
	if !cfExists(filepath.Join(dst, "sub", "deep.txt")) {
		t.Fatal("nested file must survive dir abort")
	}
	if data, err := os.ReadFile(filepath.Join(dst, "SKILL.md")); err != nil || string(data) != "# OLD" {
		t.Fatalf("original bytes must be restored, got %q err=%v", data, err)
	}
}

// ---------------------------------------------------------------------------
// 7. resource-abort：资源目录统一持久前像/恢复生命周期
// ---------------------------------------------------------------------------

// TestConsistencyResourceAbortRestoresFromPersistedPrev：资源目录更新后
// Compensate 必须可用真实持久 prev 树恢复旧内容（此前 unavailable 且 prev 已
// 被删）。恢复后 prev 材料按终结顺序清理。
func TestConsistencyResourceAbortRestoresFromPersistedPrev(t *testing.T) {
	c, fp, host := newS4ResourceCoordinator(t)
	writeHostResource(t, host, filepath.Join("commands", "demo", "a.md"), "NEW")
	c.resourceScope(CLIClaude).Scan()
	u := ResourceUnitID(CLIClaude, "commands/demo")
	if err := c.GrantUnitConsent(context.Background(), "Ubuntu", CLIClaude, u); err != nil {
		t.Fatal(err)
	}
	dst := claudeTargetPath(fp, filepath.Join("commands", "demo"))
	cfWrite(t, filepath.Join(dst, "a.md"), "OLD", 0o644)
	pv, err := c.PreviewUnits(context.Background(), "Ubuntu", CLIClaude, []UnitID{u})
	if err != nil {
		t.Fatal(err)
	}
	r, err := c.TakeOverUnit(context.Background(), "Ubuntu", CLIClaude, u, pv[0].TargetHash)
	if err != nil || r.Undo == nil {
		t.Fatalf("setup: %+v %v", r, err)
	}
	if !cfExists(absOf(r.Undo.target, r.Undo.displacedRel)) {
		t.Fatal("persistent prev tree must exist after resource dir commit")
	}
	out := c.Compensate(context.Background(), r.Undo)
	if len(out) != 1 || out[0].Disposition != CompensationConfirmed {
		t.Fatalf("resource abort must restore from persisted prev, got %+v", out)
	}
	data, err := os.ReadFile(filepath.Join(dst, "a.md"))
	if err != nil || string(data) != "OLD" {
		t.Fatalf("old content must be restored, got %q err=%v", data, err)
	}
	if cfExists(absOf(r.Undo.target, r.Undo.displacedRel)) {
		t.Fatal("prev material must be cleaned after successful compensation")
	}
}

// TestConsistencyResourceAbortLargeUserTree：接管大目录（内存快照超限=不可
// 用）时补偿仍可用持久 prev 树保全用户内容（不得仅依赖内存快照）。
func TestConsistencyResourceAbortLargeUserTree(t *testing.T) {
	c, fp, host := newS4ResourceCoordinator(t)
	writeHostResource(t, host, filepath.Join("commands", "big", "SKILL.md"), "NEW")
	c.resourceScope(CLIClaude).Scan()
	u := ResourceUnitID(CLIClaude, "commands/big")
	if err := c.GrantUnitConsent(context.Background(), "Ubuntu", CLIClaude, u); err != nil {
		t.Fatal(err)
	}
	dst := claudeTargetPath(fp, filepath.Join("commands", "big"))
	// 用户大树：两份 4.5MB（> 8MB 前像快照封顶；候选源仍小）。
	big := strings.Repeat("u", 4500000)
	cfWrite(t, filepath.Join(dst, "p1.bin"), big, 0o644)
	cfWrite(t, filepath.Join(dst, "p2.bin"), big, 0o644)
	pv, err := c.PreviewUnits(context.Background(), "Ubuntu", CLIClaude, []UnitID{u})
	if err != nil {
		t.Fatal(err)
	}
	r, err := c.TakeOverUnit(context.Background(), "Ubuntu", CLIClaude, u, pv[0].TargetHash)
	if err != nil || r.Undo == nil {
		t.Fatalf("setup: %+v %v", r, err)
	}
	if r.Undo.prevSnapshot {
		t.Fatal("fixture must exceed the in-memory snapshot cap")
	}
	out := c.Compensate(context.Background(), r.Undo)
	if len(out) != 1 || out[0].Disposition != CompensationConfirmed {
		t.Fatalf("large-tree abort must restore from persisted prev (not unavailable), got %+v", out)
	}
	for _, name := range []string{"p1.bin", "p2.bin"} {
		data, rerr := os.ReadFile(filepath.Join(dst, name))
		if rerr != nil || len(data) != len(big) || !strings.HasPrefix(string(data[:64]), "u") {
			t.Fatalf("user tree %s must be restored intact, err=%v", name, rerr)
		}
	}
}

// ---------------------------------------------------------------------------
// 8. directory-restart：按 Kind 区分文件/树验证与恢复
// ---------------------------------------------------------------------------

// TestConsistencyDirectoryRestartReconcileConverges：已提交目录重建
// Coordinator 后 reconcile 必须收敛（此前通用文件 ReadFile 读目录恒 EISDIR →
// commit-unconfirmed 永久卡死）。三态：settled → cleanup-residual（prev 材料
// 收敛清理）；未 settled 且树==after → committed（补基准）；树==基准/后改 →
// restored/conflicted（不清材料、不猜 absent）。
func TestConsistencyDirectoryRestartReconcileConverges(t *testing.T) {
	cfgDir := t.TempDir()
	fp := &fakeProbes{uncRoot: filepath.Join(cfgDir, "target"), identity: platformUserIdentityFixture()}
	probes := fp.probes()
	c := NewCoordinator(cfgDir, nil, probes)
	host := t.TempDir()
	cfWrite(t, filepath.Join(host, "demo", "SKILL.md"), "# NEW", 0o644)
	scope := NewSkillsScope(host, DefaultSkillsScanLimits())
	scope.Scan()
	c.RegisterSkills(scope)
	u := SkillUnitID("demo")
	if err := c.GrantUnitConsent(context.Background(), "Ubuntu", CLICommon, u); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(fp.uncRoot, "home/u/.agents/skills/demo")
	cfWrite(t, filepath.Join(dst, "SKILL.md"), "# OLD", 0o644)
	r := cfTakeOver(t, c, u, context.Background())
	if r.Outcome != UnitApplied {
		t.Fatalf("setup: %+v", r)
	}
	tr, err := c.ResolveTarget(context.Background(), "Ubuntu", CLICommon)
	if err != nil {
		t.Fatal(err)
	}
	// 语义 1：进程退出（租约消失）后重启——settled 在、intent 在 → 收敛清理
	// 材料（prev 树删除），不再 commit-unconfirmed。
	c2 := NewCoordinator(cfgDir, nil, probes)
	rep := c2.ReconcilePending(context.Background(), tr)
	ok := false
	for _, d := range rep.Dispositions {
		if d.OpID == r.OperationID {
			if d.Disposition != ReconcileCleanupResent {
				t.Fatalf("restart reconcile of settled dir op must converge to cleanup, got %+v", d)
			}
			ok = true
		}
	}
	if !ok {
		t.Fatalf("restart reconcile must process the dir op, got %+v", rep.Dispositions)
	}
	if _, still, _ := c2.state.readIntent(r.OperationID); still {
		t.Fatal("settled intent must be cleaned by restart reconcile")
	}
	ps, _ := filepath.Glob(filepath.Join(filepath.Dir(dst), ".amagi-supply-prev-*"))
	if len(ps) != 0 {
		t.Fatalf("prev material must be cleaned by restart reconcile, got %v", ps)
	}

	// 语义 2：committed-unregistered（intent 在、settled 不在、树==after）→
	// 补登记基准。
	c3 := NewCoordinator(cfgDir, nil, probes)
	treeHash := scanTargetSkillDir(dst, DefaultSkillsScanLimits()).TreeHash
	intent := OperationIntent{
		OpID: newOpID(), Kind: OpKindDirSwap, TargetKey: targetKeyOf(tr),
		TargetDistro: "Ubuntu", TargetHome: tr.Home, TargetCLI: string(CLICommon),
		Unit: u, RelPath: "skills/demo", BeforeExists: true, AfterHash: treeHash,
	}
	if err := c3.state.saveIntent(intent); err != nil {
		t.Fatal(err)
	}
	rep3 := c3.ReconcilePending(context.Background(), tr)
	ok3 := false
	for _, d := range rep3.Dispositions {
		if d.OpID == intent.OpID {
			if d.Disposition != ReconcileCommitted {
				t.Fatalf("dir tree==after must reconcile to committed, got %+v", d)
			}
			ok3 = true
		}
	}
	if !ok3 {
		t.Fatalf("reconcile must process dir intent, got %+v", rep3.Dispositions)
	}
	st := c3.state.load()
	if e := st.entryFor(u, tr.Generation); e.LastWrittenHash != intent.AfterHash {
		t.Fatalf("reconcile must register the dir tree baseline, got %+v", e)
	}
}

// TestConsistencyDirReconcileMismatchConflicted：dir intent 树后改（既非 after
// 也非 before）→ conflicted（保留材料；不得当 not-applied 清掉）。
func TestConsistencyDirReconcileMismatchConflicted(t *testing.T) {
	cfgDir := t.TempDir()
	fp := &fakeProbes{uncRoot: filepath.Join(cfgDir, "target"), identity: platformUserIdentityFixture()}
	probes := fp.probes()
	c := NewCoordinator(cfgDir, nil, probes)
	tr, err := c.ResolveTarget(context.Background(), "Ubuntu", CLICommon)
	if err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(tr.UNCRoot, "skills", "demo")
	cfWrite(t, filepath.Join(dst, "SKILL.md"), "# STRANGER", 0o644)
	id := newOpID()
	intent := OperationIntent{
		OpID: id, Kind: OpKindDirSwap, TargetKey: targetKeyOf(tr),
		TargetDistro: "Ubuntu", TargetHome: tr.Home, TargetCLI: string(CLICommon),
		Unit: SkillUnitID("demo"), RelPath: "skills/demo", BeforeExists: true,
		AfterHash: hexHash([]byte("not-the-tree")), PrevTreeHash: hexHash([]byte("not-the-either")),
	}
	if err := c.state.saveIntent(intent); err != nil {
		t.Fatal(err)
	}
	rep := c.ReconcilePending(context.Background(), tr)
	found := false
	for _, d := range rep.Dispositions {
		if d.OpID == id {
			found = true
			if d.Disposition != ReconcileConflicted {
				t.Fatalf("changed tree must conflict, got %+v", d)
			}
		}
	}
	if !found {
		t.Fatalf("reconcile must inspect the dir intent, got %+v", rep.Dispositions)
	}
	if _, still, _ := c.state.readIntent(id); !still {
		t.Fatal("conflicted dir intent materials must be retained")
	}
}

// ---------------------------------------------------------------------------
// 9. corrupt-pending：健康门先完成本次扫描再决策
// ---------------------------------------------------------------------------

// TestConsistencyCorruptPendingBlocksFirstRun：首轮 RunUnits 在 pending 目录
// 有损坏 json 时必须阻断（store-unhealthy），不 applied；损坏材料保留；健康
// 路径（无损坏）继续工作；移除损坏文件后恢复。
func TestConsistencyCorruptPendingBlocksFirstRun(t *testing.T) {
	e := newCommitTestEnv(t, false, nil)
	adapter := &reviewAdapter{rel: e.rel, source: func() []byte { return []byte("NEW") }}
	e.c.RegisterProjection(adapter)
	broken := filepath.Join(filepath.Dir(e.c.state.path), pendingDirName, "broken.json")
	cfWrite(t, broken, "{broken", 0o600)
	res, err := e.c.RunUnits(context.Background(), "Ubuntu", CLIPi, ReasonManual, []UnitID{adapter.Unit()})
	if err != nil {
		t.Fatal(err)
	}
	if res.Results[0].Outcome != UnitBlocked || res.Results[0].Detail != "store-unhealthy" {
		t.Fatalf("corrupt pending must block the first run, got %+v", res.Results[0])
	}
	if _, rerr := os.ReadFile(e.uncPath); !os.IsNotExist(rerr) {
		t.Fatal("corrupt pending must prevent new writes (target untouched)")
	}
	if _, serr := os.Stat(broken); serr != nil {
		t.Fatal("corrupt material must be kept in place (never auto-deleted)")
	}
	if h := e.c.state.Health(); h.Pending != StoreCorrupt {
		t.Fatalf("health must reflect this run's scan, got %+v", h)
	}
	// 修复存储后同协调器恢复（健康/fresh 正确路径继续工作）。
	if err := os.Remove(broken); err != nil {
		t.Fatal(err)
	}
	res2, err := e.c.RunUnits(context.Background(), "Ubuntu", CLIPi, ReasonManual, []UnitID{adapter.Unit()})
	if err != nil {
		t.Fatal(err)
	}
	if res2.Results[0].Outcome != UnitApplied {
		t.Fatalf("healthy pending path must keep working after repair, got %+v", res2.Results[0])
	}
	if data, _ := os.ReadFile(e.uncPath); string(data) != "NEW" {
		t.Fatalf("repaired run must write the candidate, got %q", data)
	}
}

// ---------------------------------------------------------------------------
// 10. plugin-cross-source-cleanup：只结清同一 source+anchor 责任
// ---------------------------------------------------------------------------

// TestConsistencyPluginCrossSourcePendingKept：A 源未决记录不被 B 源成功清掉；
// 同一插件身份（同包不同 ref）的旧未决才被成功收敛。
func TestConsistencyPluginCrossSourcePendingKept(t *testing.T) {
	d := newFakePluginDistro(t)
	c, target := newPluginTestCoordinator(t, d)
	idA := newOpID()
	if err := c.plugins.savePluginPending(target, idA, PluginOperation{Kind: PluginOpInstall, Source: "npm:UNCONFIRMED-A@1"}, PluginSourceSpec{Type: "npm", Name: "UNCONFIRMED-A", Ref: "1"}); err != nil {
		t.Fatal(err)
	}
	r := c.RunPluginOperation(context.Background(), "Ubuntu", PluginOperation{Kind: PluginOpInstall, Source: "npm:demo@1.0.0", AnchorRef: "1.0.0"})
	if !r.OK {
		t.Fatalf("B-source install must succeed: %+v", r)
	}
	if _, still, err := c.state.readIntent(idA); err != nil || !still {
		t.Fatalf("A-source pending must survive B-source success, still=%v err=%v", still, err)
	}
	// 同身份旧未决（先前失败的同包操作）被本次成功收敛。
	idOld := newOpID()
	if err := c.plugins.savePluginPending(target, idOld, PluginOperation{Kind: PluginOpInstall, Source: "npm:demo@0.9.0"}, PluginSourceSpec{Type: "npm", Name: "demo", Ref: "0.9.0"}); err != nil {
		t.Fatal(err)
	}
	r2 := c.RunPluginOperation(context.Background(), "Ubuntu", PluginOperation{Kind: PluginOpInstall, Source: "npm:demo@1.0.0", AnchorRef: "1.0.0"})
	if !r2.OK {
		t.Fatalf("same-identity reinstall must succeed: %+v", r2)
	}
	if _, still, _ := c.state.readIntent(idOld); still {
		t.Fatal("same-identity old pending must be converged by success")
	}
	if _, still, _ := c.state.readIntent(idA); !still {
		t.Fatal("cross-source pending must still be visible")
	}
}

// ---------------------------------------------------------------------------
// 11. a2-final-save：结果、observation、状态页与磁盘摘要同事实
// ---------------------------------------------------------------------------

type consistencyA2Sabotage struct{ c *Coordinator }

func (a consistencyA2Sabotage) Files() []string { return []string{"auth.json"} }
func (a consistencyA2Sabotage) Sync(ctx context.Context, tr TargetRef) (launcher.SyncWSLPiLoginAuthOutcome, error) {
	// 同步成功后破坏 state 保存（持久失败：路径换成目录）。
	if err := os.Remove(a.c.state.path); err != nil && !os.IsNotExist(err) {
		return launcher.SyncWSLPiLoginAuthDistroNoAuth, err
	}
	if err := os.Mkdir(a.c.state.path, 0o700); err != nil {
		return launcher.SyncWSLPiLoginAuthDistroNoAuth, err
	}
	return launcher.SyncWSLPiLoginAuthCopied, nil
}

// TestConsistencyA2FinalSaveFailureReportsUnconfirmed：state save 持续失败 →
// 内容事实保留（applied，token 不回滚），但 Registration=pending（不得
// confirmed）、PendingOperationFact=true、observation 记 a2-state-unpersisted、
// pending 摘要 intent 在盘（状态页可见）。摘要无 auth 正文/hash。
func TestConsistencyA2FinalSaveFailureReportsUnconfirmed(t *testing.T) {
	fp := newFakeProbes(t)
	c := newCoordinatorWith(t, fp, nil)
	c.SetA2Adapter(consistencyA2Sabotage{c: c})
	res, err := c.RunUnits(context.Background(), "Ubuntu", CLIPi, ReasonLaunch, []UnitID{UnitPiA2Auth})
	if err != nil {
		t.Fatal(err)
	}
	r := res.Results[0]
	if r.Outcome != UnitApplied {
		t.Fatalf("content fact must stay applied (token not rolled back), got %+v", r)
	}
	if r.Registration != OpRegistrationPending {
		t.Fatalf("registration must be pending when state save fails, got %q", r.Registration)
	}
	if r.Detail != "a2-state-unpersisted" {
		t.Fatalf("detail must be a2-state-unpersisted, got %q", r.Detail)
	}
	if !r.PendingOperationFact() {
		t.Fatal("result must carry the pending-operation fact (dirty must not count success)")
	}
	if ob, ok := c.observations.forOp(r.OperationID); !ok || ob.ErrorCode != "a2-state-unpersisted" {
		t.Fatalf("observation must record the unpersisted fact, got %+v ok=%v", ob, ok)
	}
	// 磁盘摘要（pending intent）在盘且无秘密：重建 Coordinator 的状态页可见。
	cfgDir := filepath.Dir(c.state.path)
	if err := os.RemoveAll(c.state.path); err != nil { // 恢复存储可读
		t.Fatal(err)
	}
	c2 := NewCoordinator(cfgDir, nil, fp.probes())
	s := c2.GetStatus("Ubuntu", CLIPi)
	found := false
	for _, pi := range s.PendingIntents {
		if pi.Kind == OpKindA2Sync {
			found = true
		}
	}
	if !found {
		t.Fatalf("pending a2 summary must be visible in status (no write needed first), got %+v", s.PendingIntents)
	}
	if _, still, _ := c2.state.readIntent(r.OperationID); !still {
		t.Fatal("a2 summary intent must survive on disk")
	}
	data, rerr := os.ReadFile(pendingIntentPath(cfgDir, r.OperationID))
	if rerr != nil || strings.Contains(string(data), "sk-") || strings.Contains(string(data), "beforeHash") {
		t.Fatalf("a2 summary must carry no secret material, err=%v", rerr)
	}
}

// ---------------------------------------------------------------------------
// 12. status-without-target：只读状态查询必须展示待确认事实与健康错误
// ---------------------------------------------------------------------------

// TestConsistencyStatusWithoutTargetShowsPendingAndHealth：新 Coordinator 仅
// 有 pending、尚无 TargetRecord 时 GetStatus 可以未检查（Checked=false），但
// 不得丢失 PendingIntents/StoreHealth——read-only 查询不靠先写一次才看见风险。
func TestConsistencyStatusWithoutTargetShowsPendingAndHealth(t *testing.T) {
	fp := newFakeProbes(t)
	dir := t.TempDir()
	c := NewCoordinator(dir, nil, fp.probes())
	tr, err := c.ResolveTarget(context.Background(), "Ubuntu", CLICommon)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.saveA2PendingSummary(tr, newOpID()); err != nil {
		t.Fatal(err)
	}
	c2 := NewCoordinator(dir, nil, fp.probes())
	s := c2.GetStatus("Ubuntu", CLICommon)
	if s.Checked {
		t.Fatal("fresh target without TargetRecord may report unchecked")
	}
	if len(s.PendingIntents) != 1 || s.PendingIntents[0].Kind != OpKindA2Sync {
		t.Fatalf("pending intents must be visible without a prior write, got %+v", s.PendingIntents)
	}
	if s.StoreHealth.Main == "" || s.StoreHealth.Pending == "" {
		t.Fatalf("store health must be populated even without target record, got %+v", s.StoreHealth)
	}
	// 健康错误同样可见：主清单损坏时状态页必须展示（不得静默）。
	cfWrite(t, filepath.Join(dir, StateFileName), "{corrupt", 0o600)
	c3 := NewCoordinator(dir, nil, fp.probes())
	s3 := c3.GetStatus("Ubuntu", CLICommon)
	if s3.StoreHealth.Main != StoreCorrupt {
		t.Fatalf("corrupt main store must surface in status, got %+v", s3.StoreHealth)
	}
	if len(s3.PendingIntents) != 1 {
		t.Fatalf("pending intents must stay visible alongside health errors, got %+v", s3.PendingIntents)
	}
}
