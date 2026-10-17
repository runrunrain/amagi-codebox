# Windows 真机 wslsupply 剩余失败：逐条判定与修复

## 背景（事实，均已发生）
- 主树（未提交）已集成：resumed-input → G0/correction/buildtags → hermetic → attack-core(G1) → owner → stop-lease → consistency-fixes（2a02a779）+ Leader UNC 分类、intent_store 修正。
- Leader 在 Windows 真机（Windows 宿主进程，非 WSL 内）运行 `internal/wslsupply` 测试二进制：
  - 第 2 轮：85 FAIL，根因 `intent_store.go` 用 `Mode().Perm()==0600` 判私有（Windows 恒 0666）。Leader 已改用 `platform.EnsurePrivateFile`（POSIX 语义不变；Windows 走真实 owner-only DACL，已真机验证），并把 `TestPluginCanceledContextNeverQueuesOrRuns` 无界 `<-held` 改为前置失败即 Fatal。
  - 第 3 轮（当前主树）：224 PASS / 5 FAIL / 7 SKIP（6 个 AMAGI_WSL_E2E 手动用例 + 1 个 dir-symlink 权限 skip）。日志：`agent-outputs/workflow/wsl-goal-closure/windows-wslsupply-real-tests-3.log`（必读）。

## 5 个失败（逐条判定，禁止笼统归类）
1. `TestAttackA2SummaryWriteFailureBlocksSync`（a2_consumption_test.go:120）：pending 目录 chmod 0500 在 Windows 不阻止写入 → 摘要写成功、sync 照常。
2. `TestConsistencyV2MigrationKeepsManagedRecords`（consistency_fixes_test.go:172）：Reason=managed-copy-perms-unverified。
3. `TestSensitiveSubmitUsesVerifiedPrivateStaging`（core_security_regression_test.go:334）：最终文件模式 -rw-rw-rw-。
4. `TestReviewFixPR02UnchangedSensitiveVerifiesPerms/chmod_failure_fails_the_unit_instead_of_passing`（review_fixes_test.go:294）：chmod 失败未令敏感单元失败（Outcome fresh）。
5. `TestScanSkillCompatChecks`（skills_scan_test.go:106）：期望 absolute-link，实际 abs.md 归为 non-regular-entry。

## 要求
对每一条先给出**源码证据链**，判定属于哪类：
- (A) 产品缺陷：Windows 宿主生产路径真实可达（注意生产中目标文件位于 `\\wsl.localhost\...` 9P 共享，权限真相应来自 distro 内探测 `probes.StatMode/Chmod`；若生产代码在任何地方用宿主 `os.Stat().Mode().Perm()` 推断 distro 内权限，或在探测失败时放行，即为缺陷）→ 修生产代码 + 回归测试。
- (B) 测试夹具假设：fake 探测面借用宿主 POSIX 位或 POSIX-only 故障注入，生产不可达 → 修夹具使其在 Windows 也表达同一语义（例如 fake 探测维护显式 mode 表而非读宿主 stat；故障用注入接缝而非 chmod 目录），**断言强度不得降低**。
- 仅当语义在 Windows 宿主上确实不存在时才允许按 GOOS 分支，且须在报告中逐条论证；**禁止**用 skip/放宽断言/删测试把失败修绿。
- 第 4 条尤其要核对：chmod 失败放行是否生产可达（敏感单元失败必须如实失败）。
- 第 5 条：Windows 下绝对路径链接（如 `C:\...` 或 `/abs`）的分类语义——确认扫描器在 Windows 宿主遇到真实 UNC 上的 Linux 绝对链接时的行为，生产语义正确再调夹具，不正确修扫描器。

## 边界
- 写面：`internal/wslsupply/**`；如必须动 `internal/platform` 先在报告说明理由（API 冻结，不得改导出签名）。不动 wailsjs/前端/文档 resume-relay；不 commit；不全仓 gofmt（只 gofmt 你改的文件）。
- 工作区：worktree（基于当前主树未提交状态需自行重建：请以主树 `git diff HEAD` + 未跟踪文件为基线；起步核对与主树逐字节一致并记录）。
- 无 Windows 真机执行权：交付后由 Leader 真机复跑；你须保证 macOS `go test -race ./internal/wslsupply ./internal/platform` exit 0、`go vet`、`GOOS=windows GOARCH=amd64 go test -c ./internal/wslsupply` exit 0。

## 交付
- `agent-outputs/workflow/wsl-goal-closure/windows-wslsupply-fixes.delta.patch`（相对当前主树的净增量，可 `git apply --check`）
- `agent-outputs/workflow/wsl-goal-closure/windows-wslsupply-fixes.md`：5 条逐条 判定(A/B)/证据(文件:行)/修复/回归；验证命令与结果；未覆盖项。
