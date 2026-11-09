# WSL 共享技能根与 P3-S3 退役：全量独立审核

VERDICT: REJECT

范围：`/Users/run.mao/workpace/amagi-codebox-wsl-skills`，`d06bb60..db0153cbc2617c056e10476d7bbb122c4f57a78b`，35 files，+909/−2681；版本 1.3.130。审核档位 full。发现 **3 Major、5 Minor，无 Critical**。四项指定检查全部通过，但不能覆盖本报告复现的迁移/并发反例。

上游输入：任务书正文和该工作树的 Git diff / 当前文件；无外部上游 artifact。开始时工作树干净，结束时仅新增授权报告目录；未修改任何被审文件、未执行 Git 分支/恢复/提交操作。

## Findings

### F01｜Major｜并发建链会穿透链接向 Windows 技能根写入多余的自引用链接

- 定位：`app_wsl_shared_skills.go:80-88`；`internal/platform/wsl_shared_skills_link.go:59-70`。
- 触发：同 distro 首次同时启动两个 pi 会话，或显式修复与启动重叠。缓存只有 lookup 和 record 各自加锁，二者之间执行不互斥。A 检查/备份后、执行 ln 前，B 已建好链接。
- 证据摘录：`if _, ok := lookupWSLSharedSkillsRoot(distro); ok { return }` 后独立 `a.runWSLSharedSkillsRoot(distro)`；脚本最后为 `ln -s "$T" "$L"`，没有“不把目标当目录”的保护。
- 实际影响：当 B 的 `$L` 已是指向目录 `$T` 的链接，A 的 ln 把链接写到 `$T/skills`，即 Windows 技能根里；两次调用均 exit 0 / LINKED，状态面不披露额外写入。不能只靠进程内缓存防御两个应用进程或显式入口。
- 已复现：`repro_link.py` 从当前 Go 源提取实际脚本，以暂停 ln 的接缝控制时序，原始输出见 `repro-link.log`：`UNEXPECTED HOST WRITE True`，`skills/skills` 指向其父技能根。执行于 macOS POSIX shell；Windows/WSL 真实进程链未复现，不将此冒充 WSL 集成证据。
- 最小修复方向：在 distro 内按落点串行化整个核验/迁移/发布事务（覆盖跨进程）；建链必须采用不跟随目标目录的发布语义，并核实最终 readlink。进程内补 single-flight 可减少重复，但不能替代文件系统侧安全发布。

### F02｜Major｜秒级备份名碰撞可覆盖旧备份

- 定位：`internal/platform/wsl_shared_skills_link.go:62-65`。
- 触发：迁移真实文件时，`skills.pre-shared.<同一秒>` 已存在为文件（脚本明确支持真实目录/文件）。同秒重复迁移、恢复操作或预存同名备份都可满足条件。
- 证据摘录：`B="$HOME/.agents/skills.pre-shared.$(date +%Y%m%d%H%M%S)"`；`mv "$L" "$B"`，无唯一槽位或 no-clobber 检查。
- 实际影响：mv 静默覆盖旧文件备份；若 B 为目录则会变成向目录内移动，返回的 Backup 不再精确指向该轮前像。违背“先备份、不静默覆盖用户内容”。
- 已复现：`repro_link.py` 固定 date 并预放备份 `PREVIOUS USER BACKUP`，脚本 exit 0 / BACKED-UP / LINKED，但备份字节变为 `NEW LOCAL CONTENT`（`repro-link.log`）。原备份内容丢失。
- 最小修复方向：为前像保留不可冲突的唯一槽位，禁止替换已存在目的；输出实际前像路径。不要只提升时间戳精度来代替排他性。

### F03｜Major｜旧 pending intent 可以重新登记刚剔除的 shared/skill 单元

- 定位：`internal/wslsupply/state.go:334-336`；`internal/wslsupply/reconcile.go:238-247,343-359`；`internal/wslsupply/coordinator.go:1015-1021`。
- 触发：升级前留有 shared/skill 目录交换 intent，当前目标树仍等于 intent after-hash。例如宿主技能根缺失，受管建链被跳过，旧 WSL 投影仍保留；随后 shared scope 的正常 RunUnits 触发 ReconcilePending。
- 证据摘录：加载只调用一次 `pruneRetiredUnits(&f)`；reconcile 对 after-hash 命中调用 `c.applyIntentBaseline(st, op, target)`，该函数无退役门而执行 `st.upsertEntry(entry, target.Generation)`。GetStatus 额外过滤仅有 `retiredPiUnit(entry.Unit)`，未过滤 retiredSkillUnit。
- 实际影响：同进程状态重新显示退役单元为 written，落盘也重新含该登记；直到再次重启才又 prune。清理承诺不成立，旧状态的 pending/恢复链没有与退役面闭合。
- 已复现：`go run .../repro_retired.go`，只用公开 Coordinator API 与隔离持久材料：`before reconcile: units=0 pending=1` → `Disposition:committed` → `after reconcile: units=1 pending=0` → `RESURRECTED shared/skill/alpha outcome=written`。见 `repro-retired.log`。
- 最小修复方向：明确退役 intent 的保全/收口策略，保留恢复证据但禁止重新建立在役登记；读取/对账/落盘使用一致的退役判定。不要粗暴删除 pending 或墓碑来让状态变绿。

### F04｜Minor｜内存快照补偿兜底已不可达，却保留整套恢复代码

- 定位：`internal/wslsupply/compensation.go:16-34`；`internal/wslsupply/skills_submit.go:46-55,88-142,537-550,619-717`。
- 触发：任意新建资源目录撤销句柄需要走“持久 prev 缺失 → 内存快照”分支。
- 证据：全库生产 Go 代码已无 `prevFiles` / `prevSnapshot` 的赋值；被删除的 applySkillUnit 曾是唯一赋值者。现在 `if !h.prevSnapshot` 恒提前返回，`restoreSkillPreimage` 及其 preimage* 辅助链无生产可达调用。`snapshotSkillPreimage` **实际上未删除**，仍在 skills_submit.go:89，只是已无调用；任务书中的“删除后”前提需据此纠正。
- 实际影响：注释继续承诺不存在的兜底，约百余行无测试且不可达的恢复代码制造错误维护预期。资源目录原有持久 prev 恢复仍有效，不能把该问题夸大为当前资源恢复全面损坏。
- 证据方式：静态调用/赋值搜索；原 skills_test.go 的两个恢复快照测试随删除消失，未移植。
- 最小修复方向：按退役设计删除不可达的快照字段/构造/恢复链并更新说明；如确需恢复此能力，应由实施者明确资源路径的快照生产者和对应正式测试。

### F05｜Minor｜技能 face 使用“所有 distro 的最新结果”，不是当前 distro

- 定位：`app_wsl_supply.go:492`；`app_wsl_shared_skills.go:119-147`。
- 触发：进程内 distro A 已核验成功，系统默认 distro 改为尚未核验的 B；或 A/B 均有缓存且较新记录属于另一 distro。
- 证据摘录：`wslSharedSkillsFace()` 无 distro 参数，遍历整个 `wslSharedSkillsRoots` 按 `rec.At.After(latest.At)` 选一条。
- 实际影响：B 的配置状态可显示 A 的“已同步”，B 实际仍是独立技能目录；与每 distro 缓存语义不一致。
- 证据方式：静态路径确认，未运行默认发行版切换集成实验。
- 最小修复方向：传入 GetWSLConfigSupplyStatus 已解析的 distro，只读取其缓存；无记录时 unchecked。

### F06｜Minor｜E2E fixture 未同步退役绑定，新修复按钮在 fixture 下不可用

- 定位：`e2e/wsl-cli-supply-ui.html:50-55`；`e2e/envcheck-management.html:97`；`frontend/wailsjs/go/main/App.js:127-129`。
- 触发：在现有 WSL UI 交互 harness 点击本次新增“检查/修复”。
- 证据：fixture 仍定义 Get/Check/Preview/Apply/Keep/TakeOverWSLSkillsSupply 等旧方法，却未定义 EnsureWSLSharedSkillsRoot。真实新包装直接调用 `window['go']['main']['App']['EnsureWSLSharedSkillsRoot']()`。
- 实际影响：该交互抛 TypeError（由页面显示“检查失败”），fixture 无法再有效验证新技能入口；也不满足“除历史 ignore 复现外无旧符号残留”的要求。旧符号是既有 fixture，本次 API 删除使其失配。
- 证据方式：静态绑定链确认；未运行浏览器，不宣称浏览器实测。`retired-symbols.log` 保存全库搜索结果；注释/CHANGELOG 的历史词汇不算运行残留。
- 最小修复方向：同步 fixture 的绑定表与 skills face，补对应真实组件交互验收。保留仍服务 CLI resources 的类型不是退役残留。

### F07｜Minor｜翻译探测执行失败被归类为 host-missing，而非 failed

- 定位：`internal/platform/wsl_shared_skills_link.go:101-106`；`internal/platform/wsl_paths.go:52-54`。
- 触发：首轮 wsl.exe / distro 启动或翻译脚本执行失败，并非路径不存在。
- 证据摘录：WSLTranslatePaths 在 runner error 时返回空 map；新入口对空映射统一给 `WSLSkillsHostMissing`，因此后面的脚本执行 failed 分支不会收到这种错误。
- 实际影响：运行故障被显示为“宿主目录不存在或未挂载”，并被漏斗缓存；丢失原始错误，不能完全满足“失败如实 failed”。既有翻译 API 的信息损失被本次状态接口暴露。
- 证据方式：静态错误路径确认；macOS 不可执行 Windows 平台正向入口，未伪造真机错误证据。
- 最小修复方向：让该调用链能够区分 runner 失败和经过确认的目标不存在；不要靠空 map 推导故障原因。

### F08｜Minor｜已检查但 host-missing 的 face 仍显示“未检查”

- 定位：`app_wsl_shared_skills.go:142-144`；`frontend/src/views/settings/WSLCLISettings.vue:105,343-355`。
- 触发：明确执行一次检查并得到 host-missing。
- 证据：后端把 host-missing 映射为 unchecked，faceStateText 的默认标签是“未检查”；同时 LastCheck 已填入真实检查时间。
- 实际影响：徽标与执行事实冲突，用户可能误认为按钮没工作。旁边 detail 已明确不可达，故仅列 Minor，不判整个错误面不可用。
- 最小修复方向：给“已检查、宿主根缺失”一个独立且已有样式兼容的展示状态/文案；保留与“从未检查”的区别。

## 逐维度结论与证据

| 维度 | 结论 | 核实范围/定位 |
|---|---|---|
| 1 正确性、边界、并发、安全 | 有问题 F01/F02/F07 | link.go:59-71 的 `-e && ! -L` 后 `elif -L` 能处理悬挂旧链接；同目标链接幂等短路正确。wslShQuote:76-78 使用 POSIX 标准单引号转义，wsl_home.go:43-48 通过 `--exec sh -lc <单参>` 传脚本。中文/单引号/字面 shell 元字符及悬挂链接、幂等已在隔离脚本核实。备份唯一性与跨调用发布未通过。 |
| 输出解析/缺失目标/重试 | 部分通过，见 F07/F08 | link.go:145-166 终行 gate 正确，BACKED-UP/REPLACED-LINK 优先于裸 LINKED；FAIL-LN 不会被前面的附注冒充成功。paths.go:50 存在验证使初始缺失目标不执行建链；测试与解析允许 TARGET-MISSING 后 LINKED，若目标在翻译后消失仍会建悬挂链接并报成功，此 TOCTOU 边界未由真机验证。失败也缓存是明确实现决策，显式 Ensure:93-115 不过缓存可重试，不单独报为缺陷。 |
| 2 退役与保留资源链 | 主链已退役；残留 F04/F06 | coordinator.go:276-283 knownUnit 拒绝退役技能；Plan:308、Apply:389、Preview:1234、Drop:1287、TakeOver:1343 保留资源分支并继续投影 fallback。unified.go:214-225 的 ResourceScope / RequiresConsent 控制流完整。视觉/多模态/插件未见因删除分支引入的回归，全包测试通过。 |
| 3 prune 安全/一致性/兼容 | 有问题 F03；过滤本身已核无误删 | types.go:100-136 仅 retiredPiUnits 显式全集和 shared/skill/ 前缀；retired_units_test.go:15-85 覆盖在役保留/幂等/加载。SettledOperations、pending intents 未被直接删，保全历史合理；但后者可复活登记。schema 仍 v3：没有新格式/新字段，内存过滤后随下次 mutate 持久化，旧二进制可解析，同意登记丢失后旧版可能要求重新同意，不是二进制格式不兼容。 |
| 4 测试有效性/覆盖 | 指定套件全绿，新增链路证据不足 | 新增 platform 测试 99 行主要是脚本字符串、quote、解析、输入拒绝；App 96 行注入成功/host-missing seam；prune 85 行使用真实隔离 JSON。均有正负/保留断言，不是纯负向。Go/Wails API 没有 isError 字段，不能机械套用 pi 工具断言；对应检查状态枚举/真实内容关键词。没有执行完整真实建链 happy path，故未挡住 F01/F02。 |
| 移植测试语义保真 | 所查移植未见断言弱化 | consistency_fixes_test.go:276-310,512-620，commit_recovery_test.go:328-370，review_fixes_test.go:793-828，unified_test.go:398-438，app_wsl_unified_test.go:274-303,632-676：换为 Claude commands/skills 资源单元，取消、CAS、持久恢复、重扫、新增/来源删除保留断言继续存在。回调由两作用域降为一个是在移除真实已退役作用域，不是降低仍存作用域要求。 |
| 删除测试的唯一覆盖 | 存在覆盖流失，不能据此断言生产缺陷 | 原 skills_test.go 的目录链接不递归/目标链接 hash、凭据名链接排除、候选读取后路径身份重验测试也随整文件删除；相应 scanner/readSkillCandidateFiles 仍服务资源链。搜索当前测试没有 readSkillCandidateFiles 的直接调用，scanTargetSkillDir 仅 consistency 中一次取树 hash（:564），未发现等价专门断言。应由实施者移植这些原语测试；内存前像测试则对应 F04 死代码，不应原样恢复。删除旧 S3 真 WSL e2e 符合退役，但没有新链接链路的真机替代证据。 |
| 5 前端/API/生成物 | 类型检查通过；有 F05/F06/F08 | WSLCLISettings.vue:464-493 新入口无 skillsBusy 等悬空引用；WSLSkillPreview、skillStateText/skillBadgeClass 仍用于 CLI 资源，不应清除。App.js:127 / App.d.ts:84 / models.ts:3116-3137 与 Go WSLSharedSkillsRootStatus 的 JSON 字段和 omitempty 类型吻合。Ensure 插入顺序不完全按生成器字典序，空白也有差异，仅生成外观，不影响执行；未运行 wails 重生比较，不声称逐字一致。 |
| 6 生产质量/错误/文案/版本 | 版本与描述基本符合；错误状态见 findings | package.json:4 / frontend/package.json:4 / wails.json:19 都是 1.3.130，CHANGELOG.md:9-19 与实际主改动一致；所审新增功能无 TODO/FIXME/假数据/占位。任务所问“wailsjs 生成物待下次 wails build 重生”这句话 **不在 db0153c 的 CHANGELOG 新章节中**，不能据不存在的句子判不实陈述。本次确实手改生成物，完整生成一致性未实测。 |
| 7 范围纪律 | 已核，无无关改动 | 35 路径均属于新链接、技能退役、状态 prune、接线/绑定/测试、版本说明。agent-outputs/workflow/windows-wsl-unified/repro-resource-cache.go 的资源载体移植属约定连带；未发现扩展至其它产品功能的改动。 |

## 独立复跑（四项均已运行）

所有退出码从各命令完成后即时保存；原始日志及 `.exit` 在本报告同目录。Go 两命令并行，前端两命令并行，不改变命令本体。

| cwd | 原始命令 | 退出码 | 原始关键输出 |
|---|---|---:|---|
| 工作树根 | `go vet ./...` | 0 | stdout/stderr 为空（go-vet.log） |
| 工作树根 | `go test ./... -count=1` | 0 | `ok  amagi-codebox 16.896s`；`ok  amagi-codebox/internal/platform 0.405s`；`ok  amagi-codebox/internal/wslsupply 5.073s`；共 48 个 ok 包、5 个 no-test 包（go-test.log 保留原始 tab 分隔文本） |
| frontend | `npx vue-tsc --noEmit` | 0 | stdout/stderr 为空（vue-tsc.log） |
| frontend | `npx vitest run` | 0 | `Test Files  41 passed (41)`；`Tests  405 passed (405)`；`Duration  712ms` |

补充取证：
- `python3 agent-outputs/review/2026-11-09-wsl-shared-skills/repro_link.py`：exit 0，复现 F01/F02；首次取证检查了错误的 target basename，修正隔离 fixture 为生产同名 skills 后再次运行，上述日志是修正后的真实结果。仅报告目录变更。
- `go run agent-outputs/review/2026-11-09-wsl-shared-skills/repro_retired.go`：首次因 IdentityProbe 签名缺 context 编译失败；核对 probes.go 后仅修正报告脚本并复跑 exit 0，复现 F03。未修改项目测试。
- 全库退役符号 rg：`retired-symbols.log`。生产调用面无被要求删除的旧方法/作用域引用；运行 fixture 残留见 F06，历史 ignore 复现和说明性文字不误算生产路径。
- `git diff --check d06bb60..db0153c`：exit 2，指出 app_wsl_shared_skills_test.go:96 EOF 空行、models.ts:3123/3127 尾随空白。生成物既有风格也有 tab/空格，不作为阻断或纯风格 finding。
- 取证中两次 rg 含不存在的 CONTRIBUTING.md / resources_submit_test.go / recovery*.go，返回路径错误；已改以真实 docs/developer/testing.md、resources_test.go、reconcile.go 核查，不把这些搜索失败算验证通过。

## 盲区、可信度与交接

- 四项命令结果可信，但运行宿主为 macOS；Windows 专用测试不在本次默认 go test 中，不能宣称 WSL 新入口已真机通过。未执行 Windows 编译/真实 wsl.exe、真实 pi Agent Skills 扫描、wails build 或绑定重生、浏览器交互/视觉验收；这些为 **UNVERIFIED**。
- shell 取证使用项目当前脚本、隔离 HOME 与可控 date/ln 接缝；不是 mock 成功结果。它验证 POSIX 文件系统语义和反例时序，不代替完整生产进程链。Go 取证使用合成历史状态 fixture，仅证明真实公开恢复链的状态行为。
- 建议先修 F01/F02/F03，再由实施者落正式回归：并发启动/显式检查、已有备份碰撞、退役 pending 对账；补仍在役 scanner/候选重验测试，并补新技能根真实 Windows/WSL 读取冒烟。检验者未写项目测试或修复 patch。
- 回滚：本轮仅新增 `agent-outputs/review/2026-11-09-wsl-shared-skills/` 报告、脚本、日志、隔离材料；若不需保留可由 Leader 授权删除该准确目录，无被审源码需回滚。

本 VERDICT 仅覆盖声明的 diff 和上述实际核实范围。
