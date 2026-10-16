# P3-S2 阶段集成审查报告（谛听 · full）

> 范围：amagi-codebox P3-S2 未提交工作区改动（基线 HEAD=8265ffc 叠加 S1 未提交件），权威规格 = `agent-outputs/workflow/wsl-session-friction/p3-config-supply-design.md`（下称「设计」），实施报告 = `s2-report.md`。
> 方法：本会话工具面无 bash，无法执行 `git diff`/测试；按 S2 报告 §1 的 33 文件清单直接读取工作区当前态逐项核对（当前态 = HEAD+未提交叠加，对审查目的等价）。**测试未执行，全部结论为静态路径证实**；报告声称的测试通过是上游证据。

## Findings

### F-1｜Major｜internal/wslsupply/coordinator.go:572（recordProjectionFreeze）｜冻结事件整体重建 StateEntry 时丢弃 Subs，键级登记基准被摧毁

- **触发条件**：任一键级单元（pi/settings、pi/amagi、pi/mcp）进入「无可写键 + ≥1 冻结/未托管键」的整单元阻断周期——即最常见场景「用户手改一个键、其余键未变」后的下一次启动/推送/remote 启动。
- **实际影响**：`recordProjectionFreeze` 构造的新 `StateEntry` 未携带 `Subs`（对比 ：792 `DropUnitRegistration` 显式保留 `Subs: prev.Subs`，:528 `recordProjectionState` 对 nil Subs 有保留分支），`upsertEntry` 整体替换后键级基准清空。下一轮 `resolveKeyed` 中所有原托管键落入 `!inSub` 分支 → 判为 `unmanaged-existing`：
  1. 未被手改的健康键永久停止跟随 Windows（设计 §三「有记录+目标==上次写入→来源变才更新」失效，键级粒度承诺——报告 §5.2 自述的存在理由——被破坏）；
  2. 「来源删除→托管且未手改才移除」规则失效（键已无登记，永不移除）；
  3. UI 冻结原因漂移：`local-modification-frozen` → 下一轮被覆写为 `unmanaged-existing`；
  4. 恢复仅剩整单元接管（接管会以来源覆写被手改键，恰是键级设计要避免的粗粒度动作）。
- **证据**：coordinator.go:572-585 新条目字段清单无 Subs；keyed.go `resolveKeyed` 对 `!inSub` 键的 unmanaged 判定；pi_config_test.go 全部冻结子态（both-changed/local-deletion/hand-modified）都在冻结观察当轮终止断言，无第三轮验证登记存续——测试缺口与缺陷同构。
- **最小修复方向**：`recordProjectionFreeze` 补 `Subs: prev.Subs`（对称补 `Detached: prev.Detached`）；补一条「冻结后健康键仍随来源流动 / 冻结原因不漂移」的跨周期测试。

### F-2｜Major｜internal/wslsupply/transform.go:100（ContainsWindowsDriveRef）｜URL（任意 scheme://）被误判为「内嵌 Windows 盘符路径」，整条 MCP 条目被错误拒绝

- **触发条件**：stdio MCP 条目的未声明 env 键值含任意 URL（如 `API_BASE: "https://api.x.com/v1"`、`postgres://…`）或 args 内嵌 URL（如 `--endpoint=http://x`）。谓词为「字母+`:`+`/`」三元组，`https://` 的 `s`/`:`/`/` 命中 → 返回 true。
- **实际影响**：整条 server 以「env 键 X 的值携带 Windows 路径形态…需手动适配」拒绝——设计 §五明确「**不把 URL、token 或普通含冒号字符串当路径**」；常见真实输入（stdio MCP 携带 API endpoint env）永远无法投影，且诊断文案误导。次要波及：`TransformSettingValue` 对含 URL 的白名单字符串叶子会静默剔除字段（当前白名单仅 retry 数值，暂无实害）。
- **证据**：transform.go:100-110 逐字符扫描无 scheme 排除；transform_test.go:161-168「普通值不受影响」用例全无 scheme（`tvly-dev-xyz`、`${PI_PROVIDER_KEY:…}`），:171-184 仅覆盖 url 字段 HTTP 早退分支——URL-in-env/args 反例无测试。
- **最小修复方向**：命中处排除「字母前一个字符也是字母/数字」的形态（`https` 的 `s` 前是 `p`），或改用 `^[A-Za-z]:[\\/]` 词首锚定判定；补 URL env/args 用例。

### F-3｜Major｜app_wsl_supply.go:129-146（runWSLManagedModelsAtLaunch）/ pi_login_launch.go（defaultRunWSLLoginSupply）/ launch_planner.go:762-775｜三键级单元无同意门自动投影：首启即向 WSL 写入 Windows 用户配置

- **触发条件**：任一 pi API-provider WSL 启动（桌面或 remote effect，单元清单含 settings/amagi/mcp）、登录启动（5 单元）或保存后异步推送，目标 distro 的 `.pi/agent` 缺文件或缺键——`resolveKeyed` 对「无记录+目标缺键」判 `keyAdd`，首启整体创建 settings.json/amagi.json/mcp.json 或向既有用户文件追加缺失键。`RequiresConsent()` 恒 false，协调器同意门（Plan 的 awaiting-consent 拦截）对这三个单元不生效。
- **实际影响**：违背设计 §三「无记录、目标缺失：**只有明确允许的默认托管产物可自动创建；用户配置、可执行资产需导入同意**」与 §四 Pi 表「settings.json 字段级 TrackedProjection（**同意后**）」——Windows 侧 amagi 角色/MCP 定义（含可能内嵌明文 key 的 env，经 sensitive 单元落盘）未经用户同意跨界。安全不变量（不覆盖已存在/已手改键）保持，损害面是授权模型而非数据安全。
- **对照张力（供 Leader 裁定）**：设计 §八 S2 验收「Windows 面板修改进入未手改 WSL 配置」本身预设了流动；S2 报告声明了单元扩容（行为变更②）但未声明同意缺口。若裁定为有意取舍，需回改设计并显式声明；否则应给三单元加 per-target opt-in（复用 awaiting-consent 机制）。
- **证据**：app_wsl_supply.go 单元清单无条件包含三单元；pi_config.go Plan `writable > 0 → ActionWriteProjection`；TestPiSettingsProjectionLifecycle 首段即断言「目标缺失→白名单字段建立」通过。
- **最小修复方向**：三单元 `RequiresConsent()=true` + 状态页「启用投影」入口（或 Leader 裁定修订设计条文并在报告中补行为变更声明）。

### F-4｜Minor｜internal/wslsupply/keyed.go:133｜注释与行为不符：「来源删+目标删→登记基准自然清除」实际被保留

- `!srcOK && inSub && !tgtOK` 不产生 decision，而 `buildCandidate` 非 adopt 路径先整体复制 `state.Subs` → 残留 sub 使「来源键回归+目标仍缺」判 `local-deletion-frozen`。行为本身与 §三「有记录+目标被删→冻结」一致（可辩护），但注释宣称的「自然清除」不存在，误导后续维护与测试编写。修复：改注释为「登记保留（源回归即冻结）」或真正清除该 sub 并注明理由。

### F-5｜Minor｜internal/wslsupply/transform.go:230-236（env 未声明键检测）｜Windows 变量形态路径（`%USERPROFILE%\x`）静默放行

- 未声明 env 键值仅以盘符三元组检测；`%USERPROFILE%\foo` 无冒号+斜杠 → 不被识别为 Windows 路径形态，原样写入 WSL mcp.json（运行时不可用）。设计 §五要求「未知 env 值像 Windows 路径→阻止」「%VAR% 无法确定→要求用户提供 Linux 值」。方向为漏检放行（非污染），影响低；修复：对未声明键补 `%VAR%` 形态检测并同样拒绝点名。

## 覆盖与验证可信度

| 审查关注点 | 结论 |
|---|---|
| 1 冲突状态机 | 键级引擎逐态核对：unmanaged-existing/来源变才更新/目标变删冻结/双侧同变保 WSL/来源删除两分支/停跟随/接管/预览过期 CAS——引擎内逻辑与设计一致且键级（非文件级）真实成立；**但 F-1（冻结丢登记）与 F-3（缺键无同意门）违背 §三** |
| 2 转换器 | 纯函数无执行 ✓；npx.cmd 裸名/标准位置 shim 仅验证后映射 ✓（`C:\tools\mytool\npx.cmd` 拒绝 ✓）；ps1/exe/bat/cmd/pwsh 拒绝 ✓；args 资产前缀→wslpath 实测→未验证拒绝 ✓；env 仅声明键（NODE_PATH 列表 `;`→`:`）✓；%USERPROFILE% 展开限定 ✓；HTTP url 字段原样/localhost 不改 ✓；原子单元（转换失败不新增不更新、同名保持，两形态有测试）✓；**但 F-2 URL 误拒、F-5 变量形态漏检** |
| 3 remote 接入 | BuildPlan 纯读保持 ✓（供给 effect 只携带单元 id/层 SecretBufferRef，distro 留空 Apply 解析；WSL pi 计划无 ConfigMutation、非 WSL 不变，有测试）；层内容经 SecretBuffer ✓（含 apiKey，随计划 Dispose）；OPENCODE_CONFIG 晚绑定重锚 ✓（Prepare 期 PTY 指针+运行时 `pty.applied` 双校验+计划序 rank 8<16，层失败阻断、成功注入 env+WSLENV，均有测试）；双链路共享 verdict ✓（`verdictForWSLPiConfigSupply` 桌面闸门与 effect 同函数；corrupt 阻断/冻结静默两链路一致有测试） |
| 4 PiConfig 窄接口 | ReadAgentFileAt/GetAmagiConfigAt/SaveAmagiConfigAt 显式目录、无进程级 env 变更、UNC 写入拒绝（`\\`/`//` 前缀）✓；原无 target 方法未改动 ✓ |
| 5 状态页/动作面 | 五动作齐备且无「强制覆盖全部」✓；敏感项只显文件名（relPath 恒为文件名）与键名/方向 ✓；状态 API（GetStatus/UnitStatus）不含 hash/正文 ✓；mcp Sensitive=true chmod 失败 fail-closed ✓。备注（非 finding）：预览 API 对敏感单元返回整文件内容 hash 作为接管 CAS 令牌——非登记 hash、无内容反演风险，视为设计允许的实现细节 |
| 6 测试有效性 | 冲突表 8 子态**真断言内容保留**（900000 字节保留、byte-identical、kimi/k3:high、"local"、uvx）非仅不断言报错 ✓；预览过期链完整（过期拒绝→新内容保留→重新预览→接管→恢复流动）✓；双链路字节相等真实（同一构造、各自全新目标、确定性输出比对）✓；缺口：无跨冻结周期的登记存续测试（F-1 同构盲区） |
| 7 与 S0/S1 交互 | managed_models/shared_files 适配器以 `_ *StateEntry` 兼容新签名、语义未变 ✓；StateEntry/Subs/Detached 为 additive、schemaVersion 不变、load 容忍缺字段 ✓；wailsjs 五方法已再生（App.d.ts 核对）✓；app.go SaveAmagiConfig/SavePiModelsConfig→pi-config 标脏、失败不标脏 ✓；launchplan Validate 对 WSLSupply 联合判定 additive ✓ |

**盲区**：① 测试/构建/vet 未执行（无 bash），报告 §8 的全部运行结果未经本轮复测；② app_wsl_supply_test.go、pi_login_wsl_test.go、projection_test.go、前端 wslSupply.test.ts 与 WSLCLISettings.vue 仅定向核对（动作按钮/wrapper），未逐行；③ 真机 WSL 行为（wslpath/WSLResolveCommands 探测面、9P chmod）未触碰——报告自述用假 UNC 探测面覆盖，属已声明未覆盖项；④ F-1/F-2/F-3 均为静态路径证实，未运行复现（受 ① 限制）；复现脚本属实施者职责，方向已随各 finding 给出。

**基线确认**：工作区含 S1 四个未提交件（报告 §0 声明，HEAD 不可独立构建）；本轮未改动任何项目文件，仅新增本报告。

## 结论

S2 的工程面（转换器确定性、remote 计划纯读、层晚绑定、双链路 verdict、窄接口、动作面）质量扎实且与设计/报告声明相符；但冲突状态机的两个核心承诺——「冻结保留登记的键级跟随」与「无记录+目标缺失需导入同意」——分别被 F-1（登记被冻结事件摧毁）与 F-3（无同意门自动写入）违背，另有 F-2 转换器对 URL 输入的确定性规则误判。三项 Major 均可静态复现、有明确锚点。

VERDICT: FIX
