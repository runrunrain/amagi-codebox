# P2-T1 报告：WSL2 mirrored 网络模式检测 + AI 代理分流对 WSL 会话放行

- 任务：platform 层新增 `.wslconfig` 网络模式探测；aiproxy WSL 闸门改为 mirrored 放行、NAT/未知拒绝（新中文指引文案）；envcheck 新增 WSL 网络模式信息级条目。前端零改动。
- 主上裁定依据：`agent-outputs/wsl-session-friction/evidence-report.md`（A1/B1/B2/C5）——mirrored 模式下 localhost 双向可达，宿主回环入口对 WSL 会话自然可用。
- 验证环境：Windows 宿主（真机，distro=Ubuntu，`%USERPROFILE%\.wslconfig` 实测 `networkingMode=mirrored`）+ Windows Go 1.25.5（`go.exe`，经 WSL interop 调用；GOCACHE/GOPATH 经 WSLENV 显式转发）。

## 一、改动清单

| 文件 | 改动 |
|---|---|
| `internal/platform/wsl.go` | 新增 `WSLNetworkingMode(env []string) string` 导出函数 + `probeWSLNetworkingMode`（读 `<home>\.wslconfig`，复用 `decodeWSLListOutput` 宽容解码 BOM/UTF-16）+ `parseWSLConfigNetworkingMode`（纯 INI 解析）+ 按 home 路径键控的进程级缓存（`wslNetworkingModeCache`）。不 fork wsl.exe。 |
| `internal/platform/wsl_test.go` | +3 测试：解析规则表（18 形态）、env 驱动探测（USERPROFILE/HOME/缺失/UTF-16/UTF-8 BOM）、按 home 缓存语义（改文件不重读、异 home 独立探测）。 |
| `internal/aiproxy/environment.go` | `PinProxyEnvironment` WSL 分支：`platform.WSLNetworkingMode(spec.Env.Variables) != "mirrored"` → 拒绝（新文案，见下）；`== "mirrored"` → `return spec, nil` 放行（spec 照常携带统一分流入口；代理键本就经 WSLENV 白名单跨界，mirrored 下 127.0.0.1 即宿主入口）。依赖方向不变：aiproxy 已 import platform，无循环。 |
| `internal/aiproxy/environment_test.go`（新） | 4 测试：mirrored 放行（spec 原样通过）/ NAT 拒绝 + 文案断言 / 无 .wslconfig 拒绝 / 未启用分流（无 AMAGI 标记）不过闸。 |
| `internal/aiproxy/service_test.go` | 既有 `TestProxyVariablesPinnedAfterShellProfiles` 的 WSL 断言追加 `USERPROFILE=<t.TempDir()>`（无 .wslconfig）——宿主真机可能已切 mirrored，闸门判定必须与宿主实际配置解耦（本宿主即实测命中：不钉住则该测试在本机必失败）。 |
| `internal/envcheck/wsl_network.go`（新） | `appendWSLNetworkingModeInfo`：severity=info、code=`wsl_networking_mode` 的 CheckIssue 注入；门控 = 该工具有 WSL 会话命令 + `runtimeGOOS=="windows"` + `wslHybridDistro()!=""`。探测经包级 var `wslNetworkingMode`（→`platform.WSLNetworkingMode(nil)`），测试可注入。 |
| `internal/envcheck/service.go` | `finishToolCheck` 在 `appendWSLHybridArchWarning` 后追加一行 `s.appendWSLNetworkingModeInfo(status)`——与 C7 warning 同一漏斗，severity-only 不影响 AllOK（AllOK 只由 Installed/PATHOk/Error 驱动）。 |

**aiproxy NAT/未知拒绝新文案（C5 清偿）**：

> AI 代理分流的统一入口监听宿主回环（127.0.0.1），当前 WSL 为 NAT 网络模式，发行版内不可达。可在 %USERPROFILE%\\.wslconfig 的 [wsl2] 段设置 networkingMode=mirrored，执行 wsl --shutdown 重启 WSL 后重试；或停用 AI 代理分流后启动 WSL 会话

**envcheck 三态条目**（Message / Detail 要点）：
- mirrored：「WSL 网络模式：mirrored」／localhost 与宿主双向互通，AI 代理分流与 Headroom 对 WSL 会话可用。
- nat：「WSL 网络模式：NAT」／回环不可达 + 与 aiproxy 一致的切换指引（`wslNetworkingModeSwitchGuide` 常量同源）。
- 未知：「WSL 网络模式：未知（按 NAT 处理）」／未检测到 networkingMode 配置 + 同上指引。

前端确认零改动：`EnvCheckSettings.vue` 的 issue 渲染通用（severity 徽标 + message + detail），`info` → 「信息」标签、不加 ⚠ 图标（`isNegativeSeverity` 只认 warning/error/critical）。

## 二、WSLNetworkingMode 解析规则表

home 解析（镜像 wsl.exe 自身定位方式）：`env` 的 `USERPROFILE` → `env` 的 `HOME` → `os.UserHomeDir()`；读 `<home>\.wslconfig`。

| 输入形态 | 返回 | 说明 |
|---|---|---|
| `[wsl2]` 段 `networkingMode=mirrored`（键/值大小写不限、`=` 两侧空格容忍） | `"mirrored"` | |
| `[wsl2]` 段 `networkingMode=nat` | `"nat"` | |
| 无文件 / 读失败 | `""` | fail-safe 按 NAT |
| 无 `[wsl2]` 段 / 段内无该键 / 键在段外或其它段 | `""` | 段名大小写不敏感（`[WSL2]` 等效） |
| 值非 nat/mirrored（如 bridged、空值、拼错） | `""` | 未知值不猜，fail-safe 按 NAT |
| 整行注释（`#`/`;` 开头） | 跳过 | |
| 值内行内注释（`networkingMode=mirrored # ...`） | 剥离后取值 | |
| UTF-8 BOM / UTF-16 LE/BE（含 BOM）/ NUL 启发式 UTF-16 | 正常解码 | 复用 `decodeWSLListOutput`（同包既有单测覆盖） |
| CRLF / 空行 / 非键值垃圾行 | 容忍 | |
| 重复 `[wsl2]` networkingMode 键 | 首个命中生效 | INI first-wins |

缓存选择理由：文件极少变（切模式需改文件 + `wsl --shutdown`），进程级缓存贴合单用户桌面现实；**按 home 路径键控**（而非单布尔）使跨包测试（aiproxy/envcheck 无法触达 platform 未导出的 reset 钩子）以各自临时 USERPROFILE 独立探针，确定性无需 reset。

## 三、测试结果原文

```
$ go.exe test ./internal/platform/ -run "WSLConfig|WSLNetworking" -v
=== RUN   TestParseWSLConfigNetworkingMode
--- PASS: TestParseWSLConfigNetworkingMode (0.00s)
=== RUN   TestWSLNetworkingModeFromEnv
--- PASS: TestWSLNetworkingModeFromEnv (0.01s)
=== RUN   TestWSLNetworkingModeCachePerHome
--- PASS: TestWSLNetworkingModeCachePerHome (0.01s)
PASS
ok  	amagi-codebox/internal/platform	0.885s

$ go.exe test ./internal/aiproxy/ -run "TestPinProxyEnvironment|TestProxyVariablesPinned" -v
=== RUN   TestPinProxyEnvironmentWSLMirroredPasses
--- PASS: TestPinProxyEnvironmentWSLMirroredPasses (0.00s)
=== RUN   TestPinProxyEnvironmentWSLNATRejected
--- PASS: TestPinProxyEnvironmentWSLNATRejected (0.00s)
=== RUN   TestPinProxyEnvironmentWSLNoConfigRejected
--- PASS: TestPinProxyEnvironmentWSLNoConfigRejected (0.00s)
=== RUN   TestPinProxyEnvironmentWSLInactiveBypassesGate
--- PASS: TestPinProxyEnvironmentWSLInactiveBypassesGate (0.00s)
=== RUN   TestProxyVariablesPinnedAfterShellProfiles
--- PASS: TestProxyVariablesPinnedAfterShellProfiles (0.00s)
PASS
ok  	amagi-codebox/internal/aiproxy	5.617s

$ go.exe test ./internal/envcheck/ -run "WSLNetworkingModeInfo" -v
=== RUN   TestAppendWSLNetworkingModeInfo_ThreeModes
--- PASS: TestAppendWSLNetworkingModeInfo_ThreeModes (0.00s)
=== RUN   TestAppendWSLNetworkingModeInfo_Gates
--- PASS: TestAppendWSLNetworkingModeInfo_Gates (0.00s)
=== RUN   TestFinishToolCheckAppendsWSLNetworkingModeInfo
--- PASS: TestFinishToolCheckAppendsWSLNetworkingModeInfo (0.00s)
PASS
ok  	amagi-codebox/internal/envcheck	0.738s
```

整包回归（本 Windows 宿主，`-count=1`）：

```
$ go.exe test ./internal/platform/ -count=1 -skip "TestDarwinFNMDefaultAliasDeduplicatedAgainstPhysicalInstallDir"
ok      amagi-codebox/internal/platform   0.412s
$ go.exe test ./internal/aiproxy/ -count=1 -run "TestPinProxyEnvironment|TestProxyVariables"
ok      amagi-codebox/internal/aiproxy    0.079s
$ go.exe test ./internal/envcheck/ -count=1 -skip "TestOpenCodeNPMGlobalExecutableCandidates_IncludesPrefixAndResolvedNodeModulesBin|TestUpdate_ClaudeVerifyFail_ContinuesToFallback"
ok      amagi-codebox/internal/envcheck   10.954s
$ go.exe test . -count=1 -run "AIProxy|Proxy"        # 根包 aiproxy 绑定面
ok      amagi-codebox                     0.316s
$ go.exe vet ./internal/platform/ ./internal/aiproxy/ ./internal/envcheck/   → VET-OK
$ go.exe build ./...                                                        → BUILD-OK
$ gofmt.exe -l <全部 8 个改动文件>                                           → 空（clean）
```

**4 个未 skip 的失败均为宿主环境性、且实证先在（与本改动无关）**——用 `go test -overlay` 把被改文件映射回 HEAD 版本重跑，失败逐字相同：

| 测试 | 失败原因 | 先在性证据 |
|---|---|---|
| platform `TestDarwinFNMDefaultAliasDeduplicatedAgainstPhysicalInstallDir` | `os.Symlink` 需 Windows 特权（"A required privilege is not held by the client"） | 与改动无交集（resolver/fnm 去重） |
| aiproxy `TestPrivacyPersistenceAndDisabledEnvironment` | 断言 `Perm()==0600`——Windows 文件 Perm 语义报 0666 | HEAD overlay 重跑同样失败；断言点先于任何被改代码 |
| aiproxy/v2rayn `TestReadLocalDatabaseReadOnly` | Windows 只读文件语义 | 子包零改动 |
| envcheck `TestOpenCodeNPMGlobalExecutableCandidates_...` / `TestUpdate_ClaudeVerifyFail_...` | Windows npm 布局（`\opt\npm\node_modules\.bin`）与版本比较语义 | HEAD `service.go` overlay 重跑同样失败 |

（CI 全量 `go test` 只在 macos-latest 跑；windows-latest 仅 `-run '^$'` 编译检查——此类宿主差异不在 CI 观测面内。）

**真机全链验证**（本宿主 mirrored + Ubuntu distro，模块内一次性探针，`.tmp-tests/` 零残留）：

```
$ go.exe run .tmp-tests/p2t1-realprobe/main.go
real-host WSLNetworkingMode(nil) = "mirrored"
real-host DefaultWSLDistro = "Ubuntu"
real-host PinProxyEnvironment(WSL) = admitted, startup="claude" vars=3
```

## 四、mirrored / NAT 行为矩阵

| WSL 网络模式 | aiproxy 闸门（启用分流 + WSL 会话） | envcheck 条目 | 语义依据 |
|---|---|---|---|
| mirrored | **放行**：spec 原样通过，代理键经 WSLENV 转发，distro 内 127.0.0.1 ↔ 宿主回环互通 | info「mirrored」+ 可用性附注 | localhost 双向可达 |
| nat | 拒绝 + 新中文指引（.wslconfig 路径 / [wsl2] 键 / wsl --shutdown / 停用分流四要素） | info「NAT」+ 同源指引 | distro 回环指向自身 |
| 未知（无文件/无键/畸形值） | 拒绝（同 NAT 文案） | info「未知（按 NAT 处理）」 | fail-safe，与改造前行为一致 |
| 非启用分流 / 非 WSL 会话 | 不过闸（不变） | n/a | |

## 五、未覆盖项 / 风险 / 回滚

- **前端遗留文案**：`frontend/src/views/AIProxyView.vue:99` 仍提示 WSL 不支持（写面外，未动）；`docs/developer/ai-proxy-routing.md:52` 契约行「启用分流时拒绝对应 WSL 启动」已过时（docs 不在写面）。两者建议 Leader 随 CHANGELOG 一并收口。
- **进程级缓存**：会话进行中用户改 `.wslconfig` + `wsl --shutdown` 后，应用内闸门判定沿用旧值直至重启应用（文档化取舍；切换模式本身要求重启 WSL）。
- **mirrored 放行后未加登录 shell 再锚定**：WSL 会话的 `bash -lic` 理论上可被 profile 覆盖代理键（ShellInline 路径有 `env '...'` 前缀再锚定，WSL 路径无——StartupCommand 会被 pty 层单引号包裹，嵌套引号语义不同，超出本切片）。风险与改造前 forwarded 变量一致，非新引入。
- **共享树旁证**：`agent-outputs/workflow/wsl-session-friction/workflow.md` 在任务期间由 Leader 侧并行修改（git status 可见），本任务未触碰。
- **回滚**：`git checkout -- internal/platform/wsl.go internal/platform/wsl_test.go internal/aiproxy/environment.go internal/aiproxy/service_test.go internal/envcheck/service.go` + 删除 3 个新增文件（`internal/aiproxy/environment_test.go`、`internal/envcheck/wsl_network.go`、`internal/envcheck/wsl_network_test.go`）。未做任何 commit/push。

## 上游输入

- `agent-outputs/wsl-session-friction/evidence-report.md`（A1/B1/B2/C5 证据与裁定背景）
- `internal/platform/wsl.go` / `internal/aiproxy/environment.go` / `internal/envcheck/`（现状基线，正文内联 + 实读）
