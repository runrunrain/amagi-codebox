# 验证记录

## 初始基线（HEAD 58e7577）

工作目录 `/Users/run.mao/workpace/amagi-codebox`；宿主 darwin/arm64；Go 1.27.2、Node 24.21.0（与 CI pin / manifest engines 不同，保留环境差异；当前 PATH 只发现该 Node 安装）。

|命令|退出码|结论|完整输出|
|---|---|---|---|
|`go test ./... -count=1`|1|FAIL；根包、envcheck、envvars、platform、wslsupply 失败，具体分类待诊断|baseline-go.log|
|`npm --prefix frontend test`|0|30 files / 271 tests PASS|baseline-frontend.log|
|`npm --prefix frontend run build`|0|vue-tsc + vite build PASS|baseline-build.log|
|`GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go test ./... -exec=true -run '^$'`|0|仅 Windows 测试二进制交叉编译 PASS，**未运行任何 Windows 测试**|baseline-windows-compile.log|
|`go vet ./...`|0|PASS|baseline-vet.log|

### 初始失败分类（未修复前，非最终结论）

- 根包：Pi 登录 planner / remote Pi/OpenCode planner 返回 BuildFailure；桌面 Pi 登录测试被 NAT 代理门提前截断。可能涉及宿主代理环境未隔离，已交运行链审计。
- envvars：SetPathAllow 测试期待跨平台大小写不敏感，与 keysEqual 平台规则冲突，需验证不是通过破坏原生语义来修测试。
- platform：WSLFileMTime 在非 Windows 提前返回，注入 runner 的测试不可达；另原生 Codex 解析被真实用户工具路径干扰。
- wslsupply：取消 context 与缺失 wsl.exe 的错误优先级；skills symlink 排除/越界检测失败，需区分平台路径规范化和安全逻辑问题。
- envcheck：原生 Claude 安装/解析测试受到宿主安装探测影响；暂未归因于本轮历史 WSL 升级，范围外风险单列。

### 验证边界

- 未连接 Windows/WSL 真机；没有实际消费五 CLI 配置、调用 Windows 工具或完成 Windows 工程读改/构建。
- 历史报告的真机结果仅为历史证据，不计本轮 PASS。
- 前端单元测试不等同于设置面板/终端浏览器交互验收。

## 集成中记录（不是最终通过）

- root WSL 四组基线：仅移除HTTP(S)/ALL_PROXY仍失败；进一步移除继承的 `AMAGI_CODEBOX_AI_PROXY` 后通过（`baseline-root-no-marker.log`）。C1修测试隔离，C5修产品停用时残留内部marker。
- C5稳定批次已集成，Leader复跑网络门/身份阻断目标测试PASS（`integrated-routing-tests.log`）。
- C3稳定批次已集成，Leader复跑选择键、Codex策略过滤、skill别名/fixture和Markdown资源回归PASS（`integrated-adapters-tests.log`）。C3对plugins_test的跳过hunk**未采纳**，由C4在产品runner取消前短路解决，不掩盖跨平台红灯。
- C1补充轮已集成，Leader复跑cwd/env与真实Authority发布链回归PASS（`integrated-runtime-tests.log`）。Leader补 `resolvedDesktopSession`：激活时用最终spec cwd更新Session投影；桌面列表与remote快照都不再保留预留时宿主回退目录。
- C4原回流尚未接受：显式空filters、插件跨代保存/删除、override使用缓存代际三处仍未闭合，已新任务acd47281在原隔离树补齐；不能将原报告的“完成”计为总验收。
- 新增Windows CI执行回归步骤（供给域及选定启动域），仍保留全仓编译检查。当前未提交/推送，也未触发CI，因此只记配置变更，不记Windows运行PASS。
- 真机步骤在 `windows-validation.md`，全部待执行。

## C1—C5集成完成后的实际验证（阶段审核中）

|检查|结果|证据|
|---|---|---|
|root/wslsupply/launcher/piplugin全包普通测试|PASS|integrated-core-plugins-tests.log|
|全仓Go（移除继承的内部代理marker）|FAIL，仅剩与基线相同的envcheck 8项、platform Codex发现路径1项；root与WSL相关包通过|integrated-go-final.log；baseline-go.log可逐名比对|
|frontend Vitest|PASS，31 files / 277 tests（基线271，新增6）|integrated-frontend-tests.log|
|frontend vue-tsc + build|PASS|integrated-frontend-build.log|
|go vet ./...|PASS|integrated-vet.log（空输出exit 0）|
|GOOS=windows GOARCH=amd64全仓测试交叉编译|PASS；使用-exec /usr/bin/true，**没有执行Windows测试**|integrated-windows-compile.log|
|go test -race . ./internal/wslsupply ./internal/launcher ./internal/piplugin|FAIL；launcher/piplugin通过，root测试异步push/global-probe竞争，wslsupply状态读写竞争与fakeProbes切片竞争|integrated-race.log，已交diting审查|
|git diff --check|PASS|Leader实际执行，无输出|
|Windows真实CLI/权限/交互|未执行|windows-validation.md|

集成细节：C4三处补全已回流；C2/C3 SelectedKeys与generation状态段并存，C2/C4 PluginFilters JSON三态段并存。C4 import hunk与C3新增time导入冲突由Leader人工合并。Leader接入shared gate到桌面统一activation及remote/restart PTY effect，并新增五CLI两入口阻断/安全放行测试（launch_wsl_shared_test.go）。历史集成首跑的测试常量拼写/缺失import编译错误已修，最终目标测试与vet/交叉编译通过；保留早期日志但不以旧日志替代最终结果。

## 唯一合并修复批次集成后的最终命令结果

- `review-fixes.delta.patch` 已先check再集成；`git diff --check` PASS。无commit/push，未改wailsjs。
- `go test . ./internal/wslsupply ./internal/launcher ./internal/piplugin -count=1`：exit 0，4包PASS（final-target-tests.log）。
- 同目标 `-race`：exit 0，4包PASS（final-target-race.log）。**此前真实生产state race与测试桩race已不再复现**，不将早期integrated-race.log的FAIL擦除。
- `go vet ./...`：exit 0（final-vet.log）。
- `GOOS=windows GOARCH=amd64 go test ./... -run '^$' -exec /usr/bin/true`：exit 0（final-windows-compile.log），仅交叉编译。
- `env -u AMAGI_CODEBOX_AI_PROXY go test ./... -count=1`：exit 1；主树最终仍为基线同名9项失败：envcheck 8项 + platform Codex路径发现1项（final-go-all.log）。实施worktree报告仅1项，属于宿主/路径可见性差异，**主树结果优先，不能改写为全仓通过**。
- 前端本次delta无改动，沿用已集成版本31 files / 277 tests + build PASS（integrated-frontend-*）。
- PR10：可执行verify-staging-window.ps1已撤回；UNSAFE-RETIRED.txt仅禁执行审计证据，不作为验证工具。没有实际Windows/WSL权限窗口/CLI消费验证。
- 独立增量复审27a1c590已完成并接受其结论：**FIX，PR01/02/03/05/07仍5 Major**；PR04/06/08/09/10关闭。见phase-rereview.md。本节普通测试/race通过不代替独立安全验收；实现任务9ce04747完整完成声明已拒绝，停止自动循环。总体收口见closure.md。

## 主上授权继续后的续行验证

以下为新阶段，不抹除上节历史FIX。

- 测试隔离84f8a67d已集成且接受，仅3个测试文件：主树保留真实FNM环境，`env -u AMAGI_CODEBOX_AI_PROXY go test ./... -count=1` 首次exit0（hermetic-integrated-full-go.log）。9项宿主污染基线失败关闭；其它宿主泄漏风险见hermetic-implementation.md，不声称所有机器均彻底hermetic。
- G0原delta与Windows DACL修正delta已按序check/apply。Leader发现sd.DACL第二返回是defaulted非present，03b82df8修正；新增8个Windows专项用例仅编译，未执行。
- 主树G0集成后全仓Go测试exit0，platform/launcher/wslsupply目标race exit0，Windows两包测试交叉编译exit0（g0-integrated-full-go.log、g0-integrated-race.log、g0-integrated-windows-compile.log）。
- Leader直接做小范围规范修正：stat Context生产入口按build tags拆分，消除新增runtime.GOOS分派，公共API与行为不变。净增量attack-platform-buildtags.delta.patch保留、反向--check通过；目标race与Windows交叉编译再次exit0（g0-buildtags-race.log、g0-buildtags-windows-compile.log）。该主树独立修正不扩G1写面。
- 长期SSH入口：主上选择无口令专用Ed25519；私钥只在本机~/.ssh/amagi_winpc_ed25519（600），未打印/入库；服务器指纹由主上在Windows独立核对。持久客户端~/.local/bin/winpc-ssh（隔离Paramiko）仅使用指定key+专用known_hosts，禁止自动信任/其它凭据回退。quick初审两Major已修，唯一增量复审7c6a52a0接受PASS，独立离线5/5。
- 主上手动追加Windows管理员共享授权文件并收紧ACL后，Leader执行 `~/.local/bin/winpc-ssh exec 'echo CONNECTION_OK' --timeout 20`：exit0，真实输出CONNECTION_OK（debug winpc-key-login）。仅证明SSH认证与命令入口可用，不代表任何产品Windows/WSL用例通过。
- G1事务/恢复/legacy与App接线仍在实施，产品五Major未重新裁定关闭；最终集成独立审核与Windows/WSL验证仍待完成。
