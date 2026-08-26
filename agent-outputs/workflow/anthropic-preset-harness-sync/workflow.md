# Workflow: Anthropic 桶预设标记同步到 CLI 独立配置

## 目标与总验收

用户可在 anthropic 格式的终端预设上开启「同步到 CLI 独立配置」标记；开启后该预设的模型随 provider 同步写入 Pi/OMP 托管模型配置（`~/.pi/agent/models.json` / `~/.omp/agent/models.yml`），anthropic-only 提供商的模型因此可被 pi/omp 使用。OpenCode（本就消费双桶）与未标记预设行为零变化。

总验收：
1. `TerminalPreset`/`MergedTerminalPreset` 新增 `HarnessSync bool`（json `harness_sync,omitempty`），持久化与 API 透传。
2. `launcher.ManagedPresetModels`：标记的 anthropic 桶预设模型被纳入（即使调用方只传 `TerminalPresetOpenAI`）；未标记不纳入；openai 桶/opencode 双桶语义不变。
3. anthropic-only provider + 标记预设 → `BuildPiManagedProviderConfig`/`BuildOmpManagedProviderConfig` 输出 `api=anthropic-messages` 且 models 含标记模型。
4. 前端：anthropic 格式预设编辑弹窗显示开关；列表显示「CLI」badge。
5. `go vet ./...`、相关 `go test -count=1`、`npm --prefix frontend run build` 全绿；wailsjs 绑定重新生成。

## Phase 表

| Phase | 目标 | 切片 | Agent | 依赖 | artifact | 验收 | 状态 |
|---|---|---|---|---|---|---|---|
| P1 | 后端契约与收集规则 | A: config 字段 + ManagedPresetModels 规则 + 透传 + Go 测试 | luban | — | 代码 + 测试证据 | vet/test 绿；契约字段命名与 Leader 定义一致 | done ✅ |
| P2 | 前端标记 UI + 绑定再生 | B: PresetDialog 开关 + PresetList badge + wailsjs regenerate | luoshen | P1（wailsjs 从 Go struct 生成） | 代码 + build 证据 | vue-tsc/build 绿；models.ts 含 harness_sync | done ✅ |
| P3 | 集成验收 | Leader: 全量验证 + diff 核对 | Leader | P2 | 验证记录 | 总验收 1-5 全部 met | done ✅ |
| P4 | 启动选择面扩展：pi/omp 可选 marked anthropic 预设与提供商 | C: 后端解析回退 + 三 surface 选项合并 + 测试（luban）；D: SessionSettingsView 合并预设/提供商选项（luoshen） | luban + luoshen 并行 | P2（HarnessSync 标记已就绪） | 代码 + 测试/build 证据 | 选 marked anthropic 预设启动 pi/omp 全链路可用；codex/claudecode 行为零变化 | done ✅ |

## 并行与隔离

A→B 严格串行（B 的 wailsjs 生成依赖 A 的 Go struct）。无共享写入冲突，不用 worktree。

## 专家 Gate

风险判定 medium 低风险（与 vision/video 标记完全同构的成熟模式 + 完整测试覆盖）：实现自检 + Leader 验收，不调 diting/fuxi。

## 契约（Leader 固定，切片不得偏离）

- `config.TerminalPreset.HarnessSync bool \`json:"harness_sync,omitempty"\``——语义：该预设模型是否加入 pi/omp 托管模型列表；仅对 anthropic 桶有意义（openai 桶默认全同步，标记为 no-op）。
- `config.MergedTerminalPreset.HarnessSync bool \`json:"harness_sync,omitempty"\`` + `GetMergedTerminalPresets` 透传。
- `ManagedPresetModels` 收集规则：桶 ∈ terminalTypes ∨ `preset.HarnessSync`；标记桶追加在请求桶之后处理，同 id 后序胜出，键序确定。
- UI 文案：开关「同步到 CLI 独立配置」，badge「CLI」。

## 风险、决策与修订日志

- 2025-12-19 天城：调查确认 pi/omp 全部 4 个 `ManagedPresetModels` 调用点（provider_harness_sync.go:135、launch_planner.go:668、app.go:3075/3332）只传 openai 桶；逻辑收敛在 ManagedPresetModels 内部使启动路径（托管条目整体替换语义）自动一致，调用点零修改——这是选该实现位置的关键理由。
- 2025-12-19 天城：P1/P2 均一次通过、零返工，Leader 核对 diff 与契约一致后集成验收：go vet ./... 绿、go test -count=1 ./... 全绿（含 config/launcher/remote 定向确认）、frontend build（vue-tsc+vite）绿、wailsjs 仅 +4 行干净再生。总验收 1-5 全部 met。
- 2025-12-19 天城：追加 P4（用户二次需求：pi/omp 启动选择面也要能选 marked anthropic 预设）。调查发现三个选项 surface（桌面 SessionSettingsView、v1 buildRemoteLaunchSettings、legacy launch metadata）+ 4 个后端解析点（app.go LaunchPi/LaunchOmp、launch_planner buildPiPlan/buildOmpPlan）全部硬缩 openai 桶。契约：解析优先序 openai 桶 → marked anthropic 桶（同 key 撞名时 openai 胜出，与前端 find 首匹配一致）；codex/claudecode 不动。
- 2025-12-19 天城：P4 两切片并行一次通过（luban 后端 + luoshen 前端），Leader 集成验收：vet/test/build 全绿，收口。
