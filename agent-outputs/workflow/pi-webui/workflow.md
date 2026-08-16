# workflow: pi-webui 实现（M0 起）

> **终态（2026-05-12）**：M0-M2 全部完成，已合入两仓 main（主上 D-9），后续统一 main 开发。本文为历史编排记录，证据与总验收矩阵见下文；后续迭代入口见蓝图 `交付/下一会话开发提示词.md@v2.0`。

依据：`/Users/maorun/maorun-workpace/Database/01-AI技术与工具/codebox优化/pi的webui实现/交付/下一会话开发提示词.md@v1.1`
权威计划：蓝图 `技术实现/pi的webui实现-执行计划（需回填）.md@v1.1`（本文只做编排状态，不复制其内容）
自治级别：unattended（主上指令"开始方案实现，使用编排模式"）

## 目标与总验收

按蓝图执行计划完成 M0→M1（→M2 若输入可行）全部退出条件；每个任务真实验证并回填执行计划。总验收 = A-1~A-6（蓝图需求 §5）。

## Phase 表（对齐执行计划里程碑）

| Phase | 目标 | 节点（Agent） | 依赖 | 状态 |
| --- | --- | --- | --- | --- |
| M0-W0 | 三 spike 并行 | t02-server(08d95370) · t03-input(2b25a686) · t04-iframe(bdaf40a7)，均后台 worktree | 无 | **完成**（全可行：TD-1/3/8 成立；sendUserMessage 强档位；R-1/R-2 解除） |
| M0-T01 | 契约 v1 定稿（吸收 spike） | T-0.1 luban 串行 | W0 全完 | **完成**（v1.0.1a；快审 FIX 8 项→Leader 直修+收口；Luban 修复两轮未落盘已记入风险） |
| M0-I0 | M0-GATE 集成核对 | Leader | T-0.1 | **完成**（2026-05-12，蓝图执行计划已回填 v1.2） |
| M1-LaneA | amagi-pi webui 模块+页面 | T-1.1→1.2（laojun，webui11）·T-1.3（luban，webui13，已合入 webui11）·T-1.4（laojun=70768d4e，A-6+README，进行中） | I-0 | 进行中（T-1.1/1.2/1.3 完成，T-1.4 运行中） |
| M1-LaneB | codebox 壳集成 | T-1.5→1.6（luoshen=7b98bbd5，webui15） | I-0 | **完成**（A-4 五态真实验证） |
| M1-I1 | E2E A-1/A-2/A-4/A-6 + 回归 | Leader（8921e7db）+diting（65e2c908）+R1（7b1ace67/e428850d）+R2-E（560b6f23）+R2-F（Leader 直修 4 项+2 测试） | LaneA/B | **完成**（M1-GATE 通过：E2E 全绿、安全复验 10 项、回归 1029+48/34 包/-race 净） |
| M2 | 输入接管 + 打磨 + 全量验收 | T-2.1+2.2（laojun=0f8f0fb0 完成）·T-2.3（luoshen=e593a14f 完成）·T-2.4（luoshen=c1b4cb3d 完成） | I-1 | **完成**（M2-GATE 通过：全量 E2E+回归全绿；BUG-1 allow-forms 修复；OBS-1→R-7） |

## 并行与隔离

- W0 三节点互不依赖，同 batch 派发；T-0.2/T-0.3 同在 amagi-pi 仓 → 各自 worktree（webui02/webui03）；T-0.4 在 codebox 仓 worktree（webui04）。
- 任务包（自包含契约）：本目录 `tasks/T-0x.md`。

## 专家 Gate

- M1 完成集成后 diting 审一次阶段 diff：已执行（65e2c908）——E2E 全绿但审出 1 Critical（回环端口跨源可达：无 Origin/token 防护）+3 Major（探测误采纳/503 回退积压/分页边界）+3 Minor → VERDICT FIX，M2 BLOCK。R1 修复批次双节点并行；修复后仅允许一次增量复审（只看新增 diff）。
- fuxi 不调用（方向已由蓝图冻结）。

## 风险与修订日志

- R-luban-model：路由记忆 luban 主模型历史 0% 成功（230 次）；若 W0 节点连续瞬时失败，改派 luoshen 或指定可用模型重试，并记录于此。
- 2026-05-12 建档，派发 W0（三后台任务：08d95370/2b25a686/bdaf40a7；worktree webui02/03/04）。注：batch 模式不支持 worktree/cwd 键，改三单任务后台派发，等效隔离。
- 2026-05-12 W0 回流：三 spike 全部"可行"，证据齐（报告+帧流+截图）。R-1/R-2 解除；T-0.4 任务态 error（输出帧截断）但实质完成（结论/报告/截图/清单齐），按完成验收。spike worktree 保留待合并（代码入 T-1.x 实现时参考/复用）。执行计划已回填（§3.1/§6/§7）。派发 T-0.1。
- 2026-05-12 T-0.1 回流：契约 v1（394 行）落盘 worktree amagi/webui01。diting-quick 快审 VERDICT: FIX（7 Major+1 Minor）→两轮 Luban 修复派发均未落盘（模型只说不做，生成数百秒零工具调用）→ Leader 直接修复 v1.0.1（8 项全处置）→ 增量复审 ① -⑥⑧闭合 + 2 新 Major（queue 载荷/pendingCount 阶段归属）→ Leader 收口 v1.0.1a。教训沉淀：luban(amagi-glm) 对大文档精准编辑类任务可雄性差，后续此类任务优先 Leader 直改或指定其它模型。I-0 完成，M0-GATE 过。
- 2026-05-12 M1 派发：三节点后台 worktree——LaneA-server（webui11，T-1.1+1.2）、LaneA-page=9f36df7e（webui13，T-1.3）、LaneB-shell（webui15，T-1.5+1.6）。契约引用路径：worktree amagi/webui01 内 docs/webui-protocol.md。
- 2026-05-12 M1 首轮回流处理：LaneA-server(670ca344) 任务态 done 但 worktree 零实现文件（代码留在消息内，luban/amagi-glm 第三次不落盘失败）→ 判未交付，改派 laojun=6cde8992 接手（同任务包，强调落盘自检）。LaneB-shell(247f061d) 429 限流零副作用 → 重派 luoshen=7b98bbd5。LaneA-page(9f36df7e) 仍在跑，回流后同样先验盘。另：amagi-pi 主仓出现外来改动（bash-override/edit-override/b1/b3 测试等，非 webui 相关，非本 workflow 节点所写）——不碰，记录在案。
- 2026-05-12 M1 二轮回流：laojun=6cde8992 真交付 T-1.1/1.2（webui 模块四文件+双测试，1025/1025 绿）；luoshen=7b98bbd5 真交付 T-1.5/1.6（Go 服务 12 测试+全量 34 包+vue build+wails build+A-4 五态 wails dev 实证）；luban=9f36df7e 真交付 T-1.3（页面子项目，vitest 30/30，设计稿逐项对照，marked 18.0.9 锁版；有趣：同模型不同任务包表现迥异，不落盘问题疑似与大文档精准编辑类任务相关，新代码文件写入正常）。
- 2026-05-12 Leader 集成：webui13 页面+static+build:webui 合入 webui11（amagi-pi 集成现场）；集成点验证过（server 静态路径↔产物路径、MIME、路径穿越防护）；构建✓，npm test 1025/1025。派 T-1.4（laojun=70768d4e，A-6+README）。
- 2026-05-12 T-1.4 回流：A-6 全过（静态服务/历史/实时流/CORS/sandbox/生命周期/退出清理；发现并修 4 个真实集成 bug：静态目录、ACAO 静态、[hidden] 覆盖、replay 水位去重）；npm test 1026/1026。M1 六任务全完。
- 2026-05-12 I-1 M1-GATE（luoshen=8921e7db）：A-1/A-2/A-4 真实跨仓 E2E 全 PASS（settings 受控例外零残留，md5 一致）；两仓回归全绿。登记项：①AMAGI_SUBAGENT env 链式传染导致 webui 不注册（环境归因，已解；建议 codebox 防御性剔除，入 R2）②现场披露：早期 pkill 误杀生产 2 个 pi 会话（无损失，主上可宣）。
- 2026-05-12 diting 阶段审（65e2c908）：E2E 绿但审出 1C（回环端口跨源可达：无 Origin/token）+3M（探测误采纳/503 积压/分页边界）+3m → FIX/M2 BLOCK → R1 双节点修复（7b1ace67 laojun=契约 v1.0.2+鉴权 / e428850d luoshen=webui15+分页），全绿。
- 2026-05-12 R2 收口：R2-E（luoshen=560b6f23）复验全 PASS（A-1/A-2 token 链路、安全复验 10 项全 403 拒、A-6 回退链、A-4、双仓回归、wails build 再生签名）；diting 增量复审（498cbebe）7 项中 5 CLOSED，未闭合 4 小项（Minor6 adapter 路径/Minor7 文件淘汰/新 Major ended 复活/新 Minor token 补读）由 Leader 直修（service.go 终态栅栏+probe.go file 字段+淘汰删除+app.go adapter 清理+token 同端口补读；新增 2 测试 -race 过；34 包 ok）→ M1-GATE 通过。worktree 归档事件：harness 归档删 webui11，改动保全在分支 amagi/webui11@4b4bcde；Leader 已重建现场并 1029/1029 复活验证。
- 2026-05-12 M2 启动：T-2.1+2.2（laojun=0f8f0fb0）：/api/input 三态+pendingCount+queue 载荷冻结（v1.0.3）+页面输入区。T-2.3 串行待后（避免 webui11 页面文件冲突）。
- 2026-05-12 T-2.1/2.2 回流（laojun=0f8f0fb0）：全绿（1032/1032、vitest 53/53、tsc/build 净；真实会话 idle 注入+streaming steer 排队→M2-STEER-OK 验证；queue 实测载荷冻结 v1.0.3：`{"type":"queue","steering":[...],"followUp":[...],"pendingCount":N}`；截图×3）。登记范围外风险：M1 live message_update 短暂前缀重复（交 T-2.3 排查）。派 T-2.3（luoshen 单节点双现场）。
- 2026-05-12 T-2.3 回流（luoshen=e593a14f）：NFR-3 全达标（首屏 33-35ms、流式合并 151→2 次渲染、帧 max 31.6ms，1008 条实测）；前缀重复根因=pi-ai final_answer phase 提前置 stopReason（上游），客户端事件驱动终态幂等消费修复+5 反证回归；aria-live 误用修复（sr-only 终态播报）；快照 28/28 零漂移；页面 65/65+壳 8/8。派 T-2.4（c1b4cb3d luoshen，M2-GATE E2E 终验）。
- 2026-05-12 T-2.4 回流（luoshen=c1b4cb3d）：M2-GATE 全 PASS（M2-1 Web 平面发送→流式→TUI 可见、M2-2 steer 排队 pendingCount=1 实时捕获、M2-3 行内 400 不弹窗、a11y/性能抽查、A-1/A-4 回归、两仓全量 1032+65/8+34 包+双构建全绿；settings 受控例外零残留 md5 链）。BUG-1：WebPlaneHost sandbox 补 allow-forms（缺省时壳内发送静默失效）。OBS-1→R-7 遗留观察。
- 2026-05-12 合并 main（主上指令"都合并到 main，统一在 main 进行开发"）：amagi-pi 4def6aa（webui11 全量）+01f8596（契约 v1.0.3）+51549aa（perf 门槛余量）；codebox 6ae3cc8（webui15 全量）。合并后 main 全量验证：amagi-pi npm test 1141/1141+vitest 65/65+build:webui；codebox vet 净+34 包+frontend build。已删已合并分支（webui01/11/15/04）与全部 worktree；保留 spike 归档分支 amagi/webui02/03/13（过程件，知识已入蓝图报告）。后续开发统一在 main。

## 总验收矩阵（收口）

| 验收 | 状态 | 证据 |
| --- | --- | --- |
| A-1 切换不中断 | ✅（I-1/R2-E/T-2.4 三轮） | i1/r2/t24 截图与验收记录 |
| A-2 历史+实时 | ✅ 只读（I-1/R2-E）+ 全量含 Web 输入（T-2.4 M2-1） | 同上 |
| A-3 卡片与思考 | ✅（T-1.3 对照清单+mock 断言+a6 截图） | a6-*.png |
| A-4 无插件降级 | ✅ 真实场景三轮（I-1/R2-E/T-2.4） | r2-08/09、t24-15 |
| A-5 回归与测试 | ✅ 最终：npm test 1032/1032、vitest 65/65、go 34 包+webui -race、双构建、e2e 8/8 | t24-reg-*.log |
| A-6 独立可用 | ✅（T-1.4 首发+R2-E token 回退链复验） | a6/r2-06/07 |
| FR-5 输入档位 | ✅ M1 只读+M2 输入接管（spike 强档位兑现） | t24-* |
| NFR-3 性能 | ✅ 首屏 33-35ms/150 帧合并 2 次 | t23-perf.md |
| 安全（NFR-2/4） | ✅ 127.0.0.1+capability token（v1.0.2）+密钥过滤；恶意跨源 10 项全 403 | r2-security-checks.txt |

未完成/后置：Windows WebView2 复验（R-6）；OBS-1 观察项（R-7）；两仓 main 合并与发布（待主上决策）。
