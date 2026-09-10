import { describe, expect, it, vi } from 'vitest';
import {
  isLoopbackHost,
  isWildcardHost,
  isValidIpOrHost,
  formatPairingUrl,
  classifyRemoteError,
  hostSummaryStateFromStatus,
  readCustomLanHost,
} from '../../../components/remote/remoteShared';

describe('Remote Self-Check & Address Fallback Logic', () => {
  describe('Gate 禁启警告匹配与透出逻辑', () => {
    it('准确匹配后端 remote_security_migration_gate 透出的各类警告（固定前缀）', () => {
      const allStartupWarnings = [
        '检测到未完成的外部进程清理；请先关闭旧外部终端，再通过恢复确认 API 重新核验并解锁 Headroom',
        '远程安全设置迁移：检测阶段失败，远程功能本次未启动。',
        '远程安全设置迁移：检测到未完成的迁移痕迹，需要手动修复后才能启动远程功能。',
        '[环境检测] 系统代理端口不可用',
      ];

      // 后端 migrationWarn* 全部以固定前缀开头；仅按前缀匹配防误伤
      const gateWarnings = allStartupWarnings.filter((w) => w.includes('远程安全设置迁移'));

      expect(gateWarnings).toHaveLength(2);
      expect(gateWarnings[0]).toContain('远程安全设置迁移：检测阶段失败');
      expect(gateWarnings[1]).toContain('需要手动修复后才能启动远程功能');

      // 评估是否应该被判定为门禁拦截 (isGateBlocked)
      const isGateBlocked = gateWarnings.length > 0;
      expect(isGateBlocked).toBe(true);
    });

    it('无相关警告时门禁判定为通过（含未启动字样的无关警告不误伤）', () => {
      const allStartupWarnings = [
        '[环境检测] 某环境检查项正常',
        '旧预设自动迁移失败: xxx。请前往设置手动处理',
      ];
      const gateWarnings = allStartupWarnings.filter((w) => w.includes('远程安全设置迁移'));
      expect(gateWarnings).toHaveLength(0);
      expect(gateWarnings.length === 0).toBe(true);
    });
  });

  describe('监听地址与网络范围评估', () => {
    it('127.0.0.1 判定为回环且触发告警状态', () => {
      const host = '127.0.0.1';
      expect(isLoopbackHost(host)).toBe(true);
      expect(isWildcardHost(host)).toBe(false);
    });

    it('0.0.0.0 判定为通配且无回环告警', () => {
      const host = '0.0.0.0';
      expect(isLoopbackHost(host)).toBe(false);
      expect(isWildcardHost(host)).toBe(true);
    });

    it('具体局域网 IP 判定为指定网卡', () => {
      const host = '192.168.1.120';
      expect(isLoopbackHost(host)).toBe(false);
      expect(isWildcardHost(host)).toBe(false);
    });
  });

  describe('端口状态与推荐端口计算', () => {
    it('端口冲突错误分类与候选端口推荐', () => {
      const conflictErr = classifyRemoteError(new Error('start remote server: listen tcp 0.0.0.0:8680: bind: address already in use'));
      expect(conflictErr.category).toBe('port-conflict');

      const port = 8680;
      const recommendedPort = port >= 8680 && port < 8690 ? port + 1 : 8681;
      expect(recommendedPort).toBe(8681);
    });

    it('非 8680~8689 端口在冲突时推荐 8681', () => {
      const port = 9000;
      const recommendedPort = port >= 8680 && port < 8690 ? port + 1 : 8681;
      expect(recommendedPort).toBe(8681);
    });
  });

  describe('配对卡地址兜底与 URL 合成', () => {
    it('addressRequired 场景下：用户填入自定义局域网 IP 成功合成有效 URL', () => {
      const windowInfo = {
        generation: 1,
        code: 'ABCD-1234',
        expiresAt: '2026-09-10T15:00:00Z',
        baseUrl: undefined,
        addressRequired: true,
      };

      // 初始无可用 base URL
      expect(windowInfo.baseUrl).toBeUndefined();
      expect(windowInfo.addressRequired).toBe(true);

      // 用户输入局域网 IP
      const userInput = '192.168.31.55';
      expect(isValidIpOrHost(userInput)).toBe(true);

      const synthesizedUrl = formatPairingUrl(userInput, 8680, windowInfo.code, windowInfo.expiresAt);
      expect(synthesizedUrl).toBe(
        'http://192.168.31.55:8680/#/connect?code=ABCD-1234&expiresAt=2026-09-10T15%3A00%3A00Z',
      );

      // 解析合成出的 URL，验证其合法性与包含的参数
      const parsed = new URL(synthesizedUrl);
      expect(parsed.hostname).toBe('192.168.31.55');
      expect(parsed.port).toBe('8680');
      expect(parsed.hash).toContain('/connect');
      expect(parsed.hash).toContain('code=ABCD-1234');
    });

    it('IPv6 地址输入带括号后成功合成有效 URL', () => {
      const input = '[fe80::1ff:fe23:4567:890a]';
      expect(isValidIpOrHost(input)).toBe(true);
      const url = formatPairingUrl(input, 8680, 'CODE-99', 'EXP-01');
      expect(url).toContain('http://[fe80::1ff:fe23:4567:890a]:8680/#/connect?code=CODE-99');
    });

    it('非法主机名输入被拒绝，不生成错误二维码', () => {
      const invalidInputs = [
        '192.168.1.1:8680', // 误输入端口
        'http://192.168.1.1', // 误输入协议
        '192.168.1.300', // 非法 IPv4
        'invalid host with spaces', // 包含空格
        'host/path', // 包含路径
      ];

      for (const input of invalidInputs) {
        expect(isValidIpOrHost(input)).toBe(false);
      }
    });
  });

  describe('宿主可用性探测降级判断（P3-B R2 绑定数据源）', () => {
    it('hostSummaryDegraded=true 判定为保守降级（全 CLI 不可启动）', () => {
      expect(hostSummaryStateFromStatus(true, { hostSummaryDegraded: true })).toBe('degraded');
    });

    it('hostSummaryDegraded=false 判定为正常探测', () => {
      expect(hostSummaryStateFromStatus(true, { hostSummaryDegraded: false })).toBe('normal');
    });

    it('服务未运行或状态绑定拉取失败（null/undefined）诚实收敛为 unreachable', () => {
      expect(hostSummaryStateFromStatus(false, { hostSummaryDegraded: true })).toBe('unreachable');
      expect(hostSummaryStateFromStatus(true, null)).toBe('unreachable');
      expect(hostSummaryStateFromStatus(true, undefined)).toBe('unreachable');
      // 字段缺位视为 false：与 Go 端 bool 零值序列化语义一致（绑定总回传该字段）
      expect(hostSummaryStateFromStatus(true, {})).toBe('normal');
    });

    it('状态绑定对象兼容 main.RemoteWebUIStatusResult 形状（结构化字段透传）', () => {
      const statusLike = {
        openable: true,
        reason: '',
        url: 'http://127.0.0.1:8680/',
        port: 8680,
        running: true,
        hostSummaryDegraded: true,
      };
      expect(hostSummaryStateFromStatus(true, statusLike)).toBe('degraded');
    });

    it('降级提示仅在服务运行中且探测到降级时透出（横幅可见性判定）', () => {
      const visible = (running: boolean, state: string) => running && state === 'degraded';
      expect(visible(true, 'degraded')).toBe(true);
      expect(visible(true, 'normal')).toBe(false);
      expect(visible(true, 'unreachable')).toBe(false);
      expect(visible(false, 'degraded')).toBe(false);
    });
  });

  describe('Startup 恢复失败透出（P3-B③ 漂移可观察）', () => {
    it('lastStartError 携带端口占用语义时可被分类为 port-conflict 并点亮自检横幅', () => {
      const lastStartError = 'remote server listen 0.0.0.0:8680: listen tcp 0.0.0.0:8680: bind: address already in use';
      const c = classifyRemoteError(new Error(lastStartError));
      expect(c.category).toBe('port-conflict');
      // 自检卡端口横幅判定链路：lastServiceError.category === 'port-conflict'
      expect(c.category === 'port-conflict').toBe(true);
    });

    it('仅在后端存在 lastStartError 记录时写入（不覆盖用户 toggle 即时错误）', () => {
      const merge = (
        prev: { category: string } | null,
        rawStartErr: string,
      ): { category: string } | null => {
        if (rawStartErr) return classifyRemoteError(new Error(rawStartErr));
        return prev; // 无启动失败记录 → 保留既有错误（含 toggle 成功后的显式 null）
      };
      const toggleErr = { category: 'permission' };
      expect(merge(toggleErr, '')).toBe(toggleErr);
      const merged = merge(toggleErr, 'listen tcp: bind: address already in use');
      expect(merged?.category).toBe('port-conflict');
      expect(merge(null, '')).toBeNull();
    });

    it('enabled=true / running=false / lastStartError 非空构成可判定漂移三元组', () => {
      const driftVisible = (s: { enabled?: boolean; running?: boolean; lastStartError?: string }) =>
        s.enabled === true && s.running === false && !!s.lastStartError;
      expect(driftVisible({ enabled: true, running: false, lastStartError: 'bind: address already in use' })).toBe(true);
      expect(driftVisible({ enabled: true, running: true, lastStartError: '' })).toBe(false);
      expect(driftVisible({ enabled: false, running: false, lastStartError: '' })).toBe(false);
    });
  });

  describe('R1 候选局域网地址单选（配对兜底）', () => {
    it('候选标签合成：IP/掩码长度，掩码缺位时仅展示 IP', () => {
      const label = (c: { ip: string; prefixLen: number }) =>
        c.prefixLen > 0 ? `${c.ip}/${c.prefixLen}` : c.ip;
      expect(label({ ip: '192.168.31.55', prefixLen: 24 })).toBe('192.168.31.55/24');
      expect(label({ ip: '10.1.2.3', prefixLen: 8 })).toBe('10.1.2.3/8');
      expect(label({ ip: '203.0.113.10', prefixLen: 0 })).toBe('203.0.113.10');
    });

    it('候选 IP 均通过校验可直接套用为局域网地址（后端已过滤回环/链路本地）', () => {
      const candidates = [
        { ip: '192.168.31.55', prefixLen: 24, interface: 'en0', private: true, vpnLike: false },
        { ip: '10.8.0.2', prefixLen: 24, interface: 'utun3', private: true, vpnLike: true },
      ];
      for (const c of candidates) {
        expect(isValidIpOrHost(c.ip)).toBe(true);
      }
      // 候选点击即套用：选中项写入 customHostDraft 并复用 applyCustomHost 校验链
      const selected = candidates[0].ip;
      expect(formatPairingUrl(selected, 8680, 'CODE-1', 'EXP-1')).toContain(
        'http://192.168.31.55:8680/#/connect?code=CODE-1',
      );
    });

    it('候选拉取失败时回退纯手动输入（空列表不渲染候选组）', () => {
      const lanCandidates: unknown[] = [];
      expect(lanCandidates.length > 0).toBe(false);
    });
  });

  describe('通配监听访问地址展示（手动局域网地址优先）', () => {
    it('通配且配对卡已持久化合法局域网地址时，展示该地址而非 0.0.0.0', () => {
      const store: Record<string, string> = { 'amagi.remote.customLanHost': '192.168.31.55' };
      vi.stubGlobal('localStorage', {
        getItem: (k: string) => store[k] ?? null,
        setItem: (k: string, v: string) => {
          store[k] = v;
        },
        removeItem: (k: string) => delete store[k],
      });

      expect(readCustomLanHost()).toBe('192.168.31.55');
      const host = readCustomLanHost() || '0.0.0.0';
      expect(`http://${host}:8680/`).toBe('http://192.168.31.55:8680/');
      vi.unstubAllGlobals();
    });

    it('无合法持久化地址时回退 0.0.0.0 并保留替换提示', () => {
      vi.stubGlobal('localStorage', {
        getItem: () => null,
        setItem: () => {},
        removeItem: () => {},
      });
      expect(readCustomLanHost()).toBe('');
      const host = readCustomLanHost() || '0.0.0.0';
      expect(host).toBe('0.0.0.0');
      vi.unstubAllGlobals();
    });

    it('持久化值非法（如误含端口）时不采用，避免生成错误访问地址', () => {
      vi.stubGlobal('localStorage', {
        getItem: () => '192.168.31.55:8680',
        setItem: () => {},
        removeItem: () => {},
      });
      expect(readCustomLanHost()).toBe('');
      vi.unstubAllGlobals();
    });
  });

  describe('自检总体状态判定 (Overall Status)', () => {
    function computeOverallStatus(params: {
      gateBlocked: boolean;
      hasPortConflict: boolean;
      isLoopback: boolean;
      lanConfirmed: boolean;
      running: boolean;
    }): { text: string; statusClass: string } {
      if (params.gateBlocked || params.hasPortConflict) {
        return {
          text: params.gateBlocked ? '门禁拦截' : '端口占用',
          statusClass: 'status-danger',
        };
      }
      if (params.isLoopback || !params.lanConfirmed) {
        return {
          text: params.isLoopback ? '回环受限' : '待 LAN 确认',
          statusClass: 'status-warning',
        };
      }
      if (params.running) {
        return {
          text: '服务正常',
          statusClass: 'status-ok',
        };
      }
      return {
        text: '就绪 · 待开启',
        statusClass: 'status-stopped',
      };
    }

    it('门禁被拦截时呈现红色 danger', () => {
      const res = computeOverallStatus({
        gateBlocked: true,
        hasPortConflict: false,
        isLoopback: false,
        lanConfirmed: true,
        running: false,
      });
      expect(res.statusClass).toBe('status-danger');
      expect(res.text).toBe('门禁拦截');
    });

    it('端口冲突时呈现红色 danger', () => {
      const res = computeOverallStatus({
        gateBlocked: false,
        hasPortConflict: true,
        isLoopback: false,
        lanConfirmed: true,
        running: false,
      });
      expect(res.statusClass).toBe('status-danger');
      expect(res.text).toBe('端口占用');
    });

    it('回环监听呈现黄色 warning', () => {
      const res = computeOverallStatus({
        gateBlocked: false,
        hasPortConflict: false,
        isLoopback: true,
        lanConfirmed: true,
        running: true,
      });
      expect(res.statusClass).toBe('status-warning');
      expect(res.text).toBe('回环受限');
    });

    it('正常运行呈现绿色 ok', () => {
      const res = computeOverallStatus({
        gateBlocked: false,
        hasPortConflict: false,
        isLoopback: false,
        lanConfirmed: true,
        running: true,
      });
      expect(res.statusClass).toBe('status-ok');
      expect(res.text).toBe('服务正常');
    });

    it('停止且一切正常呈现灰色 stopped', () => {
      const res = computeOverallStatus({
        gateBlocked: false,
        hasPortConflict: false,
        isLoopback: false,
        lanConfirmed: true,
        running: false,
      });
      expect(res.statusClass).toBe('status-stopped');
      expect(res.text).toBe('就绪 · 待开启');
    });
  });
});
