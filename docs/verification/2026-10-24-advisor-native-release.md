# Advisor 发行与实际 CodeBox 验收

验收日期：2026-10-24（America/Los_Angeles）；下表日志为 UTC，已跨至 10-25。

## 发行与安装

| 项目 | 版本 / 发行提交 | 已发布与本机安装 |
| --- | --- | --- |
| amagi-pi | 2.21.15 / `45113f1308ae0940cea5b4b029922607b029e852` | [GitHub Release](https://github.com/runrunrain/amagi-pi/releases/tag/v2.21.15)；通过 `pi update git:github.com/runrunrain/amagi-pi` 安装 |
| CodeBox | 1.3.119 / `4318d02` | [GitHub Release](https://github.com/runrunrain/amagi-codebox/releases/tag/v1.3.119)；下载已发布 darwin-arm64 ZIP，替换本机应用 |

CodeBox 安装位置：`/Users/run.mao/amagicodebox/amagi-codebox.app`。旧版备份：`/Users/run.mao/amagicodebox/backups/amagi-codebox-1.3.118-20261024.app`。安装后的 executable SHA-256 与发行包解压 executable 完全相同：`d62de4c6938db272eab72cbdb35a25467426586493364db515de3c6db686d302`。ZIP SHA-256：`302c18175c623d85cf344b842b2dfc8abd7bef52af8af268adbf202374ac7a38`。

原有三个 Pi 进程最后一轮均已 stop，保留原生会话文件和恢复路径后正常退出 CodeBox；未删除聊天记录或登录配置。工作区原先未提交的 `build.sh` / `build.bat` 改动未进入发行提交。

## 配置保存与回读

在安装后的原生 CodeBox 1.3.119 中进入 Provider Center → CLI 独立配置 → Pi → Agent 配置 → 执行中咨询，选择 Codex 桌面聊天 / 新建并自动续接，添加 luban，点击保存。页面提示“配置已保存”。磁盘 `~/.pi/agent/amagi.json` 新增：

```json
{"advisor":{"backend":"codex-chat","codex":{"mode":"managed"},"roles":["luban"]}}
```

Leader、次数 5、输出 4096、超时 120000ms 均采用默认值。逐字段比较确认其他配置值未变。离开页面再重新挂载配置组件，仍显示相同配置（截图 `2026-10-24-advisor-native-config.png`）。保留此配置供后续使用；子任务仍需显式 `scope.advisor=consult`。

## 实际执行与续接

从 CodeBox 正常启动 Pi 内嵌会话，工作目录 `/tmp/amagi-release-native/workspace`，使用全局安装插件。执行者全程保持 `openai-codex/gpt-6.1-sol`，未使用源码 extension 注入或 Wails fixture。开始尝试在 Web 输入 `/model` 被当作普通文本，未切换模型；本次验收按实际运行模型记录，不宣称较弱模型对照。

Pi 会话：`01a1713e-502e-743c-914c-6fde78b040f9`。两次咨询均 `contextMode=isolated`，Codex 实际模型 `gpt-6.1-sol`，共用原生聊天 `01a1713f-47e5-78f2-9cb0-947d96103191`，桌面聊天标题 **Amagi Advisor 01a1713f**。通过 Codex 桌面 `read_thread` 回读确认两个 completed 回合；均只有文本回答，没有工具执行。

| 证据 | 首轮 | 第二轮 |
| --- | --- | --- |
| 工具开始 → 返回句柄 | 04:08:54.913 → 54.931（18ms） | 04:09:52.255 → 52.263（8ms） |
| 独立 bash 完成 | 04:08:58.116，`CODEBOX_INDEPENDENT_DONE 64` | 04:09:55.209，`CODEBOX_SECOND_INDEPENDENT_DONE 81` |
| 咨询完成 | 04:09:16.116，21.193s | 04:09:58.598，6.341s |
| 交付方式 | 独立工作后显式 wait；不是纯自动交付证据 | 无 wait/query；04:10:01.410 原生 custom_message 自动回送，原执行者继续给出终稿 |
| 续接校验 | 记住 `CODEBOX_ADVISOR_119_R47` | 问题不含原标记、无历史 contextRefs，仍正确回忆原标记 |

两轮独立操作都早于咨询完成。第二轮的完整工具序列只有 `amagi_advisor(consult)` → `bash`，自动建议入原会话后继续完成。原生记录恰有两次 reserved、两次 completed；第二轮恰有一次自动建议。用量事件保留原生 input/output/cacheRead；金额未报告，未伪造费用。详见 `2026-10-24-advisor-native-results.json` 和 `2026-10-24-advisor-native-continuity.png`。

此处验证正常发行安装、配置和异步续接；不据两次短咨询推断一般任务提速。更强/较弱模型的代码质量与耗时对照见 amagi-pi `docs/verification/2026-10-24-advisor.md`。

## 检查结果与边界

- amagi-pi `npm run test:ci`：5343 项，5334 通过、9 条件跳过、0 失败，包含 typecheck / depcruise / docs:check；`npm pack --dry-run` 包含 Advisor 模块；diff check 通过。
- CodeBox 本机 `go test ./...`、`go vet ./...`、前端 329 项测试、移动与桌面前端构建均通过；配置组件 Playwright 8/8 通过。这些与上面的原生应用验收分开记录。
- [发行工作流](https://github.com/runrunrain/amagi-codebox/actions/runs/40160366618) 两平台成功，macOS arm64 / Windows amd64 ZIP 已发布。
- [新版完整 CI](https://github.com/runrunrain/amagi-codebox/actions/runs/40160369098) 的 Frontend and mobile 通过，Go 两平台被已有全仓 lint 问题拦截（macOS 22、Windows 25），因此不能声称云端完整 CI 通过。对 [旧版 v1.3.118 的同流程](https://github.com/runrunrain/amagi-codebox/actions/runs/40160657648) 实测，标准化后的诊断文本集合完全一致，无新增诊断；全部报错文件均未被此次功能改动。对照保存在 `2026-10-24-advisor-ci-baseline.json`。未为了此次功能修改其他平台模块或放宽 lint。
