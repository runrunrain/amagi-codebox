import { describe, expect, it, vi, beforeEach } from 'vitest';

// controlHoldsModel — 控制权管理卡（卡⑤）纯逻辑层测试。
// 环境为 node（无 DOM，见 vitest.config.ts）：短显/徽标/回退/空态/确认文案
// 的全部分支 + 收回动作的 api 流（mock api/remote，不依赖 wailsjs 运行时）。
import {
  EMPTY_HOLDS_TEXT,
  holdBadge,
  holderDisplayName,
  performRelease,
  releaseConsequence,
  shortSessionID,
  type ControlHold,
} from '../../../../src/components/remote/controlHoldsModel';

// mock 真实 api 模块文件（模型与测试的相对路径均解析到同一文件）。
import * as remoteApi from '../../../../src/api/remote';

vi.mock('../../../../src/api/remote', () => ({
  releaseSessionControl: vi.fn(),
}));

function hold(overrides: Partial<ControlHold> = {}): ControlHold {
  return {
    sessionID: 'sess-abcd1234efgh',
    deviceID: 'dev-1',
    deviceName: '主上手机',
    inGrace: false,
    ...overrides,
  };
}

describe('shortSessionID 会话 ID 短显', () => {
  it('≤8 位原样展示；超长取前 8 位 + …', () => {
    expect(shortSessionID('sess-abcd1234efgh')).toBe('sess-abc…');
    expect(shortSessionID('short-id')).toBe('short-id');
    expect(shortSessionID('')).toBe('—');
  });
});

describe('holdBadge 持有状态徽标', () => {
  it('连接中 → ok；宽限期 → warn', () => {
    expect(holdBadge(hold())).toEqual({ text: '连接中', tone: 'ok' });
    expect(holdBadge(hold({ inGrace: true }))).toEqual({ text: '宽限期', tone: 'warn' });
  });
});

describe('holderDisplayName 显示名回退', () => {
  it('deviceName → deviceID → 未知设备', () => {
    expect(holderDisplayName(hold())).toBe('主上手机');
    expect(holderDisplayName(hold({ deviceName: '' }))).toBe('dev-1');
    expect(holderDisplayName(hold({ deviceName: '', deviceID: '' }))).toBe('未知设备');
  });
});

describe('releaseConsequence PG-06 确认后果文案（冻结契约）', () => {
  it('逐字包含设备名、完整会话 ID 与「可重新接管」', () => {
    const h = hold();
    expect(releaseConsequence(h)).toBe(
      `设备「主上手机」将立即失去会话 ${h.sessionID} 的控制权，可重新接管。`,
    );
    // 显示名回退同样生效
    expect(releaseConsequence(hold({ deviceName: '' }))).toContain('设备「dev-1」');
  });
});

describe('空态文案', () => {
  it('冻结契约文本', () => {
    expect(EMPTY_HOLDS_TEXT).toBe('当前没有远程设备持有控制权');
  });
});

describe('performRelease 收回动作（mock api）', () => {
  beforeEach(() => {
    vi.mocked(remoteApi.releaseSessionControl).mockReset();
  });

  it('api 成功 → {ok:true}，不抛出', async () => {
    vi.mocked(remoteApi.releaseSessionControl).mockResolvedValue(undefined);
    const out = await performRelease('sess-1');
    expect(out).toEqual({ ok: true });
    expect(remoteApi.releaseSessionControl).toHaveBeenCalledWith('sess-1');
  });

  it('api 失败 → 分类后的可读错误（不抛出）', async () => {
    vi.mocked(remoteApi.releaseSessionControl).mockRejectedValue(
      new Error('会话不存在或已结束：sess-1'),
    );
    const out = await performRelease('sess-1');
    expect(out.ok).toBe(false);
    expect(typeof out.errorMessage).toBe('string');
    expect(out.errorMessage!.length).toBeGreaterThan(0);
  });
});
