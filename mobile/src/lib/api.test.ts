/**
 * lib/api.test.ts — REST v1 client 错误映射与凭据语义（M1-D1）
 * 聚焦：统一错误映射（契约错误体 / 网络失败 / 状态码兜底）、
 * credentials:'same-origin'、BASE 使用契约 REST_BASE_PATH（不复制字符串）。
 */
import { afterEach, describe, expect, it, vi } from 'vitest';
import { completePairing, getHostSummary, ApiRequestError } from './api';
import {
  REST_BASE_PATH,
  V1_REST_ENDPOINTS,
  V1_ENDPOINT_PAIRING_COMPLETE,
  V1_ENDPOINT_HOST_SUMMARY,
} from './contract';

const hostSummaryBody = {
  apiVersion: 'v1',
  serverVersion: '1.0.5',
  cliAvailability: [
    { cliType: 'claudecode', available: true },
    { cliType: 'opencode', available: false },
    { cliType: 'codex', available: true },
    { cliType: 'pi', available: false },
  ],
};

function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('contract 端点消费（Minor-01：不复制字符串）', () => {
  it('具名常量是 manifest 条目的同一引用（非复制值）', () => {
    expect(V1_ENDPOINT_PAIRING_COMPLETE).toBe(V1_REST_ENDPOINTS[0]);
    expect(V1_ENDPOINT_HOST_SUMMARY).toBe(V1_REST_ENDPOINTS[1]);
  });
});

describe('getHostSummary', () => {
  it('GET 契约相对路径，credentials same-origin，200 → HostSummary', async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, hostSummaryBody));
    vi.stubGlobal('fetch', fetchMock);

    const host = await getHostSummary();

    expect(fetchMock).toHaveBeenCalledTimes(1);
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    // 断言实际请求使用契约 manifest 值，而非客户端复制的字符串。
    expect(url).toBe(`${REST_BASE_PATH}${V1_REST_ENDPOINTS[1].path}`);
    expect(init.method).toBe(V1_REST_ENDPOINTS[1].method);
    expect(init.credentials).toBe('same-origin');
    expect(host.serverVersion).toBe('1.0.5');
    expect(host.cliAvailability).toHaveLength(4);
  });

  it('401 + 契约错误体 → ApiRequestError 保留 code/layer/actionHint', async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      jsonResponse(401, {
        requestId: 'req-1',
        code: 'auth.unpaired',
        layer: 'auth',
        message: 'device not paired',
        actionHint: 're-pair',
      }),
    );
    vi.stubGlobal('fetch', fetchMock);

    const err = await getHostSummary().catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiRequestError);
    expect((err as ApiRequestError).code).toBe('auth.unpaired');
    expect((err as ApiRequestError).layer).toBe('auth');
    expect((err as ApiRequestError).actionHint).toBe('re-pair');
    expect((err as ApiRequestError).status).toBe(401);
  });

  it('fetch 拒绝（网络失败）→ 映射 net.unreachable，不透出宿主细节', async () => {
    const fetchMock = vi.fn().mockRejectedValue(new TypeError('Failed to fetch'));
    vi.stubGlobal('fetch', fetchMock);

    const err = await getHostSummary().catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiRequestError);
    expect((err as ApiRequestError).code).toBe('net.unreachable');
    expect((err as ApiRequestError).layer).toBe('connection');
    expect((err as ApiRequestError).status).toBeNull();
  });

  it('非契约错误体 503 → 兜底分类 connection/retry', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response('bad gateway', { status: 503 }));
    vi.stubGlobal('fetch', fetchMock);

    const err = await getHostSummary().catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiRequestError);
    expect((err as ApiRequestError).code).toBe('net.unreachable');
    expect((err as ApiRequestError).status).toBe(503);
  });
});

describe('completePairing', () => {
  it('POST pairing/complete 201 → PairingCompleteResponse（device+host）', async () => {
    const body = {
      device: { id: 'dev-1', name: '我的 Android 设备', pairedAt: '2026-08-02T08:00:00Z' },
      host: hostSummaryBody,
    };
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(201, body));
    vi.stubGlobal('fetch', fetchMock);

    const result = await completePairing('one-time-code', '我的 Android 设备');

    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    // 断言实际请求使用契约 manifest 值，而非客户端复制的字符串。
    expect(url).toBe(`${REST_BASE_PATH}${V1_REST_ENDPOINTS[0].path}`);
    expect(init.method).toBe(V1_REST_ENDPOINTS[0].method);
    expect(init.credentials).toBe('same-origin');
    expect(result.device.name).toBe('我的 Android 设备');
    expect(result.host.apiVersion).toBe('v1');
  });

  it('窗口过期 → auth.window_expired 原样映射', async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      jsonResponse(401, {
        requestId: 'req-2',
        code: 'auth.window_expired',
        layer: 'auth',
        message: 'pairing window closed',
        actionHint: 're-pair',
      }),
    );
    vi.stubGlobal('fetch', fetchMock);

    const err = await completePairing('stale-code', '设备').catch((e: unknown) => e);
    expect((err as ApiRequestError).code).toBe('auth.window_expired');
    expect((err as ApiRequestError).layer).toBe('auth');
  });
});
