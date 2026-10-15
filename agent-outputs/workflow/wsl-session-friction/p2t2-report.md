# P2-T2 报告：WSL 会话可用 Pi 订阅登录模型（A2 闸门解除）+ B4 启动失败修复

- 任务：P2-T2（A2 主上裁定 2026-10-16 推翻「WSL 拒绝」契约 + B4 ConPTY Linux 形态 workDir err=267 修复）
- 探查/实施基线：仓库 X:\WorkSpace\amagi-codebox @ 16f707d（master），Windows 真机 + WSL2 Ubuntu（DefaultWSLDistro 实测可用）
- 上游输入：`agent-outputs/wsl-session-friction/evidence-report.md`（A2/B4/C5 证据）、CHANGELOG 1.3.94、docs/developer/pi-login-providers.md（R2/R3）
- 验证环境说明：本机 bash 运行于 WSL，Go 用 Windows 侧 `C:\Program Files\Go\bin\go.exe`（经 WSLENV 转发 GOCACHE/USERPROFILE 等，包装脚本 `.tmp-tests/wgo.sh`）。下述所有命令均为该通道真实执行。

## 0. 结论一览

| 验收 | 产物 | 状态 |
|---|---|---|
| B4 修复 | internal/pty/service.go：`conPtyWorkDirFor` + `isLinuxStylePath`，WSL+Linux 形态 workDir 不传 ConPtyWorkDir | ✅ 单测 + 真机 e2e 全过 |
| A2 闸门解除 | pi_login_launch.go + app.go：resolvePiLoginLaunch 不再传 wsl 拒绝；两处 spec 闸门改放行 + WSL 锚定分叉 | ✅ 单测全过（并修复本机 3 个既有失败用例） |
| A2 凭据新鲜度 | internal/launcher/wsl_pi_login_sync.go：`SyncWSLPiLoginAuth`（UNC 通道，SHA-256 比对，单文件复制）+ 桌面闸门挂接 + WSL 载荷内嵌惰性同步片段（remote 链路） | ✅ 单测 + 真机 e2e 全过 |
| C5 报错中文化 | app.go `syncWSLPiLoginAuthAtLaunch`：保留错误路径两条中文报错 | ✅ 单测断言 |
| 契约文档 | docs/developer/pi-login-providers.md：R2/R3 节新增 WSL 分叉小节；启动失败边界改新契约；回归节更新 | ✅ |
| 构建与测试 | go build ./...（win+darwin 交叉）✅；go vet ✅；改动包针对性测试 ✅（详见 §5） | ✅ |

## 1. R2/R3 锚定语义分叉说明（评审重点）

旧契约：登录启动检查经 `piconfig.RequirePiLogin(provider, wsl=true)` 对 WSL 一律拒绝；app.go:3727（桌面 embedded 二道闸）与 pi_login_launch.go buildPiLoginPlan（remote/v1）同样 fail-closed。**解除后语义按「spec.BootstrapMode 是否为 BootstrapWSL」分叉，spec 是唯一权威**：

- **非 WSL（R2/R3 原语义不变）**：`PI_CODING_AGENT_DIR` 显式钉 CodeBox 进程 Home 的 canonical 目录；POSIX shell 的启动命令包 `env -u <凭据键> PI_CODING_AGENT_DIR='<canonical>'` 前缀重锚（piLoginReAnchorSpec）。
- **WSL（新，piLoginWSLReAnchorSpec）**：认证目录锚定切换为 **distro 内 `~/.pi/agent`**（pi 在会话内读自己的 HOME）：
  1. `piLoginStripAgentDirForWSL` 从 spec 环境剥离 `PI_CODING_AGENT_DIR` 及其在 WSLENV 清单中的引用（Windows 路径跨界是 1.3.59 PI_* 泄漏事故形态；planner 链路无 resolveEmbeddedLaunchSpec 的前置剥离，此处统一兜底）；
  2. 载荷以 `env -u <凭据键> -u PI_CODING_AGENT_DIR` 收尾——shell startup 之后的重导出同样被中和（R3 的 WSL 等价物）；
  3. 载荷内嵌惰性同步片段（见 §2）。
- **实现要点**：env 构建期不再按镜像判定（EmbeddedLaunchTargetsWSL）分叉——一律按非 WSL 语义构建，spec 生成后统一在 WSL 分支剥离。这消除了镜像/真实判定不一致时的语义不确定性，同时使测试断言宿主无关（本机 WSL 可用与 macOS CI 上行为一致）。
- `login.AgentDir`（Windows canonical）在 WSL 分支的角色 = 同步源定位（→ /mnt 挂载路径）与非 WSL 锚定值，**不再作为 WSL 会话的锚定值**。
- **分叉点代码注释**：pi_login_launch.go `piLoginWSLReAnchorSpec`、app.go 闸门区、piconfig.RequirePiLogin 的调用侧均已注明「A2 主上裁定 2026-10-16」。

## 2. 同步设计（凭据新鲜度）

### 双通道：Go 侧闸门同步（桌面）+ 载荷内嵌片段（remote/v1）

**为什么 remote 路径不能在 Go 侧同步**：buildPiLoginPlan 位于 BuildPlan 内，契约钉死纯读零副作用（launch_planner.go 头注「no config write, no service mutation, no process spawn」；remote_session_adapter.go:455「BuildPlan (pure read, zero side effects)」）。且 BuildPlan 被单测直接调用——若在计划期落盘，`go test .` 在有 WSL 的真机上会向真实 distro 写入真实 auth.json（测试污染）。故 remote 链路的同步放进 bash 载荷（计划里只是字符串，执行边界才生效）。

**Go 侧同步函数** `launcher.SyncWSLPiLoginAuth(distro) (outcome, error)`（internal/launcher/wsl_pi_login_sync.go，新文件）：
- 落位选型说明：任务给了 wslsetup 或 launcher 二选一。选 launcher——与 WriteWSLPiAgentConfig 同族（resolveWSLAgentTarget/UNC/chmod 接缝现成、跨平台构建可测），wslsetup 是 Windows-only 构建面且其 wslExec 通道受 CreateProcess ~32K 限制；UNC 直写与「任务书 wslExec/UNC 通道」中 UNC 一致。
- 流程：读 Windows 侧 `piconfig.StandardAgentDir()/auth.json`（缺失 → `ErrWSLPiLoginSourceMissing`，登录失效语义）→ resolveWSLAgentTarget(".pi/agent") → UNC 读 distro 副本 → SHA-256 内容比对（比 mtime 稳：9P 时间戳语义不可靠）→ 不一致/缺失则 MkdirAll + tmp+Rename 原子写 + WSLChmod 600 补偿（对齐 WriteWSLPiAgentConfig 的 P1-7/diting m2 契约：chmod 失败清理落盘内容 fail closed）。
- 失败分级（outcome）：`Copied`/`Fresh`（err=nil）；失败后按落盘现状 re-stat 分类——`DistroHasCopy`（旧副本仍在，tmp/rename 阶段失败）或 `DistroNoAuth`（chmod 失败已清理 + rename 已毁旧副本，或 distro 不可解析/不可读——fail closed）。
- 保密：auth.json 只按字节读/比/复制，不解码、不落日志。

**桌面闸门**（app.go `syncWSLPiLoginAuthAtLaunch`，挂在原 3727 闸门位置）：Copied→INFO 放行；Fresh→放行；DistroHasCopy→WARN「WSL Pi 登录凭据同步失败，降级使用 WSL 内既有副本」+放行；DistroNoAuth→中文报错拒绝（ErrWSLPiLoginSourceMissing 时报「Pi 登录已失效…」，其余报「WSL 内没有可用的 Pi 登录凭据，且同步失败：%v…」）。distro 为空时重探 DefaultWSLDistro（B6 类镜像/spec 分叉兜底）。同步经 `syncWSLPiLoginAuthFn` 接缝注入（单测可替换，避免测试污染真实 distro）。

**WSL 载荷**（piLoginWSLReAnchorSpec 组装，pty 层 buildWSLCommandLine 对非空 StartupCommand 的 WSL spec 以其为权威 bash payload——新增泛化语义，resolver 不产 StartupCommand，当前唯一生产写入方即登录重锚）：
```
mkdir -p -- "$HOME/.pi/agent"; cmp -s -- '<mnt-src>' "$HOME/.pi/agent/auth.json" 2>/dev/null \
  || { cp -- '<mnt-src>' "$HOME/.pi/agent/auth.json" && chmod 600 -- "$HOME/.pi/agent/auth.json"; } \
  || echo 'amagi: Pi 登录凭据同步到 WSL 失败，继续使用 WSL 内既有副本'; \
env -u <凭据键…> -u PI_CODING_AGENT_DIR 'pi' '--provider' … ; exec bash -li
```
片段在 distro 内经 DrvFS 视图读 Windows 侧 auth.json（与 wslsetup ensurePiConfig 播种同通道形态）；失败不阻断 CLI（echo 提示后用既有副本）。桌面路径先经 Go 侧同步，片段通常 cmp 即中零拷贝；remote/restart 只有片段，也保证执行边界新鲜。

## 3. B4 修复

internal/pty/service.go：`StartResolvedWithRunEvidence` 的 opts 组装改经 `conPtyWorkDirFor(spec, workDir)`——`spec.BootstrapMode == BootstrapWSL && isLinuxStylePath(workDir)`（`/` 或 `~/` 前缀）时返回空（不传 ConPtyWorkDir），工作目录由 wsl.exe `--cd` 独自承担（命令拼装本就把 workDir 交给 --cd，wsl.exe 兼容两形态）；盘符/UNC/空值语义不变。注释记录 B4 实证（err=267）。

## 4. 改动清单（含写面声明）

| 文件 | 改动 |
|---|---|
| internal/pty/service.go | B4：conPtyWorkDirFor/isLinuxStylePath + opts 接线；A2：WSL spec 非空 StartupCommand 作为权威 payload（buildWSLCommandLine） |
| internal/pty/wsl_workdir_test.go（新） | isLinuxStylePath/conPtyWorkDirFor 形态分叉表测；payload 权威性断言；AMAGI_WSL_E2E 真机 `~/` 启动冒烟 |
| internal/launcher/wsl_pi_login_sync.go（新） | SyncWSLPiLoginAuth + writeWSLAuthAtomic + WSLDrvFSMountPath |
| internal/launcher/wsl_pi_login_sync_test.go（新） | 同步差异检测/播种/幂等/失败分级/路径映射，跨平台（接缝注入） |
| internal/launcher/wsl_pi_login_sync_e2e_windows_test.go（新） | AMAGI_WSL_E2E 真机同步+幂等实证（快照恢复） |
| pi_login_launch.go | resolvePiLoginLaunch 去 wsl 参数（不再拒绝）；piLoginWSLReAnchorSpec/piLoginWSLAuthSyncSnippet/piLoginStripAgentDirForWSL/stripWSLENVToken/syncWSLPiLoginAuthFn；buildPiLoginPlan 闸门解除 + spec 权威分叉 |
| pi_login_launch_test.go | 适配签名；「WSL 拒绝」断言改为「WSL 放行 + AgentDir=canonical」 |
| pi_login_wsl_test.go（新） | planner WSL 放行/载荷/环境剥离；桌面闸门同步调用与降级（4 场景）；strip/WSLENV/引号转义表测 |
| app.go | LaunchPiSession：闸门区改同步+WSL 重锚（原 3727 拒绝移除）；新增 syncWSLPiLoginAuthAtLaunch/errStringForLog；envOverrides 注释更新 |
| docs/developer/pi-login-providers.md | R2/R3 节 WSL 分叉小节；启动失败边界新契约（裁定日期+背景）；回归节更新 |
| internal/wslsetup/service_windows.go | **未改**（同步函数落位选了 internal/launcher，理由见 §2） |

写面外零改动（已核对 git status：其余改动均为并行任务 P2-T1 的写面文件）。git 未 commit/push；CHANGELOG 未写（Leader 统一）。

## 5. 验证证据（真实命令 + 结果）

环境：Windows go.exe 1.25.0（vendor 模式），cwd=仓库根。逐条：

1. `go build ./...` → exit 0（含并行任务 P2-T1 中间态共存构建）。`GOOS=darwin CGO_ENABLED=0 go build ./...` → exit 0；`GOOS=darwin go vet . ./internal/launcher/` → exit 0（darwin-only 测试文件对新签名编译通过）。
2. `go vet ./internal/... .` → exit 0。
3. `go test . -run 'TestPiLogin' -count=1` → **ok**（含新增 5 组用例；其中 TestPiLoginBuildPlanAndStableRestart、TestPiLoginPlannerReAnchorsPosixShellCommand、TestPiLoginPlannerPinsCanonicalRootOverCustomHome、TestPiLoginDesktopNativeArgsAndFailureNoMutation 在本机（WSL 可用）原本因 WSL 镜像判定+旧拒绝链**必失败**，闸门解除后随本任务一并转绿——这是解除前先跑的基线输出确认的）。
4. `go test ./internal/launcher/ -run 'TestSyncWSLPiLoginAuth|TestWSLDrvFSMountPath'` → **ok**（差异检测 hash 比对/播种/幂等/失败分级）。
5. `go test ./internal/pty/ -run 'TestIsLinuxStylePath|TestConPtyWorkDirFor|TestBuildWSLCommandLinePrefersStartupCommand|TestWorkDirMapsToDrvFS|TestWSLRealMachineSmoke'` → **ok**。
6. 真机 e2e（AMAGI_WSL_E2E=1，distro=Ubuntu）：
   - `go test ./internal/pty/ -run TestWSLRealMachineLinuxWorkDirStarts -v` → **PASS 0.29s**（`~/` workDir 真实 ConPTY 启动成功，echo 标记回读，修复前该形态 err=267 必失败）。
   - `go test ./internal/launcher/ -run TestManualRealWSLPiLoginSync -v` → **PASS**（首跑 outcome=Copied——本机 distro 内 auth.json 确实陈旧，被刷新；二跑 Fresh 幂等；t.Cleanup 快照恢复 distro 原状）。
7. **全量回归定谳（共享树+并行任务中间态下的归因）**：用 `git worktree add` 建 HEAD 基线快照，两侧各跑根包全量 `go test . -count=1`，失败集合归一化比对 → **29 个失败完全一致，0 新增**（全部为 Windows 本机既有：TestRC1_0_GapA… 以 nil log panic、TestSetRemoteEndpoint_* 系列、perm 断言类等；CI 仅在 macOS 跑全量故长期不可见）。internal/launcher 的 4 个 perm 失败（0700/0600 断言，NTFS 报 777/666）与 internal/pty 的 TestExactShellAttachReadyThenBootstrapExecutesTargetCLI 超时均经基线快照复跑确认与 HEAD 逐一相同。
8. gofmt：本机 `gofmt.exe -l` 对本任务全部新增/修改文件仅报 internal/pty/service.go 一处——经 od -c 取原始字节实证为**宿主 gofmt.exe 自身的异常改写**（把注释里的 `'\''` 序列改写为 `'\”` 智能引号；已提交的 pi_login_launch.go 历史注释正是这样被污染的）。已将该行按 HEAD 原字节恢复，未再跑 `gofmt -w`（会重新污染）；其余文件全部格式干净。

## 6. 未覆盖项 / 已知边界 / 风险

1. **登录预设 contextWindow 覆写不达 WSL 侧**：UpsertPiLoginModelOverride 写 Windows 侧 models.json；WSL 会话内 pi 读 distro 侧 models.json（播种快照），覆写不生效（回退 Pi 内置窗口，不阻断）。任务书钉定「仅 auth.json 单文件」，未扩面；如需补齐可在后续任务把覆写同步进 distro models.json。
2. **桌面真实 WSL 登录启动会有一条 WARN**：「已剥离携带 Windows 路径值的跨界环境变量: PI_CODING_AGENT_DIR」（resolveEmbeddedLaunchSpec 的 StripWSLHostPathEnv 剥掉我们自己的钉定值）——语义正确且可观测，噪音可接受；载荷 env -u 已兜底。
3. `piconfig.RequirePiLogin` 的 wsl 形参分支成为无生产调用方的保守死代码（piconfig 不在写面，未动；其单测仍按旧行为通过）。若后续清理可删形参。
4. 前端无 piLogin×WSL 组合预检（证据报告疑点 4）：本任务未加启动前 UI 提示（闸门已解除，冲突不复存在；同步失败场景有日志+载荷内 echo 提示）。
5. 并行任务 P2-T1 同树改动 aiproxy/platform/envcheck——本任务与其无共享文件；共存构建+全量测试归因已验证零新增失败。结算基线若以隔离快照复跑，预期同样全绿（本任务改动包内测试均过）。
6. 真机 e2e 的 distro 侧 auth.json 已恢复原状（快照恢复）；Windows 侧 auth.json 只读未动。

## 7. 回滚方式

单任务改动均为未提交工作区增量：`git checkout` 级回滚对象 = app.go、internal/pty/service.go、pi_login_launch.go、pi_login_launch_test.go、docs/developer/pi-login-providers.md（删除新增文件 internal/launcher/wsl_pi_login_sync*.go×3、internal/pty/wsl_workdir_test.go、pi_login_wsl_test.go 即可整体还原；注意其余 M 文件属并行任务，勿连带）。

---

# 修复轮（diting 快审 FIX，2026-10-16）：1 Major + 5 Minor

输入：`agent-outputs/workflow/wsl-session-friction/qreview-p2-diff.md`（已全文读取）。全部六条已修，逐条如下。

## M-1（Major）双向新陈判定，新者为准 — 已修

- **Go 侧**（internal/launcher/wsl_pi_login_sync.go 重写核心）：`SyncWSLPiLoginAuth` 现为双向——内容 SHA-256 一致 → `Fresh`；Windows 新（mtime 差 > 2s 容差）或 distro 缺失（首次播种）→ 前向复制（`Copied`）；distro 新（如 WSL 会话内 pi 轮转 token）→ **反向同步回 Windows 侧**（`DistroNewer`，`writeWindowsAuthAtomic`：MkdirAll+0700 → tmp 0600 → Chmod tmp → Rename，与 WritePiAgentConfig 同构，日志「已从 WSL 回同步登录凭据」）；两侧不同且 mtime 并列/容差内（`authMTimeTolerance=2s`，FAT 级精度）→ 保守取 distro 回同步（`DistroTieKept`，WARN「两侧 Pi 登录凭据时间戳不可判，保守按 WSL 侧同步」）；mtime 探测失败 → 不动任何一侧，`DistroHasCopy` 降级。
- distro 侧 mtime 按裁定走 wslExec 通道：新增 `platform.WSLFileMTime(distro, linuxPath)`（internal/platform/wsl.go，wsl.exe 内 `stat -c %Y` 取 ext4 权威值；路径缺失/非 Windows/输出不可解析 → ok=false，调用方按「不可判」保守处理，绝不当作 0）。launcher 侧 `wslStatMTimeFn` 接缝注入。
- **载荷片段**（pi_login_launch.go `piLoginWSLAuthSyncSnippet`）同步改双向：`[ src -nt dst ]` 为真（Windows 新/并列时 -nt 为假→取 distro，与 Go 侧并列语义同向）→ 前向 cp+chmod 600；否则反向 `cp dst src`（DrvFS 写覆盖已有文件保留 ACL）；src 不存在（退出登录时序）→ 不动任何一侧（不复活已退出凭据）。Go 闸门先跑（桌面路径），片段通常 cmp 即中零拷贝；remote 链路片段独立成立，BuildPlan 仍纯读。
- 契约文档 §R2/R3 WSL 分叉同步更新为双向语义。
- **测试**：`TestSyncWSLPiLoginAuthBidirectional`（distro 新→回同步且 distro 字节保全/并列→收敛到 distro/探测失败→两侧不动降级）+ `windows newer replaces stale distro copy`（原「stale replaced」用例改为显式 Windows 更新）+ 闸门两个新 outcome 放行子测 + 载荷 `-nt`/反向 cp 断言。真机 e2e 重写为双向收敛契约（两侧快照恢复 + 断言两侧内容收敛一致 + 二次幂等 Fresh），实测 PASS。
- **事故披露（诚实记录）**：修复轮首跑旧版 e2e 时，本机 distro 副本 mtime 因上一轮 e2e 的恢复写而偏新（内容陈旧、时间戳新）→ 双向代码正确判定 DistroNewer 并把 distro 字节回写 Windows 侧，旧 e2e（无 Windows 侧快照恢复、断言仍单向）失败且未恢复 Windows 侧。两侧内容实测仅差 1 字节（2741 vs 2742，疑似行尾差异，非 token 本体差异），且 pi 的 refresh/重登流程自愈；新 e2e 已双侧快照恢复杜绝复发。此事故本身就是 M-1 残余风险（mtime 说谎）的真实样本——按裁定语义（新者为准/并列取 distro）执行，已记入风险。

## m-2 WriteFile 失败清理 tmp 残留 — 已修

writeWSLAuthAtomic 的 WriteFile 错误分支补 `_ = os.Remove(tmp)`（与 Rename 分支对称）。测试：`write failure with old copy intact degrades` 追加断言「WriteFile 失败后 tmp 路径不存在」（修复前空目录残留会使断言失败）。

## m-3 chmod+清理双失败 fail closed — 已修

writeWSLAuthAtomic 在 chmod 失败且 `wslAuthRemoveFn(uncAuth)` 清理也失败时，返回包裹哨兵 `errWSLAuthLooseLeftover` 的错误；`classifyWriteFailure` 对该哨兵直接判 `DistroNoAuth`（绝不按 DistroHasCopy 放行宽松权限凭据）。测试：`chmod plus cleanup double failure fails closed even with file present`——注入 chmod 与 Remove 双失败，断言 outcome=DistroNoAuth 且宽松文件确在盘上（证明分类不依赖 stat）。

## m-4 WSLNetworkingMode 去进程缓存 — 已修

internal/platform/wsl.go：删除 `wslNetworkingModeMu/wslNetworkingModeCache`，`WSLNetworkingMode` 每次直读 .wslconfig（注释记录 m-4 理由：缓存使「wsl --shutdown 后重试」指引同进程失效；文件小、调用频率低）。测试：`TestWSLNetworkingModeCachePerHome` → `TestWSLNetworkingModeFreshRead`（改文件后同进程下一次调用必须读到新值）。aiproxy/envcheck 既有测试（独立临时 HOME 隔离）全数通过，无需重置钩子。**注**：internal/aiproxy/environment_test.go:16、service_test.go:264 与 internal/envcheck/wsl_network.go:12 的注释仍述「按 home 缓存」——属 P2-T1 写面，本轮未动，建议 Leader 在 P2-T1 收尾时顺手更正（行为已不缓存）。

## m-5 ALL_PROXY/all_proxy 入 WSLENV 白名单 — 已修

`wslENVForwardExact` 增补 `ALL_PROXY`/`all_proxy`（注释记录 m-5 依据：socks 类 scheme 与仅认 ALL_PROXY 的实现；aiproxy 注入携带两键；值为 URL，ShouldForwardToWSL 命中后路径值剥离判据自动生效）。测试：`TestShouldForwardToWSL` 的 forward 列表增补两键。

## m-6 裸 ~ 归入 Linux 形态 — 已修

internal/pty/service.go `isLinuxStylePath`：`w == "~"` 亦返回 true（注释更正为真实依据：`--cd ~` 是映射 Linux home 的合法形态——本轮已真机复证 `wsl.exe -d Ubuntu --cd ~ -- pwd` → `/home/maorun`，rc=0；CreateProcess 侧则同样 err=267）。测试：表测 `"~": true` + `conPtyWorkDirFor` 增 `wsl bare tilde` 用例。

## 修复轮验证汇总（真实命令，Windows go.exe 通道）

1. `go build ./...` exit 0；`GOOS=darwin CGO_ENABLED=0` build+vet exit 0；`go vet ./internal/launcher/ ./internal/pty/ ./internal/platform/ ./internal/aiproxy/ ./internal/envcheck/ .` exit 0。
2. `go test ./internal/launcher/ -run 'TestSyncWSLPiLoginAuth|TestWSLDrvFSMountPath'` → **ok**（双向三态/m-2/m-3/幂等/播种全过）。
3. `go test ./internal/platform/ -run 'TestWSLNetworkingMode|TestWSLFileMTime|TestShouldForwardToWSL'` → **ok**；platform 包全量仅 `TestDarwinFNMDefaultAliasDeduplicated...` 失败（Windows 建符号链接需特权，fnm 域与本改动无关的既有本机失败）。
4. `go test ./internal/pty/ -run 'TestIsLinuxStylePath|TestConPtyWorkDirFor|TestBuildWSLCommandLine|TestWorkDir'` → **ok**；pty 包全量仅 `TestExactShellAttachReadyThenBootstrapExecutesTargetCLI` 超时（首轮已以 HEAD 基线快照证为既有）。
5. `go test . -run 'TestPiLogin'` → **ok**（含新 outcome 子测与载荷双向断言）。
6. 真机 e2e（AMAGI_WSL_E2E=1）：`TestManualRealWSLPiLoginSync` → **PASS**（首跑 Fresh 收敛断言 + 二次幂等 + 双侧快照恢复）；`TestWSLRealMachineLinuxWorkDirStarts` → **PASS**（B4 回归仍在）。
7. launcher/pty 包全量失败集 = 首轮已证既有的 5 个（4 个 NTFS 权限位断言 + 1 个 shell-attach 超时），修复轮 0 新增；aiproxy/envcheck 失败（TestPrivacyPersistence… 0600 断言、npm root/claude self-heal）同为 Windows 本机环境类，与本轮 platform 改动无路径交集。
8. gofmt：宿主 gofmt.exe 的 `'\''`→`'\”` 智能引号改写异常仍在（service.go 既有注释行保持 HEAD 字节，git diff 证零漂移）；其余改动文件经 gofmt -l 全净，两处测试文件以 -w 格式化后经字节复核零污染；doc 注释的 `//（`→`// （` 规范化已逐处手改（不跑整文件 -w 以避污染 service.go）。

## 修复轮改动清单

| 文件 | 改动 |
|---|---|
| internal/launcher/wsl_pi_login_sync.go | M-1 双向判定 + DistroNewer/DistroTieKept outcome + wslStatMTimeFn/wslAuthRemoveFn 接缝 + writeWindowsAuthAtomic + m-2 tmp 清理 + m-3 哨兵 fail-closed |
| internal/launcher/wsl_pi_login_sync_test.go | 双向三态/m-2/m-3/探测失败用例重写与增补 |
| internal/launcher/wsl_pi_login_sync_e2e_windows_test.go | 双向收敛契约 + 双侧快照恢复 |
| internal/platform/wsl.go | m-4 去缓存；m-5 白名单增补；WSLFileMTime（M-1 判据） |
| internal/platform/wsl_test.go | m-4 FreshRead 替换缓存测试；m-5 白名单断言；WSLFileMTime 用例 |
| internal/pty/service.go | m-6 裸 ~ 归 Linux 形态（isLinuxStylePath + conPtyWorkDirFor 语义） |
| internal/pty/wsl_workdir_test.go | m-6 表测两用例 |
| pi_login_launch.go | M-1 载荷片段双向（-nt 判定 + 反向 cp + src 存在守卫） |
| pi_login_wsl_test.go | 载荷双向断言 + 闸门 DistroNewer/DistroTieKept 子测 |
| app.go | 闸门新增 DistroNewer（INFO 回同步）/DistroTieKept（WARN）映射 |
| docs/developer/pi-login-providers.md | WSL 分叉节改为双向新者为准语义 |

未 commit/push；未写 CHANGELOG；P2-T1 写面文件零触碰（含其缓存注释——见 m-4 注）。
