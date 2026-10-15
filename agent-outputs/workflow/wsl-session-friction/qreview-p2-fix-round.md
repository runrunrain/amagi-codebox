# qreview 增量复审：M-1 修复区（凭据双向同步重写）

- 档位：quick（任务书指定），范围仅限修复轮重写区；其余批次二 diff 不重审。
- 方法说明：本节点工具面无 bash/git——`git diff HEAD` 未执行、测试未复跑。改动范围以修复轮清单为索引逐文件读取工作区现态核验（wsl_pi_login_sync.go 全文 + 测试全文、platform/wsl.go WSLFileMTime 与白名单、pi_login_launch.go 载荷、app.go 闸门、e2e、docs）；修复轮报告声明的测试运行结果采信原文并与测试代码静态断言互证（盲区见末节）。
- 上游输入：qreview-p2-diff.md、p2t2-report.md（含修复轮章节，均已全文读取）。

## 增量结论（原编号）

| 原编号 | 结论 | 依据 |
|---|---|---|
| M-1 | **关闭** | 双向判定落地：内容 SHA-256 一致→Fresh（wsl_pi_login_sync.go:135-137）；distro 缺失→前向播种；两侧都在且不同→mtime 三分支（:154/:160/:167，容差 2s 对称）；distro 新→DistroNewer 回同步且 distro 字节零触碰；并列→DistroTieKept 取 distro；探测失败（ok=false）→两侧零写入按 DistroHasCopy 降级。writeWindowsAuthAtomic chmod 先于 Rename（比 distro 侧写法更强，天然无宽松落盘窗口），三失败分支均清 tmp。测试 TestSyncWSLPiLoginAuthBidirectional 三子测断言「distro 字节保全/并列收敛 distro/探测失败两侧不动」——正是 M-1 回归钉。 |
| m-2 | 关闭 | WriteFile 失败分支补 `_ = os.Remove(tmp)`（:188）；测试「write failure with old copy intact degrades」断言 tmp 路径不存在（修复前残留目录会使其失败）。 |
| m-3 | 关闭 | chmod+清理双失败→errWSLAuthLooseLeftover 哨兵→classifyWriteFailure 直判 DistroNoAuth（:120-127, :232-235）；测试注入双失败断言哨兵 + DistroNoAuth + 宽松文件确在盘上（证明分类不依赖 stat）。 |
| m-4 | 关闭 | wslNetworkingModeCache 已删（grep 零命中），WSLNetworkingMode 每次直读；TestWSLNetworkingModeFreshRead 断言改文件后同进程读到新值。 |
| m-5 | 关闭 | wslENVForwardExact 含 ALL_PROXY/all_proxy（wsl.go:407 区）；TestShouldForwardToWSL forward 列表含两键正向断言。 |
| m-6 | 关闭 | isLinuxStylePath `"~"`→true + conPtyWorkDirFor「wsl bare tilde」表测（wsl_workdir_test.go:24,51）。 |

## Findings（本轮新增）

### q2-1｜Minor｜pi_login_launch.go:198｜remote 载荷 `-nt` 判定无 2s 容差，与 Go 侧 tie→distro 策略在 (0,2s] src-newer 窗口反向分叉
- 触发条件：remote/v1 启动（payload 独立生效，无 Go 闸门先行）时，Windows 侧 auth.json mtime 高于 distro 侧 0 < Δ ≤ 2s 且两侧内容不同（Go 侧 wsl_pi_login_sync.go:154 要求 Δ > 2s 才前向、:167 default 分支此状态取 distro 回同步；payload `[ src -nt dst ]` 只要严格新即为真→前向 cp 覆盖 distro）。次级场景：桌面闸门探测失败降级（两侧不同、Go 刻意不动）后，同一 spec 的 payload 在会话内仍会强制择向。
- 实际影响：2 秒歧义带内同一状态两通道结论相反——payload 走 Windows 胜出，恰好违背「并列/容差内保守取 distro（会话即将消费侧）」的裁定语义；最坏形态为带内 M-1 同类危害（distro 轮转出的唯一有效 token 被旧副本覆盖，Windows 侧保留旧副本、用户须重登）。带窗极窄（需近同时分叉写入或恢复写偏移 mtime）且带内新陈本不可判，故定 Minor。另：docs/developer/pi-login-providers.md:31「两条链路消费同一载荷语义」与修复轮报告「与 Go 侧并列语义同向（含精确并列）」仅对**精确并列**成立，对 0<Δ≤2s 不成立——契约表述需修正或实现对齐。
- 证据：静态路径证实（两侧判定逻辑逐行比对）；pi_login_wsl_test.go:110-115 钉定的正是当前无容差行为（修复时需同步改断言）。未真机复现（需构造 2s 内分叉写）。
- 最小修复方向：payload 以 `stat -c %Y` 秒差复刻容差（如 `s-d > 2` 才前向，否则反向），或 Leader 裁定接受分叉并修正文档/报告的「同向/同一语义」表述。

## 覆盖与正面核验（对应任务书关注点 1-4）

1. **回同步安全**：writeWindowsAuthAtomic（:207-227）MkdirAll→tmp 0600→**Chmod tmp→Rename**——chmod 失败在替换前拦截，天然不存在 distro 侧写法的 rename-后-宽松窗口；三失败分支均清 tmp；Windows 侧 Rename 语义为替换式。路径守卫：agentDir 来自 piconfig.StandardAgentDir（canonical，非用户输入），回写失败不碰 distro 侧（:161-165 直接 DistroHasCopy 降级）。errWSLAuthLooseLeftover 只可能出自前向路径，反向无该面。
2. **判定完备**：switch 两 case 严格 `> 2s`，default ⇔ |Δ| ≤ 2s——FAT/9P 精度带内内容不同必落 DistroTieKept（distro 胜出）✓；探测失败分支位于任何写入之前，Windows 侧 Stat 失败更早 fail-closed，测试「mtime probe unavailable keeps both sides」断言两侧字节不变 ✓。WSLFileMTime（wsl.go:254-268）非 Windows/空 distro/相对路径/退出非零/不可解析/负值均 ok=false，调用方不当作 0 ✓；单引号转义（wslShSingleQuote）与 `sh -lc` 前景输出混入（profile 噪音致 ParseInt 失败）均保守降向 false ✓。
3. **载荷双向**（除 q2-1 外）：内容一致→cmp 即中零拷贝；distro 缺失→`[ ! -e dst ]` 前向播种；src 缺失→`[ -e src ]` 守卫整体跳过且 if 无 else 退出码 0（不误触 echo）✓；反向 `cp -- dst src` 覆盖已有文件保留 ACL ✓；回写后内容收敛使下次 cmp 命中，无 ping-pong（前向 cp 亦更新 dst mtime 为 now，方向单调）✓。
4. **回归确认**：见上表六项，全部「测试存在且断言行为」核验（非仅存在性）。

## 建议区（不构成 finding）

- writeWindowsAuthAtomic 相对 WritePiAgentConfig（pi_config.go:285）少两处加固：目录显式 Chmod 0700（升级旧 0755 目录）与 Rename 后 Chmod（「某些文件系统 Rename 不保留权限位」）。该写法仅可达于 Windows 宿主（BootstrapWSL 为 Windows-only 面），NTFS 下 Chmod 仅映射只读位、本地盘 Rename 属性随文件走——可达平台上无失败行为，故不入 findings；报告「与 WritePiAgentConfig 同构」表述略强，建议补齐或改述。
- payload 前向路径 cp 成功而 chmod 600 失败时 echo「继续使用 WSL 内既有副本」——此时 distro 侧已被替换为新字节（仅权限未收紧），文案与实际状态不符（remote 链路 best-effort 设计内，仅文案精度）。
- e2e 事故披露（修复轮首跑旧 e2e 致 Windows 侧真实 auth.json 被 distro 字节覆写、未恢复）与代码现状互证：新 e2e 双侧快照恢复 + 收敛断言 + 二次幂等均已落地（wsl_pi_login_sync_e2e_windows_test.go），披露诚实、复发面已闭合。

## 盲区与验证可信度

- 无 bash/git 工具面：未执行 `git diff HEAD`、未复跑任何测试；运行结果采信修复轮报告（其列明真实命令），与测试代码静态断言互证。改动清单完整性以报告自述为索引核对，未做逐字节 diff。
- q2-1 的 2s 分叉为静态构造，未真机复现；pi 在 WSL 内轮转 auth.json 的真实发生率仍未取证（M-1 已按方向对称修复，该前提不再影响结论方向，只影响风险量级）。
- mtime 说谎（恢复写/复制保 mtime）的固有残余风险两侧同在，修复轮已披露并记入风险，本轮不重复立项。

VERDICT: PASS_WITH_MINOR
