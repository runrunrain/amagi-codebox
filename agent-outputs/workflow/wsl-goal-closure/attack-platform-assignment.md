# G0 平台提交原语与 A2 消费/取消闭环

task_tier=complex，主特色工匠闭环，轻安全验证。上游attack-contract.md已接受；只实现本切片，不新设计/扩大产品。

## 起步与交付

新worktree `wsl-goal-platform-auth` 初始HEAD58e7577。确认干净后机械 `git apply --check` 再apply主树 `agent-outputs/workflow/wsl-goal-closure/resumed-input.patch`。失败blocked，不回主树写码。保存导入快照；完成导出仅你净增量 `attack-platform.delta.patch` + `attack-platform-implementation.md` 到主树同一报告目录。不commit。测试日志留本树.amagi/attack-platform。

必读 attack-contract.md（尤其文件提交、PR02/PR07 A2、平台接口与矩阵）、phase-rereview.md。你只负责 internal/platform 的下列原语与 internal/launcher A2；core的state/intent/coordinator、App聚合归另一个依赖节点。不得动 `internal/platform/resolver_test.go`（独立测试隔离任务写面）。

## 冻结的公共 API（下游按此编码，改名/改签名须先交Leader）

```go
// internal/platform：旧无ctx函数保留；Context变体必须使用真实exec.CommandContext。
func WSLTargetIdentityContext(ctx context.Context, distro string) WSLUserIdentity
func WSLFileMTimeContext(ctx context.Context, distro, linuxPath string) (int64, bool)
func WSLFileModeContext(ctx context.Context, distro, linuxPath string) (string, bool)
func WSLChmodContext(ctx context.Context, distro, mode string, linuxPaths ...string) error
// 同文件系统、原子不替换目标；文件及目录均支持；不支持就error，不降级覆盖。
func MoveNoReplace(source, destination string) error
// 宿主本地配置/pending目录用途，不用来冒充WSL POSIX权限。
// 建立/收紧并验证当前用户私有目录0700/文件0600语义；拒绝符号链接。
// Windows须真实ACL，不以os.Chmod冒充，仅修改该受管路径不改系统配置。
func EnsurePrivateDir(path string) error
func EnsurePrivateFile(path string) error
// internal/launcher
func SyncWSLPiLoginAuthContext(ctx context.Context, target WSLAgentTarget) (SyncWSLPiLoginAuthOutcome, error)
// WSLAgentTarget保留现有Distro/Home/LinuxDir/UNCDir，新增UID,MachineID string。
```

可复用vendor/golang.org/x/sys：Darwin已有unix.RenamexNp(...RENAME_EXCL)，Linux Renameat2(RENAME_NOREPLACE)，Windows原生不带replace/copy标志的移动。每OS实现用build tags，不新增runtime.GOOS分支。禁止Lstat后普通Rename假冒no-replace、mv -n、WriteFile(O_EXCL)半文件发布。Windows/UNC实际行为必须标未真机验证，不能说交叉编译证明原子性。私有ACL helper要验而非只设置；允许系统/管理员访问的威胁模型明确，不输出SID/ACL中的凭据。

## A2 必须闭合

1. 全部Copied/Fresh/DistroNewer/DistroTieKept/DistroHasCopy出口统一finish消费门，不能在成功回写Windows时跳过。mode600才可消费；unknown/宽权限可收紧复验，仍不可信=>NoAuth。同步cause与权限cause保留；取消不得按存在降为HasCopy。
2. Context入口使用已解析target，不重新用cachedHome选目标；核对当前Identity与传入UID/Home/MachineID。旧SyncWSLPiLoginAuth(distro)保留兼容包装，测试桩可适配但不得保留旧权限旁路。
3. ctx贯通identity/mtime/stat/chmod真实runner和两个方向writer实际rename前；每个同步I/O后再查。候选写入后、rename前校验字节与mode；持续损坏时原auth不变。取消前未提交侧原样；提交后取消如实NoAuth，不回滚新token。A2不做普通旧token备份回滚，不往状态/日志落token或auth hash。
4. 核对wsl_agent_config.go零变化权限/公共staging helper：新增ctx变体供A2复用，其它pi/omp/opencode旧API兼容，不能破坏此前已修路径。
5. G1下游负责A2Adapter.Sync(ctx,target)、pending摘要、App gate聚合；你报告所需接线，不能以没有接线声称全产品PR02/07关闭。

## 验证

- 真原语目标已存在（文件/目录/空目录）绝不替换，source仍在；目标不存在原子移动成功；竞争创建目标失败不丢任何一方，合理文件系统错误不回退危险路径。
- 私有权限建立/验证/拒绝symlink；Windows编译有覆盖，没Windows不得宣称ACL实测。
- A2 5消费结果 ×600/644/unknown/收紧失败/复验失败，特别成功反向/并列回写；各真实ctx seam取消（probe中、候选后、两侧rename前）、持续候选污染。断言文件字节/返回NoAuth而非只helper被调用。
- go test ./internal/platform ./internal/launcher；相关-race、vet、Windows交叉编译。platform Codex宿主污染另有独立任务，可单列，不删/skip断言。所有auth仅临时合成fixture，不跑真机或触用户凭据。
- 不全仓gofmt、不手改wailsjs、不改系统SSH/用户/全局配置。源码只写本worktree；主树只写指定两产物。
