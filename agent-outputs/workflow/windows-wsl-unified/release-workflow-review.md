# v1.3.124 Release workflow 独立审核（quick）

VERDICT: PASS

范围：仅当前未提交 `.github/workflows/release.yml` diff，以及原标签源码、CLI 版本、dispatch/发布标签/权限/注入边界。不代表两平台构建已成功。Critical/Major/Minor findings：均无。

## 输入与基线
- 已完整读取任务内联 release.yml、`go.mod`、`/tmp/codebox-v1.3.124-release-failed.log`；通过实际 `git diff -- .github/workflows/release.yml` 核对当前变更。
- HEAD 为 `6f22c90e93697a46b555d2d441deca581b72e299`。v1.3.124 是 annotated tag：tag 对象 `eced437639a48601b60a570e97ac7a46062edb4b`，剥离后为上述 commit。GitHub 只读 API 也确认相同对象与 commit；不是标签漂移。
- 失败日志两平台均明确：`@latest` 解析为 v2.17.1，需要 Go >=1.26.0，当前 Go1.25.0、GOTOOLCHAIN=local，退出1。

## 关键核对
| 验收 | 证据与判断 |
|---|---|
| 原标签应用源码 | 两 job checkout 均显式 `ref: refs/tags/${{ env.RELEASE_TAG }}`（release.yml:26、119）。dispatch 的 tag 输入为 v1.3.124 时工作目录来自标签，不是 dispatch master commit。后续 npm、Go、Wails 均从该工作目录执行；metadata 用 `git rev-parse --short HEAD`，不会误注入 `github.sha` 的 master SHA。 |
| CLI 版本修复 | release.yml:45–46、137–138 从 checkout 后模块取版本再以带版本参数安装。标签 go.mod:15 与 vendor/modules.txt 均为 v2.11.0；本地原命令 `go list -m -f '{{.Version}}' github.com/wailsapp/wails/v2` 实际输出 v2.11.0，退出0。不再选择 latest。 |
| Go/CI 不变 | 两 job 仍 Go1.25.0、Node20.19.0；`git diff -- go.mod .github/workflows/ci.yml` 为空，`git diff --exit-code refs/tags/v1.3.124 -- go.mod .github/workflows/ci.yml` 退出0。 |
| 发布标签一致 | 输入统一为 RELEASE_TAG，经 Get version 成功后输出 VERSION；两 Release action 均显式 `tag_name: VERSION`（release.yml:109、210）。因此 dispatch master 的 github.ref 不会被 action 默认解释成发布目标。资产名、版本注入均使用同一 VERSION。不存在的标签在 checkout 即失败，不会走到创建 Release。 |
| 注入边界 | 未验证的输入仅经 env 传值、checkout action 的 with 参数传递，不直接拼入 run 源码。Get version 用带引号变量和正则校验；之后插入 Python、PowerShell、cmd、bash 的 VERSION 字符集只含数字/字母/v/点/连字符/加号，排除引号、换行、反引号、美元、分号、&、管道等执行字符。输出不能注入额外 GITHUB_OUTPUT 行。 |
| 权限 | workflow 显式 `contents: write` 足够 checkout 与 Release 上传；无需额外授予 workflow/actions 写权限给运行时 GITHUB_TOKEN。仓库 API 显示 Actions enabled、allowed_actions=all；默认 token read 不覆盖 workflow 显式 contents:write。手动发起者需仓库写权限；使用 API/CLI 的调用凭据另需允许 dispatch（细粒度令牌为 Actions:write），与运行时 token 权限不同。 |

## workflow_dispatch 的必要操作条件
必须先由 Leader 将修复 workflow 提交并推送到默认分支 master，再启动一个**新** dispatch：

```sh
gh workflow run release.yml --repo runrunrain/amagi-codebox --ref master -f tag=v1.3.124
```

`--ref master` 选择修复后的 workflow 定义，`-f tag=v1.3.124` 选择应用源码与 Release 目标。这两个 ref 用途不同。不要以 `--ref v1.3.124` 调度旧 workflow，也不要重跑原失败 run 期待加载新定义；原标签内尚无这次修复。无需移动或重建标签。工作流执行时会局部同步 wails.json 版本（既有步骤），不提交该工作目录变化。

官方语义已在线核对：
- https://docs.github.com/en/actions/reference/workflows-and-actions/events-that-trigger-workflows#workflow_dispatch ：默认分支须存在 workflow；dispatch 可提供 ref 与 inputs；github.ref/sha 属于调度 ref。
- https://docs.github.com/en/actions/how-tos/manage-workflow-runs/manually-run-a-workflow ：需要仓库 Write access，CLI 用 --ref 指定运行分支。

## 验证记录与边界
工作目录 `/Users/run.mao/workpace/amagi-codebox`。git diff/status、标签剥离、标签 go.mod/vendor 查阅、go list、diff --check 及 GitHub 只读 API 查询均退出0；diff --numstat 仅 release.yml 为29增4删。只写本报告，未修复、未提交、未 dispatch、未移动标签。

本轮未执行 go install、Wails 构建或 Windows/macOS GitHub runner；待 GitHub 新 dispatch 验证安装和双平台打包结果。不将既有 CI 全绿当作 Release 构建通过。无项目改动需要回滚；本报告可独立删除。
