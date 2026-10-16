# P3-S3 quick 审核（谛听 · 独立检验）

> 范围：S3 新增面（扫描器/静态兼容检查/技能单元/目录级 staging 提交原语/条件撤销/状态页第五面+wailsjs）。基线 HEAD=bd93f4c，工作区未提交。
> 验证方式声明：本审会话无 bash/codemode 工具面——未运行 `git diff HEAD`、未复跑测试、未做真机复现；全部结论为静态代码路径分析（完整读取 skills_scan.go / skills_submit.go / skills.go / coordinator.go / compensation.go / probes.go / state.go / types.go / app_wsl_supply.go / 全部 S3 测试 / 前端 api+测试+wailsjs+Vue 技能区），并以 Go 1.23+ Windows readdir 位语义与 s3-report 自述交叉印证。「remote effect 未触碰」为结构证据级（internal/launchplan、launch_executor 全量 grep 无任何 skills 引用；S3 文件清单与报告 §1 一致）。

## Findings

### F1 | Major | internal/wslsupply/skills_scan.go:249（同构：skills.go:233 scanTargetSkillDir、skills_submit.go:86 snapshotSkillPreimage）
- **触发条件**：宿主技能树内存在目录形态 reparse point——NTFS junction（`mklink /J`）或目录符号链接（`mklink /D`）。Go 1.23+（本项目 go 1.25）Windows readdir 的 `DirEntry.Type()` 对这类条目同时置 `ModeDir|ModeSymlink`；三处 walk 的 switch 均把 `case e.IsDir():` 排在 `case e.Type()&os.ModeSymlink != 0` 之前 → 目录链接被当作普通目录递归下降。
- **实际影响**：junction 指向的**树外任意内容**（可指向用户任意目录）被静默哈希入清单、判 Portable、经用户同意后整树供给进 distro。直接违背本文件头注释与设计 §四的「越界链接不复制」「指向目录的链接 → directory-link-unsupported」契约（`directory-link-unsupported` 谓词在 Windows 宿主上对目录形态链接实际不可达，是死分支）。规模仍受条目数/单文件上限约束，循环 junction 受深度 8 截断。
- **证据**：静态路径分析（switch 顺序 + Go 1.23 起 junction/dir-symlink 在 readdir 类型位中携带 ModeSymlink）；s3-report §5 自证「LX symlink → ModeIrregular」佐证位语义敏感。测试测不出：`os.Symlink` 在 Windows 创建文件形态链接（无 FILE_ATTRIBUTE_DIRECTORY），skills_scan_test.go 的 `assets-link` 恰好落入 scanLink 分支，掩盖了目录形态路径。
- **最小修复方向**：三处 walk 将 ModeSymlink 判定提到 IsDir 之前（目录链接 → `directory-link-unsupported`，或解析后套用越界谓词）；补 junction 形态测试（fixture 经 `cmd /c mklink /J` 或注入 fake DirEntry 类型位）。

### F2 | Major | internal/wslsupply/skills_submit.go:442,458-462 restoreSkillPreimage
- **触发条件**：技能单元 Compensate（CAS 命中、prevSnapshot=true）→ `RemoveAll(h.uncPath)` 成功后 `os.Rename(staging, h.uncPath)` 瞬时失败（9P 抖动等）。
- **实际影响**：`defer os.RemoveAll(staging)` 无条件执行，把唯一持久前像删掉，而错误串却声称 "preimage kept in staging %s"——撤销原语在自身失败路径上销毁它声称保留的备份；用户原内容两侧持久副本俱失（仅剩进程内 prevFiles，无重试出口）。当前生产触发面只有 launch_executor.go:928 的 Abort（技能单元未入任何启动链，故今日仅测试可达），但 Compensate 正是本批交付的「目录级条件撤销」原语，契约面已暴露。
- **证据**：静态路径分析（defer 无条件执行 + 同函数 rename 失败分支）；TestSkillCompensationRestoresOrDeletes 只覆盖 rename 成功路径。
- **最小修复方向**：rename 失败分支跳过 staging 清理（成功才清/置标志）；顺带清理未使用的 currentHash 入参。附带见 F7。

### F3 | Minor | skills_submit.go:4-13 文件头注释过期
头注释仍描述旧时序「chmod 批量补偿 → rename 交换」并断言「chmod 失败发生在交换前——直接清理 staging，目标不受影响」；实际代码为 rename → chmod → 读回校验，chmod 失败走 restorePrev 恢复前像（报告 §5 记载的真机时序修复未同步头注释）。误导后续维护者。

### F4 | Minor | skills_scan.go scanLink（~L283）
链接条目不套用排除名单（skillsExcludedFileNames/skillsExcludedFileExts）：名为 `.env`/`id_rsa`/`*.pem` 的树内链接被实体化供给，绕开「凭据类文件不携带」名单。因链接目标文件以其本名（未命中名单）本已复制，无新增内容泄漏，属名单一致性缺口。修复：scanLink 入口先做同名/同扩展排除。

### F5 | Minor | skills_submit.go:60-79 readSkillCandidateFiles
Apply 把整个 skill 全量读入内存：仅单文件 16MB 上限、无总量上限（理论上限 4000×16MB）。扫描端流式（读后即弃）、前像端 8MB 封顶，唯独候选读取无界——大技能触发大额瞬时分配乃至 OOM（失败发生在 staging 写之前，无半提交/损坏）。修复：扫描期对 TotalBytes 设总量上限（超限判不可移植）或提交期流式分文件写。

### F6 | Minor | coordinator.go previewSkillUnit（~L640）
预览路径绕过 Plan 的同意门：未同意技能在 PreviewUnits 中 Action=ActionWriteProjection（"write"），而 Plan 对同一状态报 blocked "awaiting-consent"——同一单元两个答案。只读面（写入侧同意门有效；「尚未选择导入」附注部分缓解），状态一致性缺口。修复：previewSkillUnit 镜像 Plan 的 awaiting-consent 前置分支。

### F7 | Minor | skills_submit.go:436-461 restoreSkillPreimage
恢复写不经 distro chmod 补偿、亦无读回校验：submit 路径因「9P 不落 POSIX 位」专门做 chmod 补偿，恢复路径却信任 9P 默认位（可能 0777/含执行位），与既定 0644/0755 发布权限不一致；恢复后实际 mode 无真机验证。

## 观察项（不计 finding）
- 源 skill 内空目录不保留（清单仅文件条目）——供给保真度边界。
- preview/plan 对目标树做全量 9P 读回 hash（逐 skill 全树），用户触发且有界，性能观察。
- 扫描超限真实退出（标记 Incomplete、Plan 恒 blocked、状态面独立徽标）；启动链单元清单结构上不含技能单元；`GetWSLSkillsSupplyStatus` 非阻塞——「大目录不阻塞启动」成立。
- 六分支判定（unmanaged/手改冻结/删除冻结/双侧同变/跳过/增量）与 CAS（ExpectedTargetHash 双重复核）、树 hash 真比对（`got.TreeHash != expectedTreeHash`）、chmod 时序（交换后执行）核实通过。
- 测试断言真实性：增量重写（beta mtime 逐字节不变断言）、both-changed 冻结（Detail+内容保留）、幂等二跑（mtime 不变）均为真断言，非冒烟。
- 真机冒烟证据链与报告互洽（e2e 代码与引用日志一致；chmod 时序修复与代码一致）。
- 补偿后登记基准不回滚（下次按冻结保护）为报告 §5 已声明的有意语义，测试钉住。

## 结论
VERDICT: FIX（F1/F2 Major；F3-F7 Minor）
