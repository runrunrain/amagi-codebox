# Windows 真机 wslsupply 剩余 5 失败：逐条判定与修复报告

- 执行者：鲁班（worktree `amagi/wsl-goal-winfix`）
- 基线：主树 `58e7577` + 未提交 diff（`git diff HEAD` shasum `570eef071d615fc6ac018b6edf571cbe95ecbb79`）+ 全部未跟踪文件；起步已核对 worktree 与主树逐字节一致（status 列表相同、逐文件 `cmp` 通过；唯一目录差异是核对窗口内主树新出现的 `windows-wslsupply-real-e2e-2.log`，已同步）。
- 产物：`agent-outputs/workflow/wsl-goal-closure/windows-wslsupply-fixes.delta.patch`（相对当前主树的净增量，主树 `git apply --check` 已过，exit 0）。
- 结论先行：**1 条产品缺陷（A，扫描器分类），4 条测试夹具假设（B）**；未引入任何 skip/放宽断言/删测试；唯一一处 GOOS 分支在夹具内（第 3 条，逐条论证见下）。未动 `internal/platform`（API 冻结面无需变更）。

---

## 1. TestAttackA2SummaryWriteFailureBlocksSync — 判定 B（夹具）

**证据链**
- 夹具（主树 a2_consumption_test.go 旧 103–113 行）：对 pending 目录 `os.Chmod(dir, 0o500)` 注入「摘要写失败」。Windows 宿主上目录 chmod 不阻止写入（NTFS 无 POSIX dir exec/write 位语义）→ `os.CreateTemp` 成功 → saveIntent 成功 → Sync 照常执行 → 断言失败（outcome=applied）。
- 生产失败路径本身 OS 无关：coordinator.go:448–457（`saveA2PendingSummary` 失败 → `UnitFailed`/`prepare-state-failed`、不调 `Sync`）；a2.go `saveA2PendingSummary` → state.go/intent_store.go `saveIntent`（MkdirAll/CreateTemp/EnsurePrivateFile/rename，任一失败即返回错误）。

**修复**
- a2.go：`saveA2PendingSummary` 入口加 `c.faults.fail(stageIntent)` 确定性故障接缝（与 commitFile 既有 stageIntent 同族；生产 `faults` 恒空，行为零变化）。
- 夹具改为 `c.faults.add(stageIntent, 0, errors.New("a2 summary persist fault"))`：pending 存储面保持可读可扫描（健康门不触发），saveIntent 恒失败——与旧注释声明的隔离语义完全一致，且跨平台确定。

**回归**：断言原样保留（`UnitFailed` + `prepare-state-failed` + `calls==0`）；macOS 上 PASS（见验证）。

## 2. TestConsistencyV2MigrationKeepsManagedRecords — 判定 B（夹具）

**证据链**
- 生产：legacy_assess.go:96–102——副本在且 `probes.StatMode` 报 `600` → safe；否则 `managed-copy-perms-unverified` + RiskPending（fail-closed，探测失败不放行）。StatMode 生产实现：probes.go:55–66（契约「distro 内八进制位；UNC/宿主位不可信」）→ platform：`wsl_context_stat_windows.go`（wsl.exe 内 stat）/ `wsl_context_stat_nonwindows.go`（返回 ok=false）。生产任何位置不用宿主 `os.Stat().Mode().Perm()` 推断 distro 权限。
- 夹具：coordinator_test.go `fakeProbes.StatMode` 的回退分支读宿主 `os.Stat().Perm()`。Windows 宿主恒 0666 → 评估报 perms-unverified → 断言失败。0600 事实（cfWrite perm 参数）在 Windows 上丢失。

**修复**
- coordinator_test.go：新增 `fakeProbes.setMode(linuxPath, mode)`（锁内显式 mode 表登记——夹具的权威语义面）。
- consistency_fixes_test.go：cfWrite 副本后 `fp.setMode(tr.LinuxRoot+"/"+VisionExportFile, "600")`，与生产消费的 key（legacy_assess.go `target.LinuxRoot + "/" + VisionExportFile`）同源。

**回归**：断言原样（safe + !RiskPending + v1/v2 证据保真）；0600 事实改由权威面表达，强度不降。

## 3. TestSensitiveSubmitUsesVerifiedPrivateStaging — 判定 B（夹具；GOOS 分支论证）

**证据链**
- 生产敏感权限验证全部走探测面：commit.go:416–419 / 456–459（备份/候选 staging chmod+StatMode 复验）、commit.go:543–551（verifyPublished 发布后收紧复验）、coordinator.go:1747–1766（ensureSensitiveTargetPerms：chmod 失败/复验不过 → 错误）、coordinator.go:1770–1789（ensurePrivateStaging 0700 fail-closed）。宿主 stat 不参与生产判定。
- 夹具旧 333–336 行断言宿主 `os.Stat().Mode().Perm()==0600`：Windows 上 Go 的 os.Stat 只报 0666/0444（os.Chmod 仅映射只读位），0600 语义在 Windows 宿主文件系统上不存在。

**修复**（GOOS 分支唯一处，论证如下）
- 主断言改为 distro 权威面：`fp.statMode("/home/u/.pi/agent/core-stub.json") == "600"`（verifyPublished 复验后必然在表）——所有平台执行，比旧断言更贴生产真值面。
- 宿主 POSIX 位断言仅在 `runtime.GOOS != "windows"` 保留；Windows 侧保留存在性断言。论证：该语义（宿主位 0600）在 Windows 宿主上确实不存在（Go 标准库文档明确 Windows chmod 只翻只读位、stat 权限位仅 0666/0444 两态），不是可修的产品面；跨平台的「副本私有已验证」语义已由探测面断言承担，强度未降反升。

**回归**：macOS PASS；Windows 上主断言（探测面 600）可执行。

## 4. TestReviewFixPR02UnchangedSensitiveVerifiesPerms/chmod_failure — 判定 B（夹具；生产放行面已核实不存在）

**证据链（重点核对：chmod 失败放行是否生产可达）**
- 生产路径 coordinator.go:498–506：敏感既有副本（ActionNone/Fresh 零写入）先 `ensureSensitiveTargetPerms`；其内（coordinator.go:1747–1766）：StatMode 非 600 → `probes.Chmod("600")` 失败 → **返回错误** → 单元 `UnitFailed`/"sensitive-permissions-unsafe"；chmod 后复验不过同样失败。任何 chmod 失败都不放行——**生产不可达「chmod 失败仍 fresh」**。
- 失败真因在夹具：fakeProbes.StatMode 表优先，且 Windows 上 publish 阶段 verifyPublished（commit.go:543）因宿主回退位 0666 走过一次收紧、把 `".../amagi-media-models.json"="600"` 写进 mode 表；随后子测试用宿主 `os.Chmod(visionFile, 0o644)` 表达「松弛」，宿主位被表遮蔽（任何平台都被遮蔽，macOS 恰因表为空、回退位 0644 而未踩中）→ StatMode 读表 600 → 健康 → fresh，注入的 chmod 错误从未被消费。
- 根因归类：夹具用「宿主位」表达状态、而探测面权威是「mode 表」——正是任务书所指的夹具假设。

**修复**
- 两个子测试（loose 0644 / chmod failure）改为 `fp.setMode("/home/u/.agents/"+VisionExportFile, "644")` 从权威面注入松弛态；chmod 故障仍经既有 `setChmodError` 注入。

**回归**：断言原样（`UnitFailed` + `sensitive-permissions-unsafe`；loose 子测试的探测面 600 断言原样）；两平台同一确定路径（表 644 → chmod → 成功/注入错误）。

## 5. TestScanSkillCompatChecks — 判定 A（产品缺陷：扫描器分类）

**证据链**
- 夹具：`os.Symlink("/abs/target", abs.md)`。Windows 宿主上 CreateSymbolicLink 把目标规范化为盘根相对形态 `\abs\target`（Readlink 返回该形态；真机日志旁证：同夹具内相对目标链接 cyclic/out-of-tree/dir-link 均分类正确，Readlink 可用）。
- skills_scan.go（修复前）`isAbsoluteLikeLinkTarget`：只识别 `/`、`\\`、`//` 前缀与盘符形态——单反斜杠盘根相对 `\abs\target` 漏判 → `resolveSkillLink` 里 `filepath.IsAbs("\abs\target")` 在 Windows 为真、以绝对路径参与解析 → `Lstat(盘根\abs\target)` ENOENT → 归为 non-regular-entry。与真机失败输出完全吻合（期望 absolute-link，实际 abs.md→non-regular-entry）。
- 生产可达性：扫描器在 Windows 宿主进程运行，扫描宿主侧 NTFS 技能树（NewSkillsScope(host)）与 UNC 目标树——任何 Windows 侧创建的盘根相对/规范化链接目标都命中此路径。后果：不兼容阻断本身仍发生（不复制），但分类违反契约（absolute-link 的「不可移植」语义被误报为「非常规形态」）；若盘根恰存在同名实体还会走 out-of-tree 误分类。真实 UNC 上的 Linux 绝对链接：Readlink 或返回原始 `/abs`（已被 `/` 前缀规则覆盖）或返回规范化 `\...` 形态（本次修复覆盖）——两种形态均正确分类 absolute-link。
- 生产语义确认：扫描器遇绝对形态链接必须报 absolute-link（skills_scan.go 头注释契约「绝对链接（含盘符/UNC 形态）→ absolute-link」），修复使 Windows 宿主行为符合该契约。

**修复**：`isAbsoluteLikeLinkTarget` 末尾追加 `return filepath.IsAbs(t)`——宿主原生语义精确区分：Windows 上 `\abs` 是绝对（盘根相对即绝对）；POSIX 上同形字符串是合法相对文件名（POSIX 的 IsAbs ⟺ `/` 前缀，已被前置规则覆盖）——**POSIX 行为零变化，无需 GOOS 分支**。该谓词同时服务 scanLink 首跳与 resolveSkillLink 链中跳，两处一并修复。

**回归**：新增 `TestScanSkillAbsoluteLikeLinkTargetForms`（钉谓词：相对/绝对形态表 + 盘根相对形态「谓词 ≡ 宿主原生 filepath.IsAbs」双向断言）；既有 `TestScanSkillCompatChecks` 本身即 Windows 真机回归（本次失败用例）。

---

## 验证（命令、退出码、结果）

均在 worktree `amagi/wsl-goal-winfix` 执行（macOS 宿主）：

| 命令 | 退出码 | 结果 |
|---|---|---|
| `go test -race -count=1 ./internal/wslsupply ./internal/platform` | 0 | `ok amagi-codebox/internal/wslsupply 4.347s`；`ok amagi-codebox/internal/platform 1.455s` |
| `go vet ./internal/wslsupply ./internal/platform` | 0 | 无输出 |
| `GOOS=windows GOARCH=amd64 go test -c -o /tmp/wslsupply.test.exe ./internal/wslsupply` | 0 | 产物 11784704 字节 |
| 定向：`go test -race -count=1 -v -run '5 个失败用例|TestScanSkillAbsoluteLikeLinkTargetForms' ./internal/wslsupply` | 0 | 全部 `--- PASS`（含 PR02 三个子测试） |
| 主树 `git apply --check agent-outputs/workflow/wsl-goal-closure/windows-wslsupply-fixes.delta.patch` | 0 | 8 个文件全部 Checking 通过 |

gofmt：仅对所改文件检查；`gofmt -l` 报出的偏差与主树基线完全一致（基线即未格式化的既有 hunks，非本次改动引入；按「只 gofmt 你改的文件」且保持净增量最小，未顺手重排基线代码）。

## 净增量构成（8 文件）

- 生产代码：`internal/wslsupply/a2.go`（+stageIntent 故障接缝，生产行为零变化）、`internal/wslsupply/skills_scan.go`（isAbsoluteLikeLinkTarget 分类修复）。
- 测试/夹具：`a2_consumption_test.go`、`coordinator_test.go`（setMode）、`consistency_fixes_test.go`、`core_security_regression_test.go`、`review_fixes_test.go`、`skills_scan_test.go`（新增谓词回归）。

## 未覆盖项 / 风险

1. **Windows 真机复跑由 Leader 执行**（本执行者无真机权）。第 5 条的残留不确定性：若 Windows Readlink 对该链接直接报错（而非返回 `\abs\target` 形态），分类仍为 non-regular-entry——但真机日志旁证（同夹具相对链接分类正常、失败签名与 `\abs` 解析路径吻合）支持 Readlink 可用且返回盘根相对形态；两种可能返回形态（`/abs/target`、`\abs\target`）现均正确分类 absolute-link。
2. **主树并发漂移**：核对窗口内主树 `internal/wslsupply/core_staging_e2e_windows_test.go` 新增了 `t.Cleanup`（04:49:54，晚于基线快照 04:49:24）——非本任务改动，已从 delta patch 排除（patch 不含该文件，无合并冲突）。
3. fakeProbes.StatMode 的宿主位回退分支保留（其余既有流程依赖；fail-closed 方向，Windows 上只会更保守），仅本次涉及的 0600/0644 事实改为显式 mode 表表达。若后续要彻底去宿主位，需整批迁移夹具，超出本任务范围。
4. 第 2 条夹具中 `tr.LinuxRoot+"/"+VisionExportFile` 与生产 key 构造式（legacy_assess.go）同源；如未来 LinuxRoot 构造变化需同步。

## 回滚方式

`git apply -R agent-outputs/workflow/wsl-goal-closure/windows-wslsupply-fixes.delta.patch`（或按 patch 逐文件还原至主树当前状态）。
