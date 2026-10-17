---
标题: amagi-pi 实战评价——WSL 升级目标闭环（审计→G0/G1→Windows 真机）插件摩擦复盘（W 阶段收口）
日期: 2026-10-17
会话: amagi-codebox WSL 升级审计与闭环推进（约 20h：2026-10-16T17:23Z → 2026-10-17T13:23Z；28 次 task_submit，其中 3 次 dryRun、1 次拒单、24 个真实子任务；commit 0——未获授权）
基线: amagi-pi 当前安装版本（本会话实测；PI_MODEL=claude-opus-5-5）
证据来源: 会话轨迹 $PI_SESSION_FILE（01a145bd-…jsonl，6.99MB）机器统计；夹具摘录在本目录 *.txt；真机日志在 agent-outputs/workflow/wsl-goal-closure/
---

# WSL 目标闭环·插件摩擦复盘

## 0. 会话概况（为什么这次复盘有代表性）

一次长链路、多角色、跨机器的会话：只读审计 → fuxi 契约 → 多个 luban worktree 并行（G0/G1）→ Leader 中转 delta patch 集成 → diting 审核 → 经 SSH 在 Windows 真机复跑 → 修复 → 接受。期间做过一次上下文压缩，压缩后继续推进。

**工具统计（jsonl 机器统计，分母为该工具调用数）**

| 工具 | 调用 | 失败 | 失败率 |
|---|---|---|---|
| bash | 161 | 5 | 3.1% |
| read | 144 | 2 | 1.4% |
| task_control | 73 | 1 | 1.4% |
| run_bg | 71 | 0 | 0% |
| edit | 59 | 1 | 1.7% |
| write | 29 | 0 | 0% |
| task_submit | 28 | 1 | 3.6% |
| task_query | 25 | 0 | 0% |
| task_resume / amagi_resource | 2 / 2 | 0 | 0% |
| **合计** | **594** | **10** | **1.7%** |

另有 1 次失败没被统计到：远端 PowerShell ParserError 被 `| tee` 掩盖，返回码为 0（见 §2.4）。

**子 Agent**：baize 2、diting 6、fuxi 2、luban 14（9 个 worktree）。共发起 17 次 accept，stale 拒绝 0 次。2 个子任务以 error 收尾：diting 44954ee3 被 provider 拦截；另一个 error 计入通知计数。真实子任务中有 16 个的结算停在 `settlement=incomplete`，唯一原因是自动基线检查 not-run。

**交付健康度**：Windows 真机单元测试 230 PASS / 0 FAIL / 7 SKIP（windows-wslsupply-real-tests-5.log），真实 WSL E2E 6/6 PASS 且无残留（windows-wslsupply-real-e2e-4.log），主树全仓 vet、test 和 Windows build 均 exit 0（winfix-integrated-full-go.log）。

| 机制 | 是否触发 | 结果 |
|---|---|---|
| task_submit worktree / dryRun | 是（9 / 3） | dryRun 3 次全部预检通过，未产生副作用 |
| delivery=handle + 完成通知 | 是 | 等待期间并行完成真实 E2E 的排障 |
| accept 栅栏（receiptId+revision） | 17 次 | 0 次 stale |
| 管道退出码软提示 | 是 | 抓到一次真实的退出码掩盖 |
| 压缩保真 | 是 | 句柄、约束和补丁顺序都保留下来，压缩后无返工 |
| steer | 1 次 | 任务已终态，按契约拒绝 |
| run_bg | 71 次 | 0 失败 |

## 1. 阻塞级问题

### 1.1 provider 安全拦截使独立审核 Gate 停摆，插件没有可执行的恢复分类 ⭐ P1
- **现象**：diting full 审核 44954ee3 于 09:30Z 以 error 结束，原因是上游返回 “This content was flagged for possible cybersecurity risk”。审核对象是 symlink、ACL、竞态、no-replace 这类安全加固 diff，属于正当工作。到会话结束（13:23Z，约 4h）H Gate 仍未恢复；在“不静默换模型、不原样重试”的约束下，Leader 只能把它挂成待裁决项。
- **根因**：provider 层的内容拦截（外部）被插件归为普通 error。recoveryAdvice 没有给出“改写任务书措辞”或“显式披露并切换 agents.diting.model”这类针对性路径，task_resume 也不接受模型覆盖。
- **改进方案**：① 把 provider-safety-refusal 识别为独立的 execution 降级标签，并给出专门的 recoveryAdvice；② 提供一条显式且留痕的模型回退通道（回执里记录原模型和新模型），满足“不静默换模型”的要求；③ 在任务书预检里提示可能触发拦截的措辞。
- **责任归属**：外部 provider + 插件（缺少分类和恢复通道）。
- **证据**：reflect/provider-safety-refusal-44954ee3.txt。

## 2. 摩擦级问题

### 2.1 自动基线测试检查在 Go 仓库探测不到命令，每个子任务都落 incomplete（P1）
- **现象**：会话中出现 13 次“测试 项目基线 → 未探测到测试命令（0.0s）”。16 个子任务因 `chk-auto-baseline-test-*` not-run 停在 settlement=incomplete；17 次 accept 中有 8 次必须 waive 这项检查，每次都要先 task_query 拿 receiptId，再写 waive 理由。实际上 luban 每次都报告了 go test -race、vet、交叉编译的实跑结果。2a02a779 另因 vitest run 形态和 node_modules 跨根无法快照执行。
- **根因**：基线探测没有识别 go.mod 对应的 `go test ./...`；worktree（依赖仅主树可见）里的 npm 基线也无法执行。检查结果永远是 not-run，信号价值为零，只增加接受成本。
- **改进方案**：① 探测器补 Go（go.mod → `go test ./...`，可按改动包缩小范围）；② 能从子任务回执里识别出实跑命令和退出码时，采信为基线证据；③ 探测不到时不把它标成 required。
- **责任归属**：插件。
- **证据**：reflect/baseline-check-notrun.txt（64 行摘录）。

### 2.2 worktree 只能以 HEAD 为 base，大量未提交主树状态要靠 delta patch 人工中转（P1）
- **现象**：9 个 worktree 的 base 全是 58e7577，而主树带着 73 个 tracked 文件、+7571 行的未提交改动和 46 个未跟踪文件。为此 Leader 维护了 10 余个 delta patch 的应用顺序（resumed-input → … → consistency-fixes → winfix → leader-followup）。luban 04f892b9 起步时要自行用 `git diff HEAD` 加未跟踪文件重建基线，并逐字节核对（见 windows-wslsupply-fixes.md 第 4 行），还出现了“主树并发漂移”需要排除的情况。worktree 回执的 diff 是相对 HEAD 的 374 行，不是本次净增量，Leader 还得自己导出净增量。
- **根因**：workspace 没有“从主树当前工作区状态快照”的模式。插件已正确提示“非本次净增量”，但没有提供净增量本身。
- **改进方案**：① 增加 `workspace.base=dirty-snapshot`，创建时把主树 tracked diff 和选定的未跟踪路径落成 base commit；② 回执的产物摘要给出相对该 base 的净 diff。
- **责任归属**：插件（能力缺口）。不是 Agent 失误：用户不允许 commit，所以无法先提交再开 worktree。
- **证据**：windows-wslsupply-fixes.md 第 3–4 行、第 76 行（漂移）；04f892b9 完成通知里的“相对 HEAD，非本次净增量”。

### 2.3 inputPaths 相对路径基准和 taskFile 不一致，失败的提交仍遗留 worktree（P2）
- **现象**：11:47:55Z，`inputPaths:["agent-outputs/…/windows-wslsupply-real-tests-3.log"]` 被拒为 required-input-missing。但这个文件 11:47:07Z 已在主树存在（主树未跟踪文件，HEAD 中没有）。同一次提交里，同目录下 11:47:48Z 才写出的 taskFile 用相对路径却被接受。改成绝对路径后 13s 内提交成功。失败那一轮已经创建了 worktree，并返回 `effects=unknown` 和“现场保留”。
- **根因**：taskFile 按调用者 cwd 解析，inputs 按 workspace（新 worktree，未跟踪文件不存在）解析，两个基准不同，错误信息也没说明用的是哪个基准。另外，输入校验排在 worktree 创建之后。
- **改进方案**：① inputs 统一按调用者 cwd 解析，或在错误中给出“已解析基准”和调用者 cwd 下的候选路径；② 先校验输入再建 worktree，校验失败时 effects=none。
- **责任归属**：插件。
- **证据**：reflect/required-input-relative-path.txt；文件 mtime（实测）。

### 2.4 bash 退出码语义：末段 grep 无匹配被判错；`| tee` 掩盖失败时没有提示（P3，插件可防呆）
- **现象**：bash 的 5 次失败里有 2 次（12:09Z、12:10Z）是假失败：前面的真机 30 轮、全仓 test 都是 exit 0，只因为末段 `grep -c FAIL` 或 `grep -v` 没有匹配返回 1，整条命令被标为 error，软提示还写着“保留并阅读上方原始错误”。另一方向，linkprobe 那次远端 PowerShell ParserError 经 `| tee` 后返回 0，没有触发管道退出码提示，而之后 `git apply --check | tail` 的掩盖被正确提示了。
- **根因**：首要是 Agent 失误（命令的退出码语义写得不严谨）；插件的提示覆盖不一致（tee 不在检测范围内，grep 无匹配也没有专门说明）。
- **改进方案**：① 末段是 grep 且退出码为 1、输出为空时，附注“grep 无匹配，可能不是左侧失败”；② 透传命令列表加入 tee。
- **责任归属**：Agent 为主，插件可低成本防呆（降半级）。
- **证据**：reflect/bash-grep-nomatch-false-error.txt；windows-linkprobe 首次调用的输出（ParserError）。

### 2.5 单任务 wait/accept 返回首行 details 投影为 `?`（P3）
- **现象**：82 个 task_* 返回的首行 `[details …]` 含 `revision=? execution=?` 这类占位，其中包括单句柄的 `task_control wait 04f892b9`，而同一返回正文的内核视图有真实值（revision=0、done）。列表型查询为 `?` 尚属合理，单句柄为 `?` 则会误导。
- **根因**：插件的投影层在 wait 和列表路径上没有回填单任务字段。
- **改进方案**：单句柄结果用内核视图回填 details；列表型标成 `taskId=(list)`，不再用 `?`。
- **责任归属**：插件（展示层）。
- **证据**：reflect/wait-header-placeholders.txt（40 条摘录）。

## 3. 失败深度分析

### 3.1 失败全景统计（共 10 次，另计 1 次被 tee 掩盖）
| 类别 | 次数 | 典型案例 | 责任归属 |
|---|---|---|---|
| 运行环境/外部依赖 | 3 | SSH config 不存在；用户名含非 ASCII 被 ssh 拒；No route to host | 正常成本（之后用 winpc-ssh UTF-8 wrapper 解决） |
| shell 语义陷阱 | 2 (+1) | grep 无匹配 exit 1；PowerShell 需要 `&` | Agent（插件可防呆，见 2.4） |
| 工具参数误用 | 3 | read offset 越过 EOF ×2；edit oldText 不匹配 ×1 | Agent（报错清楚，1 轮恢复） |
| API/契约试错 | 1 | steer 终态任务 → steer-rejected | Agent（契约清楚，正常成本） |
| 路径计算错误 | 1 | inputPaths 相对路径基准不一致 | 插件（见 2.3） |

**上下文缺失型失败**：1/10（inputPaths 基准），这是插件能系统性消除的部分。其余 9 次中，3 次是外部环境，6 次是 Agent 自身问题，且都在 1 轮内恢复。

### 3.2 逐类改进（按 ROI）
P1：2.1 基线探测 → P1：2.2 dirty-snapshot worktree → P2：2.3 路径基准统一 → P3：2.4、2.5。

### 3.3 反向观察
- 同类失败第二次发生：read offset 越界发生 2 次（08:51Z、10:38Z，同一文件），其余类别都只发生 1 次。平均恢复轮数约 1。
- 失败率低（1.7%），但真正的损失集中在不算“失败”的地方：16 次 incomplete 结算、10 余个 patch 中转、H Gate 停摆 4h。这些不在工具失败率里，必须单独度量。
- 子 Agent 质量（非插件）：luban 04f892b9 第 5 项的“A 类修复”（filepath.IsAbs）没有真机证据，被 Leader 用真机探针证伪。这是使用者层面的推断不足，不记为插件债。

## 4. 顺手但可以更好的地方（正向面）

1. **管道退出码软提示拦下真实掩盖**：Leader 用 `git apply --check | tail` 判断补丁可应用，提示指出末段会覆盖退出码，改为直接执行后得到 check-exit=0，避免一次可能的假结论。可更好：覆盖 tee（见 2.4）。
2. **压缩保真**：压缩摘要保留了句柄状态、补丁应用顺序、凭据和 SSH 红线、远端验证根和 manifest。压缩后直接完成派发、集成、真机 5 轮、接受，没有因为丢失上下文而返工。
3. **delivery=handle + 完成通知真正支撑并行**：luban 04f892b9 运行期间，Leader 在 Windows 跑了真实 WSL E2E，发现并修正了一处写反的断言，并补了 distro 残留清理，两条线互不阻塞。
4. **run_bg 71 次 0 失败**，exit=124 等业务失败都如实呈现（bg-57a43466），没有被包装成成功。
5. **accept 栅栏**：17 次 accept 都带 receiptId 和 revision，0 次 stale；既有条目 ev-20261014-4f7500（幽灵 stale）本会话没有复现。
6. **dryRun**：3 次 worktree 派发前预检通过、零副作用，并列出将要创建的分支和 base。可更好：dryRun 时指出 base=HEAD 与主树脏状态之间的差距（关联 2.2）。

## 5. 改进清单汇总

| # | 改进 | 优先级 | 预期收益（本会话数据外推） | 涉及 | evolution 映射 |
|---|---|---|---|---|---|
| 1 | provider 安全拦截分类 + 显式留痕的模型回退通道 | P1 | 让被拦截约 4h、仍停摆的 H Gate 有可执行的恢复路径 | 回执降级标签 / recoveryAdvice / resume | ev-20261017-88cb27 |
| 2 | 基线检查：Go 探测 + 采信子任务实跑证据 + 探测不到不设 required | P1 | 消除 16 次 incomplete 和 8 次 waive 往返 | 自动证据采集 / 结算 | ev-20261017-ece384 |
| 3 | worktree dirty-snapshot base + 相对 base 的净 diff | P1 | 免去 10 余个 delta patch 中转和子任务自建基线核对 | workspace / 产物摘要 | ev-20261017-cd233f |
| 4 | inputs 与 taskFile 统一路径基准；先校验后建 worktree | P2 | 消除 1 次拒单和 1 个残留 worktree（effects=unknown） | task_submit 预检 | ev-20261017-348337 |
| 5 | bash 提示：grep 无匹配说明 + 透传命令加入 tee | P3 | 2 次假失败 + 1 次漏检 | bash 诊断 | ev-20261017-6d2825 |
| 6 | 单句柄 details 投影回填，不再显示 `?` | P3 | 82 个返回首行不再自相矛盾 | 返回投影 | ev-20261017-589e6d |

**豁免未沉淀（6 条）**
- SSH 三连失败（无 config、非 ASCII 用户名、无路由）：外部环境的正常成本，已由项目侧 winpc-ssh 解决，与插件无关。
- read offset 越界 ×2、edit oldText 不匹配 ×1：Agent 自身问题，插件报错已清楚给出总行数和解析基准，1 轮内恢复，没有低成本防呆点。
- steer 终态任务被拒：契约行为正确，错误码清楚，属于正常成本。
- luban 推断性修复被证伪：子 Agent 推断质量问题，不是插件机制问题。
- ev-20261014-4f7500（幽灵 stale-receipt）：本会话没有复现，不追加证据，也不新开条目。
- 子任务 error 计数中除 44954ee3 以外的那一次：轨迹中没有找到独立的失败原因证据，按“零证据不落笔”不入队。

## 6. 一句话总结
工具层很稳（594 次调用失败率 1.7%，run_bg 和 accept 栅栏零失误，压缩保真有效），损失集中在结算和工作区层面：基线检查没有信号（16 次 incomplete）、worktree 不支持未提交主树（10 余个 patch 中转）、provider 拦截没有恢复通道（Gate 停摆 4h）。下一步优先做 #2 和 #3，它们按次数线性节省 Leader 轮次。
