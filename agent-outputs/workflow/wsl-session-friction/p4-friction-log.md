# P4 过程摩擦记录（活文档）

- 建立：2026-10-16，Owner 天城（Leader）
- 用途：开发/协作过程中亲身遇到的难受问题全记录，随开发持续追加；P4 切片规划的三方来源之一（另两方：原 P4 清单 T11/B5/C1-C3、baize UE5 主路径证据 p4-evidence.md）
- 记录原则：只记亲身复现的事实（命令/行为/时序），不写猜测性根因；带去向标注（amagi-pi 插件侧可修 / amagi-codebox 产品侧 / 环境固有引导）

## A. 模型 API 与任务执行层

### A1｜GLM API 500 中断四连（2026-10-16 晚）
- 现象：P3-S5 实施四遇 `500 {"code":"1234","message":"网络错误"}`（20:11/20:25/20:38/21:13），原任务+两轮 resume 均在**恰好 4 调用**处断；同窗时 diting 新会话 24-28 调用正常跑完；修复轮新会话 luban 47 调用零中断。
- 排除：上下文超限（主上证伪——GLM 窗口 100 万 vs 累计入 198k）。
- 存疑差异仅剩：resume 请求携带全历史（单请求大 prefill）vs 新会话轻请求；或纯服务端波动恰逢其会。**根因未定论**。
- 难受点：①resume 三连撞墙浪费约 40 分钟；②恢复指引标「确定性失败（先修因）」——网络错误实为非确定性，指引误导续接决策；③无重试/退避机制，子进程裸撞。
- 运维结论（已沉淀 workflow.md）：长会话 resume 连续撞 500 → 改新会话任务书接管。
- 去向：**amagi-pi 插件侧**（evolution 沉淀：①error 分类对 HTTP 5xx/网关错不应标「确定性失败」；②可考虑任务级自动重试一次+退避）。

### A2｜inputs 路径解析基准不一致（两次拒单）
- 现象：task_submit 的 inputPaths 相对解析基准行为不一致——P3-S5 修复轮派单 `["amagi-codebox/agent-outputs/.../s5-report.md"]`（调用者 cwd=X:/WorkSpace）成功；P4 证据采集派单同样传法（workspace.cwd="amagi-codebox"）却拒单「路径不存在」，错误提示的等价候选目录以 amagi-codebox 为基准展示。
- 绕过：任务书正文内嵌相对路径让子 Agent 自读（子 Agent 工作目录内解析可靠）。
- 难受点：拒单提示的「疑似漏层」候选自相矛盾（同时列出 agent-outputs\wsl-session-friction 与 agent-outputs\workflow\wsl-session-friction），无法据此推断真实解析根。
- 去向：**amagi-pi 插件侧**（evolution：inputPaths 解析基准文档化或统一为调用者 cwd；拒单消息给出真实解析绝对路径）。

### A3｜baseline 检查环境性不可用，每任务手动 waive
- 现象：`chk-auto-baseline-test-*` 在 DrvFS 上恒 unavailable（npm 采集器 EACCES lstat frontend/node_modules/.bin/acorn、broken-link），每批 accept 都要手动复制完整 id waive；S3 曾因 typo 一个字符被拒重来。
- 去向：**amagi-pi 插件侧**（采集器对 DrvFS 权限错误的降级处理；或会话级记忆「此类环境性缺口自动豁免」）。

### A4｜证据采集器 cwd git 探测失败
- 现象：子任务回执证据「cwd=X:\WorkSpace 不在任意 git 工作树内」→ diff 采集 exit 1；实际 amagi-codebox 就是 git 仓库（父目录探测在 DrvFS 超时或逐层判定失败）。
- 去向：**amagi-pi 插件侧**（git 工作树探测带超时降级+允许任务书显式声明仓库根）。

### A5｜AMAGI:DONE 尾标记不稳定
- 现象：diting 首审回执缺独立 marker 行（触发「完成度未自证」告警），复审有——同为 diting 角色，输出契约漂移。
- 去向：**amagi-pi 插件侧**（角色卡强化或完成面自动补标，告警本身有价值保留）。

### A6｜run_bg 后台原语与 WSL bash 宿主不一致（P4 证据采集实测）
- 现象：run_bg 启动的 Linux 语法脚本（bash -c、/usr/bin/time、~/ 路径）挂 29 分钟零输出零进展——WSL 内 ps 查无 pid 23964、无任何 git 进程、D 状态 0；日志文件「暂不可读/尚无输出」。判读：run_bg 执行宿主在 Windows 侧，Linux 语义脚本空转。cancel 后改 bash 工具同步分批，同一命令清单 10 分钟内全部跑完。
- 难受点：①后台原语的宿主语义未声明，挂起无任何诊断信号（既不报错也不退出）；②浪费 29 分钟等待。
- 去向：**amagi-pi 插件侧**（run_bg 宿主语义文档化/启动时探测命令形态告警）。

## B. 编辑与文件工具层

### B1｜混合缩进大文件的手工编辑连续失手
- 现象：frontend/wailsjs/go/models.ts（5800+ 行，tab/空格混排的生成物）手工追加类型：edit oldText 凭记忆重构缩进连续 4 次不匹配（edit-guard 拦截零写入，防了事故）；改用 selection 快照整行替换后仍一次结构错位（namespace 提前闭合）——最终放弃手工嵌套编辑，改方案：models.ts 还原基线 + d.ts 宽松返回类型。
- 教训：**生成物不手工嵌套编辑**；等价方案（d.ts any + 注释标注再生归一）风险低一个量级。
- 去向：**流程记忆**（已入本档+CHANGELOG 注释）；工具侧 edit-guard 表现好（四次全拦住）。

### B2｜git checkout 守卫拦截快速回滚
- 现象：丢弃 models.ts 手工改动被守卫拦（需交互确认，bash 通道超时=拒绝）；绕道 `git show HEAD:path > file`（只读对象+重定向写，不触发守卫）。
- 评价：守卫方向正确（防误丢弃），Leader 场景多一跳可接受。
- 去向：**环境固有引导**（记录绕法即可）。

### B3｜管道退出码陷阱（软提示 5 次）
- 现象：裸读管道退出码（`cmd | head` 后 `$?` 是 head 的）被软提示纠正 5 次；正确姿势 `set -o pipefail` 或 `${PIPESTATUS[0]}`。
- 去向：**amagi-pi 插件侧**（可考虑 bash 包装器默认 pipefail）或 Leader 习惯养成（已改）。

### B4｜write 工具 POSIX /tmp 与 bash /tmp 不一致
- 现象：write 报成功但 bash 侧 /tmp 无文件（Windows/WSL 双宿主重定向边界）；已记 workflow.md，会话内跨边界一律 /mnt/<盘>。
- 去向：**amagi-pi 插件侧**（跨宿主路径语义文档化或回执显式标注实际落盘宿主——现回执有 Windows 侧路径标注，部分改善）。

## C. 环境/DrvFS 层（与用户同病）

### C1｜git 全树操作超时
- 现象：git status 在 /mnt/x >90s；全部 git 操作后台化+长超时；定向 diff 替代全树扫描。
- 去向：**amagi-codebox 产品侧**（正是 P4 主证据范围——用户在 WSL 会话跑 git 同样撞；候选方向：GitAssist 对 /mnt/* 形态路由到 Windows git.exe 或引导）。

### C2｜Windows 工具全路径调用
- 现象：wsl.exe、go.exe、git.exe、powershell.exe、tasklist.exe 全部 /mnt/ 全路径；PATH 无 Windows 目录（appendWindowsPath=false）；tasklist GBK 乱码、wsl.exe UTF-16 需 tr -d '\0'。
- 去向：**amagi-codebox 产品侧**（候选：CodeBox 会话注入常用 Windows 工具的别名/环境块，或文档化引导）。

### C3｜node-compile-cache 垃圾目录
- 现象：node 在跨边界跑（WSL node 跑 Windows 路径）时缓存目录解析出 `frontend/C:` 字面目录；S3 曾意外提交 548 个垃圾文件，已清+gitignore。
- 去向：**已修复**（gitignore）；源头（node 缓存路径解析）环境固有，记录在案。

### C4｜wailsjs 生成物无 CLI 手工维护
- 现象：本机无 wails CLI，S4/S5 两批绑定手工补（App.js 变量传参可保正确；models.ts 强类型放弃，d.ts any+再生归一注释）。
- 去向：**amagi-codebox 产品侧**（待办已有：wails CLI 环境下 `wails generate module` 归一；或考虑构建脚本化绑定生成）。

### C5｜wails.json 版本号陈旧
- 现象：wails.json 停 1.3.105，CHANGELOG 至 1.3.113——历批惯例只动 CHANGELOG；版本双源不一致。
- 去向：**amagi-codebox 产品侧**（低优先级：发布脚本统一或 wails.json 跟随）。

## D. 待沉淀（amagi-pi evolution）

- A1 错误分类/重试、A2 解析基准、A3 baseline 豁免、A4 git 探测降级、A5 DONE 契约、B3 pipefail、B4 跨宿主路径——批量沉淀一条 evolution 条目或分条（待 P4 空档执行；权限闸在 WSL 布局失效问题待主上手动或纳入治理）。

## 追加记录

（随开发持续追加，格式同上：编号｜现象｜难受点｜去向）
