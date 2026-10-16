# P3-S5 实施报告（插件面板目标化与原生重放）

- 实施主体：luban（任务 9390fd17，两轮续接 ca5e0f0b / 37fee6bb）
- 收尾：Leader 直做（模型 API 网络故障三次中断后的验证收口），2026-10-16
- 基线：HEAD=6b015b8

## 一、中断归因与完成度

- 第一轮（9390fd17）：实现主体完成（16 文件：wslsupply plugins 单元/piplugin target 接缝/app_wsl_plugins 门面/前端两组件），中断于 GLM API 500（错误 id 20261016201125af6e19bd3d3a476f）。
- 第二/三轮（ca5e0f0b / 37fee6bb）：续接均于 4 调用处再断（同族 500，错误 id 20261016202500… / 20261016203814…）；第二轮写入 +244 行（状态/协调器扩展与微调）。
- 实现与测试代码在盘完整：`internal/wslsupply/plugins.go / plugins_api.go / plugins_test.go`、`internal/piplugin/target.go / target_test.go`、`app_wsl_plugins.go / app_wsl_plugins_test.go`、`app_wsl_supply_s4.go`（S4 尾巴合并）、前端 `PiPluginWSLSection.vue / WSLPluginTargetPlaceholder.vue / api/piPlugin.ts`。
- Leader 收尾：全部验证（下节）+ wailsjs 绑定补齐 + 本报告。未发现半成品代码路径（编译与全量测试佐证）。

## 二、验证记录（Leader 直做，2026-10-16）

| 验收项 | 证据 | 结果 |
|---|---|---|
| Go 构建/静态检查 | `go build ./...` exit 0；`go vet ./internal/...` 通过；gofmt 干净 | ✅ |
| 插件生命周期测试（20 项） | `go test ./internal/wslsupply/ -run TestPlugin -count=1` ok——覆盖：锚定版本固定重放/git commit 固定/未固定 distro 内解析/失败绝不登记已安装/幂等 AlreadyOK/未管理项保护/本地源拒绝/CLI 缺失/来源不可解析冻结诊断/跨代不继承/更新须登记管理/脏仓库冻结绝不 reset/更新携带锚定/锚定匹配 AlreadyOK/登记不一致阻断/移除登记/移除本地项阻断/移除已消失清理登记/超时单次无重试风暴/取消结构化 | ✅ |
| wslsupply/piplugin 全量 | `go test ./internal/wslsupply/ ./internal/piplugin/ -count=1` ok（含 S0-S4 回归） | ✅ |
| 根包插件面 | `go test . -run "WSLPlugin|PluginTarget|WSLSkill"` 全 PASS（TargetValidation/SupportContract/SkillsSupply 三态/KeepLocal/Preview/TakeOver） | ✅ |
| 前端 | `vue-tsc --noEmit` exit 0；`vitest run` 254/254 | ✅ |
| wailsjs 绑定 | 本机无 wails CLI：App.js 六方法手工补齐（**变量传参**，S4 F1 教训）；App.d.ts 返回类型暂用 any（注释标注 wails generate 再生归一）；models.ts 保持基线（手工嵌套编辑风险过高，强类型留待再生） | ✅（带再生态） |
| 真机链路冒烟（验收 2 核心） | Ubuntu distro 内 `npm install nanoid@5.1.5`（固定版本原生安装 3s）+ `node -e` 消费验证（replay-ok）+ 清理，exit 0 | ✅ |
| 面板级真机 e2e | 未做（需 App 运行时实例）——留主上桌面验收；链路级证据已覆盖「固定版本 WSL 原生安装」语义 | ⚠️ 降级声明 |
| frontend/ 无 C: 垃圾 | 提交前核查 | ✅ |

## 三、实现要点（从代码与测试反推的语义摘要）

- 目标化：install/update/remove 显式目标（windows/wsl/both），每目标独立结果（`PiPluginOpOutcome.target` 区分），Windows 成功不代 WSL 报喜。
- 原生重放：npm 源带锚定版本、git 源固定 commit；重放记录入状态清单；失败绝不写已安装登记（TestPluginInstallFailureNeverRegistersInstalled）。
- 冻结保护：WSL git 仓库未提交修改 → update 冻结且绝不 reset/clean（TestPluginUpdateDirtyRepoFrozenNeverResets）；来源不可解析 → 该插件冻结诊断不影响他项。
- 复检：登记与实体同时有效才 installed，单向漂移显 mismatch 状态；启动仅提示缺失（PluginRegistrationSummary.missing）不自动安装。
- 其他 CLI：GetWSLPluginTargetSupport 返回静态契约（claude/codex/opencode=status-placeholder 占位、omp=excluded——B7 主上裁定的声明式排除）。

## 四、遗留与建议

1. **wailsjs 再生**：主上下次有 wails CLI 环境时执行 `wails generate module` 归一强类型（App.js 六方法 + d.ts any → 生成器格式）。
2. 面板级 e2e（App 运行时实例上的插件重放全链）可随主上桌面验收补做。
3. 三轮 GLM API 500 中断为基础设施故障（非任务缺陷），错误 id 已归档；本会话同时段 Leader/其他子任务未受影响，属子任务进程独立通道故障。

## 回滚

定向还原 S5 全部文件（见 git status 清单）；状态清单 wsl-config-supply.json 中插件登记由 DropUnitRegistration 路径清理；distro 内已装插件不自动卸载（用户确认后由 pi 原生 CLI 移除）。

## 五、修复轮记录（diting quick 审 FIX 7 条，2026-10-16，全新会话接管）

基线：盘上未提交的 S5 实现（本报告一至四节）；本轮只做定向修复，不重写已完成部分。

| 条 | 定级/定位 | 修复 | 测试 |
|---|---|---|---|
| R1 ctx 从不终止 wsl.exe | Major，plugins.go defaultPluginCommandRunner；app_wsl_plugins.go 两处 context.Background() | `exec.Command`→`exec.CommandContext`；App 编排面两处（RunPluginOperation/ProbeWSLPlugins 调用点）改 `context.WithTimeout(Background, piPluginReplayTimeout)`，常量对齐 piplugin `piCommandTimeout`=3 分钟惯例；超时归类结构化 timeout，挂起命令不再永久持有 opMu | 新增 `TestPluginDefaultRunnerHonorsCanceledContext`（真 default runner：已取消 ctx 立即返回 context.Canceled，不启动 wsl.exe）与 `TestPluginContextDeadlineYieldsStructuredTimeout`（可注入短 deadline：ctx 到期→结构化 Timeout、单次尝试、无幽灵登记） |
| R2 前端整层未接线 | Major，PiPluginPanel/ExtensionsView | PiPluginPanel 挂载 PiPluginWSLSection（ref 联动刷新）+ 目标选择器（Segmented：Windows/WSL/同时应用，经 computed 收窄类型）；install/update/remove 接通三个 targeted wrapper——windows 默认走既有 store 路径，含 WSL 目标每目标独立 toast（单侧失败独立显示），完成后两侧列表各刷新自己的实体；移除确认文案按目标动态化。WSLPluginTargetPlaceholder 重写为消费 GetWSLPluginTargetSupport 契约（claude/codex/opencode 占位、omp excluded 排除态分列，后端读取失败回退同义本地声明）并挂载到 ExtensionsView 四个 CLI 面板（claude/codex/opencode/omp） | wailsjs 绑定零新增（六方法已齐，无字面量传参风险）；vue-tsc 0 错误 + vitest 254/254；接线核验：4 个 wrapper/组件全部有消费点（见 grep 证据） |
| R3 幽灵登记行 | Major，plugins.go runInstall fail 路径/savePluginEntry | `!hasReg` 失败路径跳过 savePluginEntry（不再以空 Identity/Source/Generation append）；savePluginEntry 删除未用的 generation 形参（删参：所有写入方均显式携带 Generation，mutateEntry 同步简化为仅 hasReg 路径可用） | `TestPluginInstallFailureNeverRegistersInstalled` 补断言「失败后 Plugins 长度为 0」 |
| R4 损坏 settings 误判 already-ok | Minor，plugins.go readDistroSettingsPackages/runRemove | 返回三态（readable/missing/unreadable，os.IsNotExist 区分真缺失）；不可读/损坏在 RunPluginOperation 入口统一阻断（registration-mismatch），remove 不再把「读不到」冒充「已消失」；Probe 的 Reason 分列缺失/损坏文案 | 新增 `TestPluginRemoveCorruptSettingsBlockedNotFakeOK`（非法 JSON→blocked、登记保持、零 pi 重放） |
| R5 切换路径旁路脏仓库冻结 | Minor，plugins.go runInstall 版本切换段 | distroEntry.source ≠ replaySource 的 remove+install 切换前置与 runUpdate 同一 gitDirtyCheck（git-status 不可用保守冻结、脏仓库冻结盖戳，绝不 reset/clean） | 新增 `TestPluginInstallVersionSwitchDirtyRepoFrozen`（脏仓库→blocked dirty-repo、零 pi 重放、冻结盖戳） |
| R6 矛盾注释 | Minor，app_wsl_plugins.go | 改为「未运行（仅 wsl 目标）→distro 内独立解析；失败→WSL skipped」 | 注释与行为一致（skipped 分支代码未动，有既有 TestInstallPiPackageTargetedWindowsFailSkipsWSL 钉子） |
| R7 ValidateReplaySource 双源漂移 | Minor，piplugin/target.go | **选删参（删除死导出）**，理由：wslsupply 架构上不 import piplugin（解析经接缝注入），且其 parseSpec 闸门除基础校验外还须检查 parser 装配与 spec.Type 有效性——validateSource 无法单独服务该闸门；导出零生产消费者，删除后 WSL 侧闸门唯一落在 wslsupply.parseSpec、Windows 侧唯一落在 piplugin.validateSource，各自单一职责 | 删除 TestValidateReplaySource；piplugin 包全量测试通过 |

附带：对 5 个 S5 新文件（plugins.go/plugins_test.go/target.go/target_test.go/app_wsl_plugins.go，均未跟踪）按本机 go1.25.5 gofmt 归一（仅注释空格/对齐/字符串拼接空格，无语义变化；基线其余文件如 service.go/state.go 在同版本 gofmt 下本就有同类偏差，未动）。

验证（全部自跑，2026-10-16）：`go build ./...` exit 0；`go vet ./internal/wslsupply/ ./internal/piplugin/ .` exit 0；`gofmt -l`（5 个改动文件）零输出；`go test ./internal/wslsupply/ ./internal/piplugin/ -count=1` 全过（含上表 4 个新增测试与 20 项既有插件生命周期钉子）；`go test . -run "WSLPlugin|PluginTarget|WSLSkill"` 全过；前端 `npx vue-tsc --noEmit` 0 错误 + `npx vitest run` 254/254；frontend/ 无 C: 垃圾目录。未 commit/push。
