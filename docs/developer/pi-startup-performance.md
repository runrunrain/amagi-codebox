# Pi 会话启动性能

范围：桌面端「新建会话 → 内嵌终端」，Windows PowerShell 和 WSL。诊断未修改用户的 Pi 配置、凭据或插件安装。

## 分开记录三个时间

1. **终端页显示启动进度**：从点击到页面可见；可早于进程创建，不是会话就绪。
2. **真实终端挂载**：后端提交实际 PTY 会话后，前端选中该会话，xterm 挂载。系统日志的 `Pi PTY进程已启动` 是后端里程碑；新增的 `Pi 首次 PTY 输出（不代表 Pi 就绪）` 统计进程创建到第一段数据，可能只是终端控制序列。
3. **Pi 就绪**：实际能接受请求或交互。Web 扩展的 `webui 探测 available` 是另一条时间线，不能拿它代替 TUI 就绪，也不应阻塞 TUI 挂载。

## 本机证据

| 场景 | 观测 | 口径 |
| --- | --- | --- |
| 修改前桌面原生日志 | 启动请求 → PTY 已启动约 128 ms；随后代码另有固定 1000 ms 的命令注入等待 | 进程创建，不含 Pi 初始化 |
| 修改前桌面 WSL 日志 | 温启动请求 → PTY 已启动约 0.76–1.43 s；一次冷预检 4.964 s；一次进程内首次共享技能检查耗掉约 3.07 s | 真实日志，不含 Pi 初始化 |
| 修改后 Windows Chrome 生产前端 | 点击 → 启动进度 46 ms；模拟启动 RPC 放行 → xterm 可见 27 ms，保存默认值的 RPC 仍未返回 | 真实页面与 xterm，隔离 Wails 测试桥；不是桌面整链成绩 |
| 修改后 PowerShell 7 + ConPTY | 约 357–413 ms 收到 Pi shim 的完整实参，包括中文、引号和 shell 元字符 | 真 Windows PTY，隔离 shim，不是 Pi 扩展初始化 |
| PowerShell 7 + 真实 Pi，无用户扩展 | 637–683 ms | 3 次隔离 `get_state` RPC，不含 CodeBox UI |
| PowerShell 7 + 真实 Pi，加载本机 amagi-pi | 1.57–2.05 s | 同口径，隔离状态目录，正常加载扩展 |
| 修改后 WSL 共享技能检查 | 155 ms，单次 `wsl.exe` | 温 Ubuntu，临时 Windows 目标与 Linux HOME，真实翻译和建链 |
| 真实 WSL Pi + 本机安装的 amagi-pi（Windows 挂载） | 33.70 / 36.20 s | 两次离线 `get_state` RPC；其中扩展模块导入 26.25 / 28.87 s，工厂初始化 6.06 / 5.88 s |
| 同一 WSL Pi，不加载用户扩展 | 147–160 ms | 隔离 agent 根，对照组，不是产品配置 |
| 同一 amagi-pi 包与依赖放 WSL ext4 | 4.10 s（该路径首轮）/ 426 ms（重复） | 仅实验目录中的包代码、依赖与资源；没有复制现用认证配置 |

WSL 数十秒等待的最大项是 Pi 跨 DrvFS 加载扩展，而不是 ConPTY、页面路由或模型接口。`PI_OFFLINE=1` 已开启，并不能消掉本地文件 I/O 或首次编译。

## 改动与边界

- 内嵌 Pi 在 PowerShell 中以内联命令启动，不再等待 profile、交互提示符和 CodeBox 的 1 秒注入定时器。使用 `-NoProfile`，依赖 profile 设置的环境变量需显式配置给 CodeBox。Pi 退出后保留 PowerShell，不改系统执行策略。其他 CLI 继续走原有启动方式。
- WSL 的共享技能根翻译、检查与按需修复合用一次调用；保留原有备份、原子发布和终验。仍在真实进程创建前执行工作目录预检和敏感副本供给检查。
- Pi 启动时先并行加载终端页；只有后端返回实际会话后才挂载对应的终端。默认值落盘和 WebUI 探测不阻塞终端页。失败不显示虚构会话，用户在等待期间切走时不抢回页面。
- 不自动关闭扩展、迁移项目或复制用户 agent 根。要把 **Pi 可交互** 也稳定压到 WSL 2 秒内，仍需将 Pi 的扩展包解析/执行目录与共享 agent 配置目录分离，使用 Linux 本地扩展产物，并处理按包版本更新与首次编译。当前构建没有在加载 Windows 扩展、冷 WSL 或冷编译场景下达到这个硬上限；也没有把所有原生配置宣布为 1 秒内。

## 回归与采样

在仓库根目录、Windows PowerShell 7 中执行（按实际安装路径与发行版调整）：

```powershell
$env:AMAGI_CODEBOX_TEST_PWSH = (Get-Command pwsh.exe).Source
$env:AMAGI_CODEBOX_TEST_WSL = 'Ubuntu'
go test ./internal/platform ./internal/pty -run 'Test(WindowsPiPowerShell|PiPowerShell|WSLSharedSkills|ParseWSLSharedSkills|WSLEnsureSharedAgentsSkills)' -count=1 -v
```

Windows 上的专项测试、`go vet . ./internal/platform ./internal/pty`、仓库编译检查和桌面前端构建通过；Vitest 738 项通过。Windows 平台/PTY 全包实跑仍有符号链接权限、PATH 缺 `sh` 与 OpenCode attach 测试超时；用未修改的 HEAD 源码覆写后复现同样失败。

`useSessionLaunch.progress.test.ts` 覆盖 RPC 尚未完成时的进度、失败恢复、切换会话和独立的默认值落盘。

复测完整路径时，用实际桌面构建分别记录：新建按钮点击 → 进度可见 → xterm 可见 → Pi 能接受输入；另记 WebUI 可用。区分 WSL 温/冷启动、工作目录与扩展包所在文件系统。定位 Pi 内部时，在隔离 agent 目录内用 `PI_OFFLINE=1`、`PI_TIMING=1` 启动 `pi --mode rpc --no-session`，发送 `{"id":"check","type":"get_state"}` 并计时到对应成功响应；不发送推理请求。切勿将隔离测试的结果当作用户现有配置的端到端成绩。
