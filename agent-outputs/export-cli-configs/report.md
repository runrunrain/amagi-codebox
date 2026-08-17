# 导出/导入完整配置：增加 CLI 独立配置（opencode、pi、oh-my-pi）

## 摘要

v2 Portable 快照（`portableConfigSnapshot`）扩展 5 个可选 CLI 独立配置字段，覆盖 pi 的三个独立配置文件与 omp 的两个独立配置文件；opencode 此前已由 `opencode_global_config` 覆盖（本次保留并在文档中明确）。导出侧"文件存在且合法才导出、失败 Warn 跳过"，导入侧"按存在性整体替换、写失败 errors.Join 聚合并触发共享回滚"，旧 v2 导出文件（无新字段）导入行为完全不变。

## 一、字段清单（portable snapshot 新增）

| JSON 字段 | Go 字段 | 源文件（导出） | 读取方法 | 恢复写入 | 内容格式 |
|---|---|---|---|---|---|
| `pi_models_config` | `PiModelsConfig json.RawMessage` | `~/.pi/agent/models.json` | `PiConfig.GetModelsConfig` | `PiConfig.SaveModelsConfig` | JSON object |
| `pi_auth_config` | `PiAuthConfig json.RawMessage` | `~/.pi/agent/auth.json` | `PiConfig.GetAuthConfig` | `PiConfig.SaveAuthConfig` | JSON object（**含明文 auth token**） |
| `pi_amagi_config` | `PiAmagiConfig json.RawMessage` | `~/.pi/agent/amagi.json` | `PiConfig.GetAmagiConfig` | `PiConfig.SaveAmagiConfig` | JSON object |
| `omp_config` | `OmpConfig string` | `~/.omp/agent/config.yml` | `OmpConfig.GetOmpConfig` | `OmpConfig.SaveOmpConfig` | YAML mapping |
| `omp_models_config` | `OmpModelsConfig string` | `~/.omp/agent/models.yml` | `OmpConfig.GetModelsConfig` | `OmpConfig.SaveModelsConfig` | YAML mapping（**可含内联 apiKey**） |
| `opencode_global_config` | 既有 | OpenCode 全局配置 | `OpenCodeConfig.GetOpenCodeConfig` | `SaveOpenCodeConfig` | JSON object（本次确认保留） |

- 字段命名与任务书一致；`omp_config` 之外补充 `omp_models_config`——任务书要求"先查 internal/ompconfig/service.go 实际方法名与对应文件"，实际 omp 有两个独立配置文件（config.yml 角色配置 + models.yml provider 注册表），后者与 pi 的 models.json 对等（同为 CLI 自管 provider 注册表、含内联凭据），故一并纳入。
- agentDir 解析沿用服务现状：两者都优先 `$PI_CODING_AGENT_DIR`（ompconfig 现有行为即如此），否则分别回退 `~/.pi/agent` / `~/.omp/agent`。

## 二、行为设计

### 导出（`buildCompleteExportConfig` → `appendCLIConfigSections`）
- 每个字段经 `readCLIConfigSection`：**os.Stat 文件不存在 → 静默跳过**（不导出 Get* 返回的占位骨架，避免把"源设备未使用该 CLI"误表达为"空注册表"，防止导入时清空目标设备真实配置）；读取失败或内容非法（非 JSON object / 非 YAML mapping）→ `Log.Warn` + 跳过该字段，**不阻断导出**（区别于 OpenCode 全局配置的硬性要求，与现有容错风格对齐）。
- nil 服务（仅测试/部分初始化场景）→ 跳过。

### 导入（`applyCompleteConfig`）
1. **写入前校验**（`validateCompleteImportServices` 末尾新增 `validateCLIConfigSections`）：内容校验镜像 Save* 写入校验（JSON: 合法+根对象；YAML: 合法+根映射），畸形 section 的错误用 errors.Join 聚合一次性报出，**在触碰任何实际状态前失败**，不依赖回滚。
2. **写入**（OpenCode 之后、`rollbackNeeded = false` 之前，新增 `applyCLIConfigSections`）：按字段存在性逐个调用 Save*，**整体替换目标文件**；写失败用 **errors.Join 聚合**（全部字段尝试完再返回），返回错误触发 applyCompleteConfig 既有 defer 回滚。
3. **回滚**（`restoreCompleteConfig`）：复用 `applyCLIConfigSections` 按（回滚快照中的）存在性恢复，errors.Join 进既有聚合。

### 兼容策略
- 版本保持 `2.0` 不变：新字段全部 optional（`omitempty`），旧 v2 文件缺字段 → 导入跳过写入，行为与改动前完全一致；旧版本 App 读新文件 → `json.Unmarshal` 忽略未知字段，前向兼容。
- `portableConfigSnapshot.validate()` 刻意**不**校验新字段：回滚快照经 `decodePortableConfig` 解码，若在此严格校验，源设备上存在畸形 pi/omp 配置的用户将连导入都失败（回滚快照解码失败）→ 畸形内容改由导出侧跳过 + 导入侧写入前校验兜底。

## 三、与 codebox 管理配置的共存（任务书第 4 点核查结论）

- pi/omp 的 Save* 方法为**全量覆盖写**（临时文件 + rename 整文件替换）。导入即以导出设备快照整体替换 `models.json` / `models.yml` 等——属 v2"完整快照替换"既定语义（与 `ReplaceProviders` 空集清空 provider 同类），未做合并，符合任务书"不额外做合并"的裁定。
- 会话启动路径（`LaunchPiSession` / `LaunchOmpSession`）经 `launcher.MergePiAgentConfig` **合并**写入 `amagi-<name>` 托管条目（保留用户已有 provider 与顶层配置），因此导入的自定义 provider 与 codebox 托管条目在同一文件**共存**：导入替换一次，后续会话启动只做合并覆盖，不冲突。

## 四、兼容矩阵

| 场景 | 导出 | 导入 |
|---|---|---|
| 源/目标均无 pi/omp 配置文件 | 5 字段全缺省（无骨架导出） | 无写入，不创建任何骨架文件（有测试） |
| 源有、目标无 | 全文导出 | 按存在性创建并写入 |
| 源无、目标有 | 字段缺省 | 目标文件不动（有测试） |
| 旧 v2 文件（无新字段） | — | 行为与改动前逐字节一致（有测试） |
| 新文件 → 旧版本 App | — | 未知字段被忽略，回退为不含 CLI 段的导入 |
| 源文件畸形（非法 JSON/YAML/不可读） | Warn + 跳过该字段，导出继续（有测试） | — |
| 快照内容畸形（手工篡改等） | — | 写入前整体报错（errors.Join），零改动、零回滚（有测试） |
| 目标写入失败（目录占位等） | — | errors.Join 聚合全部失败 + 触发回滚（有测试） |

## 五、敏感项说明

导出文件为明文 JSON，除既有 provider API key / secrets / 环境变量外，本次新增明文敏感内容：
- `pi_auth_config`：pi auth.json 的 auth token / API key（与现有 `ExportProvider.APIKey` 明文导出语义一致）；
- `pi_models_config` / `omp_models_config`：models 注册表的内联 `apiKey` / `auth` 字段。

已在 docs/api.md、docs/user/providers.md、docs/user/usage.md 三处明示"导出文件含明文凭据，请妥善保管"。

## 六、修改文件

- `app_config_portable.go`：结构扩展 + 导出/导入/回滚/校验五处链路（新增 `appendCLIConfigSections` / `readCLIConfigSection` / `applyCLIConfigSections` / `validateCLIConfigSections` / `validateJSONObjectConfig` / `validateYAMLMappingConfig` / `warnCLIConfigSkip`；引入 `gopkg.in/yaml.v3`，已在 vendor）。
- `app_config_portable_test.go`：测试 App 助手补 `PiConfig`/`OmpConfig`/`PI_CODING_AGENT_DIR`；新增 5 个测试（round-trip、缺文件/损坏跳过、旧文件兼容、畸形拒绝、写失败聚合）。
- `docs/api.md`、`docs/user/providers.md`、`docs/user/usage.md`：导出内容清单与导入语义更新（含敏感项明示）。

## 七、验证证据

- `go vet ./...` → 干净（VET_CLEAN）。
- `go test . -count=1`（package main 全量）→ ok 6.186s。
- `go test ./internal/piconfig/... ./internal/ompconfig/... ./internal/config/... -count=1` → 全 ok。
- 新增/既有 portable 测试 8 项全 PASS（`-run 'TestCompleteConfig|TestDecodeConfigExport' -count=1 -v`）。
- `gofmt -l` 对两个改动 Go 文件无输出。

## 八、剩余风险与未覆盖项

- **回滚尽力而为边界**：若源设备某 CLI 配置文件畸形/不可读，回滚快照缺该字段，导入中途失败回滚时该文件保持"导入后的内容"（原畸形内容丢失）。与既有 best-effort 回滚同类，已在设计注释中说明。
- omp 与 pi 共用 `$PI_CODING_AGENT_DIR` 环境变量优先级是 `internal/ompconfig` 既有行为（疑似上游复制遗留），本次未改动；如需改为 omp 专属变量属范围外。
- Windows 上 `writePrivateFile` rename 失败回退覆盖路径已由服务自身处理，本次未新增平台分支；CI Windows 仅编译检查，与仓库现状一致。
- 前端 `frontend/src/views`（Provider Center 导出说明文案）未改动——后端绑定方法签名无变化，前端无需感知新字段；如需 UI 文案同步可另行任务。
