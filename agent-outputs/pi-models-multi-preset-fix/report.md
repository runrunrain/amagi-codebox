# pi/omp models.json 托管 provider 模型被覆盖为单个 — 根因与修复

## 症状

会话设置中选择预设启动 pi：所选 provider 有多个模型预设时，`~/.pi/agent/models.json` 中 `amagi-<provider>` 条目的 models 被替换为仅启动选中的那一个模型；多次用不同预设启动互相覆盖。

## 根因

启动链：`app.go LaunchPiSession` → `BuildPiModelsConfig(providerID, provider, 启动模型, key, 该预设参数)` → `MergePiAgentConfig`（现有内容并入，**同名托管条目 amagi-<name> 整体替换**）→ 写 models.json。

`BuildPiModelsConfig` 的 desired 只注册**当次启动选中的单个模型**（`entry["models"] = []{m}`）。merge 对托管条目是整体替换语义（该语义本身正确——托管条目应镜像 codebox 配置、自清理），因此 desired 单模型 → 每次启动把同 provider 其他预设模型全部挤掉。OMP 同构同病。

## 修复（v1.3.34 多模型注册）

`internal/launcher/pi_config.go`（OMP 共享，同构调用）：

- 抽取共享助手 `buildManagedModelEntry`（单模型条目构建，参数透传逻辑不变）+ `buildManagedModelEntries`：
  - **启动选中的模型排首位，参数以本次传入为准（权威）**；缺省回落 DefaultModel
  - 其余预设按 **key 排序**注册、各带**自己的 Parameters**（contextWindow/maxTokens/reasoning/compat 独立生效）
  - DefaultModel 未被覆盖时零参数兜底裸注册
  - 按模型 id 去重（同模型多预设：先注册者参数优先，输出确定）
- `BuildPiModelsConfig` / `BuildOmpModelsConfig` 改为注册完整 models 列表；merge 整体替换语义不变（desired 现已完整自洽，且预设删除后能自清理）。

## 行为变化

- 同 provider 任意预设启动 → models.json 保留**全部**预设模型（选中者排首）
- 单预设/无预设 provider 行为不变（启动模型排首，旧断言零改动通过）
- 托管条目镜像 codebox 当前配置：codebox 侧删预设 → pi 侧对应模型下次启动时消失（自清理）

## 测试（internal/launcher/pi_config_test.go）

- `TestBuildPiModelsConfigRegistersAllProviderPresetModels`：3 预设（含同 id 重复）+ DefaultModel → 3 模型、顺序/权威参数/去重/兜底裸注册逐项断言 ✅
- `TestBuildOmpModelsConfigRegistersAllProviderPresetModels`：omp 同构 2 模型 + 各自参数 ✅
- 既有用例（reasoning_effort 单独出现、env headers、文件权限等）零回归；launcher/webui/根包全绿。

## 手验

provider 配 2+ 模型预设 → 任选一预设启动 pi → `~/.pi/agent/models.json` 的 `amagi-<name>.models` 应含全部预设模型（选中者第一）；pi 内 `--model` 可直接引用任一预设模型。
