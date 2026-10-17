# Windows / WSL 真机验收清单（本轮尚未执行）

本清单不是 PASS 记录。当前实施宿主为 macOS；交叉编译和 fake-probe 测试不能证明下列行为。请使用专用 Windows 测试用户、可恢复 distro 和隔离工程，不在日常账号的 auth/密钥文件上做故障注入或回滚。

> **PR-10脚本已撤回**：不再交付可执行的 `verify-staging-window.ps1`。原文仅以 `verify-staging-window.UNSAFE-RETIRED.txt` 留审计证据，禁止复制执行：它含固定用户创建/删除、固定目录清理、错误采样判定，也未真正驱动 Go writer。本轮从未运行。不用未经真机核验的自动替代脚本补证；所有 `AMAGI_WSL_E2E=1` 用例也须逐个审查隔离目录与清理范围，禁止直接全包启用。
>
> 权限窗口验收必须由获授权测试人员准备**已存在的隔离非默认用户**（CodeBox及其测试不得创建/删除系统用户），选择并核验本次唯一临时根，驱动真实Go敏感writer，记录观察进程的开始/完成/退出码及mode/前像断言。只看终态mode不能证明整个窗口无泄露；观察未完整结束不得PASS；不得删除未经本次创建归属证明的目录或用户。

## 环境记录

记录本次构建的 commit + 未提交 diff 摘要、Windows build、`wsl --version` / `wsl --list --verbose`、各 CLI 版本、实际默认 distro/用户/Home。工程存储卷类型要单独核实，不能从 `X:/...` 输出推断是本地盘。

不要自动修改 `.wslconfig`、全局 PATH/Git 配置或执行 `wsl --shutdown`。网络模式切换、distro 重装/换用户、UE 构建均须使用测试机授权范围。测试输出不得包含 API key、token、auth.json 正文。

## 主路径验收

|场景|从真实用户入口操作|必须记录|
|---|---|---|
|Windows 工程|建立含空格/中文名称的隔离 Windows 工程；CodeBox 选择 WSL shell 启动会话|WSL 中 `pwd` 指向同一工程；Linux 工具新增哨兵文件后 Windows 可读；没有默认目录旁路产物|
|路径等价|同工程分别填 Windows 盘符路径与实际 `wslpath` 得出的 Linux 路径；另测 `~`、`~/...`、`/home/...`|CLI cwd 精确一致；不能仅证明终端 bash 活着|
|无效路径|显式填不存在的 Windows / Linux 目录|不静默回退其它工程；错误可见|
|默认路径|请求不填目录，默认路径分别设 Windows / Linux 形态|默认路径语义保留；原生 Windows shell 行为不因 WSL 修复改变|
|env|自定义标量、带空格中文的单路径变量，路径放行关/开；只观察合成非秘密值|desktop 与 remote/restart 行为一致；放行经 `/p`，未放行宿主根不泄漏；Linux PATH 未整体覆盖|
|工具互操作|模型会话运行目标 Linux rg/脚本，再以明确完整路径调用 Windows git.exe / PowerShell 对同工程做只读检查|两侧读取同一文件；Windows 参数使用其可消费形态；中文输出/退出码/取消行为记录|
|真实工程工具|经授权的隔离 UE 工程运行最小 UBT/构建检查|版本/SDK/引擎匹配，构建结果可在 Windows 读取；`--version` 不能替代本项|
|五 CLI 身份|分别启动已安装且授权的 Claude/Pi/Codex/OpenCode/手装 OMP；Pi API 与订阅分开|至少一次实际模型响应，provider/model/endpoint正确；不以生成配置文件代替消费|
|网络/压缩|NAT/未知与实际生效 mirrored；代理、Claude Headroom、Codex全局压缩分开测|NAT依赖宿主回环时明确拒绝；mirrored真正请求成功才算连通；WSL未消费的Codex全局压缩不能记为生效|

## 配置安全与恢复（仅合成秘密、专用 distro）

1. 两个不同 distro（含克隆 machine-id 样本）A→B→A：授权/选择集/插件状态不串用；同名换代后的旧预览不能写新目标。
2. UI 预览→勾选 Claude MCP/settings、Codex MCP/profile→导入：实际目标文件内容与选择一致；同名不同作用域不误选；0项不假报导入成功；空选择仅撤回受管内容。
3. Codex profile 含审批/sandbox/未知字段：不能从 Windows 导入这些策略；WSL 本地已有策略不丢失。
4. Skill 中普通名字链接到 `.env`、排除目录、多跳链接：不供给秘密；扫描后替换源形态必须拒绝。
5. 秘密首次写入、无变化再次写入、文件权限被改宽、chmod/rename/清理失败：用第二 Linux 用户观察整个 staging/提交窗口，不能只 stat 最终0600；失败保住旧混合配置，残留明确报告。
6. 停机/活跃会话期间 Windows 撤回视觉导出→下次启动：补漏/撤回执行；未确认撤回的旧受管副本不能静默继续被消费。撤回文件不等于提供商吊销凭据。
7. 保存期间再次保存、运行中取消、取消后再次保存：推送不会永久卡住；取消返回后是否仍存在Linux安装进程/写入要实测。
8. 插件 both：Windows失败或版本锚点缺失，WSL不追latest；filters不支持时明确blocked，不能启用整包后报等价成功。
9. OpenCode使用jsonc/尾逗号/不可读全局配置/同名MCP：未知冲突不覆盖；授权绑定目标身份；需要实际CLI验证多层配置优先级。
10. 故障后的状态页显示最新失败/部分成功，而非仍显示旧“已同步”；手改、删除、keep-local→takeover→再次更新可恢复。

## 已知历史测试注意

仓库既有 `AMAGI_WSL_E2E=1` 测试中存在固定 distro / 真实用户路径约定。运行前必须逐个审查测试目标及清理逻辑，不能把该开关直接用于日常 distro，不能认为设置另一个环境变量就自动隔离。

## 每条结果格式

`构建标识 | 环境 | 入口 | 操作 | 目标/cwd | 预期 | 实际 | 耗时 | 退出状态 | 清理 | met/partial/missed`

模型调用、UI点击、文件落地、权限和网络结果分别记账。实际CLI/账号/工程未具备的项目保持未覆盖，不按其它CLI的成功推断。
