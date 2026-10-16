# Pi 登录认证提供商

## 使用

1. 在 Pi 内完成 OAuth 登录，登录项位于标准 `~/.pi/agent/auth.json`。
2. 打开 **服务提供商**，登录项会以「登录认证 · Pi」卡片显示。可用「刷新登录状态」重新发现登录/退出。
3. 点击卡片，保存预设名称、Pi 原生模型 ID、思考等级。没有本地模型目录时可以手动输入模型 ID。
4. 打开 **会话设置 → Pi**，选择「提供商名 · 登录认证 (Pi)」与已保存预设，通过现有新建会话入口启动终端。

登录类型不能用于 Claude Code、Codex、OpenCode 或 OMP。它不会出现在这些 CLI 的提供商/预设选择中，后端也拒绝将登录引用用于其它 CLI。普通同名 API 服务商仍独立存在。

## 命名空间与解析（R1）

登录引用与公共 provider/stable key 规则真正不相交，判定以「精确命中专用登录存储 + 结构化登录来源 id」为准：

- 登录来源 id：`pi-login/<base64url(原生 provider 名)>`（恰好两段）；登录预设 key：`pi-login/<base64url(provider)>/<base64url(name)>`（恰好三段）。
- **登录启动**：`modelRef` 精确命中已存登录预设 key，或 `providerRef` 是结构化登录来源 id（普通 provider 名不含 `/`，结构上不可能撞上）。后者而预设已删除/退出登录时按登录失效失败关闭，绝不回退普通路径。
- **普通路径**：字面名为 `pi-login` 的 provider 与其公共预设（含 `pi-login/Daily` 两段 key、`pi-login/Foo/Bar` 三段 key）在保存、加载、导入与所有 CLI 启动中保持既有可用性，不被前缀劫持。
- 同字符串 key 双向互斥：`SaveTerminalPreset` 拒绝已存登录 key，`SavePiLoginPreset` 拒绝已存公共 key，避免导入/旧数据路径产生歧义。

## 认证目录锚定（R2）与 Shell 启动重锚（R3）

登录分支在**最终 Pi 执行边界**钉定 canonical 标准根：

- 发现/启动检查始终使用 CodeBox 进程 Home 的 `~/.pi/agent`（忽略 `PI_CODING_AGENT_DIR` 环境覆盖）。检查通过后，desktop embedded、external、v1/restart 全部把 `PI_CODING_AGENT_DIR` **显式设为该 canonical 目录**（而非删除）：环境变量面板自定义 `HOME`/`USERPROFILE` 无法再把 Pi 导向另一份 `auth.json`。
- POSIX shell（zsh/bash/sh/fish，`-ilc`/`-lc`）的 startup 文件在 CLI 之前运行，可能重导出 `PI_CODING_AGENT_DIR` 或 API 凭据。因此登录分支把这些 shell 的启动命令本身包上前缀：`env -u <凭据键…> PI_CODING_AGENT_DIR='<canonical>' <原命令>`，在 shell 初始化完成后重新钉定并剥离凭据。direct 模式与 Windows 家族 shell（pwsh `-NoProfile`、cmd `/K`，不加载用户 startup 文件）依赖已钉定的 `spec.Env`，不加 POSIX 前缀。
- 不修改认证文件；继承的常见 API 凭据/端点覆盖被删除。普通 API 会话的环境语义完全不变。

**WSL 分叉（A2 解除，主上裁定 2026-10-16）**：WSL 会话不钉 Windows 路径——认证目录锚定切换为 distro 内 `~/.pi/agent`（pi 在会话内读自己的 HOME）：
- `PI_CODING_AGENT_DIR` 从 WSL spec 环境中剥离（含 WSLENV 清单），工作目录由 wsl.exe 承担；登录载荷以 `env -u <凭据键…> -u PI_CODING_AGENT_DIR` 收尾，shell startup 之后的重导出同样被中和。
- 凭据新鲜度由启动时惰性同步保障，**双向新陈判定、新者为准**（qreview M-1：唯一有效凭据可能在任一侧——WSL 会话内 pi 轮转 token 写 distro 侧后，单向覆盖会摧毁它）：内容一致 → 零拷贝；Windows 新（或 distro 缺失首次播种）→ 复制进 distro；distro 新 → 反向回同步 Windows 侧；两侧不同且 mtime 并列/容差内不可判 → 保守取 distro（会话即将消费侧）。同步算法为 `launcher.SyncWSLPiLoginAuth`（SHA-256 + mtime，mtime 经 wsl.exe 内 `stat -c %Y` 取 ext4 权威值）；P3-S0 起桌面闸门与 remote/v1 重启链路共用同一执行边界——remote 计划仍纯读零副作用，待供给描述由 `EffectWSLConfigSupply`（PTY 之前）经 wslsupply 协调器统一 Inspect/Plan/Apply，登录载荷只剩 `env -u` 凭据键重锚（cmp/cp 内嵌同步片段已退役，消除 shell/Go 双实现安全差异）。同步失败降级为 distro 既有副本（WARN）；两侧都无凭据或宽松权限副本无法收敛时 fail closed 中文报错拒绝启动；Windows 侧未登录维持登录失效错误路径。
- 上表 R2/R3 的 Windows Home 锚定与重锚语义仅保留给非 WSL 会话。

## 失效选择与 UI（R4）

会话页刷新（含登录状态）后由 `reconcilePiLaunchSelection`（`frontend/src/views/piLaunchSelection.ts`）协调 Pi 选择：

- 当前登录选择失效（退出登录/预设删除）或登录状态读取失败：**保留失效引用**并标记 `piLoginStale`，界面显示明确告警，启动入口（侧栏与启动 composable）禁用并报错；用户必须显式选择新的认证来源（选择任意 provider/preset 即清除锁定）。
- 公共选择失配维持既有自动改选行为（同 provider 首个 → 列表首个 → 清空），普通 API 语义不变。后端对失效登录引用本身也固定拒绝，双层防线。

## 数据与接口

- `piconfig.Service.GetPiLoginProviders()`：只读动态 DTO，字段为 `id/provider/cli/source/models`。扫描认证项只解码 `type`，只有 `oauth` 会显示；`api_key` 不算登录。
- 扫描始终使用标准用户 Home 的 `.pi/agent`，不跟随 `PI_CODING_AGENT_DIR`（与启动钉定值同源）。其它 Pi 配置编辑器原有覆盖目录行为不变。
- `config.ConfigService.GetPiLoginPresets/SavePiLoginPreset/DeletePiLoginPreset`：专用 CRUD，写入 CodeBox `models.json` 的 `pi_login_presets`。字段仅有 `name/provider/model/thinking`，不存端点、API 密钥、token、同步或媒体标记。
- `config.ConfigService.ResolvePiLoginLaunch`（desktop/v1/restart 共用分类器）与 `RejectPiLoginLaunchRefs`（非 Pi 入口隔离拒绝）实现上述命名空间规则。
- 登录元数据不写入 `models` 或公共 `terminal_presets`。专用预设不参与 harness provider 同步、媒体导出或公共 API 预设导出。当前完整配置导出不携带 `pi_login_presets`；这是本机持久化功能，不迁移登录凭据。

## 启动与失败边界

桌面 `LaunchPiSession` 与 v1/restart `buildPiPlan` 共享登录解析/再检查。登录预设使用 Pi 原生 `--provider <native-id> --model <model-id>`，可选 `--thinking <level>`。不创建 `amagi-*` provider，不调用 API 密钥库、不改写认证或模型配置。

桌面 stable recipe 保留登录预设 key；重启重新解析预设和认证，不能静默回退到普通 API 提供商或默认账号。登录项被移除、预设不存在/损坏、来源不匹配会拒绝启动。WSL 会话（主上裁定 2026-10-16，取代旧「拒绝而非复制 token」条款）经启动时惰性同步消费 distro 内凭据副本：同步成功/降级用 distro 既有副本即可放行，两侧都无凭据时拒绝启动并给出中文报错；Windows 侧未登录仍按登录失效关闭，绝不回退普通 API 路径。

缺少认证文件正常返回空列表。坏 JSON/文件读取失败返回固定安全错误，不附认证内容或底层路径。缺少/损坏模型目录只影响模型提示，不隐藏登录卡片。发现 OAuth 类型表示有原生登录条目，不保证远端订阅有效或模型仍可用；token 刷新与推理错误由 Pi 管理。

## 回归与交互复核

Go 用临时 HOME/合成认证 fixture 覆盖发现、隔离、持久化、重启、参数、退出登录、WSL 放行与同步闸门（同步调用/失败降级/两侧无凭据关闭）、R1 命名空间共存、R2 canonical 钉定、R3 shell 重锚、WSL 锚定分叉（载荷同步片段/env -u/PI_CODING_AGENT_DIR 剥离）。macOS 的 `TestPiLoginDesktopRealPTY` 经真实桌面入口、生产 CLI resolver、真实 PTY 启动临时 Pi 可执行 fixture；`TestPiLoginDesktopRealShellStartupReAnchor` 用真实 zsh startup（`.zshenv` 重导出 agent root 与凭据）+ 自定义 HOME 双重对抗验证最终进程环境。不访问真实账号或提供商网络。Windows 真机：`AMAGI_WSL_E2E=1` 下 `internal/pty` 的 `~/` 工作目录真实 ConPTY 启动冒烟（B4）与 `internal/launcher` 的真实 auth.json 同步/幂等实证（A2，快照恢复）。

前端 `piLaunchSelection.test.ts` 覆盖 R4 状态协调（失效保留/错误保留/公共改选不变/显式重选），`useSessionLaunch.piLogin.test.ts` 覆盖启动闸门。

浏览器到 Go/PTY 的可重复验收（macOS）：

```sh
# 两个终端，均从独立 worktree 根目录运行；测试 bridge 最长 170 秒。
PI_LOGIN_BROWSER_BRIDGE=1 go test . -run '^TestPiLoginBrowserBridge$' -v -count=1 -timeout=180s
frontend/node_modules/.bin/vite --config e2e/pi-login.vite.config.mjs
```

第一个命令输出 `PI_LOGIN_BROWSER_URL`。打开 `http://127.0.0.1:5187/e2e/pi-login.html?bridge=<该URL>`，执行「登录卡片 → 保存预设 → 会话页 → Pi 登录选项 → 直接启动 Shell → 启动」；`GET <bridge>/trace` 可读到实际 CLI fixture 的 argv 与钉定的 agent root。`POST <bridge>/logout` 清空临时认证，再在 UI 刷新：登录卡片消失、当前选择转为失效告警且启动被禁用——必须显式改选后才能再次启动（R4 预期）。最后 `POST <bridge>/finish` 结束测试，清理 PTY 与临时文件；Vite 进程单独退出。

该 harness 是测试专用 Wails transport adapter。核心功能 RPC 委托真实 Go 服务；认证和 CLI 可执行文件是明确的隔离 fixture。不属于生产远程接口，也不证明真实订阅网络推理或 Wails WebView 的完整外壳行为。
