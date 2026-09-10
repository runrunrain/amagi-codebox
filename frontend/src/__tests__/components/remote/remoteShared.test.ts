import { describe, expect, it, vi } from 'vitest';
import {
  classifyRemoteError,
  isLoopbackHost,
  isWildcardHost,
  isValidIpOrHost,
  formatPairingUrl,
  copyTextToClipboard,
  formatCountdown,
  formatDateTime,
  formatEventTime,
} from '../../../components/remote/remoteShared';

describe('remoteShared helpers', () => {
  describe('isLoopbackHost', () => {
    it('识别各类回环地址', () => {
      expect(isLoopbackHost('127.0.0.1')).toBe(true);
      expect(isLoopbackHost('127.0.0.2')).toBe(true);
      expect(isLoopbackHost('localhost')).toBe(true);
      expect(isLoopbackHost('LOCALHOST')).toBe(true);
      expect(isLoopbackHost('::1')).toBe(true);
      expect(isLoopbackHost('[::1]')).toBe(true);
    });

    it('非回环地址返回 false', () => {
      expect(isLoopbackHost('0.0.0.0')).toBe(false);
      expect(isLoopbackHost('192.168.1.100')).toBe(false);
      expect(isLoopbackHost('10.0.0.1')).toBe(false);
      expect(isLoopbackHost('my-host.lan')).toBe(false);
      expect(isLoopbackHost('')).toBe(false);
      expect(isLoopbackHost(undefined)).toBe(false);
    });
  });

  describe('isWildcardHost', () => {
    it('识别通配地址', () => {
      expect(isWildcardHost('')).toBe(true);
      expect(isWildcardHost('0.0.0.0')).toBe(true);
      expect(isWildcardHost('::')).toBe(true);
      expect(isWildcardHost('[::]')).toBe(true);
      expect(isWildcardHost(undefined)).toBe(true);
    });

    it('非通配地址返回 false', () => {
      expect(isWildcardHost('127.0.0.1')).toBe(false);
      expect(isWildcardHost('192.168.1.1')).toBe(false);
      expect(isWildcardHost('localhost')).toBe(false);
    });
  });

  describe('isValidIpOrHost', () => {
    it('合法 IPv4 地址返回 true', () => {
      expect(isValidIpOrHost('192.168.1.1')).toBe(true);
      expect(isValidIpOrHost('10.0.0.1')).toBe(true);
      expect(isValidIpOrHost('172.16.0.254')).toBe(true);
      expect(isValidIpOrHost('0.0.0.0')).toBe(true);
      expect(isValidIpOrHost('127.0.0.1')).toBe(true);
    });

    it('非法 IPv4 地址返回 false', () => {
      expect(isValidIpOrHost('192.168.1.256')).toBe(false);
      expect(isValidIpOrHost('192.168.1')).toBe(false);
      expect(isValidIpOrHost('192.168.1.1.1')).toBe(false);
      expect(isValidIpOrHost('999.999.999.999')).toBe(false);
    });

    it('合法主机名返回 true', () => {
      expect(isValidIpOrHost('my-laptop')).toBe(true);
      expect(isValidIpOrHost('codebox.local')).toBe(true);
      expect(isValidIpOrHost('desktop-pc-01.lan')).toBe(true);
    });

    it('包含路径、端口或非法字符的输入返回 false', () => {
      expect(isValidIpOrHost('192.168.1.1:8680')).toBe(false);
      expect(isValidIpOrHost('http://192.168.1.1')).toBe(false);
      expect(isValidIpOrHost('192.168.1.1/test')).toBe(false);
      expect(isValidIpOrHost('192.168.1.1?query=1')).toBe(false);
      expect(isValidIpOrHost('192.168.1. 1')).toBe(false);
      expect(isValidIpOrHost('')).toBe(false);
      expect(isValidIpOrHost(undefined)).toBe(false);
    });

    it('带括号的 IPv6 地址返回 true', () => {
      expect(isValidIpOrHost('[fe80::1]')).toBe(true);
      expect(isValidIpOrHost('[::1]')).toBe(true);
    });
  });

  describe('formatPairingUrl', () => {
    it('格式化完整配对 Web URL', () => {
      const url = formatPairingUrl('192.168.1.50', 8680, 'ABCD-1234', '2026-09-10T12:00:00Z');
      expect(url).toBe('http://192.168.1.50:8680/#/connect?code=ABCD-1234&expiresAt=2026-09-10T12%3A00%3A00Z');
    });

    it('对带 IPv6 地址自动添加中括号包装（若未带中括号）', () => {
      const url = formatPairingUrl('fe80::1', 8680, 'CODE', 'EXP');
      expect(url).toBe('http://[fe80::1]:8680/#/connect?code=CODE&expiresAt=EXP');
    });
  });

  describe('classifyRemoteError', () => {
    it('正确识别端口占用错误', () => {
      const err = classifyRemoteError(new Error('listen tcp 0.0.0.0:8680: bind: address already in use'));
      expect(err.category).toBe('port-conflict');
      expect(err.message).toContain('端口被占用');
    });

    it('正确识别安全子系统未就绪', () => {
      const err = classifyRemoteError(new Error('security state unavailable'));
      expect(err.category).toBe('security-unavailable');
      expect(err.message).toContain('安全子系统未就绪');
    });

    it('正确识别权限拒绝', () => {
      const err = classifyRemoteError(new Error('bind: permission denied'));
      expect(err.category).toBe('permission');
      expect(err.message).toContain('权限不足');
    });
  });

  describe('formatCountdown', () => {
    it('正确格式化秒数倒计时', () => {
      expect(formatCountdown(90_000)).toBe('01:30');
      expect(formatCountdown(5_000)).toBe('00:05');
      expect(formatCountdown(0)).toBe('00:00');
      expect(formatCountdown(-1000)).toBe('00:00');
    });
  });

  describe('copyTextToClipboard', () => {
    it('使用 navigator.clipboard 成功复制', async () => {
      const writeText = vi.fn().mockResolvedValue(undefined);
      vi.stubGlobal('navigator', { clipboard: { writeText } });

      const ok = await copyTextToClipboard('test-copy-payload');
      expect(ok).toBe(true);
      expect(writeText).toHaveBeenCalledWith('test-copy-payload');

      vi.unstubAllGlobals();
    });
  });
});
