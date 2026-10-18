# 本机只读 PATH 对比

2026-10-17，Leader 仅执行 command -v（未运行 Agent CLI、未修改 PATH）：

| 命令 | 会话继承环境 | GUI最小环境 PATH=/usr/bin:/bin:/usr/sbin:/sbin |
|---|---|---|
| claude | ~/.local/state/fnm_multishells/46826_1792297810938/bin/claude | 未命中 |
| pi | ~/.local/state/fnm_multishells/46826_1792297810938/bin/pi | 未命中 |
| node / npm | 同上 fnm multishell bin | 未命中 |
| codex | ~/.local/bin/codex | 未命中 |
| python3 | /usr/bin/python3 | /usr/bin/python3 |
| opencode / omp / brew | 未命中 | 未命中 |

存在目录：~/.local/share/fnm/node-versions、~/.local/bin、/usr/local/bin。

解释边界：command -v 未命中不代表未安装；仅说明 PATH 可见性。本次未枚举全部安装根、未读用户配置、未运行网络/软件管理命令。请后端用临时目录夹具验证稳定fnm安装根与瞬态multishell路径的识别和执行一致性。
