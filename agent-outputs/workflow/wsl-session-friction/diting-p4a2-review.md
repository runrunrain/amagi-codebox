# P4-A2 终审（谛听 full）：路径值 per-key 放行同意门 — 安全面

- 基线：HEAD=e1a6b13（工作区未提交，含并行批 A3 go fmt 波及 wsl_agent_config.go 纯格式化）。
- 方法：全量静态审核（本轮工具面无 bash：git diff / go test / vitest 不可执行；测试通过结论引自实施者报告 §3，未独立复跑）。
- 审核：internal/launcher/wsl_agent_config.go(+test)、internal/platform/{wsl.go,resolver.go}(+wsl_test)、internal/envvars/service.go(+wsl_path_allow_test)、app.go 漏斗+SetEnvVarPathAllow、launch_executor.go/pi_login_launch.go WSLENV 其他写入点、frontend/{api/envvars.ts,EnvVarsPanel.vue,__tests__}、frontend/wailsjs/go/main/{App.js,App.d.ts,models.ts}。

## 安全基线核对（全部成立）

| # | 基线 | 结论 | 证据 |
|---|---|---|---|
| 1 | 默认剥离不变 | ✓ | StripWSLHostPathEnv 薄壳传 nil（wsl_agent_config.go:188-190）；既有 TestStripWSLHostPathEnv 原断言完整在位（含 P1 事故键与剥序钉定）；AllowGate 测试末段复钉 legacy 行为 |
| 2 | 保留键三重拒绝无绕过 | ✓ | ①validateEnvVars（service.go:874-877，覆盖 BatchSet/Import/SaveJSON/ReplacePortableConfig）+SetPathAllow（:234-237）②转发侧 extra/pathFlags 双处 IsWSLENVReservedKey 过滤（wsl.go:540-551）+ShouldForwardToWSL 保留键短路（:426-428）③剥离豁免面过滤（wsl_agent_config.go:206-211）。含手改 envvars.json 经 Load() 注入保留键标记的旁路：引擎两侧均再过滤，实测路径无 /p/无豁免 |
| 3 | /p 只惠及放行键 | ✓ | pathFlags 唯一来源=pathFlagKeys（即 PathAllowKeys，req.EnvPathFlagKeys 单次赋值同源交给剥离侧 app.go:2386 与转发侧 resolver.go:152）；缺席键/未标记键/保留键均无标志（wsl_test.go:614-660） |
| 4 | 最小暴露日志 | ✓ | 放行 INFO 仅键名逗串（app.go:2388）；剥离 WARN 保持；值不出现在任一日志 |

## 关注点核对

1. **豁免链一致性**：两侧恒同源（同一 slice、无中途变异）；「翻译但没放行」方向不可能（单一来源）；「剥离豁免但 /p 未加」存在一个窄口 → F-1。strip 闸门（EmbeddedLaunchTargetsWSL+DefaultWSLDistro）与 resolver WSL 分支镜像（wsl_home.go:243-265 复用同一 resolveRequestedShell），差异仅在 env 参差（nil vs resolvedEnv），危险方向（漏剥但转发）需 ambient PATH 找不到 wsl.exe 而合并 env 找得到——合并 env 含 os.Environ，不成立；该镜像自 P1 起未变。
2. **/p 语义**：`NAME/p` 条目格式与微软文档一致；值 verbatim 留 env、翻译跨界执行（双侧测试钉定）；空格路径/尾反斜杠由 WSL /p 语义覆盖。UNC 值（`\\server\...`）本就不被 isWindowsDrivePath 判为路径——剥离基线从 P1 起就不覆盖 UNC（遗留观察，非本批引入）。
3. **持久化/导入面**：omitempty 缺省 false=旧文件兼容（RoundTrip 测试钉定未标记键不落字段）；Import/BatchSet/SaveJSON 同走 validateEnvVars；Set(key,value) 不丢标记；PortableConfig 携带标记。Load() 不校验（预存设计），引擎侧兜住（见基线 2）。
4. **UI 同意面**：确认门文案覆盖「跨界可见+/mnt 翻译+确认无敏感信息」；动作可逆（关开关即撤）无需不可逆声明；Switch 绑定持久态——开启仅在 API 成功+reload 后翻转、失败 toast 不变；关闭成功后本地翻转、失败回滚；保留键只显示徽标无开关。
5. **测试断言质量**：抽核为行为断言（WSLENV 内容、剥离键序、磁盘 JSON 字段、幂等不写盘），非实现细节；TestStripThenForwardPathAllowChain 以 resolver 同调用形态钉漏斗顺序。
6. **UE5 模板无夹带**：静态清单仅 key/描述/pathLike；描述中的 `C:\Program Files\...` 为通用示例非本机路径；添加走 setEnvVar(key,'')，allowPathValue 恒缺省；前端测试钉无 value/allowPathValue 字段。

## Findings

### F-1｜Minor｜internal/platform/wsl.go:514-540（appendWSLENVForwardingWithPaths 保留段 + addName 去重）
- 触发条件：Windows 用户环境已自带 `WSLENV` 含某键裸名（无 /p，如 `WSLENV=UE_ROOT`），且该键在 envvars.json 开启 allowPathValue。
- 可失败行为：保留段先把 `UE_ROOT` 计入 seen，env 循环里 `addName(k,"/p")` 被去重吞掉——剥离侧已豁免（值跨界）但 WSLENV 条目无 /p（形态不译），正是任务书关注点 1 的「剥离豁免但 /p 未加」半开态。
- 实际影响：该显式放行键的 C:\... 值以未翻译形态进入 distro（Linux 侧非法路径，复现 P1 事故的功能症状）；非泄漏（双重用户显式同意），安全基线 1-4 均不受损。静态路径证实，未运行复现。
- 证据：addName seen 去重逻辑 + 保留段先于 env 循环；无 preserved-entry × pathFlags 交叉测试。
- 最小修复方向：保留段发现 base 命中 pathFlags 且原条目无 /p 时升级为 `base+"/p"`（或至少记 WARN）。

### F-2｜Minor｜frontend/wailsjs/go/main/App.js:773-777、App.d.ts:403（手工补齐绑定）
- 触发条件：下次 `wails generate`/`wails build` 再生前，或他人按 models.ts 类型消费 EnvVar。
- 可失败行为：违反仓库规则「Never hand-edit frontend/wailsjs — regenerate it」（报告已声明偏差并留再生注释）；models.ts EnvVar 类型无 allowPathValue，models 类型链上读不到该字段（面板用内联接口+运行时 JSON 字段，功能不受影响）。
- 实际影响：类型债务与签名漂移窗口；运行时无缺陷。
- 证据：App.js P4-A2 注释块；models.ts:2121-2123。
- 最小修复方向：下次 wails dev/build 后删除手工块并确认再生输出一致、models.ts 补齐字段。

## 遗留观察（非本批引入/非缺陷）
- Load() 不跑 validateEnvVars：手改文件可载入保留键+标记，引擎双侧过滤兜住（已核实）。
- UNC 形态 Windows 绝对路径值自 P1 起就不在剥离判据内（isWindowsDrivePath 只认盘符），本批未改变。
- pi_login planner 链（pi_login_launch.go:276+）对 PI_CODING_AGENT_DIR 无条件兑底剥离，即使被标记也是过剥方向（安全侧）；remote effect（launch_executor.go:896）用旧壳无 envvars 注入链，放行门不适用（与报告 §6 一致，已核实）。
- pty 层 LANG 追加为纯串接（service.go:1042-1055），不动既有 /p 条目。

## 盲区
- 无 bash 工具面：git diff 基线比对、go test/vet、vue-tsc、vitest 均未独立执行；「旧测试未改一字」仅静态核对（内容与 p1-report 记录的断言一致），测试通过引自实施者报告。
- WSL 真机 /p 跨界翻译行为未实测（实施者同样未做，属文档语义钉定）。

## 结论
安全基线四项全部成立；保留键防线经三条路径（含 Load() 旁路与手改文件）静态推演无绕过。两条 Minor（F-1 半开态窗口、F-2 wailsjs 手工绑定偏差）不阻断。

VERDICT: PASS_WITH_MINOR
