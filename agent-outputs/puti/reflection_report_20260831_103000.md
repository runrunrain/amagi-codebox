# 反思报告：共享工作区批量写盘误杀事故（go fmt 整包 + 黑名单还原）

- 报告人：菩提（Thinker，独立节点）
- 日期：2026-08-31（事件发生于 2026-08-30 diting findings 修复批次；时间戳为任务执行会话时刻，本环境无实时时钟，取近似值）
- 级别：P1（未提交工作区数据丢失，可恢复，实际损耗约 30 分钟 + 2 次 Agent 调用）
- 手册条目：`EP-BATCH-WRITE-SNAPSHOT-WHITELIST-20260830`（action: add，section: error-patterns）
- 关联：amagi_evolution `ev-20260830-2c709d`（同事件沉淀）

## 一、触发来源

Leader（天城/amagi 协作框架）在 workflow `fix-extensions-flash-and-wsl-pi-config` 的 diting findings 修复批次中触发。关键定性依据：**同型事故在同会话内已发生两次**——SubAgent（luban）侧先发生一次（bugA-report.md §4.1：`go fmt` 整包 35 文件幻影重写 + checkout 还原，当时因还原前确认工作区无其他在途改动而侥幸无损）；Leader 侧重演时工作区恰有两个并行切片的合法未提交改动，同一套操作造成真实损失。两次重演证明这是**流程缺陷而非个体失误**，满足沉淀门槛。

## 二、事件描述

- **事故前状态**：两个并行 SubAgent 切片（P1-A 闪窗修复 + P1-B WSL pi-config）的未提交改动（10 个 modified 文件 + 多个 untracked 新文件）与用户基线共存。
- **事故链**：
  1. Leader 为验证 3 个手改文件的 gofmt 合规性，执行 `go.exe fmt ./internal/launcher/ ./internal/platform/`——`go fmt` 带包路径时作用于**整包**而非指定文件，波及 28 文件写盘（gofmt 对含 CJK 注释文件的列对齐重排 + 行尾归一化制造大量零内容差「幻影 M 标记」）；
  2. 随后用黑名单排除式脚本还原「非目标文件」：`git status --short | grep "^ M"` 逐个 `git checkout`，仅白名单 3 个目标文件被保留——但该清单同时含有切片的合法 modified 改动，全部被一并还原。
- **直接损失**：app.go（140 行 WSL 分支）等 10 文件的合法改动丢失。
- **恢复（三路）**：untracked 新文件天然幸存（`git checkout` 不触及）+ Leader 手补 6 个 diff 仍在上下文中的文件 + 2 次 resume 由原 Agent 基于其会话上下文逐字重建，最终以 diff-stat 逐文件行数比对验证与事故前对齐（终态 12 files 196+/37-，见 workflow.md 修订日志「事故恢复闭环」）。
- **Expected vs Actual**：
  - Expected：验证 3 个手改文件的 gofmt 合规性，工作区其他改动不受影响。
  - Actual：整包 28 文件被重写；随后批量还原误删两个切片的全部合法改动，需三路恢复才复原。

## 三、蝴蝶循环分析（5 轮）

### 第 1 轮：现象——发生了什么？
「验证格式」这一只读意图，通过 `go fmt <包路径>` 变成整包写盘；「还原污染」这一修复动作，通过黑名单排除式脚本变成对全部非白名单 modified 文件的清除。两次操作叠加，把 28 文件的幻影污染放大为 10 文件的真实丢失。放大链的关键在第二跳：git status 中「fmt 污染」与「切片合法改动」完全同形（未提交 modified），来源信息在 git 层不存在。

### 第 2 轮：根因——为什么？
1. **工具作用域错觉**：`go fmt`（无参数/带包路径）作用于整包且写盘，与操作者心智中的「验证 3 个文件」不符——命令的隐式作用范围超出显式意图；
2. **状态不可区分**：`git status --short` 无法区分 modified 的来源（fmt 污染 vs 切片合法改动 vs 用户基线）；
3. **排除式还原的错误成本不对称**：在状态不可区分时，「全量 checkout + 白名单豁免」的脚本只能靠白名单完整性兜底，漏列即误杀一切；
4. **无快照锚点**：批量写盘前没有 `git stash create` / commit 建立回退点，一旦污染发生，就没有干净的可对照基线，只能靠「上下文记忆 + Agent 会话重建」这种昂贵且不完整的手段恢复；
5. **流程层缺失**：luban 首次事故后未沉淀防护规则，同会话 Leader 侧即重演——教训没有结构化就没有传播。

### 第 3 轮：解决——怎么做？
- **事后恢复**（已完成）：三路恢复 + diff-stat 逐文件比对验证；workflow 增加 P2R 阶段显式记录事故恢复闭环。
- **正确做法**（沉淀为门禁）：
  1. 批量写盘工具（fmt / 批量重命名 / grep 批量替换）执行前，先 `git stash create`（创建悬空提交，不修改工作区、不动 untracked）或直接 commit，建立快照锚点；
  2. 还原必须用显式文件白名单逐文件 `git checkout -- <path>`，禁用「grep status 全量 checkout + 黑名单排除」式脚本；
  3. 格式**检查**与**修复**分离：检查用 `gofmt -l <files>` 只列不改（输出为空即合规），仅对确认的目标文件执行 `gofmt -w <file1> <file2>`。

### 第 4 轮：抽象——能泛化吗？
- **元规则**：当批量操作建立在「不可区分状态」之上时，任何基于该状态的推断都是赌博——防护必须前置为结构性机制（快照锚点 = 无条件安全网；白名单 = 显式枚举替代隐式推断；只读检查 = 检查永不产生副作用）。
- 适用面超出 go fmt：sed 批量替换、IDE 重构重命名、`git clean`、包管理器清理（与 EP-HOMEBREW-UNINSTALL-AUTOREMOVE-20260520 同族——工具副作用超出显式目标），均可套用三重门禁。
- 反例/边界：单人干净工作区（无其他未提交改动）中黑名单还原不会造成损失——但这是状态依赖的运气而非流程安全；三重门禁成本极低（stash create 一条命令），应无条件执行。

### 第 5 轮：验证——规则有效吗？
- **反面证据即正面证明**：luban 事故中「还原前逐文件人工核对 diff」曾兜底成功（当时工作区恰好无其他切片改动），Leader 重演时同一核对方式失效——证明人工核对不可靠，必须换成结构性防护；
- **恢复路径有效性已验证**：resume 上下文重建经 diff-stat 逐文件行数比对与事故前对齐，证明「快照锚点 + 可比对基线」是可靠验证手段（事故中我们付出代价才拿到基线，规则让基线免费）；
- **待未来验证**：下一次批量工具操作前执行 `git stash create` 并在还原时使用白名单，若成功避免误伤，可作为晋升 active 的 promotion 证据。

## 四、提取洞见

1. **快照锚点先行**：多源未提交改动的工作区中，任何批量工具执行前先 `git stash create` 或提交快照——安全网必须在风险动作之前建立。
2. **白名单还原**：批量还原必须正向枚举目标文件逐个 checkout，禁用黑名单排除式脚本——「不可区分状态 + 排除式脚本 = 误杀放大器」。
3. **检查与修改分离**：`gofmt -l` 只列不改，仅对确认目标文件 `-w`——只读验证命令永远优先于写盘命令，命令作用范围显式枚举优先于包路径隐式推断。
4. （元洞见）**教训不结构化就不传播**：同会话两次重演证明依赖个体记忆/报告旁注不可靠，必须进手册成为可检索条目。

## 五、Delta 更新

- **查重**：Leader 已用 `amagi_handbook query` 验证 gofmt / 还原 / 快照 / 工作区 关键词全部 0 命中；Thinker 全文复核手册确认无等价条目——最近似的 `EP-HOMEBREW-UNINSTALL-AUTOREMOVE-20260520` 是包管理器隐式清理副作用问题（防护靠环境变量开关），本次是工作区状态不可区分 + 排除式还原误杀问题（防护靠快照+白名单+只读检查），语义与门禁均不同，**不合并，走 add**。
- **操作**（严格增量，无删除/重写/批量修改）：
  - `add`：error-patterns 追加 `EP-BATCH-WRITE-SNAPSHOT-WHITELIST-20260830`（status: candidate，defaultLoad: false，含 lifecycle 与 6 条 validationGate；example 引用真实事故链与三路恢复）；
  - 账本同步：`metadata.totalEntries` 53→54、`metadata.statusCounts.candidate` 12→13、`lastUpdated`→2026-08-31。
- **过程事故（透明记录）**：首次写入时 edit 的替换文本在 insight 字段后截断，短暂产生非法 JSON，被 amagi JSON 守卫拦截告警；立即以最小修复补全条目剩余字段并恢复 `tool-usage` 段头，二次写入守卫静默通过（即合法）。未影响任何既有条目。
- **路径偏差说明**：技能模板默认输出到 `projects-memory/projects/<activeProject>/agent-outputs/puti/`；本任务明确指定输出至 `X:\WorkSpace\amagi-codebox\agent-outputs\puti\`（git-tracked archive），遵循任务指定。条目 source 字段已按此路径回链本报告。

## 六、验证计划

1. **已验证（本会话）**：
   - JSON 合法性：amagi JSON 守卫二次写入静默通过；
   - 结构完整性：读回确认新条目字段完整、`tool-usage` 及后续 section 结构未损；
   - 账本一致：totalEntries=54 / candidate=13 / lastUpdated=2026-08-31 均已生效；
   - 可检索性：本 pi 环境无 `amagi_handbook` 工具，以 grep handbook.json 等价验证——新条目 id `EP-BATCH-WRITE-SNAPSHOT-WHITELIST-20260830` 及关键词「快照锚点」「白名单还原」「gofmt -l」均可命中（见终稿验证表）。
2. **待未来验证（晋升 active 条件）**：再在一个多 Agent 共享工作区任务中成功复用三重门禁（stash create 快照 + 白名单还原 + gofmt -l 只读检查）避免批量工具误伤。
3. **降级条件**：若工作流改为每切片独立 worktree 或自动 commit 基线（结构性消除多源未提交共存），条目降级 dormant 并注明替代机制。
4. **验证建议（给 Leader）**：后续 diting/执行 Agent 的修复批次任务书可引用本条目 id 作为工具纪律锚点；grep 检索关键词建议：`gofmt`、`stash create`、`白名单还原`、`幻影`。
