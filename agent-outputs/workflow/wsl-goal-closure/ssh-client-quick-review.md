# 专用 Windows SSH 客户端 quick 安全复审

VERDICT: FIX

结论：未发现可由本轮代码路径证实的主机密钥绕过、认证回退或主动泄露私钥问题；存在两项 exec 运行契约缺陷（Major），不是私钥泄露级漏洞。Critical 0 / Major 2 / Minor 0。结论仅适用于下述静态审核范围，不代表 SSH 认证或部署验收通过。

## 基线、输入与边界

- 完整读取 `/Users/run.mao/.local/share/amagi/ssh-utf8/client.py`（111 行）及 `/Users/run.mao/.local/bin/winpc-ssh`（2 行），以当前内容为基线；没有前版 diff，不能判定为历史回归。
- 补充只读核查同一 venv 中 `lib/python3.9/site-packages/paramiko/{channel,client,sftp_client}.py` 的相关路径。
- 未读取、stat、枚举或访问真实 `~/.ssh` 文件、临时 SSH 材料；未导入/运行客户端、未连接网络、未认证、未执行远端操作、未改源码。唯一写入为本报告。
- 主上授权无口令专用密钥、既有权限及独立指纹核对属于任务输入，不将其本身当漏洞，也不冒称本轮已独立复核。

## Findings

### Q1｜Major｜`client.py:44–50`：exec 请求应答阶段绕过命令超时

- **触发条件**：SSH 已认证并打开 channel，服务端收到 exec 请求后既不回复成功/失败，也不关闭连接（例如服务端卡住）。
- **实际影响**：`--timeout` 的循环永远无法开始；本地调用可能无限挂起，不能按承诺返回 124。`settimeout(10)` 不会约束此事件等待。影响是可用性/自动化挂死，不是认证绕过。
- **证据（静态路径证实，未运行复现）**：客户端第 47 行先调用 `channel.exec_command()`，第 50 行才检查 deadline。当前安装的 Paramiko `channel.py:247–255` 发送带应答请求并调用 `_wait_for_event()`；`:1216–1218` 是不带 timeout 的 `self.event.wait()`；`:586–600` 说明 `settimeout` 约束读写操作，而不是该事件等待。
- **最小修复方向**：让绝对 deadline 同时约束 exec 请求应答阶段，超时主动关闭 channel 并返回既定超时状态；不要只给 recv 设置超时。实施者使用纯 mock 的“请求不应答”场景验证有限时间退出，并确认无遗留等待。

### Q2｜Major｜`client.py:60–64,108`：Windows 非零退出码可能变成本地成功

- **触发条件**：远端返回正的非零状态，其低 8 位为零，例如 Windows 程序 `exit /b 256` 对应的 SSH exit-status 为 256。
- **实际影响**：`execute()` 直接返回 256，最终 `sys.exit(256)` 在 macOS/POSIX 进程状态中呈现为 0；下游 shell/自动化误判命令成功。Windows 状态空间不能无损塞入 POSIX 的 8 位退出状态。
- **证据（静态语言/平台语义，未发起远端命令）**：第 61 行只处理负状态，第 64 行原样返回正状态，第 108 行直接交给 `sys.exit`。没有范围检查、非零保真映射或原始码输出。上面的远端命令仅为触发示例，**未执行**。
- **最小修复方向**：保留合法可表示状态；对超范围的非零状态映射成约定的本地非零码，并在 stderr 明示原始远端状态。由实施者用 mock 的 0、1、255、256、较大 Windows 状态及 -1 核验，尤其保证非零绝不变 0。

## 审核覆盖与可信度

| 检查项 | 本轮证据与判断 |
|---|---|
| host-key fail-closed | `client.py:26–30` 先校验专用文件、仅加载指定 hosts、明确 RejectPolicy；没有 AutoAddPolicy 或系统 hosts 加载。已知主机不匹配由 Paramiko 拒绝。连接异常会关闭 client。静态符合。 |
| 无密码/agent/默认 key 回退 | `:32–35` 指定 key，`allow_agent=False`、`look_for_keys=False`，未传 password。依赖 `client.py:799` 的密码认证受 `password is not None` 控制。未发现密码输入或 agent 回退路径。 |
| 凭据输出边界 | 源码没有打印/读取输出私钥内容或启用认证调试日志；失败输出异常类型和文本，exec 则按设计原样转发远端 stdout/stderr。不能承诺输出永远不含秘密：主上显式执行的命令可以自行输出远端秘密，服务端错误文本也属外部输入。此类输出不是本轮证实的客户端凭据泄漏。 |
| exec 超时/退出码 | 正常循环超时返回 124 并注明远端完成 UNKNOWN；缺失状态返回 125；finally 关闭 channel。Q1/Q2 未满足边界契约。关闭 channel 不保证杀死远端进程，现有提示没有作出这种保证。 |
| SFTP 不覆盖 | `:93` 使用 wx；依赖 `sftp_client.py:360–370` 同时设置 CREATE/TRUNC/EXCL，按协议由服务端原子排他创建；`:97` 本地 O_CREAT\|O_EXCL，权限 0600。没有先检查再覆盖的竞争窗口。未连接真实 Windows SFTP，服务端对 EXCL 的实现未验证。 |
| 持久配置权限 | `:19–22` lstat 拒绝叶节点 symlink、非普通文件、非当前 UID 或 group/other 任意权限；不会自行扩大权限。wrapper 参数使用引号且固定 venv 解释器/脚本路径，未调用 shell 拼接命令。真实 key/hosts、父目录及 wrapper/源码/venv 权限未检查（授权边界），因此不签发部署权限通过结论。 |

## 非阻断建议与盲区

- 传输失败可能留下新建的空文件或部分文件；没有覆盖旧目标，但重试需明确处理残留。可考虑临时文件与成功后发布策略，不能为重试静默覆盖已有文件。这是增强建议，不作为当前“不覆盖”契约违背。
- 输出接入阻塞的本地 pipe 时，stdout/stderr write/flush 也可能阻止 deadline 检查；Q1 修复时宜同时明确超时是否覆盖本地输出背压。
- 文件权限检查与实际使用之间没有防竞态句柄绑定；在父目录已可信且同 UID 没有敌对进程的通常本机边界下，不凭这一点升级为漏洞。父目录若可被不可信主体替换则需另审，当前未取证。
- 没有执行 shell/mock/动态测试，也没有生成复现脚本；本报告用源码与依赖路径提供可复核证据。主上提供的既往 py_compile、--help 和 mock 结果是上游陈述，未当成本轮独立执行证据，且不能抵消上述未覆盖反例。
- 专用公钥尚待 Windows 安装；真实认证、主机身份文件内容、Windows SFTP 行为、远端退出码实际传输均未验证。

## 交付与回滚

审核输入为上述两个客户端文件，无额外上游 artifact。未修改客户端或产品 WSL 代码；仅产出本报告。回滚仅涉及本报告，不涉及密钥、系统 SSH 配置或远端状态。


---

## 唯一增量 quick 复审：Q1/Q2 修复核验

**增量 VERDICT: PASS**（仅限 Q1/Q2 及其修复直接影响面）。Q1 已关闭，Q2 已关闭；未发现修复引入的 Critical/Major/Minor。上文初审结论及其当时的未执行事实原样保留，当前修复状态以本节为准。

### 本轮输入与范围

完整重新读取 `/Users/run.mao/.local/share/amagi/ssh-utf8/client.py` 和新增 `test_client.py`；读取上游日志 `/Users/run.mao/workpace/amagi-codebox/agent-outputs/workflow/wsl-goal-closure/ssh-client-offline-tests.log`。未扩大到产品 WSL 或既有无关问题。

### 原编号逐项结论

| 编号 | 状态 | 修复及独立证据 |
|---|---|---|
| Q1（Major） | **已关闭** | `main()` 在父进程连接前调用 `supervise_execute()`；后者通过固定解释器/脚本的 argv（非 shell 拼接）运行 worker，`subprocess.run(timeout=...)` 超时后由标准库 kill+wait，返回 124 且提示远端完成 UNKNOWN。连接、请求应答、输出和 finally 清理都位于受监督进程。独立运行的测试确实启动本地 Python 子进程，将 fake `channel.exec_command` 永久阻塞；1 秒监督超时后断言 124、耗时小于 5 秒、UNKNOWN 文案、PID 存在过且随后 `os.kill(pid, 0)` 得到 ProcessLookupError，全部通过。无真实 SSH 调用。 |
| Q2（Major） | **已关闭** | `execute()` 将大于 255 的状态先打印原值再映射 1，负状态映射 125；正常 0–255 原样保留。独立测试覆盖 0/1/255/256/512/4294967295/-1，分别为 0/1/255/1/1/1/125，并核验超范围原值出现在 stderr。`supervise_execute()` 又将 worker 的负信号状态映射 125，相关测试通过；不存在已知的非零 256 倍数误报成功路径。 |

### 测试可信度及本轮执行记录

先读源码确认测试边界：导入客户端只构造路径常量，不读取认证文件；认证测试同时 mock `private_file` 和 `paramiko.SSHClient`；监督测试的子进程导入后先将 `connect` 替换为 fake，再进入 worker。临时目录仅存合成 PID 文本，不是 SSH 材料。测试不联网、不读取真实密钥/known_hosts。测试由实施者提供，本审核未编写或修改测试。

- cwd：`/Users/run.mao/workpace/amagi-codebox`
- 命令：`PYTHONDONTWRITEBYTECODE=1 /Users/run.mao/.local/share/amagi/ssh-utf8/venv/bin/python -B /Users/run.mao/.local/share/amagi/ssh-utf8/test_client.py`
- 工具 timeout：15 秒；退出状态：0。
- 实际输出：`Ran 5 tests in 1.031s`，`OK`，五项均通过。禁止字节码写入，避免向客户端目录生成缓存。
- 除阻塞请求测试及退出码矩阵外，还通过父进程不发起认证、严格认证参数与失败清理、worker 信号退出映射三项测试。上游日志也是五项 PASS（1.046 秒），本轮结果是另一次独立运行，不以日志代替执行。

### 增量引入风险与证据限度

- argv 原样传递命令，没有新增本地 shell 注入路径；worker 仍使用原有严格 host-key / 指定 key / 禁用 agent 与默认 key 规则。SFTP 分支不进入监督器，其原有不覆盖路径未改变。
- 连接阻塞、输出背压及清理阻塞由 worker 进程隔离和标准库监督路径静态覆盖；本轮五项中**仅请求应答阻塞执行了真实本地子进程超时验证**，没有把其他阻塞场景冒称动态覆盖。
- `--exec-worker` 是内部入口；显式直接调用它不提供外层硬 deadline，不应对外推荐该用法。这不是权限边界，也不构成当前正常 `winpc-ssh exec` 入口的回归。
- 超时不保证远端程序停止；实现明确 UNKNOWN，符合约定。本地监督器诊断仍写 stderr；若调用方完全不消费 stderr，不能将结论扩大为“任意本地输出环境下整个父进程绝对按时退出”。受监督的 SSH worker 在标准库超时处会先被终止并回收。
- 未访问任何真实 SSH 文件、未认证、未联网；真实 Windows exit-status 传输、SFTP EXCL 实现、部署权限盲区沿用初审，不因离线通过而关闭。

仅追加本节到原报告，未改客户端/测试源码；回滚本轮交付只需移除此追加节，不涉及 SSH 配置或远端状态。
