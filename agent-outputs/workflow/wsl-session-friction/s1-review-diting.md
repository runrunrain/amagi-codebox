# P3-S1 集成 diff 对抗检验报告（谛听 · full）

- 基线：HEAD=0cf7591，工作区未提交改动 + 未跟踪新文件（33 文件，报告 §0 清单）。
- 方法：只读静态审查（read/grep/find；工具面无 bash，未运行 git diff/构建/测试/真机）。任务书七项关注点逐项沿代码路径构造失败假设核实；s1-report 声称的测试结果与真机 PASS 按命令记录采信，未独立复跑（盲区见 §四）。
- 上游输入：`p3-config-supply-design.md`（必读，已完整读取）、`s0-review-diting.md`（必读，已完整读取）、`s1-report.md`（必读，已完整读取）。

## 一、审查关注点结论

### 1. 密钥跨界安全（最高优先）
- **同意门是真门**：`Coordinator.Plan` 在进入适配器前统一拦截（coordinator.go Plan 段 `adapter.RequiresConsent() && !entry.Consented → ActionBlocked awaiting-consent`），未同意时零文件接触——`TestVisionExportConsentFlow` 断言 blocked + 文件不存在（os.Stat IsNotExist），真机 e2e 同款断言。UI 按钮只是授权入口，不是防线。✅
- **0600 链**：CreateTemp(0600) → os.Chmod 0600 → rename → distro 内 `WSLChmod 600` → 读回校验；真机 `TestManualRealWSLVisionExportPublish`/`TestManualRealWSLManagedModelsSupply` 从 distro 内 `stat -c %a` 实证 600（Windows os.Chmod 不作数），隔离根 `/tmp/amagi-wslsupply-e2e`，桩字节无真实密钥。✅
- **撤回语义**：关闭 vision_export 走「内容重写」路径（ExportVisionModels 幂等全量导出，无可导出时写 `models:[]`，vision.go:81）→ sourceChanged → 正常更新目标（撤回落地，测试覆盖）；目标手改+来源变化 → `withdrawal-unconfirmed-frozen` 冻结 + 目标密钥字节保留 + 状态面「密钥副本未确认撤回」（`TestVisionExportHandEditFreezesAndWithdrawalUnconfirmed`）。不虚报已删。✅（宿主文件被直接删除的旁路场景见建议区 2）
- **密钥/hash 不泄漏**：GetStatus 的 UnitStatus 无 hash 字段（types.go）；A2 恒不落 hash；日志面（GrantUnitConsent/Notes/Detail）只含 provider 名、模型 ID、错误码短串（sanitizeErrorCode 截断 160）；Candidate 正文只在 Apply 内存。✅

### 2. provider 分发正确性
- **严格解析与非 amagi 保留**：`BuildWSLPiManagedModelsCandidate` 复用宿主 `readStrictJSONObject` 抽出的字节变体 `parseStrictJSONObject`（provider_sync.go:329-338，同语义同源）；merge 保留域外 provider 与全部顶层字段（`TestPiManagedModelsPublishAndMerge` 断言 user-own/equivalence 保留、amagi-stale 被 prune；真机 distro 内 cat 实证）。「从业务快照构建」声明属实：`wslPiManagedSource.DesiredPiManagedModels` 与宿主 `syncProvidersToHarnessesLocked` 同 builders/同桶/同过滤/同 `getProviderAPIKey` 静默语义（provider_harness_sync.go:54 vs app_wsl_supply.go:78 逐行同构）。✅
- **两级损坏保护**：语法/空文件/根非对象 + `applyLoginModelOverrides` 嵌套形态错误（modelOverrides 非对象）均返回 `ErrWSLManagedModelsCorrupt` → 协调器哨兵 `target-corrupt` + 原文件字节不动（`TestPiManagedModelsCorruptTargetRefused`）。✅
- **部分失败不 prune**：RetainProviders 保留旧条目 + BuildErrors 进 Detail/日志（`TestPiManagedModelsPartialBuildFailureRetains`）。✅
- **contextWindow 窄字段**：`applyLoginModelOverrides` 经 `overrideMapAt` 嵌套校验，仅 set contextWindow，兄弟字段/兄弟键保留；仅 cw>0（补齐语义，S2 接管框架的取舍已披露）。✅
- **选中 provider 构建失败阻断**：桌面 WSL 路径 `cfgErr` 分支阻断（app.go:3670-3680）+ 登录/remote 双链路 effect 双单元（launch_executor.go:763-780 HasUnit 能力过滤 + pi_login_launch.go 双单元）——**但桌面阻断分支本身引入 Critical 死锁（F1）**；remote API-provider 路径未接入为已披露的 S2 范围。

### 3. OpenCode 语义变更
- 阻断条件精确：`AdaptWSLOpenCodeMCP` 只区分确定性形态——裸 npx.cmd/npm.cmd 经真实登录 shell `command -v` 批量验证（WSLResolveCommands，单次 wsl.exe）后才映射，未验证/盘符/exe/ps1/bat/cmd/pwsh/参数盘符 → 丢弃+中文诊断；HTTP/远程保留 URL。探测失败按不可确认保守丢弃。✅
- 全局同名冲突默认保留本地不写入附加层（JSONC 注释剥离后仍不可解析 → 空集合不误伤）——`TestAdaptWSLOpenCodeMCPGlobalNameConflictKeepsLocal`。✅
- 附加层机制未被替代（仍 `~/.config/opencode/amagi-codebox.json` + OPENCODE_CONFIG，`WriteWSLOpenCodeAgentConfig` 保留 + 零写入跳过）；全局 opencode.json 不触碰。新失败语义（写入失败 → MarkFailed + 中文阻断）已批准且测试钉文案。✅

### 4. 前置门槛真实性（M1/M2/M4）
- **M1**：`Compensate` 整批持 `c.applyMu`（coordinator.go Compensate 段）；锁序 applyMu → state.mu 一致，GrantUnitConsent/MarkDirty 只触 state.mu 无反转。`TestCompensateApplySerialized` 4 轮真并发 + chmod 入口重叠检测（30ms 宽窗）断言 overlap=0 + 终态完整 + 无 staging 残留。✅
- **M2**：`submitFile` sensitive 分叉（敏感删/普通恢复前像，恢复失败才清理）；`restorePreimage` 唯一临时名+rename。`TestSubmitFileChmodFailureRestoresPreimage` 三子测覆盖 update/first-create/sensitive。✅
- **M4**：`TestSubmitFilePrimitiveTrueConcurrency` 直接并发调用私有 `submitFile`（绕开 applyMu），distinct/same-file/chmod-failure 三子测；「already exists」类错误判唯一名破坏，Windows 瞬态共享冲突如实记录为容忍（生产同目标由 applyMu 串行化，声明一致）。✅

### 5. 异步推送面
- 守卫链完整：single-flight（rerun 合并，自死锁修复有回归）→ distro Running（`wslDistroRunningFn`）→ 无活动会话（全 CLI 宽判，理由已注明）→ 分作用域推送。`TestPushWSLSupplyGuards` 三子测覆盖停机不推/活动推迟/运行推送。停机 distro 不因保存 spawn wsl.exe。✅
- **但 PendingSources 永不清除（F2）**：MarkDirty 只写不清，推送/启动供给成功后无任何清除点 → pendingPush 恒亮、状态面「已同步」在首次保存后不可达。

### 6. UI 面
- 同意按钮授权落点：`GrantWSLVisionConsent` → `GrantUnitConsent`（按目标 generation upsert，per-target 持久化于 wsl-config-supply.json）；跨代场景存在 F3 缺陷。幂等成功；incapable runner 中文拒绝。
- 状态区独立区块（WSLCLISettings.vue supply-section），envcheck wsl_hybrid.go 不在改动面，不进 CheckIssue、不触碰 AllOK。✅
- wailsjs 再生：App.d.ts 含 GetWSLConfigSupplyStatus/GrantWSLVisionConsent（与既有生成风格一致）；报告声明基线字节稳定后再生，采信（盲区标注）。

### 7. 测试有效性
- 真机 e2e 断言质量高（隔离根/distro 内 stat/cat/mtime/同意门/零写入）；M1/M2/M4 均真并发或真原语级。
- **F1 死锁未被测试暴露的原因**：`TestWSLPiProviderBuildFailureBlocksLaunch` 只断言单次调用返回中文错误，无锁可用性断言（TryLock/二次调用），泄漏的锁在测试进程退出时静默丢弃。
- Windows 布局限制披露属实（windows-only 测试 CI 两条线均不执行断言，与 S0 审查遗留观察同源）。

## 二、Findings

**F1｜Critical｜app.go:3623(Lock)→3676(return)→3690(Unlock)｜providerSyncMu 死锁**
- 触发条件：Windows 宿主 + WSL embedded Pi 会话 + API-provider 路径（loginPreset==nil && providerID!=""）+ `BuildPiModelsConfig` 失败（如选中 provider 无 baseURL）。
- 实际影响：`return "", wslPiProviderBuildError(...)` 在 Lock/Unlock 区间内直接返回，函数无 defer Unlock——mutex 永久持有。此后同进程内所有 providerSyncMu 使用方永久阻塞：再次启动 Pi 会话（provider 与登录路径的 UpsertPiLoginModelOverride）、`syncProvidersToHarnesses`（任何 provider 保存）、`SaveOpenCodeConfig` 等全部挂死，Wails 绑定 promise 永不 resolve，相关 UI 功能失效直至重启应用。注释中恰记有 v1.3.13 同位置锁事故历史，本批引入新的锁事故。
- 证据：静态路径完整（完整读取该函数段确认无 defer，3676 为区间内唯一 return）；测试只断言单次错误文案不暴露死锁。未复现运行时（无 bash），路径无歧义。
- 最小修复：区间改 `defer a.providerSyncMu.Unlock()`（或该 return 前 Unlock）；补「阻断后锁可用」回归（TryLock 断言或同 App 二次调用不挂起）。

**F2｜Major｜internal/wslsupply/coordinator.go:503-519 + app_wsl_supply.go:374-382｜PendingSources 永不清除，状态面永久失真**
- 触发条件：任意一次保存事件后（provider 保存每次必经 provider_harness_sync.go:25 无条件标脏），无论后续推送/启动供给成功多少次。
- 实际影响：全仓无 PendingSources 清除点（仅 MarkDirty 写入、GetStatus 读取）→ `GetWSLConfigSupplyStatus` 的 pendingPush 恒 true → providers/vision/modality 三面在非 frozen/failed 时恒显示「stale 有保存后的变更待推送」，前端恒提示「有保存后的变更待同步」。「已同步」状态在首次保存后不可达，用户无法分辨真实陈旧；任务书关注点 5「重启后 pending 补漏」的完成信号缺失。
- 证据：全仓 grep PendingSources 全部命中仅写入/读取两向；pushWSLSupplyOnce 成功路径无清除调用。
- 最小修复：RunUnits 所选单元全部 Applied/Fresh 后清除对应 PendingSources（或新增 ClearPending），失败/阻断保留标记。

**F3｜Major｜internal/wslsupply/coordinator.go:GrantUnitConsent + state.go:126-135｜跨代继承旧 hash，distro 重装后同意按钮失效**
- 触发条件：曾在旧 distro 成功同意并发布 vision-export（entry 有 LastWrittenHash）→ distro 重装/换默认用户（generation 变化）→ 用户在新 distro 点击「同意共享到 WSL」。
- 实际影响：GrantUnitConsent 构造新 entry 时复制 `prev.SourceHash/LastWrittenHash`（旧代值），upsertEntry 换代分支直接写入——违背其自身注释「不带任何旧 hash 继承」。此后 Plan 判定 recorded=true，新 distro 无目标文件 → `local-deletion-frozen` 冻结：授权后无法发布，状态显示「WSL 侧文件已被删除，保留该状态待确认」，S1 无解冻/接管操作，用户无出路。设计 §七「重装后旧授权不自动扩散、需重新确认」的重确认路径破损（密钥不泄漏，安全面无损）。
- 证据：静态路径；s1_adapters_test.go 全部同意测试为同代场景，跨代无覆盖。
- 最小修复：GrantUnitConsent 检测 prev 非本代（prev.Generation != target.Generation）时不复制 hash；或 upsertEntry 换代分支强制剥离继承 hash。补跨代重授权测试。

**F4｜Minor｜internal/wslsupply/coordinator.go:GetStatus｜跨 CLI 检查时间语义混合**
- 触发条件：CLIPi 的 Inspect 更新全局 LastInspectedAt 后读取 CLICommon 状态。
- 实际影响：vision-export/modality 面显示「检查于 T」时 T 时刻可能只探测了 .pi/agent 文件——检查时间跨 CLI 共享，语义轻微失真；faces 状态值本身由单元登记推导不受影响。
- 证据：GetStatus 的 Checked/CheckedAt 取全局 st.LastInspectedAt，不按 CLI 区分。
- 最小修复：按 CLI 记录检查时间或仅对该 CLI 作用域实际探测过时置 Checked。

**F5｜Minor｜app_wsl_supply.go:GrantWSLVisionConsent → 前端 grantConsent｜授权后状态短暂虚高**
- 触发条件：点击同意后立即刷新（前端 await load()），异步推送尚未执行完成。
- 实际影响：Consented=true 且无 Frozen/LastErrorCode/pending → 状态面短暂显示「已同步」，实际发布未落地；下次推送/启动后收敛。轻微时序失真，无安全影响。
- 证据：GrantUnitConsent 只落登记不等待推送；scheduleWSLSupplyPush 异步。
- 最小修复：Consented 后 LastOutcome="consented" 单独映射为「待发布」状态而非 synced。

## 三、建议区（不影响 VERDICT）
1. `runWSLManagedModelsAtLaunch` 与推送均用 context.Background() 无超时——WSL 探测若挂起将阻塞启动链（与 S0 A2 闸门同形态的既有边界，供 S2 统一）。
2. 宿主视觉导出文件被直接删除（非关闭导出）时 vision 面显示沿用上次登记（synced）+ 日志 WARN source-unavailable，不显示「未确认撤回」——正常撤回走内容变化路径不受影响，旁路场景可在 S2 接管框架内补「来源不可达且登记存在」的显式状态。
3. `GetWSLConfigSupplyStatus` 只合并 piStatus.PendingSources（sharedStatus 的同名字段是同一全局副本）——无实际影响，可读性建议。
4. S0 审查建议区第 2 条（Windows-only 测试无 CI 断言执行线）对本批 TestWSLPiProviderBuildFailureBlocksLaunch 同样适用，随 F1 修复一并考虑本地门禁。

## 四、盲区与验证可信度
- 未运行 `git diff HEAD`、go build/vet/test、前端 vitest、真机冒烟（本角色无 bash）：「全绿」「3/3 PASS」「stat mode=600」等采信 s1-report §8 命令记录，未独立复跑。
- wailsjs「再生非手改」采信报告声明 + App.d.ts 形态核对（两新方法在列、风格一致），未做字节级 diff。
- remote/v1 链路仅静态核验 effect 单元过滤与登录计划双单元描述；API-provider remote 未接入为报告已披露的 S2 范围，未按缺陷处理。
- F1 为静态路径证实（无运行时复现手段），路径无分支歧义；F2/F3 同为静态路径 + 测试盲区交叉印证。

VERDICT: FIX
