# P3-S3 实施报告：共享 skills 与用户资源同步

> 批次：S3（共享 `~/.agents/skills` 按目录选择供给 + 有界扫描 + 静态兼容检查 + 预览同意导入 + 增量跟随/冻结，P1 批次）· 状态：已实施并验证
> 权威规格：`agent-outputs/workflow/wsl-session-friction/p3-config-supply-design.md`（下称「设计」§四「共享 ~/.agents」表 + §八批次 S3）；前置：S0–S2（HEAD=bd93f4c，全部复用：同意门/GrantUnitConsent 跨代语义、TrackedProjection 冲突判定、私有 staging 原语、状态页五动作框架）
> 验证环境：WSL bash 内驱动 Windows 工具链（go.exe 1.25.x windows/amd64，GOCACHE/USERPROFILE 经 WSLENV `/w` 转发；wails v2 `generate module` 再生绑定；WSL distro=Ubuntu 即本机默认发行版——真机冒烟直接落在真实 wsl.exe/UNC/9P 通道）

## 1. 改动总览（10 文件修改 + 7 文件新增）

新增（Go 实现）：`internal/wslsupply/skills_scan.go`（宿主侧有界扫描器 + 静态兼容检查 + 排除物 + `skillTreeHash` 统一树 hash）、`internal/wslsupply/skills.go`（`SkillsScope`：单飞后台扫描/进度/缓存；技能单元 `planSkill` TrackedProjection 判定；目标侧有界只读树扫描 `scanTargetSkillDir`；协调器注册面 `RegisterSkills/StartSkillsScan/SkillsSnapshot`）、`internal/wslsupply/skills_submit.go`（目录级提交原语：唯一 staging 目录 → 写文件 0644 → rename 交换 → distro chmod 批量补偿 644/755 → 树 hash 读回校验，chmod/校验失败恢复前像；`takeOverSkill` 接管路径）。
新增（测试）：`internal/wslsupply/skills_scan_test.go`、`internal/wslsupply/skills_test.go`（全链行为钉子 13 测）、`internal/wslsupply/s3_skills_e2e_windows_test.go`（真机冒烟，AMAGI_WSL_E2E=1）、根包 `app_wsl_skills_test.go`（门面 6 测）。
修改：`internal/wslsupply/types.go`（UnitStatus.Stale 脱敏陈旧标记）、`internal/wslsupply/compensation.go`（目录级撤销句柄扩展：树 hash CAS + 前像快照/超限标注）、`internal/wslsupply/coordinator.go`（技能单元接入 Plan/Apply/PreviewUnits/DropUnitRegistration/TakeOverUnit/GrantUnitConsent 六分支 + compensateOne 目录分发 + skillUnitStale）、`app_wsl_supply.go`（S3 门面：状态/检查/预览/勾选导入/保留本地/接管 + 配置状态区第五面 skills face + `newWSLSupplyCoordinator` 注册宿主技能根）。
前端：`frontend/src/api/wslSupply.ts`（+6 wrapper）、`WSLCLISettings.vue`（共享技能区：扫描进度/扫描未完成徽标/勾选导入/逐技能状态与不兼容清单/五动作按钮/「已供给≠运行已验证」文案）、`__tests__/api/wslSupply.test.ts`（+4 测；顺带把同文件叠加的 4 个 `vi.mock` 工厂合并为单一工厂——叠加工厂在全量跑时胜者不确定，S3 绑定会随机解析失败）、`frontend/wailsjs/*` 经 `wails generate module` 再生（非手改）。

未触碰（边界纪律）：piconfig/launcher 配置链（S2 域）、插件（S5）、CHANGELOG、未跟踪件 `pi_wsl_provider_build_windows_test.go`/`wsl_s2_remote_test.go`（S2 遗留未入库测试，见 s2-report §0 同类提醒）。

## 2. 设计对照（S3 范围 → 实现位置）

| 任务书条款 | 实现位置 | 状态 |
|---|---|---|
| 资源清单：skill 目录最小单元、内容清单（相对路径/类型/大小/hash）增量基线 | `SkillManifest`（Files 条目含 rel/size/hash/linkTarget + 聚合 `SourceHash`）；来源与目标共用 `skillTreeHash` 同构序列化——同步 ⇔ 两 hash 相等 | ✅ |
| 扫描有界（深度/数量上限可配）；未完成显示「扫描未完成」不冒充完成 | `SkillsScanLimits`（默认深度 8/单 skill 4000 条/单文件 16MB/总 60000 条，可注入）；超限 → `Incomplete`+原因，Plan 恒 blocked `scan-incomplete`，状态面独立徽标 | ✅ |
| 大目录后台分批+进度反馈，不放进会话启动等待 | `SkillsScope.StartScanAsync`（单飞 goroutine，按目录分批推进，`Snapshot` 随时读进度）；启动链单元清单不含技能单元（结构性不阻塞）；`GetWSLSkillsSupplyStatus` 非阻塞返回缓存 | ✅ |
| 预览同意模式（与 S2 同构）：一次预览勾选全部可移植项；per-target 授权持久化（跨代语义） | `PreviewWSLSkillsSupply`（逐项动作/不兼容原因/CAS 令牌）→ 前端勾选 → `ApplyWSLSkillsSupply(names)`（逐项 `GrantUnitConsent` 复用同意门 + 一次 `RunUnits`）；未同意 Plan 恒 `awaiting-consent`；generation 变化旧授权失效（复用 F3 语义） | ✅ |
| 清单增量跟随（TrackedProjection：WSL 手改该 skill 目录即冻结该单元） | `planSkill`：无记录+目标存在→unmanaged-existing 冻结；目标树 hash≠上次写入→local/both-changed 冻结（本地内容保留）；目标被删→local-deletion-frozen；来源变+目标未手改→整目录重写 | ✅ |
| 不兼容清单逐项报因：越界/循环/绝对链接、无法保留权限资产、Windows 专用脚本明确「未支持」 | `scanSkillDir`：absolute-link/out-of-tree-link/cyclic-link/directory-link-unsupported/windows-script(.ps1/.cmd/.bat)/windows-binary(.exe 等)/oversize-file/non-regular-entry；**含不兼容项的 skill 整体不入供给（不产半残 skill）**，预览逐项列出 | ✅ |
| 排除物：.git、node_modules、缓存、凭据文件、会话产物 | `skillsExcluded*` 名单（.git/node_modules/__pycache__/.cache/.pytest_cache/.venv/sessions/.env/*.pem/*.key/credentials.json 等）；排除≠不兼容，不阻断，预览/写入附注计数 | ✅ |
| 禁止自动执行安装/验证脚本 | 全链只读+哈希，无任何 exec 技能内容；chmod 补偿是 distro 内权限操作非执行技能 | ✅ |
| 写入走私有 staging 原语（唯一临时名/权限收紧/rename）；只替换已登记目录（hash 条件） | `submitSkillDir`：`os.MkdirTemp` 唯一名 → 写文件 → rename 交换（目标现存先挪隐藏备份名）→ chmod 批量（50/批，交换后执行——修正过一版交换前 chmod 打不存在路径的缺陷）→ 树 hash 读回校验；失败恢复前像；Apply 前 CAS 复核目标树 hash | ✅ |
| 状态页技能面五状态 + 五动作（复用 S2 框架） | 状态：supplied/stale/frozen/incompatible(含因)/awaiting-consent/importable/scan-incomplete/empty/detached/source-deleted/failed；动作：检查/预览/应用（勾选导入与逐技能安全更新）/保留本地/确认接管（树 hash CAS） | ✅ |
| 「已供给」与「运行已验证」区分（诚实标注） | supplied 态文案固定「静态检查通过；未执行内容——『已供给』不等于『运行已验证』」；skills face 同款标注 | ✅ |

## 3. 独立验收（设计 §八 S3 清单 → 证据）

1. **选中 skill 连同相对依赖由目标实际消费者可发现（真机冒烟）** — `TestManualRealWSLSkillsSupply` 第二段：真实技能 `agent-browser`（2 文件/26332 字节）供给到 distro 临时验证位 `/tmp/amagi-wslsupply-e2e-real-*/.agents/skills/agent-browser`，清单逐文件 `wsl.exe test -f` 全过 + `head -c 64` 读回 SKILL.md 内容正确（消费者视角）。树内相对链接实体化由跨平台单测钉死（`TestScanSkillCompatChecks`：guide-alias.md hash==目标文件 hash；本机 Windows 无建链特权，e2e 段 best-effort 跳过并注明——见 §5 边界）。**PASS**
2. **源改动增量更新（只重写变更目录，mtime/hash 证据）** — 单测 `TestSkillIncrementalUpdateOnlyRewritesChangedDir`：改 alpha 后 alpha/SKILL.md 内容更新且 mtime 前移，兄弟 skill beta mtime 逐字节不变；`TestSkillFirstSupplyAndIdempotentSecondRun`：无变化二跑 fresh、文件 mtime 不变。真机段：v2 更新后 `head` 读回含 v2。**PASS**
3. **WSL 改动冻结（手改后来源更新不覆盖）** — 单测 `TestSkillWSLModificationFreezesUnit`（both-changed-frozen，本地内容保留）；真机段：distro 内 `printf 'local edit' > SKILL.md` 后来源 v3 更新被 blocked，读回仍是 local edit。**PASS**
4. **大目录不阻塞启动（扫描后台化/有界）** — 单测 `TestSkillsScopeStartScanAsyncNonBlocking`（40 技能异步扫描立即返回+单飞+收敛）；启动链单元清单结构上不含技能单元；`GetWSLSkillsSupplyStatus` 非阻塞。**PASS**
5. **越界/循环/绝对链接不复制且报告；Windows 专用脚本明确未支持** — 单测 `TestScanSkillCompatChecks`/`TestScanSkillWindowsScriptExplicitlyUnsupported`（.ps1 逐项报因含「未支持」字样，整 skill 阻断）；`TestSkillIncompatibleBlocksSupplyEntirely`（目标零写入）；真机段：e2e-win（.ps1）blocked=incompatible-items 且 distro 内无该目录。**PASS**
6. **go build/vet/gofmt + 改动包测试 + 前端测试 + wailsjs 随批** — `go build ./...` ✅、`go vet ./...` ✅、gofmt 改动文件零输出 ✅；`go test ./internal/wslsupply/` 全绿（含 S0–S2 既有测试回归）；根包 WSL 相关测试全绿（12/12）；前端 `vue-tsc --noEmit` ✅、`vite build` ✅、`vitest run` 235/235（单文件+全量双模式）✅；`wails generate module` 再生 `frontend/wailsjs/*`（含 6 个新绑定 + WSLSkill*/WSLSkillsSupplyStatus 模型）。**PASS**

## 4. 真机冒烟记录（Ubuntu@WSL2，AMAGI_WSL_E2E=1）

```
=== RUN   TestManualRealWSLSkillsSupply
s3_skills_e2e_windows_test.go:71:  symlink unsupported on this host (...A required privilege is not held by the client); link materialization covered by unit tests
s3_skills_e2e_windows_test.go:176: isolated full-chain PASS (distro=Ubuntu)
s3_skills_e2e_windows_test.go:207: real skill smoke: "agent-browser"（2 文件，26332 字节，0 排除项）
s3_skills_e2e_windows_test.go:244: real skill discoverable: /tmp/amagi-wslsupply-e2e-real-7172/.agents/skills/agent-browser/SKILL.md head="---\r\nname: core\r\ndescription: Core agent-browser usage guide. Re"
s3_skills_e2e_windows_test.go:252: real skill smoke PASS
--- PASS: TestManualRealWSLSkillsSupply (4.16s)
```

隔离段全链：可移植 skill 供给（distro 内 stat 实证文件 644/目录 755）→ `.env`/`node_modules` 未跨界 → 不兼容 skill 零写入 → 增量更新生效 → 手改冻结保留。验证后 /tmp 残留已清理。

## 5. 边界决策与未覆盖项（诚实清单）

- **跨 skill 链接按越界处理**：任务书「解析目标不在选中树内」的「选中树」按**本 skill 目录**解读——解析出 skill 目录（含跨 skill 共享）即 `out-of-tree-link` 不复制。保守取向，v1 不做跨技能依赖跟随（预览逐项报因可见）。
- **指向目录的链接未支持**：`directory-link-unsupported` 明确报因（不猜语义、不产半结构）。
- **本机无 Windows 建链特权**：真机 e2e 的链接实体化段 best-effort 跳过（该行为由 Linux/macOS CI 单测钉住）。附带发现：从 WSL 侧在 NTFS 创建的符号链接，Windows Go 视角是 `ModeIrregular` 且 Readlink 失败——扫描器将其诚实归入 `non-regular-entry` 不兼容（无法从宿主侧解析）。
- **chmod 补偿时序**：目录 chmod 必须在 rename 交换**后**执行（目标路径才存在）；失败恢复前像或删除新增（不留半提交）。该缺陷在真机首跑暴露（chmod exit 1）并当场修复+复测。
- **撤销前像内存上限 8MB**：超限不快照，Compensate 按 `unavailable` 处置（诚实标注不可恢复，不假装）；技能内容非密钥，0644/0755 为既定发布权限（无 0600 需求——排除物已剔除凭据类文件）。
- **补偿后登记基准不回滚**（与 S1/S2 单文件语义一致）：Apply 成功后 Compensate 恢复前像会使登记与目标不一致，下次运行按冻结保护处理（绝不覆盖）；该语义由 `TestSkillCompensationRestoresOrDeletes` 的分段断言钉住。
- **根包全量测试在本 Windows 机存在既有失败**（与本批无关）：用 `git archive HEAD` 干净导出实证同样失败——R9/R6/R10/R11 系列缺 Unix `sleep`、remote-events.log 在 TempDir 清理时被占用（Windows 文件锁）。CI 权威全量在 macOS；windows-latest 仅编译检查（本批 `go build ./...` 在 Windows 通过，强于 CI 门槛）。WSL 相关测试（本批+既有）全绿。
- **「运行已验证」不在本批宣称**：静态检查（形态/链接/排除/哈希）≠运行验证；任何 skill 内容未被本系统执行。

## 6. 回滚方式

停用技能面=不注册 `SkillsScope`（`newWSLSupplyCoordinator` 中该分支删除即 A2-only 降级）。按登记撤回本批产物：各技能单元的 `LastWrittenHash`（树 hash）匹配时经 Compensate/接管语义删除或恢复；本地新增、改动或未登记目录不动（unmanaged-existing/冻结语义天然保护）。前端区随门面缺省自动隐藏（`skillsCoordinator()==nil`）。不涉及 piconfig/launcher/S2 域回滚。

## 7. diting quick 审 FIX 修复轮（2 Major + 5 Minor）

> 审核报告：`agent-outputs/workflow/wsl-session-friction/s3-quickreview-diting.md`（VERDICT: FIX）。七条全部修复并钉测试；本节为增量记录，正文 §1–§6 未改动。

| Finding | 修复 | 测试锚点 |
|---|---|---|
| F1（Major）walk 的 IsDir 先于 ModeSymlink——Windows junction/目录符号链接成死分支，树外内容可静默入清单 | 三处 walk（`skills_scan.go::scanSkillDir`、`skills.go::scanTargetSkillDir`、`skills_submit.go::snapshotSkillPreimage`）全部把 `ModeSymlink` 判定提到 `IsDir` 之前：目录形态链接按链接条目处理，绝不递归下降 | `TestScanSkillDirectoryLinkNotRecursed`（Unix 目录符号链接 + Windows `mklink /J` junction 双形态：断言零递归、树外内容不入清单、整条目判不兼容）；`TestScanTargetTreeDirLinkEntryNotRecursed`（目标树/前像侧：链接条目入 hash、FileCount 不含链接内文件） |
| F2（Major）restoreSkillPreimage 的 defer 无条件销毁前像（rename 失败时唯一持久备份被删，错误串却称保留） | rename 失败分支置 `keepStaging` 跳过 staging 清理（前像保留供人工恢复），写入阶段失败才清理；顺带移除未用的 `currentHash` 入参；接缝 `skillPreimageRenameFn`（生产 os.Rename，测试注入失败） | `TestRestorePreimageRenameFailureKeepsStaging`：注入 rename 失败 → Disposition=unavailable + 错误串如实指向 staging + 目录下 `.amagi-supply-skill-*` 前像仍在 |
| F3（Minor）文件头注释与实际时序矛盾 | `skills_submit.go` 头注释按当前时序改写：staging 写入 → rename 交换 → chmod 批量补偿（交换后，目标路径才存在）→ 树 hash 读回校验；任一步失败恢复前像或删除新增 | 注释与代码同段核对（无独立测试面） |
| F4（Minor）scanLink 不套用排除名单（.env/*.pem 名链接可实体化） | `scanLink` 入口先按同名/同扩展套用 `skillsExcluded*` 名单：命中 → 记入 Excluded 不实体化（与常规文件名单一致） | `TestScanSkillLinkNameExcluded`：dot-env/cert.pem 链接入排除清单、不入 Files、不阻断可移植性 |
| F5（Minor）readSkillCandidateFiles 无总量上限（理论上限 4000×16MB 无界读入） | 新增 `skillCandidateCap`（=skillPreimageCap 8MB，与封顶对齐）：计划期 `planSkill` 超限先行 blocked `oversize-skill`；Apply 期 `readSkillCandidateFiles` 总量复核超限报因拒绝（不静默截断、不无界读入） | `TestSkillOversizeTotalBlocked`：9MB 便携技能 → RunUnits blocked `oversize-skill` |
| F6（Minor）previewSkillUnit 绕过 Plan 同意门（未同意预览显示 write，Plan 显示 awaiting-consent——同单元两个答案） | 预览镜像 Plan 同意门：未同意恒 `awaiting-consent`（保留目标 hash 令牌与「目标已存在内容」附注，接管流程可用）；Detached 同样前置 | `TestSkillPreviewUnitsReportsIncompatReasons`（未同意→awaiting-consent；同意后→write/incompatible 各归位）；App 侧 `TestWSLSkillsTakeOverViaFacade` 同步改为两段式断言 |
| F7（Minor）前像恢复写无 chmod 补偿、无读回校验（与主提交原语不同构） | `restoreSkillPreimage` 落位后补 distro chmod 批量补偿（644 文件/755 目录，链接条目无字节不参与）+ 树 hash 读回校验（`preimageTreeHash` 与 `scanTargetSkillDir` 同构比对） | `TestRestorePreimageChirmodAndVerify`：恢复路径 chmod 调用数递增 + 前像内容正确恢复 |

### 修复轮验证（全部实跑）

- `go build ./...` ✅；`go vet ./internal/wslsupply/ .` ✅；gofmt 改动文件零输出 ✅
- `go test -count=1 ./internal/wslsupply/` 全绿（原 13 测 + 修复轮 6 新测 + F6 适配；含 Windows junction 真机形态）
- 根包 WSL 域 11 测全绿（含 F6 适配后的 App 接管流程）；真机 e2e `TestManualRealWSLSkillsSupply` 复跑 PASS（isolated 全链 + agent-browser 真实技能冒烟，证据同 §4）
- 前端 `vitest run` 235/235 全绿（本修复轮无前端改动，约束复跑确认）
- junction 形态观察（诚实记录）：本机 Go 1.25 Windows 下 `mklink /J` junction 在 readdir 类型位中不携带 ModeSymlink 也不携带 ModeDir → 落 `non-regular-entry` 判不兼容（阻断且不递归，安全谓词成立）；测试按「dir-link/absolute-link/non-regular 三码任一即阻断」钉住，不过拟合单一 Go 版本的位映射
