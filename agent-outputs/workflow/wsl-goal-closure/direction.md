# WSL × Windows 工程：目标对齐与本轮收敛决策

## 结论

主上的目标是：模型使用 WSL/Linux 的搜索、脚本与 CLI，同时可靠操作留在 Windows 的工程和 Windows 工具链。推荐保留现有按目标供给配置的方向，停止扩张覆盖面，把本轮投入集中到启动目标一致、错误不换工程或模型、凭据安全、Windows interop 实际可用这几项。

现有门禁需要按对象区分：凭据、目标身份和用户文件归属必须严格；自动移植无法确认的命令应拒绝该导入单元；普通可选配置失败不应连带阻止模型工作。“Windows 形态不能自动移植”不能扩大解释为“WSL 不允许调用 Windows 工具”。目前代码既有过宽的形态拒绝，也有过宽的失败放行，不能用统一“放宽门禁”或“全面 fail-closed”解决。

约 3.4 万行历史增量说明维护面已经很大，不证明整套机制应删除，也不证明用户目标已达成。本轮不新建同步框架、跨平台执行平台、UE5 专用管理器或多 distro 产品面。优先修既有入口和提交原语，再用真实工程验证收益。

## 输入与证据口径

完整读取的上游：

- `agent-outputs/workflow/wsl-goal-closure/workflow.md`：当前 V1–V5、无提交推送授权、macOS 无已知 Windows 测试入口。
- `agent-outputs/workflow/wsl-session-friction/workflow.md`：历史裁决、P1–P4 范围；其中历史 commit/push 授权不适用于本轮。
- `agent-outputs/workflow/wsl-session-friction/p3-config-supply-design.md`：归属、A2 凭据例外、可选配置失败、最小供给职责。
- `agent-outputs/wsl-session-friction/p4-evidence.md`：历史代码观察、后续纠正及 §8 历史真机数据。

本报告为设计态目标决策，不替代 baize/diting 的独立缺陷审计。下列现状来自本轮实际源码读取；风险触发条件不等于完成了运行复现。未读取尚在并行产出的审计结论，不预判它们 PASS。

### 当前实现与历史材料的差异

| 本轮源码事实 | 锚点 | 对方向的影响 |
|---|---|---|
| 路径变量已经有显式放行：EnvVar.AllowPathValue → PathAllowKeys → 剥离豁免 → WSLENV `/p`；前端有开关 | `internal/envvars/service.go:16–26,209–255`；`app.go:2350–2400`；`internal/platform/wsl.go:481–587`；`frontend/src/components/extensions/EnvVarsPanel.vue:108–123` | 历史 E6“所有盘符值都被剥离、没有 /p”已不代表现状，不能再立一个同样的放行功能 |
| WSL resolver 先于 Windows CLI 查找，返回 Linux 裸命令；PTY 使用 `wsl.exe -d … --cd … -- bash -lic`，Linux 形态不传 ConPtyWorkDir | `internal/platform/resolver.go:109–176`；`internal/pty/service.go:918–1013` | Linux CLI 独立安装和 WSL shell 是正确基础，应保留 |
| App 工作目录入口仍统一执行宿主 `filepath.Abs/Clean + os.Stat`，失败回退 default/home | `launch_workdir.go` 全文；`app.go:5404–5410`，文件注释列出五入口 | PTY 支持 Linux cwd 不等于真实 App 入口支持；先消除更上游改错目录的风险 |
| GitAssist/PathPicker 已增加 Linux 形态显式拒绝；DrvFS advisory 已跨入口记录 | `internal/gitassist/service.go:108–140`；`internal/paths/list_directories.go`；`internal/platform/drvfs_advisory.go` | 历史“无感知、静默回 home”不能照抄；当前能力仍是提示边界，尚非 Linux 路径桥接 |
| wslsupply 已注册托管 provider、共享文件、Pi/Claude/Codex 投影、skills、插件来源；A2 仍委托既有同步器 | `app_wsl_supply.go:128–153`；`internal/wslsupply/coordinator.go:333–428`；`internal/wslsupply/a2.go` | 保留归属与适配器，修实现偏差；不再为同一职责建协调器 |
| 桌面统一 env 接线与 remote planner 接线不同：planner env port 只有 MergeWithSystem，resolveLaunch 仅加代理→resolver→PinProxyEnvironment | `launch_planner.go:61–64,367–373,734–744`；`app_ai_proxy.go:87–100`；对照 `app.go:2373–2399` | 同一 env 配置不应因桌面/远程/restart 而改变转发和防护；这是本轮优先核对链 |
| Pi 供给 verdict 仅对 managed-models target-corrupt 阻断；桌面 WSL writer 失败仍可回退内置 provider，而该映射会丢自定义 baseURL | `app_wsl_supply.go:1092–1120`；`app.go:5009–5109` | 当前并非处处严格；选中 endpoint/身份不能被“可选配置告警”规则吞掉 |
| OpenCode 桌面附加层准备失败已阻断；remote effect 复用准备函数并晚绑定 OPENCODE_CONFIG | `app.go:5433–5480`；`launch_executor.go:794–898` | 保留真实消费者附加层；writer 文件中的“失败仍无 provider 启动”旧注释不能当现状 |
| MCP 转换按后缀/基名拒绝 exe、PowerShell、pwsh；skills 扫描把 ps1/cmd/bat、exe 等列为不兼容 | `internal/wslsupply/transform.go:257–397,450–458`；`internal/launcher/wsl_opencode_mcp.go:231–289`；`internal/wslsupply/skills_scan.go:296–315` | 这些是自动供给策略，不能证明 interop 不可用；Linux 原生 pwsh 也不能仅凭名字判成 Windows 程序 |
| 网络门基于 `.wslconfig` 声明；不是活动网络连通测试 | `internal/platform/wsl.go:212–250`；`internal/aiproxy/environment.go:79–105` | mirrored 声明只能作为现有准入条件，不能成为“代理已通”的验收证据 |

## 必须保持的不变量

1. 一次启动的 distro、默认用户/Home、cwd、provider/model/endpoint、认证来源贯穿 App/planner、供给、PTY 与 CLI。失败不偷偷换宿主、工程或模型。
2. Windows 工程允许继续位于 Windows 卷；WSL 运行环境与工程存储位置分别选择。CodeBox 不自动迁移工程，不全局改 Git、PATH、WSL 网络或安全设置。
3. 用户自有配置、未知字段、本地修改和删除不被自动覆盖。Windows 保存成功、WSL 已供给、CLI 实际消费成功分别记账。
4. 凭据范围不扩大；不复制整棵 agent 根、历史、信任状态或 Windows 插件实体。Pi A2 是已批准的认证例外，不推广到其它 CLI。
5. 可选 MCP/skill/偏好没有供给成功时，清楚报告该能力不可用；仅当它确为当前启动必要条件才阻断。
6. omp 已有 WSL 启动行为纳入回归；不新增 omp 安装面、MCP 供给或其它历史未立项能力。手装 omp 的既有启动测试不能写成新增支持完成。

## 重大取舍

### 保留目标侧配置，停止追求两侧全部相同

| 候选 | 结果与代价 | 决定 |
|---|---|---|
| 撤掉供给，只让 Linux CLI 读 Windows 配置树 | 表面代码少；Linux 路径、原生依赖、凭据权限、并发写与用户归属仍冲突，还把运行配置绑到跨界 I/O | 不采用 |
| 两侧完整镜像或普通配置双向“新者赢” | 扩大密钥、信任、历史复制；Linux 手改被覆盖，失败恢复更难 | 不采用 |
| 保留业务源生成托管配置、用户配置显式选择、WSL 手改冻结 | 需要维护各 CLI 适配器和状态；能保护用户内容且贴合真实消费位置 | 采用，冻结新增覆盖面 |

双侧供给的合理部分是“同一业务选择在实际运行端可消费”。provider 可以从 CodeBox 源生成到两端；用户配置只在有归属授权时投影；插件继续目标侧原生重放。它不要求两份文件字节一致。已有 skills/插件面不撤除，也不把重放、全树扫描或联网安装塞进会话启动。

A2 保持已裁决算法和调用边界，修安全缺口而不再设计凭据同步协议。hash+mtime 只能选择副本，不能证明 token 有效，也不能解决 Windows/WSL 多进程同时刷新 OAuth 的竞争。回滚普通配置时不得回滚新 token。

### 严格门禁按失败对象生效

| 对象/失败 | 推荐语义 | 禁止的折中 |
|---|---|---|
| 目标未知或已变；明确 cwd 不可用；选定模型配置失败且无等价传输 | 阻止当前启动，保留原输入，说明目标和修复动作 | 回退 home/裸 wsl/default provider 后记成功 |
| 密钥权限不安全、清理结果不明、撤回未确认 | 阻止消费受影响的秘密；逐项报告残留与恢复状态 | 仅以“文件存在”或“字节相同”认定安全 |
| 普通偏好冻结、可选导入未授权/转换失败 | 保留 WSL 本地内容，启动继续，显示未生效项 | 为同步整齐而覆盖本地；把所有单元失败升为全局禁用 |
| 自动迁移中的未知命令/参数形态 | 拒绝该导入单元，说明“未验证自动迁移” | 声称 Windows 工具本身无法在 WSL 使用 |
| 已明确选择的必需 MCP 不可用 | 失败可见；有现成 required 语义时阻断 | 静默删除该 server，再把会话标成全能力可用 |

不为这张表新造复杂策略引擎。先在现有 verdict/UnitResult/启动错误处纠正分类，并让直接入口与 remote effect 使用同一窄判定。必需标记若尚无真实调用入口，本轮不扩建必需依赖管理系统，但也不得声称已保证这一语义。

安全实现不能用注释替代。`coordinator.go::submitFile` 当前是 UNC 建临时文件、写入、宿主 os.Chmod、rename 后才执行 distro chmod；`WriteWSLPiAgentConfig/WriteWSLOpenCodeAgentConfig` 的字节相同分支会直接返回；A2 Fresh 也直接返回。结合仓库自身“9P 不落实 POSIX chmod”的说明，需要审计和真机确认临时阶段、失败清理、无变化路径的权限。若确认不满足安全要求，修这些既有写入原语和错误传播，不能再增加一层名义上的安全适配器。

### Windows interop 是目标内能力，自动移植保持保守

需要区分以下消费方，路径相同字符串不能通用：

| 场景 | 正确语义 | 本轮最小处理 |
|---|---|---|
| Linux 工具读取 Windows 文件，如 rg 读 UE 工程 | 接受目标内可达的 Linux 路径；现有单路径 env `/p` 符合用途 | 复用并验证 AllowPathValue；补齐所有启动入口，不再新增同类开关 |
| WSL 启动 Windows exe | exe 由 interop 运行，工具参数仍按 Windows 程序自己的语义；工作目录、引用和 stdout 编码单独验证 | 优先验证既有终端真实命令；需要时在单次调用边界用目标内 wslpath 取得 Windows 参数，避免拼 `/mnt` 猜映射 |
| cmd/bat/PowerShell 脚本 | 指定对应解释器；不能只删后缀或改 bash；Windows PowerShell 与 Linux pwsh 不等价 | 输出具体调用条件，不自动启用 interop、不加载未知 rc、不自动安装 |
| Windows 工具需要 UE_ROOT/VSINSTALLDIR 的 Windows 形态 | Linux 消费用的 `/p` 不等于 Windows 子进程最终需要的值；跨界环境方向需实测 | 在该工具调用的局部环境/参数中处理；不把 AllowPathValue 改成“任意值原样放行”，不急于新增全局多态 env schema |
| MCP/skill 自动供给含 Windows 资产 | 代码复制成功不代表消费者、解释器、依赖、协议都正确 | 默认不自动转换未知命令；可保留用户明确维护的 WSL 本地配置/工具脚本，不把其纳入宿主覆盖 |

`PATH/PATHEXT` 不由 CodeBox 整体转发是合理产品防护，不是 WSLENV 不能表达路径的机制限制；现有 `/p` 已证明有路径转换能力。也不应把 `appendWindowsPath=false` 当故障强制改回：全路径启动可与 Linux PATH 隔离共存。`command -v` 未命中只说明当前 PATH 不可达，不证明工具未安装。

当前自动 MCP 分类的 `pwsh`/`powershell` 名称判断过粗。推荐在已有目标命令解析接缝内区分目标原生命令与未验证 Windows 形态；只为真实反例加测试，不全面去掉 `.exe` 防护。对于显式 interop MCP：先由一项真实需要的 server 证明命令解析、Windows 参数、stdio 握手、退出与取消行为，再决定是否复用现有逐项预览增加有限支持。没有消费者证据时保持手动配置，不立通用 interop MCP 平台。

skills 中存在 `.ps1` 也不等于整个 skill 永远不可用，但允许它进入自动供给意味着执行依赖和用户授权需要说明。本轮先纠正文案“自动供给未支持”，验证目标本地的真实 Windows 工具调用；不放开所有二进制/脚本复制、不自动执行验证脚本。

### 路径修复限定在既有入口

主路径推荐继续让 Windows UI/GitAssist 使用 Windows 工程路径，WSL 内的 Linux 工具使用其目标侧路径，Windows 工具参数使用 Windows 路径。这是操作约定，不是要求用户迁移或重建仓库。

- 优先修 `resolveLaunchWorkDir` 对 WSL 目标的宿主校验问题：明确给出的 WSL cwd 不能被宿主 Abs/Stat 淘汰后换目录。先确定运行目标，在执行阶段按目标验证目录；为空时才采用默认值。remote 纯计划不加入新的 WSL 探测副作用。
- 保留 PTY 的 ConPtyWorkDir 隔离。Windows CreateProcess 的 cwd 与 WSL `--cd` 分工不同，不能合并成一个宿主路径校验。
- GitAssist/PathPicker 当前显式报错比静默错位安全。暂不增加 WSL GitAssist 执行后端，也不把 Linux git 自动替换为 git.exe；二者配置、hooks、凭据和行尾语义不同。
- 若审计证明 Windows 工程仅因 `/mnt/...` 表达而在现有面板受阻，可在已明确 distro 的入口做有验证的目标路径转换；映射不明就保留错误。不为这一点新建任意文件系统浏览器；无可靠目标信息的入口不猜盘符。
- 原生 Windows/macOS 路径语义不随 WSL 修复悄改。需要改变“非空无效目录回退”的一般产品行为时，单列行为变化；本轮至少保证 WSL 明确工程不被换走。

## 本轮可执行优先级

以下是沿现有实现的修复顺序，不是新建体系的 Phase 蓝图。Leader 将审计发现归入这些切片后再确定精确 diff。

| 优先级 | 可执行切片与责任边界 | 完成判据 | 等待证据/代价 |
|---|---|---|---|
| P0 | 启动身份与目录一致：App/planner → resolver → supply → PTY，消除错误目标回退；一次解析的目标在执行链复用，必要字段只加到现有 spec/effect | 明确 cwd/provider/distro 失效时不启动到其它目标；五 CLI 桌面、remote create、restart 对同一选择一致 | baize 确认默认 distro 缓存、cwd 与远程差异；会有原本“假成功”变明确失败的行为变化 |
| P0 | 选中 provider/认证与敏感写安全：收紧非等价 fallback，修目标侧临时权限、失败残留、无变化安全校验、同文件写入一致性 | 指定 endpoint 不丢；危险失败零 CLI 消费；清理不明可见；外部修改不被回滚覆盖 | diting 给出具体 writer、清单、权限和并发证据；复用现有原语，不能只修某个 facade |
| P0 | 配置/授权状态真实：target/generation、来源版本、部分成功、清单写失败和恢复语义 | 不跨 distro 复用授权、不把旧版本完成记成新版本完成、不在未持久化时虚报已同步 | 需审计确认具体状态缺陷；若有同 machine-id 克隆/unknown 误继承，窄修身份键和清单迁移，不借机做多 distro 产品 |
| P1 | 现有 env 放行贯穿桌面/远程/restart，明确 Linux 消费与 Windows 参数边界 | 标量和单路径按同一选择进入真实 CLI；保留键未被新逻辑覆盖；PI/CODEX 根等误注入不产生垃圾目录 | 复用 Keys/PathAllowKeys/strip/WSLENV；实际 Windows 子进程环境必须真机验证 |
| P1 | 消除 interop 误导与已证实误拒；Windows 工程真实任务验收 | Linux 搜索/编辑后 Windows 工具能读同一工程；原生 pwsh 等反例按实际解析处理；失败带具体原因 | 无实际 server 不扩 MCP 导入；不自动启用 interop或追加 Windows PATH |
| P1 | 诊断与耗时分账：目标、供给状态、CLI 消费状态、跨界等待 | 从 UI 可定位是配置失败、工具不存在/不可达、网络失败或 DrvFS 慢；无密钥正文 | 复用现有日志/状态/advisory，不建遥测后台；先测再压缩重复探测 |
| 暂缓 | 新 CLI 同步对象、全量资产迁移、任意转换器、自动 Git 选引擎、UE5 构建面板、多 distro 选择器、全局调优 | 本轮不增加这些写面；已上线功能继续保留和修缺陷 | 不以暂缓为由吞掉现有能力回归；omp 安装/MCP 排除保持 |

### 条件决策

- 若审计发现多个 writer 对同一 models/settings 文件使用不同锁/提交语义，优先统一这一个文件的写入边界；只有确有共同消费者的代码才抽取，不展开全项目 launcher 重构。
- 若 OpenCode 同 distro 并发启动或运行时重载复现附加层互串，再将该附加文件收敛到会话专属生命周期；单纯“有覆盖可能”不足以预建另一套配置体系。需同时验证 secrets 清理和 restart。
- 若权限、凭据或用户文件损坏问题不能在本轮安全修复，可暂停受影响的自动写操作并报原因；不能关闭全部 WSL 会话，也不能退回整树复制。
- 当前 `.wslconfig` mirrored 判定保留既有准入策略。本轮加真实连通验收和准确文案，不自动改 NAT 路由/绑定地址。发现声明与运行态不一致时明确失败，不能靠改监听到公网消除失败。
- 健康无变化启动若供给耗时明显，先记录跨界进程次数、读写字节和关键路径耗时；合并重复探测，不缓存掉凭据安全、目标身份或当前选择校验。

## 无法声称消除的固有成本

历史 `p4-evidence.md §8` 报告同仓库 DrvFS `git status` 冷 37.7s/热 3.7s、ext4 0.19s，5000 小文件复制 17.4s，DDC `du` 42s。这些是历史环境的实测，不是本轮复测、性能 SLA 或所有 UE 工程的固定倍率。历史“git 输出 X:/… 自证 X: 为本地卷”推理不足：输出路径不能证明底层卷类型；新验收必须单独记录本地卷/映射盘事实。

保留 Windows 工程会继续承担部分 Linux 侧跨界元数据查询、小文件操作成本；Windows Defender/索引、缓存冷热、目录规模、挂载方式也影响结果。改配置供给不能让 UE 构建、DDC、资产扫描自动获得 ext4 性能。跨 Windows 工具还存在进程创建、参数 quoting、路径形态、代码页/UTF-8、文件锁、大小写与行尾、Git hooks/凭据差异。Linux LANG 不能保证 Windows PowerShell 或 Windows 构建工具输出 UTF-8。

可降低的成本：不把全部工程扫描放到启动；模型搜索按任务限定范围，默认不遍历与任务无关的 DDC/Intermediate/Binaries；对 Windows 主场工具使用其原生入口。范围限制必须不漏当前任务需要的文件，也不自动修改用户全局 ignore/Git 配置。ext4 可作为 Linux 临时工作或对照测试的用户选择，不能作为 UE 工程支持的前提。现有“建议迁移 ext4”的 advisory 应补充 Windows 工程保留原位与分工操作的选择，避免把合法主路径描述成错误用法。

## 端到端验收矩阵

### 证据分层

- L1/L2 自动化：策略、参数、故障注入、目标身份/来源版本/CAS、调用顺序、相关包回归；前端改动做相关组件/API 测试及构建。mock/fixture 只证明隔离逻辑。
- 构建与平台编译：Go 测试、vet、Windows 编译检查，前端构建按改动面；它们均不能证明 ConPTY、WSL、UNC 权限或实际 CLI 可用。
- 真机：当前修复版本的 Windows CodeBox → 真实 UI 或 remote API → WSL → 已安装 CLI → 实际模型/工具 → Windows 工程结果回读。仅执行 shell 命令、配置文件存在或终端留在 bash 均不算模型链成功。

| 验收组 | 真实入口/样本 | 必须观察的结果 | 依赖与失败覆盖 |
|---|---|---|---|
| 启动身份 | 五 CLI 各走桌面、remote create、restart 的受支持既有入口；Pi 另测 API 与订阅两种身份 | distro、uid/Home、pwd、CLI 实际解析路径、选中模型/endpoint 与 UI 选择一致；至少一次真实模型响应 | CLI/模型凭据；omp 仅手装现有启动。未具备某入口/凭据标未覆盖，不能按其它 CLI 推断 |
| 工程路径 | Windows 本地测试工程 `C:\…\空 格中文\…`；同工程经目标实际映射的 Linux 路径；独立 `/home/...`、`~`；不存在目录；UNC/网络盘作为边界样本 | CLI 在精确目标目录；Linux 编辑哨兵文件，Windows 工具读回同一文件且无其它 cwd 产物；不静默回 home | 核对实际挂载，不能假定 `/mnt/c`；未支持 UNC/网络盘明确报错，不伪装支持 |
| 环境转发 | 自定义标量；盘符单路径开关关/开；已有 WSLENV flags；PI/CODEX 根变量；PATH 保留键；含空格中文值 | 在 CLI/其工具进程观察实际值和路径可读性；无 secret 输出；三种入口一致；Linux PATH 未被整体覆盖 | Windows→WSL 与 WSL→Windows 分开测；路径列表不按单路径成功推断 |
| Linux 工具收益 | 同一个 WSL 模型会话用真实 rg/fd、脚本读取/编辑测试工程并检查 diff | 工具为目标 Linux 可执行文件，读写范围正确；模型能消费输出继续完成任务 | 实际 PATH 与安装根分别核实；缺依赖明确提示，不自动安装 |
| Windows interop | 从该模型会话调用 git.exe 的只读命令、PowerShell 中文输出探针；经授权运行实际 UE 工程的最小 UBT/构建校验 | exe 和工程真实可达；参数路径正确；返回码、中文输出、取消/超时可解释；Windows 侧读取构建结果 | Windows/WSL interop 状态、UE/VS/SDK、隔离工程和写入预算；仅 `--version` 不能证明 UE 可操作 |
| provider/登录 | 桌面与 remote 同预设；自定义 endpoint；A2 同内容、双侧新旧、容差、缺源、刷新和认证失效 | 不误打默认 endpoint；本次真实请求身份正确；退出不由 WSL 副本复活；降级提示不冒充凭据有效 | 受控账号与刷新授权；禁止用日常 auth 单侧快照回滚测试 |
| 供给保护与恢复 | 保存→已运行空闲目标/停机目标；WSL 手改/删除；损坏 JSON/TOML；旧预览；同名重装/换用户/克隆身份；清单写失败 | 宿主保存与目标结果分开；停机目标不因后台保存唤醒；用户内容保持；旧授权/旧计划不扩散；重试不重复接管 | 专用 distro；故障注入后需从真实入口核查状态及目标文件，不能只断言错误码 |
| 密钥落盘 | 首次写入、无变化、权限被改宽、rename/chmod/清理失败、并发与中断恢复 | distro 内确认目录/临时/最终权限；无明文日志；不安全残留不被消费，清理失败可见；安全恢复不覆盖后改文件 | 专用测试秘密及权限观察；宿主 os.Chmod 成功或最终 0600 不证明临时阶段安全 |
| MCP/skill/插件 | 现有受支持 CLI 的 Linux stdio/HTTP MCP；一个真实非破坏性 skill；现有 Pi 固定 ref 插件重放；Windows 形态拒绝样本 | 消费者真正发现并调用；本地配置不被覆盖；供给/握手/执行状态区分；可选项失败不阻断模型 | 不含 omp MCP/安装。Windows interop MCP 只有真实需求与授权时加验，不以静态通过冒充握手 |
| 网络 | 实际生效 mirrored、NAT/未知及“文件改了但尚未生效”；代理、Headroom、系统代理按各自入口测 | 真正模型请求到达选定端点/代理；不可达时错误对应实际目标，不输出秘密 | 测试机网络权限；不擅自 shutdown WSL，不为通过而全局改监听/路由 |
| 并发/生命周期 | 同 distro 不同 provider 并发启动、保存与启动交错、remote 取消/重启、CLI 退出后留 shell | 无配置串读、错 generation、旧完成覆盖新 revision；模型失败与 shell 存活区分；无未清理秘密或孤儿任务 | 专用账号/工程，过程观测与失败注入；与外部编辑器竞争的剩余窗口单列 |
| 配套与回归 | Windows 路径 GitAssist/PathPicker；Linux 形态显式边界；原生 Windows shell 与 macOS 启动/配置 | Windows 主路径仍可用；错误不会错位浏览；本轮 WSL 逻辑不改变原生路径/env | Windows、macOS 各需实机入口，前端状态须实际操作，视觉结论另审 |
| 性能 | 当前 Windows 工程冷/热启动、无变化/有更新供给、git status/有界搜索/小文件写；ext4 仅隔离对照 | 记录总耗时与供给、CLI 初始化、工具操作分项，含超时/取消；判断修复降低了哪项摩擦 | 相同仓库规模/缓存条件说明；不从单次测量承诺固定倍率 |

避免不必要的全笛卡尔积：身份/env/模型关键链覆盖五 CLI × 桌面/remote/restart；路径与故障共用机制先在代表 CLI 做边界组合，再给其余 CLI 做真实入口冒烟。任何 CLI 特有 writer/适配器必须补自己的故障与消费验证，不能由代表 CLI 代替。

真机证据每条记录当前构建标识、Windows/WSL/distro/CLI 版本、入口、测试 cwd、实际命令或 UI/API 步骤、开始/耗时、退出状态、脱敏关键结果及清理情况。验收用 met/partial/missed；“编译通过”“单测通过”“历史通过”“当前真机未运行”分别列账。

### 当前真机依赖

本轮环境是 macOS，未取得 Windows 远程入口。需要 Leader 安排已授权 Windows + WSL 测试机、可恢复专用 distro/用户、各 CLI 凭据，以及可写的隔离 Windows 工程。UE 验收还需要对应 UE/VS/SDK 和允许的构建输出预算。配置层单测和交叉编译可先完成，不能据此关闭 V1/V2/V3 的真机部分。

无需因为缺真机而推翻路线或先做大重构；本报告可以完成设计决策。实现是否可发布由审计与上述真机结果共同决定。

## 迁移、回滚与本轮自检

- 保留现有配置清单、授权和用户内容。若审计要求修身份/状态键，无法唯一归属的旧登记退为未确认，重新同意后继续；不能自动扩大旧授权。迁移必须保留旧数据用于诊断，不整树重播。
- 运行时修复按小切片回滚入口或关闭受影响自动写入；普通文件仅在仍匹配本次写入结果时条件恢复。凭据刷新不回滚成旧副本；停止同步不等于密钥已撤回或吊销。
- 接口范围仅涉及现有 ResolveRequest/ResolvedLaunchSpec、WSLConfigSupplySpec、Runner、UnitResult、EnvVar 的必要传递与判定。是否新增字段由已证实缺口决定；不把方向报告当作新增 API 批准。
- 本轮仅写 `agent-outputs/workflow/wsl-goal-closure/direction.md`；撤回本文即完成本轮回滚，无源代码、配置、凭据、全局设置或 Git 变更。
- 验证方式：read/grep/find 静态取证与文档自检；未执行 shell、构建、测试、真机或视觉验证。大文件只读取本文列出的函数/区间，不宣称全仓审计。核心覆盖为 platform/PTY/envvars、wslsupply 协调/状态/提交/转换/A2、桌面与 remote 启动及直接路径消费者；插件实体、各格式适配器全量正确性由独立审计覆盖。
- 一次候选前端路径 `frontend/src/views/settings/EnvVarsSettings.vue` 不存在，随后由 `allowPathValue` 检索定位并读取真实 `frontend/src/components/extensions/EnvVarsPanel.vue`；未据错误路径推断 UI 缺失。
- 自检结论：重大取舍、适用边界、优先级、可逆性和未知项齐全；历史与现状分开；既有 omp 排除、无 ext4 强迁移、无全局配置改写、无新增庞大体系边界保持。
