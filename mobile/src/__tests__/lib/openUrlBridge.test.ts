/**
 * __tests__/lib/openUrlBridge.test.ts — pi webui → 移动宿主 open-url 桥（跨仓契约 v2.7.4）
 * ---------------------------------------------------------------------------
 * 覆盖纯函数守卫（正负成对）：
 *   · parseOpenUrlMessage：合法消息返回具体 URL；token 不符/缺失、type 不符、
 *     非 object 载荷、url 非 string/空、非 http(s)、超长 → 各返回 null；
 *   · extractCapabilityToken：合法 fragment（含 WebPlaneView 实参 ?skin=light 形态）、
 *     缺失 fragment、形状不合法、解码失败；
 *   · openExternalUrl：http(s) 才调用 window.open（_blank + noopener），其余拒绝。
 * 组件接线（监听注册/移除）在 WebPlaneView.test.ts 的 open-url 桥用例中验证。
 * ---------------------------------------------------------------------------
 */
import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  MAX_OPEN_URL_LENGTH,
  OPEN_URL_MESSAGE_TYPE,
  extractCapabilityToken,
  openExternalUrl,
  parseOpenUrlMessage,
} from '../../lib/openUrlBridge';

/** 22 字符 capability token（形状 [A-Za-z0-9_-]{22,}，与桌面端测试同值）。 */
const TOKEN = 'AbCdEfGhIjKlMnOpQrStUv';

describe('extractCapabilityToken（iframe URL → capability token）', () => {
  it('从 `#/t=<token>` fragment 提取 token', () => {
    expect(extractCapabilityToken(`/webui/sess-1/#/t=${TOKEN}`)).toBe(TOKEN);
  });

  it('带 ?skin=light 的宿主实参形态（query 在 # 之前）同样提取成功', () => {
    expect(extractCapabilityToken(`/webui/sess-1/?skin=light#/t=${TOKEN}`)).toBe(TOKEN);
    expect(extractCapabilityToken(`https://host:8443/webui/sess-1/?skin=light#/t=${TOKEN}`)).toBe(TOKEN);
  });

  it('fragment 缺失 / token 形状不合法 / 解码失败 → null', () => {
    expect(extractCapabilityToken('/webui/sess-1/')).toBeNull();
    expect(extractCapabilityToken('/webui/sess-1/#/t=token123')).toBeNull();
    expect(extractCapabilityToken('/webui/sess-1/#/t=')).toBeNull();
    expect(extractCapabilityToken('/webui/sess-1/#/t=%E0%A4%A')).toBeNull();
    expect(extractCapabilityToken(`/webui/sess-1/#/t=${TOKEN}&x=1`)).toBeNull();
  });
});

describe('parseOpenUrlMessage（webui → 宿主 open-url 桥）', () => {
  it('合法消息：type/token/url 全对 → 返回具体 URL', () => {
    const msg = { type: OPEN_URL_MESSAGE_TYPE, token: TOKEN, url: 'https://example.com/docs' };
    expect(parseOpenUrlMessage(msg, TOKEN)).toBe('https://example.com/docs');
  });

  it('http 与大小写 scheme 同样放行（http(s) 白名单）', () => {
    expect(
      parseOpenUrlMessage({ type: OPEN_URL_MESSAGE_TYPE, token: TOKEN, url: 'http://example.com/a' }, TOKEN),
    ).toBe('http://example.com/a');
    expect(
      parseOpenUrlMessage({ type: OPEN_URL_MESSAGE_TYPE, token: TOKEN, url: 'HTTPS://Example.com/a' }, TOKEN),
    ).toBe('HTTPS://Example.com/a');
  });

  it('token 不符 / 宿主无 token（iframe 未挂载）→ null', () => {
    const msg = { type: OPEN_URL_MESSAGE_TYPE, token: 'WRONG'.repeat(6), url: 'https://example.com' };
    expect(parseOpenUrlMessage(msg, TOKEN)).toBeNull();
    expect(
      parseOpenUrlMessage({ type: OPEN_URL_MESSAGE_TYPE, token: TOKEN, url: 'https://example.com' }, null),
    ).toBeNull();
  });

  it('token 缺失 / 非 string → null', () => {
    expect(parseOpenUrlMessage({ type: OPEN_URL_MESSAGE_TYPE, url: 'https://example.com' }, TOKEN)).toBeNull();
    expect(
      parseOpenUrlMessage({ type: OPEN_URL_MESSAGE_TYPE, token: 42, url: 'https://example.com' }, TOKEN),
    ).toBeNull();
  });

  it('type 不匹配 / 缺失 → null', () => {
    expect(
      parseOpenUrlMessage({ type: 'amagi:insert-input', token: TOKEN, url: 'https://x.com' }, TOKEN),
    ).toBeNull();
    expect(parseOpenUrlMessage({ token: TOKEN, url: 'https://x.com' }, TOKEN)).toBeNull();
  });

  it('data 非 object（字符串 / null / undefined / 数字 / 布尔）→ null', () => {
    for (const data of ['amagi:open-url', null, undefined, 42, true]) {
      expect(parseOpenUrlMessage(data, TOKEN)).toBeNull();
    }
  });

  it('url 非 string / 空 → null', () => {
    expect(parseOpenUrlMessage({ type: OPEN_URL_MESSAGE_TYPE, token: TOKEN, url: 42 }, TOKEN)).toBeNull();
    expect(parseOpenUrlMessage({ type: OPEN_URL_MESSAGE_TYPE, token: TOKEN, url: '' }, TOKEN)).toBeNull();
  });

  it('非 http(s) scheme → null（javascript/file/mailto/相对路径/ftp/data）', () => {
    for (const url of [
      'javascript:alert(1)',
      'file:///etc/passwd',
      'mailto:a@b.c',
      '/relative',
      'ftp://x.com',
      'data:text/html,<b>x</b>',
      'intent://scan/#Intent;scheme=zxing;end',
    ]) {
      expect(parseOpenUrlMessage({ type: OPEN_URL_MESSAGE_TYPE, token: TOKEN, url }, TOKEN)).toBeNull();
    }
  });

  it('长度上限：恰为 2048 通过，2049 超限 → null', () => {
    const base = 'https://example.com/';
    const atLimit = base + 'a'.repeat(MAX_OPEN_URL_LENGTH - base.length);
    expect(atLimit.length).toBe(MAX_OPEN_URL_LENGTH);
    expect(parseOpenUrlMessage({ type: OPEN_URL_MESSAGE_TYPE, token: TOKEN, url: atLimit }, TOKEN)).toBe(atLimit);

    const overLimit = `${atLimit}a`;
    expect(overLimit.length).toBe(MAX_OPEN_URL_LENGTH + 1);
    expect(parseOpenUrlMessage({ type: OPEN_URL_MESSAGE_TYPE, token: TOKEN, url: overLimit }, TOKEN)).toBeNull();
  });
});

describe('openExternalUrl（本机打开动作，二次白名单）', () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('http(s) → window.open(url, _blank, noopener) 且返回 true', () => {
    const openSpy = vi.spyOn(window, 'open').mockReturnValue(null);

    expect(openExternalUrl('https://example.com/docs')).toBe(true);

    expect(openSpy).toHaveBeenCalledTimes(1);
    const [url, target, features] = openSpy.mock.calls[0] as [string, string, string];
    expect(url).toBe('https://example.com/docs');
    expect(target).toBe('_blank');
    expect(features).toContain('noopener');
    expect(features).toContain('noreferrer');
  });

  it('非 http(s) / 空串 → 拒绝且不产生打开副作用', () => {
    const openSpy = vi.spyOn(window, 'open').mockReturnValue(null);

    for (const url of ['javascript:alert(1)', 'file:///etc/passwd', '/relative', '']) {
      expect(openExternalUrl(url)).toBe(false);
    }
    expect(openSpy).not.toHaveBeenCalled();
  });
});
