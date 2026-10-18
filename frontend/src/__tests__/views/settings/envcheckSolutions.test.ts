/**
 * 环境检测 solution 执行决策回归测试。
 * 镜像 frontend/src/views/settings/envcheckSolutions.ts（决策层从
 * EnvCheckSettings.vue 抽出，组件层不可在 node 环境挂载，根因行为以纯决策层锁定）。
 *
 * 覆盖 20261017 E2 修复的前端根因：
 *  1. restart_app 透传后端必报 unsupported fix action → 必须本地提示；
 *  2. install_claude_method 经 RunEnvFixAction 丢 method 且无进度/刷新 → 必须走
 *     StartInstallClaudeWithMethodAsync（claude_method_install 计划携带 method）；
 *  3. 修复成功后不刷新/不轮询 → planPostFixRefresh 成功一律 fetch；
 *  4. fix_path requiresRestart 不提示 → buildFixResultBanner 必须追加重启提示行；
 *  5. E1 共享契约 uninstall_tool：受理 ≠ 成功，终态以 operation(kind=uninstall) 为准。
 */
import { describe, it, expect } from 'vitest';
import {
  planSolutionExecution,
  solutionConfirmMessage,
  solutionLabel,
  operationKindLabel,
  buildFixResultBanner,
  planPostFixRefresh,
  operationCompletionBanner,
  isAgentCLITool,
  visibleTopLevelSolutions,
  installBlockedReason,
  solutionActionKey,
  isWriteSolution,
  type SolutionLike,
} from '../../../views/settings/envcheckSolutions';

const CTX = {
  cardKey: 'pi',
  claudePickerMethod: 'npm',
  claudeStatusInstallMethod: 'native',
};

const sol = (patch: Partial<SolutionLike> & { type: string }): SolutionLike => patch;

describe('planSolutionExecution：solution type → 前端执行策略', () => {
  it('RC1: restart_app 决不透传 RunEnvFixAction（后端白名单不受理，透传必报错）', () => {
    expect(planSolutionExecution(sol({ type: 'restart_app' }), CTX)).toEqual({ kind: 'restart_notice' });
    // 重启提示是本地展示，不属于破坏性操作，不需要确认
    expect(solutionConfirmMessage(sol({ type: 'restart_app' }), 'Pi')).toBeNull();
  });

  it('RC2: install_claude_method 走专用异步安装并携带 method（RunEnvFixAction 三参签名带不了 method）', () => {
    expect(planSolutionExecution(sol({ type: 'install_claude_method' }), CTX)).toEqual({
      kind: 'claude_method_install',
      method: 'npm', // sol.method 缺失时用户选择器值优先于 snapshot 状态
    });
    // solution 显式 method 最优先
    expect(planSolutionExecution(sol({ type: 'install_claude_method', method: 'native' }), CTX)).toEqual({
      kind: 'claude_method_install',
      method: 'native',
    });
    // 选择器与 snapshot 均缺失时回退 native
    expect(
      planSolutionExecution(
        sol({ type: 'install_claude_method' }),
        { cardKey: 'claude_code', claudePickerMethod: '', claudeStatusInstallMethod: '' },
      ),
    ).toEqual({ kind: 'claude_method_install', method: 'native' });
  });

  it('F-1 保持：clean_claude_install 的 method 顺序 = sol.method > snapshot.installMethod > native（选择器不参与）', () => {
    expect(planSolutionExecution(sol({ type: 'clean_claude_install' }), CTX)).toEqual({
      kind: 'clean_claude',
      method: 'native', // snapshot 的 native，而不是选择器的 npm
    });
    expect(planSolutionExecution(sol({ type: 'clean_claude_install', method: 'npm' }), CTX)).toEqual({
      kind: 'clean_claude',
      method: 'npm',
    });
    expect(
      planSolutionExecution(sol({ type: 'clean_claude_install' }), { cardKey: 'claude_code' }),
    ).toEqual({ kind: 'clean_claude', method: 'native' });
  });

  it('install_tool 走异步安装入口（有进度/终态横幅/自动刷新），不落 RunFixAction 同步分支', () => {
    expect(planSolutionExecution(sol({ type: 'install_tool', tool: 'pi' }), CTX)).toEqual({ kind: 'async_install' });
  });

  it('E1 契约：uninstall_tool 映射到专用卸载计划（受理 ≠ 成功）', () => {
    expect(planSolutionExecution(sol({ type: 'uninstall_tool', tool: 'codex' }), CTX)).toEqual({
      kind: 'uninstall_tool',
    });
  });

  it('manual_command/retry 与白名单透传动作各归其位', () => {
    expect(planSolutionExecution(sol({ type: 'manual_command' }), CTX)).toEqual({ kind: 'display_only' });
    expect(planSolutionExecution(sol({ type: 'retry' }), CTX)).toEqual({ kind: 'full_check' });
    expect(planSolutionExecution(sol({ type: 'fix_path' }), CTX)).toEqual({ kind: 'fix_action' });
    expect(planSolutionExecution(sol({ type: 'install_node' }), CTX)).toEqual({ kind: 'fix_action' });
    expect(planSolutionExecution(sol({ type: 'install_wsl_search_tools' }), CTX)).toEqual({ kind: 'fix_action' });
    // 未来后端新增且已白名单的动作透传，不静默吞掉
    expect(planSolutionExecution(sol({ type: 'brand_new_action' }), CTX)).toEqual({ kind: 'fix_action' });
  });

  it('isAgentCLITool：仅四个 Agent CLI 走通用卸载；Claude/Headroom 排除', () => {
    expect(['opencode', 'codex', 'pi', 'omp'].every(isAgentCLITool)).toBe(true);
    expect(isAgentCLITool('claude_code')).toBe(false);
    expect(isAgentCLITool('headroom')).toBe(false);
    expect(isAgentCLITool('')).toBe(false);
  });
});

describe('solutionConfirmMessage：破坏性动作确认文案', () => {
  it('既有确认面保持：fix_path / install_node / install_wsl_search_tools / clean_claude_install', () => {
    expect(solutionConfirmMessage(sol({ type: 'fix_path' }), 'Pi'))!.toContain('PATH');
    expect(solutionConfirmMessage(sol({ type: 'install_node' }), 'Pi'))!.toContain('Node.js');
    expect(solutionConfirmMessage(sol({ type: 'install_wsl_search_tools' }), 'Pi'))!.toContain('ripgrep');
    expect(solutionConfirmMessage(sol({ type: 'clean_claude_install' }), 'Claude Code'))!.toContain('清理');
  });

  it('uninstall_tool 必须确认且声明不删用户配置', () => {
    const msg = solutionConfirmMessage(sol({ type: 'uninstall_tool', tool: 'pi' }), 'Pi');
    expect(msg)!.toContain('卸载 Pi');
    expect(msg)!.toContain('不删除用户配置');
  });

  it('install_claude_method 有确认；requiresConfirm 的其余动作走通用兜底；普通动作不确认', () => {
    expect(solutionConfirmMessage(sol({ type: 'install_claude_method' }), 'Claude Code'))!.toContain('重新安装');
    // 后端在 claude_npm_package_binary_fallback 上挂的 install_tool + RequiresConfirm
    const generic = solutionConfirmMessage(sol({ type: 'install_tool', requiresConfirm: true }), 'Claude Code');
    expect(generic)!.toContain('Claude Code');
    expect(generic)!.toContain('安装工具');
    expect(solutionConfirmMessage(sol({ type: 'install_tool' }), 'Pi')).toBeNull();
    expect(solutionConfirmMessage(sol({ type: 'retry' }), 'Pi')).toBeNull();
    expect(solutionConfirmMessage(sol({ type: 'restart_app' }), 'Pi')).toBeNull();
  });
});

describe('buildFixResultBanner：FixActionResult → 结果横幅', () => {
  it('RC4: requiresRestart=true 时追加条件式重启提示（不宣称运行中无法刷新，E1 会同步刷新当前进程 PATH）', () => {
    const banner = buildFixResultBanner({
      success: true,
      message: '已将 2 个目录添加到 PATH（配置文件: /Users/x/.zprofile）',
      changed: true,
      requiresRestart: true,
      profilePath: '/Users/x/.zprofile',
      backupPath: '/Users/x/.zprofile.amagi-backup-20260101-000000',
      addedPaths: ['/opt/homebrew/bin', '/Users/x/.local/bin'],
      nextSteps: ['已重新检测工具状态；外部终端如仍未生效，请重启终端'],
    });
    expect(banner.type).toBe('success');
    expect(banner.title).toContain('PATH');
    expect(banner.description)!.toContain('若界面仍显示旧 PATH');
    expect(banner.description)!.toContain('重启 CodeBox 后生效');
    expect(banner.description)!.not.toContain('无法');
    expect(banner.description)!.toContain('备份文件');
    expect(banner.description)!.toContain('已加入路径');
    expect(banner.description)!.toContain('重启终端');
  });

  it('失败结果：title 取 error，type=error，不追加重启行', () => {
    const banner = buildFixResultBanner({ success: false, error: 'brew install node 失败: 网络', changed: false });
    expect(banner.type).toBe('error');
    expect(banner.title).toBe('brew install node 失败: 网络');
    expect(banner.description).toBeUndefined();
  });

  it('空结果按失败处理（不抛异常、不误报成功）', () => {
    expect(buildFixResultBanner(null).type).toBe('error');
    expect(buildFixResultBanner(undefined).title).toBe('修复操作失败');
  });
});

describe('planPostFixRefresh：修复成功后的确定性复检（集成批次 Major 3）', () => {
  it('失败不复检（错误横幅即重试入口）', () => {
    expect(planPostFixRefresh({ success: false, error: 'x' })).toEqual({ recheck: 'none' });
  });

  it('成功但 changed=false：拉一次快照（后端分支内已同步重检，无竞态窗口）', () => {
    expect(planPostFixRefresh({ success: true, changed: false })).toEqual({ recheck: 'snapshot' });
  });

  it('成功且 changed=true：确定性全量复检（本端 runEnvCheck），不依赖碰运气轮询', () => {
    expect(planPostFixRefresh({ success: true, changed: true })).toEqual({ recheck: 'full' });
  });
});

describe('operationCompletionBanner：异步操作终态横幅（uninstall 受理≠完成）', () => {
  const nameOf = (tool: string) => ({ pi: 'Pi', codex: 'Codex' } as Record<string, string>)[tool] || tool;

  it('uninstall 成功终态：按 kind=uninstall 生成「卸载成功」，带版本/消息', () => {
    const banner = operationCompletionBanner(
      { id: 'op-1', tool: 'pi', kind: 'uninstall', status: 'succeeded', result: { message: 'removed 1 package' } },
      nameOf,
    );
    expect(banner)!.toEqual({
      title: 'Pi 卸载成功',
      description: 'removed 1 package',
      type: 'success',
    });
  });

  it('uninstall 失败/超时终态：错误优先于 result.error 与 message', () => {
    const failed = operationCompletionBanner(
      { id: 'op-2', tool: 'codex', kind: 'uninstall', status: 'failed', error: 'npm uninstall exited 1', message: '清理中断' },
      nameOf,
    );
    expect(failed)!.toEqual({
      title: 'Codex 卸载失败',
      description: 'npm uninstall exited 1 - 清理中断',
      type: 'error',
    });
    const timeout = operationCompletionBanner(
      { id: 'op-3', tool: 'codex', kind: 'uninstall', status: 'timeout' },
      nameOf,
    );
    expect(timeout)!.toEqual({ title: 'Codex 卸载失败', description: undefined, type: 'error' });
  });

  it('install 终态沿用版本后缀；running/idle/空不是终态，返回 null（不得提前报成功）', () => {
    const ok = operationCompletionBanner(
      { id: 'op-4', tool: 'pi', kind: 'install', status: 'succeeded', result: { version: '1.2.3', message: 'done' } },
      nameOf,
    );
    expect(ok)!.toEqual({ title: 'Pi 安装成功 (v1.2.3)', description: 'done', type: 'success' });
    expect(operationCompletionBanner({ id: 'op-5', tool: 'pi', kind: 'uninstall', status: 'running' }, nameOf)).toBeNull();
    expect(operationCompletionBanner({ id: 'op-6', kind: 'install', status: 'idle' }, nameOf)).toBeNull();
    expect(operationCompletionBanner(null, nameOf)).toBeNull();
  });
});

describe('visibleTopLevelSolutions：顶层 solutions 渲染与去重（Leader dependency 夹具场景）', () => {
  it('夹具场景：issue.solutions 为空、仅顶层有 fix_path/install_node → 两个依赖按钮都必须可见', () => {
    const status = {
      issues: [
        {
          severity: 'error',
          code: 'npm_not_found',
          message: 'npm is required but not available',
          solutions: [],
        },
      ],
      solutions: [
        { type: 'fix_path', description: 'Fix PATH to include node/npm', requiresConfirm: true },
        { type: 'install_node', description: 'Install Node.js' },
      ],
    };
    expect(visibleTopLevelSolutions(status).map((s) => s.type)).toEqual(['fix_path', 'install_node']);
  });

  it('后端双挂场景：issue.solutions 已有同 type 动作 → 顶层重复项被去重（含 tool 缺失副本，靠 cardTool 回退）', () => {
    const status = {
      issues: [
        {
          solutions: [
            { type: 'fix_path', tool: 'pi', requiresConfirm: true },
            { type: 'install_node' },
          ],
        },
      ],
      solutions: [
        { type: 'fix_path', description: 'Fix PATH（顶层副本，无 tool）' },
        { type: 'install_node', description: 'Install Node.js（顶层副本）' },
      ],
    };
    // cardTool 回退：顶层无 tool 的 fix_path 与 issue 内 tool='pi' 的副本同身份
    expect(visibleTopLevelSolutions(status, 'pi')).toEqual([]);
    // 无 cardTool 上下文时按字面身份比较（严格模式，不误合并）
    expect(visibleTopLevelSolutions(status)).toHaveLength(1);
  });

  it('同 type 不同 tool 不误合并；顶层内部同动作去重（Minor：身份含 tool）', () => {
    const status = {
      solutions: [
        { type: 'manual_command', tool: 'pi', command: 'brew install node' },
        { type: 'manual_command', tool: 'omp', command: 'brew install node' },
        { type: 'manual_command', tool: 'pi', command: 'brew install node' },
        { type: 'fix_path' },
        { type: 'fix_path' },
        { type: 'clean_claude_install', method: 'npm' },
        { type: 'clean_claude_install', method: 'native' },
      ],
    };
    // pi+omp 手动命令各一 + fix_path 一份 + 两个不同 method 的清理动作
    expect(visibleTopLevelSolutions(status)).toHaveLength(5);
  });

  it('空状态：无顶层 solutions 或无 status → 返回空数组', () => {
    expect(visibleTopLevelSolutions(null)).toEqual([]);
    expect(visibleTopLevelSolutions(undefined)).toEqual([]);
    expect(visibleTopLevelSolutions({ issues: [], solutions: [] })).toEqual([]);
  });
});

describe('installBlockedReason：canInstall gating 文案', () => {
  it('canInstall=false 且后端给出原因 → 原样展示', () => {
    expect(installBlockedReason({ canInstall: false, installBlockedReason: 'node is not in PATH' })).toBe('node is not in PATH');
  });

  it('canInstall=false 无原因 → 通用兜底文案（按钮仍置灰但用户能读到解释）', () => {
    expect(installBlockedReason({ canInstall: false })).toContain('依赖缺失');
  });

  it('canInstall=true / 字段缺失 / null → 不阻断（避免异常快照误锁按钮）', () => {
    expect(installBlockedReason({ canInstall: true, installBlockedReason: 'x' })).toBe('');
    expect(installBlockedReason({})).toBe('');
    expect(installBlockedReason(null)).toBe('');
  });
});

describe('Major 2：稳定 action 身份与写操作分类', () => {
  it('solutionActionKey 与位置无关：issue 下标 0/2 与顶层同一 solution 同 key', () => {
    const issueSol = { type: 'fix_path', tool: 'pi', method: 'npm', command: '' };
    const topSol = { type: 'fix_path', tool: 'pi', method: 'npm', command: '' };
    expect(solutionActionKey(issueSol, 'pi')).toBe(solutionActionKey(topSol, 'pi'));
    expect(solutionActionKey(issueSol, 'pi')).toBe('pi|fix_path|pi|npm|');
    // method/command/tool 任一不同则不同 key
    expect(solutionActionKey({ ...issueSol, method: 'native' }, 'pi')).not.toBe(solutionActionKey(issueSol, 'pi'));
    expect(solutionActionKey({ ...issueSol, command: 'x' }, 'pi')).not.toBe(solutionActionKey(issueSol, 'pi'));
    expect(solutionActionKey({ ...issueSol, tool: 'omp' }, 'pi')).not.toBe(solutionActionKey(issueSol, 'pi'));
  });

  it('isWriteSolution：仅 manual_command / restart_app 非写，其余为写', () => {
    expect(isWriteSolution('manual_command')).toBe(false);
    expect(isWriteSolution('restart_app')).toBe(false);
    expect(isWriteSolution('fix_path')).toBe(true);
    expect(isWriteSolution('install_node')).toBe(true);
    expect(isWriteSolution('uninstall_tool')).toBe(true);
    expect(isWriteSolution('install_tool')).toBe(true);
  });
});

describe('标签映射（模板渲染输入）', () => {
  it('solutionLabel 覆盖全部后端发出的 type 与 uninstall_tool', () => {
    expect(solutionLabel('fix_path')).toBe('修复 PATH');
    expect(solutionLabel('install_node')).toBe('安装 Node.js');
    expect(solutionLabel('install_wsl_search_tools')).toBe('安装搜索工具');
    expect(solutionLabel('install_claude_method')).toBe('重装 Claude Code');
    expect(solutionLabel('clean_claude_install')).toBe('清理 Claude 安装');
    expect(solutionLabel('restart_app')).toBe('重启应用');
    expect(solutionLabel('manual_command')).toBe('查看手动命令');
    expect(solutionLabel('uninstall_tool')).toBe('卸载');
    expect(solutionLabel('mystery')).toBe('解决方案');
  });

  it('operationKindLabel：install/update/uninstall 中文，未知透传', () => {
    expect(operationKindLabel('install')).toBe('安装');
    expect(operationKindLabel('update')).toBe('更新');
    expect(operationKindLabel('uninstall')).toBe('卸载');
    expect(operationKindLabel('')).toBe('操作');
  });
});
