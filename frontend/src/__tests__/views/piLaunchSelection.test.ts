import { describe, it, expect } from 'vitest';
import {
  reconcilePiLaunchSelection,
  isPiLoginProviderId,
  isPiLoginPresetKey,
  PI_LOGIN_STALE_MESSAGE,
  PI_LOGIN_LOAD_ERROR_MESSAGE,
} from '../../views/piLaunchSelection';

/** 与后端 PiLoginProviderID 同构的编码（仅测试数据用） */
const loginId = (provider: string) => `pi-login/${btoa(provider).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')}`;
const loginKey = (provider: string, name: string) => `${loginId(provider)}/${btoa(name).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')}`;

const publicChoice = { key: 'openai-codex/API Daily', provider: 'openai-codex' };
const loginChoice = { key: loginKey('openai-codex', 'Review Login'), provider: loginId('openai-codex') };

describe('isPiLoginProviderId / isPiLoginPresetKey（命名空间形态，R1 镜像）', () => {
  it('接受结构化登录 id/key，拒绝裸段与普通公共 key', () => {
    expect(isPiLoginProviderId(loginId('openai-codex'))).toBe(true);
    expect(isPiLoginPresetKey(loginKey('openai-codex', 'Daily'))).toBe(true);
    expect(isPiLoginProviderId('pi-login')).toBe(false);
    expect(isPiLoginProviderId('pi-login/Daily')).toBe(false);
    expect(isPiLoginProviderId('openai-codex')).toBe(false);
    expect(isPiLoginPresetKey('pi-login/Daily')).toBe(false);
    // 结构同形的公共三段 key 形态上是登录 preset key；UI 判定以 provider id 为准，
    // 该形态只用于把 key 关联到登录来源，不单独决定账号语义。
    expect(isPiLoginPresetKey('pi-login/Foo/Bar')).toBe(true);
  });
});

describe('reconcilePiLaunchSelection（R4：失效登录不静默降级）', () => {
  it('退出登录后刷新：登录预设从候选消失 → 保留失效选择 + stale + 明确错误，绝不改选 API', () => {
    const result = reconcilePiLaunchSelection({
      provider: loginChoice.provider,
      model: loginChoice.key,
      choices: [publicChoice],
    });
    expect(result.provider).toBe(loginChoice.provider);
    expect(result.model).toBe(loginChoice.key);
    expect(result.stale).toBe(true);
    expect(result.message).toBe(PI_LOGIN_STALE_MESSAGE);
  });

  it('登录状态读取失败：登录选择保留 + stale；公共选择原样保留，不触发自动改选', () => {
    const login = reconcilePiLaunchSelection({
      provider: loginChoice.provider,
      model: loginChoice.key,
      choices: [publicChoice],
      loginError: 'boom',
    });
    expect(login.stale).toBe(true);
    expect(login.message).toBe(PI_LOGIN_LOAD_ERROR_MESSAGE);
    expect(login.provider).toBe(loginChoice.provider);

    const pub = reconcilePiLaunchSelection({
      provider: publicChoice.provider,
      model: publicChoice.key,
      choices: [publicChoice],
      loginError: 'boom',
    });
    expect(pub).toMatchObject({ provider: publicChoice.provider, model: publicChoice.key, stale: false, message: '' });
  });

  it('有效登录选择原样保留；显式选择的新来源不带 stale', () => {
    const valid = reconcilePiLaunchSelection({
      provider: loginChoice.provider,
      model: loginChoice.key,
      choices: [publicChoice, loginChoice],
    });
    expect(valid).toMatchObject({ provider: loginChoice.provider, model: loginChoice.key, stale: false, message: '' });

    const reselected = reconcilePiLaunchSelection({
      provider: publicChoice.provider,
      model: '',
      choices: [publicChoice, loginChoice],
    });
    expect(reselected.stale).toBe(false);
  });

  it('公共选择失配维持既有自动改选行为（普通 API 语义不变）', () => {
    const sameProvider = reconcilePiLaunchSelection({
      provider: 'openai-codex',
      model: 'openai-codex/removed',
      choices: [publicChoice],
    });
    expect(sameProvider.model).toBe(publicChoice.key);
    expect(sameProvider.stale).toBe(false);

    const fallbackFirst = reconcilePiLaunchSelection({
      provider: 'other-provider',
      model: 'other/x',
      choices: [publicChoice],
    });
    expect(fallbackFirst.provider).toBe(publicChoice.provider);
    expect(fallbackFirst.model).toBe(publicChoice.key);

    const empty = reconcilePiLaunchSelection({ provider: '', model: '', choices: [] });
    expect(empty.model).toBe('');
    expect(empty.stale).toBe(false);
  });
});
