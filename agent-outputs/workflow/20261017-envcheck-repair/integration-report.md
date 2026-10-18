# 环境检测与五 Agent 管理链路：集成验证

## 状态
- **最终状态：完成并验收。** E1/E2修订净增量与Leader补修已集成主树；未提交 Git。最终验证为 bg-703550de / bg-a7d929e4，独立增量复审91f7c414 PASS并已接受。
- E1 初稿 c41d185e、E2 初稿 33093cc2 已拒绝；修订分别为 9531a896、dd8f40d4。
- 前序 GeneralSettings/默认五引擎与 usage/价格改动保留，不属于本阶段审查范围。

## 已落实修复
- PATH：排除 resolver 诊断标签、规范化目录去重、幂等写入；成功修复同时同步当前 CodeBox 进程并复检，外部终端重启要求独立说明。
- 管理操作：补 OpenCode/Codex/Pi/OMP 通用异步卸载，复用 RunEnvFixAction 的 uninstall_tool 动作；没有新增 Wails Service 绑定方法。npm 包/前缀归属与 Homebrew formula 映射核实后才执行，未知来源拒绝自动删除。
- 结果语义：依赖安装失败/未确认不再伪成功；安装健康与确知最新分开，查询失败不等于最新；卸载以复检终态而非受理结果为准。
- UI：展示顶层/问题内修复入口、正确传 Claude method、尊重 canInstall、提示类动作本地处理；全局写忙碌防重入；变更后确定性复检；异步受理状态独立于快照观察、快照短时失败继续轮询、即时终态不漏通知。

## Leader 集成加固（在子任务修订之上）
1. 无 ID 的卸载受理：记录请求前 operation ID，排除时间容忍窗内旧槽；以请求开始时间匹配（避免慢受理回执错过实际早已完成的操作）；首次匹配后锁定新 ID。
2. 45 秒界限改为连续失去匹配观察的窗口，正常长安装不会消耗容错预算；状态未知时清理陈旧 running 观察，避免永远锁住界面。
3. 过期快照响应不覆盖后到的新状态；组件卸载后不再重建轮询。
4. Homebrew 路径以已验证 prefix 为基准提取 Cellar/opt 相对段，防止把 `/opt/homebrew` 的根目录 `opt` 误认为 formula 布局。
5. npm shim 只认普通小文件中的 npm 风格执行行和完整包路径边界；注释、同名前缀 sibling 包、foreign absolute path 不构成归属证据；读取限制 64KiB。
6. 当前版本为空或实际比较落后时，不能因 HasUpdate=false 而认定最新；有更新的已安装 no-op 文案明确导向更新按钮。

## 验证证据
### 前端：已通过
- `npm --prefix frontend run test`：34 文件 / 327 测试通过。
- `npm --prefix frontend run build`：vue-tsc + Vite 构建通过。
- `node e2e/envcheck-management-smoke.mjs`：13 场景通过，涵盖依赖按钮/阻止重复点击/手动命令不执行/PATH确认及取消/即时完成/首次快照失败/失败不伪成功/四工具卸载/卸载首拉失败及即时终态。
- 最新三项联跑日志：`/Users/run.mao/.pi/agent/amagi/data/run-bg/bg-9cc45b4a.log`，exit 0。
- 过程记录：初稿 smoke 第5项失败（bg-39faf13e）；修订13场景+324测试通过（bg-c0cac497）；Leader加固后曾触发 TypeScript 的 operation:null 与生成类型不匹配（bg-8b762e37），修正为可选字段 undefined 后完整联跑通过。未修改生成绑定。

### Go 集成：已通过
- `go test -mod=vendor ./... -count=1`：全仓通过。
- `go test -mod=vendor -race ./internal/envcheck ./internal/platform -count=1`：两包通过。
- `go vet -mod=vendor ./...`：通过。
- `GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go test -mod=vendor -exec /usr/bin/true ./internal/envcheck ./internal/platform .`：Windows目标编译通过，**未运行Windows测试二进制**。
- 四项顺序联跑：bg-dbfa105f，exit 0，85秒；日志 `/Users/run.mao/.pi/agent/amagi/data/run-bg/bg-dbfa105f.log`。
- 首次全仓（bg-291398ac）发现Leader新增shim单测把历史包名fixture与现行包常量混用，3个正向断言失败；修正测试输入包名一致性后，定向测试和上述全量验证通过。

## 实测边界
- UI 测试使用真实 Vue 页面与浏览器，Wails 绑定为故障可注入替身；不是实际 OS 安装/卸载测试。
- 未执行真实全局安装、卸载、更新、系统 PATH/用户 shell 修改；测试仅临时目录与注入 runner。
- Windows 注册表/winget/PowerShell 的真实行为未在本机运行，只作逻辑夹具与交叉编译；应在 Windows 实机补冒烟。
- Homebrew未知formula/自定义安装/另一Node前缀拒绝自动卸载；Pi无已知Homebrew映射，按拒绝处理。
- Headroom缺Python仍提供原因/手动指引，本次未新建Python安装器；WSL既有链路仅回归，不宣称已深挖所有环境组合。
- E4首审两项Major均经红绿测试关闭；唯一一次增量复审PASS，未遗留阻断finding。接受的是最终主树整合成果，不是未经Leader补修的旧worktree快照。

## E4 首审修复与最终回归
- 初审报告保留为 `review-initial-report.md`（5f1d1dc4，报告已接受，不代表实现通过）。
- R-1 已运行复现：新增 `uninstall_probe_failure_test.go` 在四个Agent×前检已损坏/卸载后损坏共8条路径复现误报，bg-d4910beb失败。修复四个checker的npm fallback：候选存在但版本探测失败仍保留Installed=true、路径和错误，不再改成missing。卸载额外通过Lstat确认原入口不存在；空路径/权限或I/O错误/残留入口不作为成功证据，保留命令原错。新增确认不存在的单测。未增加绑定字段。
- R-2 已在真实组件复现：新增旧成功槽/其他工具槽持续替换至超时的两条e2e，bg-f6dbda60出现实际「Pi 卸载成功」覆盖unknown断言失败。移除组件watcher的任意槽终态兜底，异步横幅仅由匹配的tracker事件发布；watcher仅维护轮询。两条新用例加时间推进只在隔离夹具里，不缩短生产预算。
- 修复后定向Go、前端327测试/build、**15条**真实组件smoke全部通过（bg-a7d929e4，exit 0）。
- 最终完整Go联跑：**bg-703550de，exit 0，91秒**。全仓 `go test -mod=vendor ./... -count=1`、envcheck/platform `-race`、全仓vet、Windows目标三包编译全部通过（Windows二进制仍未运行）。日志 `/Users/run.mao/.pi/agent/amagi/data/run-bg/bg-703550de.log`。
- 顺带将导出面反射检查改为 `reflect.TypeOf((*Service)(nil))`，覆盖指针接收者方法，而非值类型方法集。
