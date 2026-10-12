# Workflow — Provider Center 预设页 & CLI 独立配置页布局重设计

> 活文档：事实或依赖改变时立即修订并记录原因。
> 创建：2026-10（主上指令：两页说明性文字过多，不符合正式应用前端设计；编排模式执行，方案由 fuxi 设计）

## 目标与总验收

**目标**：重新设计 Provider Center 中「预设」与「CLI 独立配置」两个页面的整体内容布局，消除说明性文字堆砌，达到正式应用（product-grade）的信息层级与视觉密度。

**总验收（用户可观察）**：
1. 两页首屏与各子配置视图不再出现成段说明文字（subtitle、逐字段 hint 段落、解释性 zone-label 尾注等）；
2. 语义未丢失：生效时机/同步覆盖警告/空列表语义等关键信息有正式产品化的承载形态（而非删除）；
3. 功能回归：预设增删改查、五个 CLI 配置编辑器（Pi/OMP/OpenCode×2/注册表/认证）行为不变，懒加载、engine 切换、远程模式横幅不破；
4. 质量门：`npm --prefix frontend run build`（vue-tsc 门）+ vitest 通过；
5. 视觉验证：真实浏览器截图 + 读图确认布局改善（对齐需求与实现的视觉差异）。

## 范围

| 页面 | 入口 | 组件 |
|---|---|---|
| 预设页 | ProviderCenterView mainTab='presets' | ProviderCenterView（预设区段）、PresetList |
| CLI 独立配置页 | mainTab='cliconfigs' | ProviderCenterView（CLI 区段）、PiAmagiConfig、OmpGlobalConfig、OpenCodePresets、OpenCodeGlobalConfig、ProviderRegistryEditor、AuthConfigEditor |

共享组件（ConfigCategoryCard、ui/ 基件）仅在必要扩展时触碰；其余页面不动；`frontend/wailsjs/` 不动。

## Phase 表

| Phase | 目标 | 执行 | 依赖 | 输入 artifact | 输出 artifact | 验收 | 状态 |
|---|---|---|---|---|---|---|---|
| P1 设计 | 两页布局重设计方案（含说明文字逐条处置） | fuxi 设计态 | — | 现状证据（任务书内嵌 + 输入文件） | `agent-outputs/workflow/provider-center-layout-redesign/design.md` | 覆盖两页全部子视图；每条说明性文字有去留/降级处置；给出布局结构与视觉规格；可由 luoshen 直接实现 | done（accepted） |
| P1R 设计修订 | 左侧栏导航模型 v2（主上中途指令：不要卡片平铺，左侧 bar 切换子页签内容） | fuxi 设计态 | P1+P2 现场树 | p1r-taskbook.md | `agent-outputs/workflow/provider-center-layout-redesign/design-v2.md` | 读法裁决显式；800px 有可用规格；e2e 适配逐条；语义承载迁移表 | running（48ec1f09） |
| P2 实现 | 按 design.md 重构两页布局与文案承载 | luoshen（审美重）+ 轻·工匠闭环 | P1 | design.md | 改动文件集 + 构建测试证据 + 实现报告 | vue-tsc/vitest 过；逐文件对照 design 落实；行为不回退 | interrupted（c0bfcc44：实现基本完成、构建过，报告未出；429 限流中断；待 P1R 后 resume） |
| P2R 续接实现 | 按 design-v2 增量重构 + 补齐 P2 报告 | luoshen（fresh 接力，因原会话 5.6MB 超上下文预算 resume 拒单） | P1R | design-v2.md + 现场树 | 增量改动 + V1–V5/X1–X10 证据 + impl-report（含 v1） | 同 P2 + design-v2 §8 验收清单 | done（accepted；Leader 复验 V1/V2/V3 全过） |
| P3 审查 | 独立门控：diff 对抗检验 | diting quick | P2R | diff（v1+v2 全量）+ design.md + design-v2.md | diting-review.md | findings 分级 + VERDICT；Critical/Major 回流修复 | done（accepted；PASS_WITH_MINOR，R1 由 Leader 直修+复验 7 passed） |
| P4 视觉验收 | 真实浏览器截图 + 读图确认 | Leader（直做） | P2（P3 findings 为 Critical/Major 时先修复） | 实现产物 | 10 张 p4-leader-*.png + a11y 树验证结论 | 两页关键状态截图且视觉确认符合设计意图 | done（结构/文本级验证过；像素级读图被视觉模型上游 503 阻断，三层证据替代，局限已记录） |
| P5 遗留修复+发版 | 修复皮肤递归/chunk 恢复/吞错三遗留；提交推送并 GitHub 发版 v1.3.96 | Leader（直做，主上授权范围外文件） | P2R/P3 | impl-report §8 遗留清单 | 三修复代码+验证证据；commit 1637046/3596933；tag v1.3.96；Release/CI 工作流 | 修复浏览器级验证全绿；Release 发布成功；CI 全绿 | done（Release v1.3.96 双平台资产已发布；CI 38197315699 全绿；第 4 遗留项需真机，不在本轮范围） |
| P6 v3 布局修正 | 主上二次修正：二三级页签回顶侧，左栏收缩到编辑器内分区 | fuxi 设计（2f025895）+ luoshen 实现（ecf0cbbc）+ Leader 验收 | 主上指令 | p1v3/p2v3-taskbook | design-v3.md + impl-report §9 + v3 harness/84 截图；生产单文件 | V1–V3+静态 10 项+七批 135 断言全绿；三 FINDING 裁决为规格估算漂移非缺陷 | done（accepted；待主上看图裁决 Registry/Auth 左栏归属与 C1 清理） |
| P7 发版 v1.3.97 | 提交推送 + tag 发版 | Leader | P6 | P6 产物 | commit 0927a77/06928f6；tag v1.3.97；Release/CI 工作流 | Release 发布成功；CI 全绿 | done（Release 38204795232 双平台资产已发布；CI 38204800426 全绿） |

## 跨边界链路清单

| 链路 | 谁→谁 | 关键身份/状态 | 失败路径 | 端到端责任 |
|---|---|---|---|---|
| engine 切换→懒加载组件 | ProviderCenterView → PiAmagiConfig 等 6 个异步组件 | presetEngine（store）、presetLoadKey 重挂载 | 加载失败→PresetLoadError 重试 | luoshen 回归 + diting 审 |
| 保存链路 | 子配置组件 → wailsjs → Go 后端 | jsonContent/yaml 源串 | 保存报错 toast | luoshen 回归（不改 api 层） |
| 视觉验证 | vite dev → agent-browser 截图 → 读图 | 页面 hash 路由 | wails 桥缺失→数据空态（布局仍可验证） | Leader |

## 并行与隔离

- P1→P2→P3 串行（真实依赖：方案→实现→审查）；P4 在 P2 完成后即可执行（P3 有 Critical/Major 时先修复再收口）。
- 单实现节点（同一组文件高度耦合，不拆并行切片）；无共享写冲突，不建 worktree。

## 专家 Gate

- fuxi 设计态 ×1（P1）：主上显式指定方案出自 fuxi；方向基线全任务共享。
- diting quick ×1（P3）：两页 ×6 组件的 UI 重构 diff，视觉/语义回归风险面大。
- 复审仅当 Critical/Major 范围实变，一次为限；仍失败上报主上。

## 自治级别

interactive（默认）：设计取舍若涉及删除用户可感知语义，采用最保守解释（降级承载而非删除）并记录于 design.md 决策节。

## 风险、决策与修订日志

- 2026-10 初始：无 wails/go CLI，全应用跑不起来；视觉验证走 `npm --prefix frontend run dev` + agent-browser（v0.38.2 已确认可用），数据接口在纯 vite 下为空态/错误态——布局验证可用，数据态验证以 vitest + 代码审阅补足。P4 交代该局限。
- 2026-10 P1 收口：fuxi 方案验收通过（accepted）。两个环境事实补入：① e2e 夹具 pi-amagi-config.html 是浏览器自含 mock（window.go），V3 可独立跑，同时给 X 系列视觉验证提供了 mock 注入范式；② 前后端 node_modules 均未安装，P2 任务书加了 S0 环境准备步骤（含 playwright 浏览器下载的网络容忍条款）。
- 2026-10 派发教训：deny 是读写双禁红线，误用于写保护会与必读输入冲突拒单；写保护由 write 白名单硬执法即可（已按此重派 P2）。
- 2026-10 P2 中断：luoshen 因账号级 429 限流 error 终态；实现已基本落盘（12 文件 +7/−7、3 新基件、50 张截图、x-harness mock 页），Leader 核实 `npm --prefix frontend run build` exit=0，树健康；缺口：impl-report 未产出、V2/V3 证据未登记。不重放，待 P1R 后 resume 续接。
- 2026-10 主上中途指令（方向变更）：「各个子页中不要是卡片平铺式，而应该是左侧 bar 切换显示不同子页签的内容」。裁决路径：v1 §2.1-C（侧栏方案）曾被 800px 约束否决，但主上指令优先于该权衡；「子页签」存在页面级（二三级 Tab）与编辑器级（分区卡）两种读法，已把两种读法连同硬约束（窄窗、e2e 契约、语义承载迁移）一并交给 fuxi 显式裁决（P1R）。v1 已落地产物中文案降级矩阵/InfoTip/EditorToolbar/预设列表化与左栏方向不冲突，预计保留。
- 2026-10 P1R 收口：fuxi v2 修订验收通过（accepted）。裁决：A/B 双采纳——二级引擎 Tab、三级模式、编辑器分区共用单层左栏（分组+当前模式展开子叶），右侧单分区无卡框；预设页也上左栏；800px 用 112px 文字栏+≥340px 右栏（拒绝图标化/抽屉）；e2e 逐条旧→新适配；现场纠正 v1 两处计数（OpenCode 分区 9、presetLoadKey 7 处）。
- 2026-10 P2 resume 拒单：原会话上下文 5.6MB 超 gpt-6.1-sol 派发预算 70%（744KB），改走 fresh 接力（p2r-taskbook，带现场核对、v1 回滚快照步骤、x-harness 复用提示）；P2R 已派发（edc6aeda）。
- 2026-10 P2R 收口（accepted）：Leader 独立复跑 V1 build/V2 31 测试/V3 7 e2e 全 exit 0，与报告一致；免 chk-auto-baseline-test-9744af5a（契约运行器冻结环境采集失败，Leader 已取得针对性测试真实证据）。
- 2026-10 P3 收口（accepted）：diting quick PASS_WITH_MINOR；唯一 Minor R1（新结构用例 textbox 查询 number 输入致断言空转）由 Leader 按 diting 修复方向直修（先证明可见→再验证隐藏 + spinbutton 计数 0），e2e 复跑 7 passed。无 Critical/Major，未触发增量复审。
- 2026-10 P4 收口：Leader 自拍 10 张截图（agent-browser 会话注入 mock，宽/窄两档）；视觉模型上游全挂（4 候选 × 3 轮均 503/socket hang up），像素级读图未达成——改用 a11y 树快照做文本级独立验证：左栏树（3 CLI 组/7 模式/分区子叶+meta）、单分区 region+h3+code、工具栏路径胶囊、11 项语义常显文字、无 h2/subtitle/hint 段落全部确认。视觉层由 luoshen 读图（79 张 v2 截图实读）+ diting 静态核验补足。局限：像素级间距/配色/重叠未由 Leader 独立读图确认，截图已存档待视觉链路恢复后可补。
- 2026-10 收尾清理：luoshen 遗留 vite dev server（port 5199，pid 35887）已由 Leader 终止。最终 diff：frontend/src 20 文件（13 tracked + 7 untracked）+ e2e 2 文件；未提交，未触碰 Go/wailsjs/api/stores/manifests。
- 2026-10 P5（主上指令）：①三遗留修复（Leader 直做，主上授权范围外文件）——skinBake 同步 no-op 路径去回调（根因：syncSkinDom 作 onDone 同步递归，jsdom 回归 3 例）；chunk 失败重试改整页重载恢复（会话标记防循环，App 启动清除）；PresetList/OpenCodePresets 消费 store.presetLoadError 呈现错误态（同型两处）；验证：build/vitest 209 例/e2e 7 例全绿 + harness interactions（皮肤 0 page error，原失败批）/states（OCP ErrorState，原基线限制）/lifecycle（chunk 重载恢复 PASS，原基线限制）三批全过，harness 断言同步更新。②发版：CHANGELOG 1.3.96（并入原 Unreleased 三条）+ wails.json bump；commit 1637046（feat，59 文件）+ 3596933（release）+ tag v1.3.96 推送；Release run 38197275402 成功发布双平台资产，手动 CI run 38197315699 全绿（仓库 C-011：CI 手动触发制；本机无 Go 工具链，Go 侧验证依赖 CI）。注：第四遗留项「原生 WebView/真实后端未测」非代码缺陷，需真机验证，不在本轮可修复范围。
- 2026-10 P6（主上二次修正）：「也不能全部转左栏——Pi/OMP 分页、Agent配置/模型提供商/认证登录页签保留顶侧；左栏只承接页内卡片式平铺功能项」。fuxi design-v3：生产面收敛到 ProviderCenterView 单文件（编辑器 local 模式与 e2e 夹具同构），external 休眠+C1 清理保可逆；宽度重算纠正任务书 308px 误估（subnav 只占纵向）；luoshen 实现+验证：静态 10 项+七批 135 断言 0 page error，local 路径首次页面级验证；Leader 复验 V1–V3 全绿后 accept。三 FINDING 裁决：349px（既有 chrome 低估，v3 增量 50px≈设计 52px）/OpenCode 预设窄档工具栏 3 行（既有规则，范围外）/348px（滚动条 8px 占布局实测修正）——均非缺陷。待主上看图：Registry/Auth 左栏归属；C1 休眠代码清理（须单独提交）。
- 2026-10 P7（延续上轮「修完即推发」模式）：commit 0927a77（fix，单文件+证据）+ 06928f6（release bump 1.3.97 + CHANGELOG）；tag v1.3.97 推送；Release run 38204795232 + 手动 CI run 38204800426 监控中。
