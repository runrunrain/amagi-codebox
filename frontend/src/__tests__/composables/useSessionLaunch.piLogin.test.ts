import { describe, it, expect } from 'vitest';
import { useSessionLaunch } from '../../composables/useSessionLaunch';
import { useDashboardState } from '../../composables/useDashboardState';

type LaunchStateInput = Parameters<ReturnType<typeof useSessionLaunch>['canLaunchFromSettings']>[0];

/** 构造最小 dashState 形状（与 useDashboardState state 字段对齐） */
function piState(overrides: Partial<ReturnType<typeof useDashboardState>['state']> = {}): LaunchStateInput {
  const { state } = useDashboardState();
  Object.assign(state, {
    engine: 'pi',
    piProvider: 'pi-login/b3BlbmFpLWNvZGV4',
    piModel: 'pi-login/b3BlbmFpLWNvZGV4/RGFpbHk',
    piLoginStale: false,
  }, overrides);
  return state as unknown as LaunchStateInput;
}

describe('useSessionLaunch Pi 登录失效闸门（R4）', () => {
  it('canLaunchFromSettings：登录选择完整时可启动，stale 时拒绝', () => {
    const { canLaunchFromSettings } = useSessionLaunch();
    expect(canLaunchFromSettings(piState())).toBe(true);
    expect(canLaunchFromSettings(piState({ piLoginStale: true }))).toBe(false);
  });

  it('canLaunchFromSettings：公共 Pi 选择不受 stale 标记影响（默认 false）', () => {
    const { canLaunchFromSettings } = useSessionLaunch();
    const state = piState({ piProvider: 'openai-codex', piModel: 'openai-codex/API Daily', piLoginStale: false });
    expect(canLaunchFromSettings(state)).toBe(true);
  });
});
