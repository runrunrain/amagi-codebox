# attack-platform-owner — 受管文件 owner 规范化缺陷修复（净增量轮）

- 轮次：owner 规范化修复轮（相对已交 G0 + correction 的净增量；不改旧 patch）
- 工作区：`.amagi-worktrees/wsl-goal-platform-auth`（沿用，G0+correction 现场保留）
- 产物：`agent-outputs/workflow/wsl-goal-closure/attack-platform-owner.delta.patch`（318 行，223+/30-）
- 改动面：仅 `internal/platform/ensure_private_windows.go` 与其专项测试 `internal/platform/ensure_private_windows_test.go`

## 上游输入

- `agent-outputs/workflow/wsl-goal-closure/windows-platform-real-tests-v2.log`（必读，已读）
- `agent-outputs/workflow/wsl-goal-closure/windows-preflight.json`（必读，已读：`adminToken=true`，用户 `DESKTOP-NAALPFC\毛润`，Win10 26200）

## 真机缺陷证据（Leader 提供，本轮未 SSH、未读真实用户配置）

v2 日志中：7 个内存 SD 判据测试全过（修正轮判据正确）；`TestEnsurePrivateDirEstablishesAndTightens`（creates/tightens 两个子例）、`TestEnsurePrivateFileEstablishesAndTightens`（creates/tightens 两个子例）、`TestEnsurePrivateWindowsRealACLRoundTrip` 全部失败于同一错误 `private path owner is not the current user (isolation unverified)`；MoveNoReplace 五个真实用例（文件/目录/并发/错误路径）全过。

## 根因

preflight 证实 SSH 进程 `adminToken=true`：提升令牌的默认 TokenOwner 是内建 Administrators 组（Windows 提升语义），因此 `os.MkdirAll`/`os.OpenFile` 新建的对象以**组 SID 为 owner** 落盘。而修正轮的 `setPrivateACL` 只设置 `DACL_SECURITY_INFORMATION | PROTECTED_DACL_SECURITY_INFORMATION`，从不覆写 owner；随后 `verifyPrivateACL` → `validatePrivateDescriptor` 按契约判据（owner 必须是当前用户）fail closed 拒绝。缺陷在**设置侧**（未规范化 owner），不在判据侧——判据与真机行为都是对的。

## 修复（ensure_private_windows.go）

`setPrivateACL` → 重命名 `setPrivateOwnerAndDACL`：同一 `SetNamedSecurityInfo` 调用追加 `OWNER_SECURITY_INFORMATION`，以当前用户 SID（进程身份本就可合法持有的 owner 取值）连同显式 owner-only DACL 一次写入；随后既有 `verifyPrivateACL` 读回复验不变。

边界遵守：

- 不启用 SeTakeOwnership/Restore/Backup 等额外系统特权；无权设置 owner（如目标 owner 他人且无 WRITE_OWNER）时如实返回错误（fail closed），不静默降级。
- 不改全局 ACL/账户/配置；只作用于传入的受管路径自身。
- G0 冻结 API（`EnsurePrivateDir`/`EnsurePrivateFile`/错误变量）零改动；`validatePrivateDescriptor` 判据零改动（未放宽）。
- MkdirAll 中间级目录的 owner 语义维持 G0 现状（判据只针对受管路径本身），未扩范围。

## 测试（ensure_private_windows_test.go，新增 3 项，既有 8 项零弱化）

1. `TestEnsurePrivateWindowsNormalizesOwnerToCurrentUser`（owner 规范化）：先尽力把目标 owner 强制为 Administrators（提升令牌默认 owner 的合法取值）构造真机同形前置，EnsurePrivate 后 owner 必须为当前用户且 `verifyPrivateACL` 通过。提升令牌下前置构造失败 = 环境前提断裂，**t.Fatal 不跳过**；非提升令牌无权置组 owner，退化为断言 owner 恒为当前用户（同一验证路径），非 skip-admin。
2. `TestEnsurePrivateWindowsTightenPreservesFileBytes`（字节不动）：既有内容 + 最严 owner 前置，收紧后字节逐位相等。
3. `TestEnsurePrivateWindowsForeignSecurityStillRejected`（非法主体仍拒绝）：Everyone allow-ACE 仍拒（unexpected principal）；私有 DACL + owner 拨回 Administrators 仍拒（owner mismatch）——钉住修复不得放宽 fail closed。

辅助：`currentTokenElevated`/`forceOwnerTo`/`ownerSIDOf`/`adminsSID`（`ERROR_ACCESS_DENIED`/`ERROR_INVALID_OWNER` 视为前置不可用，其它错误如实失败）。

## 验证证据（darwin 导出机，cwd=工作区）

| 检查 | 命令 | 结果 |
|---|---|---|
| 格式 | `gofmt -l <两文件>` | 无输出（exit 0） |
| darwin vet | `go vet ./internal/platform` | exit 0 |
| darwin 目标包测试 | `go test ./internal/platform -count=1 -run 'EnsurePrivate\|ValidatePrivate\|MoveNoReplace' -v` | PASS（ensure_private + move_no_replace 全过） |
| darwin 全包测试 | `go test ./internal/platform -count=1` | 1 例环境性失败：`TestDarwinResolveExecutableFindsCodexInLocalNodeBin`（PATH 命中 `~/.local/bin/codex` 压过 fixture）——与本轮无关：本轮两文件均 `//go:build windows`，不参与 darwin 编译 |
| Windows 全仓编译 | 临时暂存主树 `frontend/dist`/`mobile/dist` 后 `GOOS=windows GOARCH=amd64 go build ./...` | exit 0；dist 为 gitignore 构建产物，检查后已删除并经 `git status` 核实无残留 |
| Windows 全仓 vet（含测试编译） | `GOOS=windows GOARCH=amd64 go vet ./...` | exit 0 |
| delta patch 往返 | 干净前像 + `git apply attack-platform-owner.delta.patch` | APPLY-OK，产物 SHA256 与工作区一致 |

前像（/tmp/wsl-goal-owner-round-pre，SHA256）：
- `ensure_private_windows.go` pre `fbb413f4…53162c` → post `9ec1a752…a1acd`
- `ensure_private_windows_test.go` pre `5ac2947b…48b49a` → post `85f10551…4099a`

## 未覆盖项 / 风险

- **真实 Windows 复测未执行**（任务边界：Leader 负责）。预期：v2 日志中 4+1 个 owner 失败用例转绿，新增 3 项测试同过；建议命令 `go test ./internal/platform -count=1`。
- owner 覆写在「目标 owner 他人且无 WRITE_OWNER」场景会如实失败（设计行为，fail closed）；真实受管路径均为本进程自建，风险低。
- 非提升环境下测试 1 退化为弱断言（无权构造组 owner 前置），真机为提升令牌、覆盖强断言路径。

## 回滚

`git apply -R attack-platform-owner.delta.patch` 于工作区（或按前像哈希恢复两文件）；无其它文件改动，无 commit。
