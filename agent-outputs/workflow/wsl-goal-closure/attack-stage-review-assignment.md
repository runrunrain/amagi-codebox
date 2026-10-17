# H 攻坚阶段集成独立审核（full）

task_tier=complex，review_mode=diting，full档，主特色对抗检验。主上已明确继续直到目标闭环；这是F/G续行阶段的首次合并审核，不是重跑历史审查。

## 对象与真实状态

主树 /Users/run.mao/workpace/amagi-codebox，HEAD58e7577加既有未提交集成。G0原delta、G0 ACL correction、Leader平台stat入口build-tags微调、hermetic测试隔离、G1 core delta均已集成；不commit。

必读本目录 attack-contract.md、phase-rereview.md、attack-platform-implementation.md、attack-platform-correction.md、attack-core-implementation.md、attack-core-assignment.md；按实际源码与对应delta核验，不把met声明当证据。

主树真实验证：G1 root/wslsupply/launcher/platform/piplugin五包-race exit0；frontend 31文件277测试通过且build已通过；vet及Windows全仓测试编译在给历史audit-only supply-audit-repro.go补齐//go:build ignore后通过（不是删正式测试，原repro正文保留）。首次全仓普通go test另复现既有TestR6_001_ClaudeExternalRunRejectsStopAndUninstallUntilStopSucceeds立即释放lease断言失败，正由baize abbca12e独立只读诊断；不要替Leader归因为flaky，也不要因此掩盖WSL评审。Windows真实用例尚未跑；SSH入口已通但不授权你使用SSH或读取密钥。

## 必须裁定

1. PR01/02/03/05/07逐项closed/open及真实依据；保留已经关闭的PR04/06/08/09/10不回归。G1已承认compensateSkillDir仍是旧staging恢复而非displaced/no-replace：它实际能否从remote Abort/目录resource/其它公开路径触发损坏、覆盖后改或丢唯一前像？不得因为写成partial就略过。
2. 写前intent+前像/候选验证顺序、发布失败事实识别、原子no-replace仅防路径覆盖不等于CAS、外部后改保全、取消后独立有界恢复。持续故障不能只测首次；不先破坏目标再用backup验证失败报错自证。目录/空目录与文件都需覆盖。
3. state save失败首发/第N次、操作墓碑与多操作关联、corrupt/unreadable状态读不覆盖、GetStatus/启动gate/dirty同事实；新Coordinator没有内存Undo也能识别。重点重启reconcile不得误删未确认材料或复活秘密，代码写/删成功但登记失败不能消失成Fresh。
4. 旧版本证据与目标归属：A发布->B最后检查->升级，未知与clone、Detached/withdrawn/consent-only、逐目标手动absent确认，不错绑/全局误阻/虚假恢复写权限。
5. A2全25消费矩阵真实launcher→adapter→login/desktop/remote/restart接线：成功回写不能跳权限；取消不HasCopy/Applied；pending摘要无secret/hash；optional failure早退不能绕资格。不得把返回NoAuth当作已保全唯一新token事实。
6. 插件applyMu/opMu/state.mu顺序、runner返回后ctx、filters CAS/intent/补偿、首次失败摘要+重启不自动重放；外部原生安装副作用与filters分别记。检查并发plugin×Apply覆盖缺口是否需要确定性反例。
7. 公开入口与UI：五CLI gate、ReleaseCompensations生命周期是否覆盖remote/desktop/manual真实成功路径，而非仅helper调用；pending-op是否真实呈现且有测试，不要求捏造绑定变更。BuildPlan仍只读零spawn，原生回退语义保持。
8. G0 Windows关键API使用：DACL defaulted修正已叠加，no-replace错误/GC生命周期/ACL读回等依据vendor核对；仅编译不能作Windows运行证明。无需再造平台，只指出真实风险与可运行的合成验证清单。

## 执行与产出

只读项目代码；可运行本地现有test/vet（env -u AMAGI_CODEBOX_AI_PROXY）。不改源码/测试来修通过，不读真实auth/私钥，不连SSH，不运行AMAGI_WSL_E2E，不用用户目录做fault fixture。反例只用t.TempDir/受管临时目录和合成数据；如落独立Go反例，报告目录文件带//go:build ignore，不进入全仓构建。必要以临时overlay复现私有路径而不改正式源码。

报告 attack-stage-review.md：一行VERDICT PASS/FIX；Critical/Major/Minor findings（触发条件、实际危害、源码锚点、运行/静态证据区分、建议最小修复）；五PR逐项状态；未执行矩阵/Windows盲区；与已接受attack-contract的偏离。可以引用已有日志但不能冒称自己运行。所有问题合并一次交付，不零碎催改，不自动修复循环。可选反例与日志 attack-stage-review-repro.go、attack-stage-review-tests.log、attack-stage-review-overlay.json、attack-stage-review-repro_test.go（测试文件同样显式专用build tag）。
