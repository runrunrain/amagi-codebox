# G0 修正轮 — verifyPrivateACL DACL 判据缺陷修复报告

- 任务：G0 交付后 Leader 复审发现的 Windows API 语义错误，在 `wsl-goal-platform-auth` worktree 上追加修复。
- 产物：`attack-platform-correction.delta.patch`（相对已交 G0 成果的纯增量，2 文件，不携带 G0 本体；G0 原 `attack-platform.delta.patch` 未做任何改动）。
- 本报告目录（主树）：`/Users/run.mao/workpace/amagi-codebox/agent-outputs/workflow/wsl-goal-closure/`。
- 过程日志与前像：worktree `.amagi/attack-platform-correction/`。

## 一、缺陷根因

`internal/platform/ensure_private_windows.go` 的 `verifyPrivateACL` 原实现：

```go
dacl, present, err := sd.DACL()
...
if !present || dacl == nil { return ...("private path has no DACL...") }
```

x/sys 契约（`vendor/golang.org/x/sys/windows/security_windows.go:1240` 起）：`SECURITY_DESCRIPTOR.DACL()` 的签名是 `(dacl *ACL, defaulted bool, err error)`——第二返回值是 **defaulted**（SE_DACL_DEFAULTED 标志），不是 present：

- DACL 不在场 → `err = ERROR_OBJECT_NOT_FOUND`；
- 在场但为 NULL（「空 DACL」＝全放行）→ `dacl == nil` 且 `err == nil`。

后果（判据完全反向）：`setPrivateACL` 设置显式 DACL 后读回 `defaulted=false`，`!present` 恒真——**每一条显式合法 ACL 都被拒绝**；反之 default DACL 反而放行。Windows 真机上 `EnsurePrivateDir`/`EnsurePrivateFile` 必失败。

## 二、修正内容（只动该 helper 及必要测试）

`internal/platform/ensure_private_windows.go`：

1. 提取可测 validator `validatePrivateDescriptor(sd, allowed, currentUser)`（包内私有，冻结 publicAPI 零改动——`EnsurePrivateDir`/`EnsurePrivateFile` 签名与行为入口未动）：owner 检查、DACL 判据、逐 ACE 遍历全部收拢于此；`verifyPrivateACL(path)` 缩为「现取允许集合 → `GetNamedSecurityInfo` → 调 validator」。
2. DACL 判据修正为三条，全部 fail closed：
   - **缺失**：`DACL()` 返回 `ERROR_OBJECT_NOT_FOUND`（`errors.Is` 判别）→ 拒绝（`"private path has no DACL"`）；
   - **NULL**：`dacl == nil` 且 `err == nil`（全放行语义）→ 拒绝（`"private path has a NULL DACL granting everyone access"`）；
   - **defaulted**：`defaulted == true`（默认机制产物，非我们设置的显式 DACL）→ 拒绝（`"private path DACL is defaulted, not explicit"`）。
3. owner 必须当前用户、每个 ACE 必须 ACCESS_ALLOWED 且 SID ∈ {当前用户, LOCAL SYSTEM, 内建 Administrators} 的检查**原样保留**（ACE 遍历逻辑逐行未改）。错误信息仍不携带 SID/DACL 内容。

新增 `internal/platform/ensure_private_windows_test.go`（`//go:build windows`，内存构造 SD 判定，不依赖宿主 ACL 形态）：

| 用例 | 覆盖 |
|---|---|
| `TestValidatePrivateDescriptorExplicitDACLAccepted` | **修正直接回归**：显式 defaulted=false 合法 DACL 被接受（旧实现拒绝的正是它） |
| `TestValidatePrivateDescriptorMissingDACLRejected` | 缺失 DACL 拒绝（含 `errors.Is(ERROR_OBJECT_NOT_FOUND)` 链断言） |
| `TestValidatePrivateDescriptorNullDACLRejected` | NULL DACL（在场/nil/err==nil）拒绝 |
| `TestValidatePrivateDescriptorDefaultedDACLRejected` | defaulted DACL 拒绝 |
| `TestValidatePrivateDescriptorOwnerMismatchRejected` | owner 失配拒绝（保留项） |
| `TestValidatePrivateDescriptorUnexpectedPrincipalRejected` | Everyone allow-ACE 主体越界拒绝（保留项） |
| `TestValidatePrivateDescriptorDenyACERejected` | deny-ACE 类型拒绝（保留项） |
| `TestEnsurePrivateWindowsRealACLRoundTrip` | 真实子系统端到端（set→读回→判据），供 Windows 真机/CI 执行 |

## 三、验证证据（真实命令、cwd=worktree、退出码直读）

| 验证 | 命令 | 结果（日志） |
|---|---|---|
| 格式 | `gofmt -l internal/platform/` | 空（干净） |
| mac 目标测试 | `go test ./internal/platform ./internal/launcher -count=1` | exit 1：唯一失败 `TestDarwinResolveExecutableFindsCodexInLocalNodeBin`（`resolver_test.go` 无任何工作区改动，本机确定性失败=phase-rereview §4 记录的基线宿主污染，与本修正无关）；launcher ok（`mac-target-tests.log`） |
| mac 基线隔离 | `go test ./internal/platform -count=1 -skip 'TestDarwinResolveExecutableFindsCodexInLocalNodeBin'` | ok（`mac-platform-minus-baseline.log`） |
| Windows vet | `GOOS=windows GOARCH=amd64 go vet ./internal/platform` | exit 0（`win-vet-platform.log`） |
| Windows 测试编译 | `GOOS=windows GOARCH=amd64 go test ./internal/platform ./internal/launcher -run '^$' -exec /usr/bin/true` | ok×2（测试二进制含新 windows 测试文件完成编译链接；`win-test-compile.log`） |
| Windows 构建 | `GOOS=windows GOARCH=amd64 go build ./internal/...` | exit 0（`win-build-internal.log`）；`./...` 因根包 `//go:embed all:frontend/dist` 缺前端产物失败——worktree 前置条件，与本修正无关 |
| Delta 前向 round-trip | G0 态模拟树（前像文件）`git apply --check` | 通过（修正补丁可干净应用于 G0 后形态，即 G1 导入 G0 delta 后的树） |
| Delta 反向 round-trip | worktree `git apply -R --check attack-platform-correction.delta.patch` | 通过（前像/后像精确） |

## 四、补丁应用与回滚

- **应用**（在已应用 G0 `attack-platform.delta.patch` 的树上）：`git apply attack-platform-correction.delta.patch`（仅 2 文件：`ensure_private_windows.go` 修改 + `ensure_private_windows_test.go` 新增）。
- **回滚**：先 `git apply -R --check attack-platform-correction.delta.patch` 精确验证，通过后 `git apply -R`。分层回滚顺序：先反本修正，再反 G0 delta（G0 delta 的 `-R --check` 已在修正前的 worktree 原状态验证通过）。
- 本轮前像：`.amagi/attack-platform-correction/pre/`（`ensure_private_windows.go` sha256 `af659663…995867b`；`attack-platform-implementation.md` sha256 `f3bf2fcf…827af8bb82`）。

## 五、G0 文档两处纠正（attack-platform-implementation.md，已直接修正于主树）

1. **§二「目标包测试」行**：原文同时写 ok×2 与「platform 唯一失败」；实际 `final-target-tests.log`（该轮）为 ok×2 无失败，失败出现在此前一轮 `target-tests.log`。已改为按日志归属分述（ok×2 该轮无失败；此前一轮曾现基线宿主污染，最终轮未复现）。
2. **§四回滚建议**：原文建议 `checkout -- internal/launcher internal/platform` 整目录回退——会连带回退 `resumed-input.patch` 对这两个目录既有跟踪文件的改动（丢失输入快照）。已改为精确回滚流程（`git apply -R --check` 验证后 `-R`，仅撤销 22 文件净增量）。

纠正以 `attack-platform-implementation.two-spot.patch` 经 `git apply` 应用（worktree `.amagi/attack-platform-correction/` 留档），应用后 `git apply -R --check` 已验证通过（回滚=先 --check 再 -R）。旧成果与历史日志未删除、未改动。

## 六、验证限度与未覆盖项

1. **未在 Windows 真机执行**：全部 Windows 用例（含新增 8 个）仅交叉编译验证，真实 ACL 行为（含修正后 EnsurePrivate* 端到端）仍待 Windows 真机/CI-Windows 运行确认；本报告不以编译等同运行。
2. mac 侧基线项 `TestDarwinResolveExecutableFindsCodexInLocalNodeBin` 在本机失败（宿主污染，独立任务写面，未动 resolver）。
3. 未读任何用户凭据；测试用进程自身 token/Well-known SID，属测试进程身份，非凭据读取。
4. 根包 Windows 全量构建受 `frontend/dist` 缺失前置限制，本轮以 `./internal/...` 覆盖修正爆炸半径。
