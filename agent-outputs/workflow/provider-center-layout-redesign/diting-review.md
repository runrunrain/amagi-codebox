# P3 diting quick 审查

结论：未发现本阶段 Critical/Major；发现 1 项 Minor（新增测试有一条空转断言）。原六用例的业务断言未放松、五个保存函数与 v1 逐字符一致等声明**已核对成立**。此结论仅适用于本轮两页集成改动，不是全应用或原生平台通过证明。

## 基线与取证

- HEAD：`0131c80`；v1 归因输入：`v1-snapshot/tracked.patch`、`status.txt`、三个新增基件副本。HEAD 不是 v1。
- 必读输入：同目录 `p3-taskbook.md`、`p2r-taskbook.md`、`design.md`（全文，含 T01–T57/§5.3）、`design-v2.md`（全文，含 §8.2）、`impl-report.md`。
- 获取 `git diff HEAD -- frontend/src e2e`，另读未跟踪的七个源码文件。实际为 **frontend/src 20 文件（13 tracked＋7 untracked，tokens.css 已计入）＋e2e 2 文件**，不是任务预期/实现报告所说的 frontend/src 22 文件；这是计数纠正，不存在缺失或越界源码的证据。
- 本轮仅写本报告；未改代码、测试、快照或用户配置，未执行回滚/提交。报告回滚方式：经授权删除本报告即可，代码无需回滚。

## Findings

### R1｜Minor｜`e2e/pi-amagi-config.spec.ts:151–152`

- **触发条件**：执行新增结构用例，已访问并发分区后回到压缩分区。
- **可失败行为/影响**：声称检查隐藏控件不能进入 Tab 路径的断言，实际查询 `textbox`，而默认并发输入是 `input type="number"`（隐式角色 `spinbutton`）。即便该控件意外可见/可聚焦，这个 textbox 查询仍为零，不能证明所声称的键盘隔离。`toHaveCount(1)` 的单显断言及后续 roving 流程仍有效，故不认定整个用例空转，也不认定产品已发生焦点泄露。
- **证据（静态路径证实，未新跑浏览器）**：`PiAmagiConfig.vue:234–241` 传入 `type="number"`；`ui/TextInput.vue:4–13` 将 type 直接传给原生 input。spec:152 查询的角色与真实控件不一致。
- **最小修复方向**：实施者定位真实默认并发输入，先证明并发页上该输入存在/可见，再回压缩页核对隐藏；以实际 Tab 流程确认焦点不落入隐藏分区。保留已有单显、保存和 roving 断言，不由审查者编写测试。

## 七项重点复核

### 1. e2e 映射/断言有效性

**原业务断言未放松已核对成立**：v1 patch 没有 e2e diff，故直接以 HEAD 原版对照当前文件。独立提取断言行，将旧 `card(page)` 仅归一化为 `compaction(page)` 后，原 **30 条断言全部仍在（missing=[]）**。完整保存对象 `toEqual`、null/删键、冒号模型/level 重置、broken 源文、save-count 为 null、完整 fixture 相等及目录错误原文均保留。

- spec:13–19 真点击生产本地导航，检查唯一压缩 section、region 与精确 h3；没有 evaluate 改 Vue 状态。
- spec:30–34 新增三行 `.mss-dd` 与 index=2 时三个 trigger 的断言；未把无 provider 初态强行伪装成三个 trigger。
- 3 处 reload 后全部先 showCompaction；invalid-root 使用 section:null，修复成合法对象后才导航；目录失败新增 input count=1。
- spec:137–175 新用例确实执行空 custom→切区→返回、错误仍在/禁止保存、单显，以及 Up/Down/Home/End/Enter/Space/Tab；只有 R1 所列断言不能支持其注释。
- HTML diff 仅生产组件的 `navigation:'local'` 和说明注释；transport、catalog/localStorage/save-count/toast 未改。

### 2. I2 保存与生命周期

**五个 handleSave 与 v1 逐字符一致已核对成立**。读取并审查 `x-harness/v2-static.mjs`：按补丁 hunk 校验 HEAD 原行、只读重建 v1；用 TypeScript AST 提取完整函数文本并严格相等比较。实际运行 exit=0，五个编辑器全部通过；lazyPreset、onProfileApplied、handleExport、handleImport 亦严格相等，七处 `:key="presetLoadKey"` 保留。

另独立 AST 比较 `formatModel`、`cliModel`、`watch(mainTab)` 与 HEAD：三个均 EXACT HEAD MATCH。`ProviderCenterView.vue:226–310` 的同文件 selectNavigation 只改选择记忆，跨模式才调用 engine setter；父级七模式来自纯静态表，不提前加载动态文件。外层 page-id key 隔离 Pi/OMP Registry，内层仍用重试 key。PresetLoadError:287–305 仅新增 mounted 导航 error 报告，重试仍递增原 key；不宣称其整个实现字节不变。失败 ESM URL 缓存仍是已披露的未恢复路径。

### 3. I1：§6.1 十一项语义

以下承载逐项定位，**语义迁移声明已核对成立**。源码路径均以 `frontend/src/components/provider/` 为前缀。

| 语义 | 常显落点；完整说明 |
|---|---|
| Leader 重启 | PiAmagiConfig:158；MCP_TIPS.leader:582–586 |
| OMP 新会话 | OmpGlobalConfig:23 的全文件工具栏 |
| MCP default 即时 | PiAmagiConfig:181；MCP_TIPS.default:587–590 |
| OpenCode 即时 | OpenCodeGlobalConfig:39 的全文件工具栏 |
| 下次压缩 | PiAmagiConfig:111；configNavigation.ts 的 PI_SECTIONS meta；COMPACTION_TIP:415–417 |
| Registry 重新进入 | ProviderRegistryEditor:28–29；REGISTRY_EFFECT_TIP:402–404 |
| 受管覆盖风险 | ProviderRegistryEditor:55–64 常显 meta/警告；navigationItems:465 受管标记 |
| MCP 三态/空名单 | PiAmagiConfig:163–196 的 Segmented/StringListEditor；MCP_MODE_ITEMS/MCP_EMPTY_TEXT:559–601，保留角色附加例外 |
| default 清空回落4 | PiAmagiConfig:238 placeholder；CONCURRENCY_DEFAULT_TIP:448–451 含应用预填10/移除键 |
| OAuth 管理/只读 | AuthConfigEditor:60–81 常显管理标签/InfoTip，:137 空态登录途径，:212 完整只读/令牌保留文案；正文只展示 accountId/过期状态，不编辑 token |
| 压缩失败取消/保历史 | PiAmagiConfig:129 custom 后果句；COMPACTION_TIP:419 含配置/认证/摘要失败 |

InfoTip 是原生可聚焦 button，键盘开关/escape 与描述节点存在；未擅自增加未经证实的生效时机。

### 4. S11 导航泄露面

**指定密钥/令牌/accountId 不进入 snapshot 的声明已核对成立**：Registry:464–468 items 显式投影为 id/label/受管 meta，不 spread provider 数据；Auth:270 显式投影 uiId/name/typeLabel，不 spread AuthRow。Auth:196–201 内存 `entry:N`；合法重命名:358–369 转移 id、模板 key=row.uiId，避免逐字输入重建。snapshot:484–485 / Registry:860–861 仅 owner/phase/mode/activeId/items。父级:242–252 检查 owner，转换导航 id 通过映射找回，不拆分用户名称。SectionNav 仅接展示元数据，不接凭据对象。允许的提供商名称/类型标记不当成秘密字段；源码模式仍是既有整文件编辑边界。

### 5. S7/S8 范围

`git status --short`、`git diff HEAD --stat` 和 static audit 都支持：Go/wailsjs/API/stores/manifests/lockfile/其他视图改动为零。e2e 恰好批准的两文件；源码七个新增文件全部为声明的基件/纯导航模块；x-harness/log/screenshots 在 agent-outputs 内，不在源码树。tokens 仅 v1 三项追加，v2 与 v1 严格相等。计数差异见基线节。

### 6. S10 草稿/共享消费者

**visited＋v-show、切区不清洗/重读的静态声明已核对成立**：五编辑器 selectSection 只校验选择、写 activeSection/visited；已访问 EditorSection 保留，未访问不实例化。OpenCode 的条件 model/unknown 消失时回退 provider；Registry/Auth 新增/删除只在原成功路径尾部维护选择/焦点。无 sectionId key 包整个编辑器，原保存仍面向整文件。

`EditorSection.vue:18–20` provide computed active；`sectionVisibility.ts` 只一个布尔 InjectionKey。Dropdown:94–95、InfoTip:114–115 可选 inject 默认 true，隐藏时关菜单/原 close；InfoTip:164 阻止延迟再开。未处于 EditorSection 的其他页面不注入，默认真与原 click toggle 等价，未发现范围外消费者回归路径。

上游 `v2-drafts.log` 六项结果与 `v2-runtime.mjs:32–100` 的真实输入/返回断言相符，含 MCP 空白名单、并发空值、RawJson 非法局部草稿、读调用数组相等以及浮层关闭；这属于上游浏览器证据，本轮未重跑。跨模式草稿仍按既有行为卸载丢弃，不扩大承诺。

### 7. 遗留归因

**皮肤 RangeError 与 OCP 错误未抵达本地 ErrorState 是范围外既有，归因已核对成立**：App.vue、utils/skinBake.ts、stores/provider.ts 不在 diff。

- App.vue:55 将 syncSkinDom 作为回调；skinBake.ts:101–111 的 blur<=0/不支持/缓存命中同步调用 onDone，静态路径支持递归风险。没有以皮肤截图证明整路径健康。
- provider.ts:273–279 catch 记录 presetLoadError 但不 rethrow；OpenCodePresets:335–343 只在 await 抛错时设置自己的 error，未消费 store error。准确说是错误未传播到该组件，而非完全没有记录错误。此链路未由本阶段修改引入。

## 验证可信度与盲区

本轮 cwd 均为 `/Users/run.mao/workpace/amagi-codebox`：

| 本轮执行 | 状态/关键结果 |
|---|---|
| git rev-parse/status/diff/stat | exit=0，基线/范围如上；完整 diff 初次工具输出截断，重点面另以分文件 diff/源码取证 |
| `git diff HEAD --check -- frontend/src e2e` | exit=0，无 whitespace error |
| `node agent-outputs/workflow/provider-center-layout-redesign/x-harness/v2-static.mjs` | exit=0，保存函数/v1共享件严格相等、七 key、禁止范围等通过；已阅读脚本，不把简易 includes 检查当运行证据 |
| 独立 `node -e` AST/断言核对 | exit=0，三段生命周期 EXACT HEAD MATCH；原30条断言缺失0；reload导航3；v1 e2e patch块0 |

一次 node heredoc 取证在启动前被守卫拦截，未执行；改为明确非交互 `node -e` 后完成上述取证，未绕过守卫。

未新跑 build/Vitest/Playwright或浏览器，不将上游日志称为本轮复现；`v2-pi-e2e-final.log` 的7 passed/exit0已读取，仅作上游证据。视觉判断仍由洛神承担；原生 Wails/WebView、真实磁盘/凭据、旧 WebView fallback、远程横幅及极值规模未独立验证。R1 是静态证实的测试有效性缺口，不是产品缺陷复现；quick 不扩大为全仓审计。

VERDICT: PASS_WITH_MINOR
