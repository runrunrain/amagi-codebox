# P4-A2 报告：UE5/VS 变量转发——预置模板 + 路径值 per-key 放行同意门（E5+E6）

- 任务：P4-A2（主上已批按计划）。上游证据：`agent-outputs/wsl-session-friction/p4-evidence.md`（E5/E6/§1）。
- 执行环境：WSL bash 宿主，Windows go1.25.5 经 interop 调用（`"/mnt/c/Program Files/Go/bin/go.exe"`，GOCACHE/GOPATH 经 WSLENV `/p` 转发）；Node v22.23.2。
- 终态：**完成**。全部验收自跑通过；默认防泄漏基线不变（回归钉定）。

## 1. 改动文件（全部为本任务产物；未 commit/push）

### Go 后端
| 文件 | 改动 |
|---|---|
| `internal/envvars/service.go` | `EnvVar` 新增 `AllowPathValue bool`（json `allowPathValue,omitempty`，缺省 false=默认剥离）；`validateEnvVars` 拒绝保留键+标记（BatchSet/Import/SaveJSON 全走此处）；新增 `PathAllowKeys()`（保序返回已标记键）与 `SetPathAllow(key, allow)`（翻转标记持久化；保留键开启拒绝；未知键报错） |
| `internal/platform/wsl.go` | `isWSLENVReservedKey` → 导出 `IsWSLENVReservedKey`（三包共用判据）；新增 `appendWSLENVForwardingWithPaths(env, extraForwardKeys, pathFlagKeys)`：放行键在 WSLENV 清单获 `NAME/p` 条目（值原样留 env，/p 翻译由 WSL 跨界执行）；旧 `appendWSLENVForwarding` 变薄壳（nil 放行清单，行为不变）；导出 `AppendWSLENVForwardingWithPaths` 供链路测试/复用 |
| `internal/platform/resolver.go` | `ResolveRequest` 新增 `EnvPathFlagKeys []string`；WSL 分支改调 `appendWSLENVForwardingWithPaths(..., EnvForwardKeys, EnvPathFlagKeys)` |
| `internal/launcher/wsl_agent_config.go` | `StripWSLHostPathEnv` 变薄壳；新增 `StripWSLHostPathEnvWithAllow(env, extraForwardKeys, allowPathKeys) → (kept, stripped, allowed)`：标记键豁免剥离并计入 allowed（供日志），未标记键行为与原完全一致；保留键标记无效（本就不在转发面） |
| `app.go` | `resolveEmbeddedLaunchSpec` 漏斗：`req.EnvPathFlagKeys = a.EnvVars.PathAllowKeys()`；strip 换 WithAllow 变体；**放行键记 INFO**「已放行跨界路径值环境变量（per-key 放行 + /p 路径翻译）」，剥离键 WARN 保持不变；新增 App 门面 `SetEnvVarPathAllow(key string, allow bool) error` |

### 测试（Go）
| 文件 | 覆盖 |
|---|---|
| `internal/platform/wsl_test.go` | `TestAppendWSLENVForwardingPathFlagKeys`：放行键 `/p`；未放行键无标志（基线不变）；保留键标记无效不入清单；不在 env 的键不入清单；值原样保留；大小写不敏感；旧壳零 `/p`；extra 清单键与前缀键（PI_*）两条真实链路均生效 |
| `internal/launcher/wsl_agent_config_test.go` | `TestStripWSLHostPathEnvAllowGate`：标记键保留+计入 allowed、未标记键照剥、保留键 no-op、非路径值不产生豁免记录、大小写不敏感、**旧签名回归=默认剥离完全一致**；`TestStripThenForwardPathAllowChain`：漏斗顺序（strip→forward 同一 allow 清单）端到端断言——放行键存活且 WSLENV 带 `/p`、被剥键不入清单、标量键无标志转发、值 verbatim |
| `internal/envvars/wsl_path_allow_test.go`（新） | 标记 JSON 往返（omitempty：未标记键不落字段）；保留键拒绝（BatchSet+Import 双路）；`SetPathAllow` 持久化/幂等/大小写/未知键/保留键；`Set(key,value)` 不丢标记；PortableConfig 携带标记 |

### 前端
| 文件 | 改动 |
|---|---|
| `frontend/src/api/envvars.ts` | `setEnvVarPathAllow` 包装（原生参数透传）；判定面 `isWindowsDrivePathValue` / `WSL_RESERVED_ENV_KEYS` / `isWSLReservedEnvKey`；**UE5 模板** `UE5_ENV_TEMPLATE`（静态清单：UE_ROOT/UE_VERSION_MAJOR/MINOR/HOTFIX，仅描述+pathLike，无预设 value、无放行标记） |
| `frontend/src/components/extensions/EnvVarsPanel.vue` | 工具栏「UE5 模板」入口（勾选添加为待填键，值空、已存在键禁选）；表格键名下方 WSL 状态行：路径值键「WSL：路径值默认不转发」+ Switch；放行前弹**风险确认门**（值将跨界可见并自动译为 /mnt 路径）；已放行态「路径值已放行（自动译为 /mnt）」；**保留键显示「保留键 · 不参与 WSL 转发，不可放行」**（不放开关） |
| `frontend/wailsjs/go/main/App.js` / `App.d.ts` | 手工补齐 `SetEnvVarPathAllow(arg1, arg2)` / `(arg1:string,arg2:boolean):Promise<void>`（含再生注释；未改 models.ts——门面走原生类型） |
| `frontend/src/__tests__/api/envvars.test.ts`（新） | 判定面真/假例（盘符双斜杠形态、/mnt、标量、C:rel、空白容忍；保留键大小写）；`setEnvVarPathAllow` 透传断言；模板清单结构（含 UE_ROOT+版本键、无重复、键名合法、无 value/allowPathValue 字段、仅 UE_ROOT pathLike） |

## 2. 机制说明（放行门全链路）

```
envvars.json {key, value, allowPathValue}
   │ PathAllowKeys()                Keys()
   ▼                                ▼
req.EnvPathFlagKeys            req.EnvForwardKeys
   │（app.go 漏斗，同一来源同时交给两侧）
   ├─▶ StripWSLHostPathEnvWithAllow(env, EnvForwardKeys, EnvPathFlagKeys)
   │     未标记路径值键 → 剥离（WARN，P1 基线不变）
   │     标记路径值键   → 保留 + 计入 allowed（INFO「per-key 放行 + /p 路径翻译」）
   └─▶ resolver WSL 分支 appendWSLENVForwardingWithPaths(env, EnvForwardKeys, EnvPathFlagKeys)
         标记键 → WSLENV 条目 NAME/p → WSL 跨界时把 C:\... 译为 /mnt/c/...
         （值本身 verbatim 留在进程 env，翻译由 WSL 执行）
```

**安全默认不变声明**：无 `allowPathValue` 标记时，`StripWSLHostPathEnv`（薄壳）行为与本批之前逐字节一致（既有 `TestStripWSLHostPathEnv` 全绿即为回归钉定）；`appendWSLENVForwarding`（薄壳）零 `/p` 标志；保留键（WSLENV/PATH/PATHEXT）三重防线不参与放行门——保存校验拒绝（validateEnvVars/SetPathAllow）、转发清单排除（IsWSLENVReservedKey 先于 extra 匹配）、剥离豁免面过滤（保留键标记静默无效）。

## 3. 验证证据（全部自跑，cwd=仓库根）

| # | 验收 | 命令 | 结果 |
|---|---|---|---|
| 1 | 构建 | `go.exe build ./...` | ✅ EXIT=0（2026-10-17，含中途兄弟包短暂坏态恢复后复跑） |
| 2 | vet | `go.exe vet ./internal/launcher/ ./internal/platform/ ./internal/envvars/ .` | ✅ 全 EXIT=0。注：platform vet 曾仅命中兄弟任务未跟踪文件 `drvfs_advisory_test.go:73`（append with no values）——隔离副本（剔除兄弟未跟踪文件，.tmp-tests/p4a2-vet，已清理）证明我方文件 vet 干净；稍后兄弟修复，真实树复跑 EXIT=0 |
| 3 | gofmt | `gofmt.exe -l <我的 9 个 Go 文件>` | ✅ 零输出 |
| 4 | launcher+platform+envvars | `go.exe test ./internal/launcher/ ./internal/platform/ ./internal/envvars/ -count=1 -skip "TestDarwinFNM…\|AgentConfigTightPerms\|AgentConfigUpgradesLegacyPerms"` | ✅ 3 包 ok。skip 的 5 项为 Windows 通道预存环境失败（p1-report §97-98 钉定：POSIX 权限位断言 + os.Symlink 特权），与本批无交集；**不加 skip 时该 5 项失败、其余全绿** |
| 5 | 根包 | `go.exe test . -run "WSL\|ENV\|Forward" -count=1` | ✅ ok |
| 6 | 前端类型 | `npx vue-tsc --noEmit`（frontend/，Node v22.23.2） | ✅ 0 错误 |
| 7 | 前端单测 | `npx vitest run`（frontend/） | ✅ 30 文件 271 测试全过（含新增 envvars.test.ts 6 例单跑 ✅） |

未跑项：无（任务验证清单 1-5 全数执行）。未做真机 WSL 会话内 `env | grep UE_ROOT` 实测（需 GUI 启动会话，超出本任务通道；WSLENV /p 语义为微软文档化机制，代码侧已按「值 verbatim + NAME/p 条目」钉定）。

## 4. 事故与处置记录（共享树）

- **go fmt ./... 误伤**（已完全恢复）：本批中途误跑 `go fmt ./...`，对 29 个未staged 文件产生纯空白格式化写入。识别方法：`git diff --name-only` vs `git diff -w --name-only` 差集=纯空白改动集；处置：`git checkout -- <29 文件>`（恢复到 index 状态，仅丢弃我方空白写入；index 内容零改动），恢复后 unstaged 集与真实编辑集完全一致（diff 为空）。残留风险：①`app_remoteclient_test.go`（兄弟有未staged真实编辑+我的 gofmt 空白归一，无法分离，保留——语义零影响）；②兄弟未跟踪 .go 文件（drvfs_advisory* 等）可能被同步格式化（无基线可比对，空白级风险）。
- 兄弟任务中间态两次影响验证：`internal/gitassist` 包名冲突（后自愈）、`drvfs_advisory_test.go` vet 命中（后自愈）——均已按「先甄别归属再处置」处理，未动兄弟文件。
- 派发时 300+ staged 基线文件全程未触碰。

## 5. 回滚方式

全部改动未 staged、未 commit。回滚=丢弃下列文件的未staged改动（`git checkout --` 或 IDE 放弃更改）：
`app.go`、`internal/envvars/service.go`、`internal/platform/resolver.go`、`internal/platform/wsl.go`、`internal/platform/wsl_test.go`、`internal/launcher/wsl_agent_config.go`、`internal/launcher/wsl_agent_config_test.go`、`frontend/src/api/envvars.ts`、`frontend/src/components/extensions/EnvVarsPanel.vue`、`frontend/wailsjs/go/main/App.js`、`frontend/wailsjs/go/main/App.d.ts`；删除新文件 `internal/envvars/wsl_path_allow_test.go`、`frontend/src/__tests__/api/envvars.test.ts`。⚠ 注意 `App.js/App.d.ts/EnvVarsPanel.vue` 若后续混入他人改动，回滚前需先比对（当前 App.js/App.d.ts 含兄弟的 GetDrvFSAdvisories 手工绑定，勿整文件回退到 HEAD，只撤本批新增的 SetEnvVarPathAllow 块）。envvars.json 运行时数据无需回滚（allowPathValue 缺省即旧行为）。

## 6. 未覆盖 / 风险

- WSLENV `/p` 双向语义：`/p` 同时作用于 W→L 与 L→W 翻译；WSL 内工具改写该变量回传 Windows 的场景（极少见）会得到翻译后路径——已在 UI 风险提示中覆盖主语义（跨界可见+/mnt 翻译）。
- `StripWSLHostPathEnv` 旧签名保留为薄壳（pi_login planner 链兑底剥离未接放行门——该链路只剥 PI_CODING_AGENT_DIR，与 envvars 自定义键无关，无需接入门）。
- remote v1 / OpenCode remote effect（`AppendWSLENVForwarding` 旧壳）未接 pathFlagKeys——remote 会话无 envvars.json 注入链（EnvForwardKeys 恒空），放行门自然不适用。
