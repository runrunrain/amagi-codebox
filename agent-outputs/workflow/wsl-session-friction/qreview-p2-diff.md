# qreview：amagi-codebox 批次二阶段 diff（P2-T1 + P2-T2，基线 HEAD=16f707d 工作区未提交改动）

- 审查档位：quick（任务书列明 quick 覆盖充分），关注点 5 项全查。
- 方法说明：本节点工具面无 bash/git——未能执行 `git diff HEAD` 与测试复跑。diff 范围以两报告改动清单为索引、逐文件读取工作区现态核验；报告声明的测试以测试代码静态核验其断言有效性，运行结果采信报告原文（标注于盲区）。
- 上游输入：`agent-outputs/workflow/wsl-session-friction/p2t1-report.md`、`p2t2-report.md`（均已全文读取）。

## Findings

### M-1｜Major｜单向 Windows→distro 凭据覆盖会摧毁 distro 侧轮转后的新凭据
- 锚点：`internal/launcher/wsl_pi_login_sync.go:90-107`（SHA-256 比对「不一致」即无条件复制，方向恒 Windows→distro）；`pi_login_launch.go:184-186`（载荷片段 `cmp || cp` 同方向）。
- 触发条件：pi 在 WSL 会话内刷新 OAuth token 写入 distro `~/.pi/agent/auth.json`（两侧副本分叉，distro 侧为唯一有效凭据）→ 任意下一次经 CodeBox 的 WSL 登录启动（桌面闸门 Go 同步或 remote/v1 载荷片段）。
- 实际影响：distro 侧唯一有效凭据被 Windows 旧副本原子覆盖。若 provider 轮转已使旧 refresh token 失效——这正是 p2t2 报告自述的同步动机（「OAuth refresh token 轮转通常使旧 token 失效」）——新会话 401 且无法自愈（有效凭据已被删除），用户必须回 Windows 侧重登；Windows 侧 pi 此后用旧 token 刷新还可能触发 provider 的 token 复用保护（provider 相关）。对「主要经 WSL 消费订阅登录」的目标用户，A2 特性被同步机制自身周期性破坏。
- 证据：静态路径证实——比对无两侧新陈判定、无回同步、载荷与 Go 闸门同一方向；现有测试（`TestSyncWSLPiLoginAuthSeedsAndRefreshes/stale copy is replaced`）把「stale distro copy 被替换」钉为期望行为，即方向不对称是设计现状而非笔误。未真机复现（需真实 OAuth 轮转）。两报告均未讨论 distro 侧刷新分叉。
- 最小修复方向：复制前判定两侧新陈（distro 副本较新时降级 WARN 不覆盖，或先 distro→Windows 回同步再前向同步）；或由 Leader 显式裁定「Windows 侧为唯一权威、distro 侧刷新一律被覆盖」并写入 pi-login-providers.md 契约与报告风险节（当前文档只写降级语义，未写权威方向）。

### m-2｜Minor｜writeWSLAuthAtomic 的 WriteFile 失败分支不清理 tmp 残留
- 锚点：`internal/launcher/wsl_pi_login_sync.go:122-124`（无 `os.Remove(tmp)`）vs `:125-128`（Rename 失败分支有清理）。
- 触发：tmp 写入中途失败（distro 磁盘满 / 9P 断开）。
- 影响：`auth.json.amagi-tmp` 残留部分凭据字节；0600 参数经 9P 不落 POSIX 位（同文件注释自证），实际建档权限由 9P 缺省决定。下次成功同步会重写并 rename 掉（自愈），失败后放弃则长期残留；与 Rename 失败路径不对称。
- 修复：WriteFile err 分支补 `_ = os.Remove(tmp)`。

### m-3｜Minor｜chmod 失败且清理失败时，宽松权限凭据被按 DistroHasCopy 降级放行
- 锚点：`internal/launcher/wsl_pi_login_sync.go:129-131`（`os.Remove(uncAuth)` 失败被忽略）+ `:100-105`（re-stat 分类）。
- 触发：rename 已毁旧副本后 `wslChmodFn` 失败且 `os.Remove(uncAuth)` 同时失败（同一 9P 故障面的相关失败）。
- 影响：distro 留下未 chmod 的 auth.json（非 600），Stat 命中 → 闸门按 DistroHasCopy WARN 放行；击穿文件头自述契约「chmod 失败不留宽松权限落盘」。窗口窄（rename 成功后 distro 才失联），另 rename→chmod 完成之间成功路径也有一段非 600 的瞬态（设计固有）。
- 修复：区分「清理失败的新写」与「既有副本」——如 rename 前 stat 记录旧副本特征，分类据此判 NoAuth；或放行前重试 chmod 校验。

### m-4｜Minor｜mirrored 进程级缓存使新拒绝文案的「重试」指引在同进程内无效
- 锚点：`internal/platform/wsl.go:252-260`（按 home 缓存、无失效钩子）+ `internal/aiproxy/environment.go:92-94`（「执行 wsl --shutdown 重启 WSL 后重试」）。
- 触发：用户按指引改 `.wslconfig` + `wsl --shutdown` 后在未重启的 CodeBox 内重试启动。
- 影响：闸门仍用缓存值（nat/空）继续拒绝——C5「可操作指引」对唯一受众（刚被拒的用户）失效；p2t1 报告文档化了缓存取舍但未覆盖文案与缓存的矛盾。
- 修复：拒绝路径重探一次（绕过缓存），或文案补「并重启 CodeBox」。

### m-5｜Minor｜ALL_PROXY/all_proxy 不在 WSLENV 转发白名单，「代理键经 WSLENV 转发跨界」的注释与报告表述不成立
- 锚点：`internal/platform/wsl.go:378-394`（`wslENVForwardExact` 无 ALL_PROXY/all_proxy）vs `internal/aiproxy/environment.go:62-64`（注入含两者）。
- 触发：mirrored WSL 会话 + 分流启用；CLI 的非 http(s) scheme 流量或仅认 ALL_PROXY 的实现。
- 影响：distro 内只见 HTTP(S)_PROXY/NO_PROXY；https 主路径已覆盖，socks 类/ALL_PROXY-only 工具不经分流入口。白名单为既有缺口（非本 diff 引入），因本批 mirrored 放行才进入活跃行为面；environment.go 放行注释与 p2t1 报告「代理键本就经 WSLENV 白名单跨界」表述不准确。
- 修复：`wslENVForwardExact` 增补两键（或 WSL 路径不注入 ALL_PROXY 并修正注释/报告表述）。

### m-6｜Minor｜裸 `~` workDir 仍直传 ConPtyWorkDir，B4 边缘形态未闭合
- 锚点：`internal/pty/service.go:985-992`（`isLinuxStylePath("~")==false`，注释称「wsl.exe --cd 也无法消费」裸 `~`）+ `:998-1002`。
- 触发：workDir 恰为裸 `~`。桌面路径被 `resolveLaunchWorkDir` 的 Clean+Abs+Stat choke point（launch_workdir.go:96-111）先回退，实际只有 remote/plan 链路可能到达。
- 影响：ConPtyWorkDir="~" → CreateProcess 267（B4 同型失败）；而 MS 官方文档记载 `--cd ~` 恰是映射 Linux home 的合法形态（与测试注释相反——未真机复证）。非回归（改前同失败），属修复不完整边缘；`~user/` 形态为明确声明的范围外。
- 修复：`isLinuxStylePath` 把裸 `~` 归入 Linux 形态；或注释改为真实依据。

## 覆盖与正面核验（对应任务书关注点 1-5）

1. **凭据安全（除 M-1/m-2/m-3 外）**：auth.json 全链只按字节读/比/复制，错误信息与日志不含内容；源缺失 → `ErrWSLPiLoginSourceMissing` 登录失效语义不复制；读 distro 失败（权限/断开）→ DistroNoAuth fail closed；「降级用旧 token 无提示」不成立——桌面 DistroHasCopy 后载荷片段重试，再失败有终端 echo 提示；Windows→distro 读写间隙的轮转由执行边界 cmp 兜住。e2e 测试有快照恢复。
2. **重锚完整性**：`env -u` 对 distro 无该变量幂等（GNU/BusyBox 语义，静态判定）；载荷结构（snippet; env -u … 'pi' …; exec bash -li）与 buildWSLCommandLine 权威语义闭合——resolver WSL 分支确认不产 StartupCommand（resolver.go:148-154）；WSLENV 剪除含 `/flags` 形态与大小写；windowsQuoteArg 双引号转义被测试钉定（`\"$HOME…\"`）。非 WSL 路径：env 构建一律非 WSL 语义 + `spec.BootstrapMode` 唯一分叉权威，`piLoginReAnchorSpec` 现态与 R2/R3 契约一致（结构性核验；未与 HEAD 字节比对，见盲区）。
3. **mirrored 判定面**：aiproxy 双调用面（app.go:2376 桌面 buildLaunchSpec / app_ai_proxy.go:99 planner resolveLaunch）与 envcheck 三态条目同源于 `platform.WSLNetworkingMode`；fail-safe 方向一致（`""` 一律按 NAT 拒绝/提示）；解析器对 BOM/UTF-16/段外键/畸形值/重复键 first-wins 均落 `""`；service_test.go 的 USERPROFILE 钉住解耦宿主真机配置。文档（ai-proxy-routing.md:52）与前端（AIProxyView.vue:99）文案与代码语义一致。
4. **B4 形态分叉**：`conPtyWorkDirFor` 接线正确（service.go:343）；盘符/UNC/空值语义不变、非 WSL passthrough 钉定；无 ConPtyWorkDir 时 cwd 由 `wsl.exe --cd` 独自承担且 Linux 形态必伴随非空 workDir（--cd 必在），不引入新假设；真机 `~/` 冒烟采信报告。
5. **测试有效性**：总体强——同步五态断言内容与副作用（ModTime 不改写/tmp 无残留/chmod 调用记录/源缺失不触 distro/旧副本幸存）；闸门 4 场景断言同步调用次数与两条中文错误文案；载荷断言覆盖 strip/WSLENV 剪除/单引号转义；B4 形态表测 + 权威载荷断言（缺省拼装不得泄漏）。aiproxy 4 测试 + envcheck 三态/门控/接线测试断言到位。

## 盲区与验证可信度

- 无 bash/git 工具面：`git diff HEAD` 未执行——改动范围以两报告清单 + 文件现态核对；「非 WSL R2/R3 逐字节零变化」只能结构核验。两报告自述的测试运行、真机 e2e、全量归因（29 失败先在）未独立复跑，采信报告（与测试代码静态核验互证）。
- `~/` 以外 Linux 形态（`~/sub`、`/home/…`）的 `--cd` 兼容性无真机实证；完整登录载荷（snippet+env -u+pi+exec）经真实 ConPTY 的端到端执行未验证（仅字符串拼装断言 + 组件级真机）。
- remote/v1 链路 workDir 的入口校验未展开（m-6 可达性据此存疑）。
- pi 是否在会话内自动刷新 auth.json（M-1 的实际发生率前提）未取证——建议实施者以真实订阅登录验证一次轮转行为后裁定 M-1 处置。

## 结论

VERDICT: FIX（M-1 为 Major：凭据同步方向不对称可在目标使用形态下静默摧毁唯一有效凭据；m-2~m-6 为 Minor，不阻断其余验收）。
