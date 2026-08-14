/**
 * lib/api.test.ts — REST v1 client 错误映射与凭据语义（M1-D1）
 * 聚焦：统一错误映射（契约错误体 / 网络失败 / 状态码兜底）、
 * credentials:'same-origin'、BASE 使用契约 REST_BASE_PATH（不复制字符串）。
 */
import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  completePairing,
  getHostSummary,
  listSessions,
  getSessionDetail,
  createSession,
  stopSession,
  restartSession,
  removeSession,
  acquireControl,
  releaseControl,
  ApiRequestError,
} from './api';
import {
  REST_BASE_PATH,
  V1_REST_ENDPOINTS,
  V1_ENDPOINT_PAIRING_COMPLETE,
  V1_ENDPOINT_HOST_SUMMARY,
  V1_ENDPOINT_SESSIONS_LIST,
  V1_ENDPOINT_SESSION_DETAIL,
  V1_ENDPOINT_SESSION_CREATE,
  V1_ENDPOINT_SESSION_STOP,
  V1_ENDPOINT_SESSION_RESTART,
  V1_ENDPOINT_SESSION_REMOVE,
  V1_ENDPOINT_CONTROL_ACQUIRE,
  V1_ENDPOINT_CONTROL_RELEASE,
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

  it('会话端点具名 handle 与 manifest index 2-9 同一引用（M2-B）', () => {
    expect(V1_ENDPOINT_SESSIONS_LIST).toBe(V1_REST_ENDPOINTS[2]);
    expect(V1_ENDPOINT_SESSION_DETAIL).toBe(V1_REST_ENDPOINTS[3]);
    expect(V1_ENDPOINT_SESSION_CREATE).toBe(V1_REST_ENDPOINTS[4]);
    expect(V1_ENDPOINT_SESSION_STOP).toBe(V1_REST_ENDPOINTS[5]);
    expect(V1_ENDPOINT_SESSION_RESTART).toBe(V1_REST_ENDPOINTS[6]);
    expect(V1_ENDPOINT_SESSION_REMOVE).toBe(V1_REST_ENDPOINTS[7]);
    expect(V1_ENDPOINT_CONTROL_ACQUIRE).toBe(V1_REST_ENDPOINTS[8]);
    expect(V1_ENDPOINT_CONTROL_RELEASE).toBe(V1_REST_ENDPOINTS[9]);
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

// ---------------------------------------------------------------------------
// M2-B：会话端点（design §5.2 index 2-9）
// ---------------------------------------------------------------------------

const SESSION_ID = 'sess-abc-123';

const sessionDetailBody = {
  id: SESSION_ID,
  title: 'Claude Code · amagi-codebox',
  cliType: 'claudecode',
  state: 'running',
  control: { state: 'you' },
  lastActivityAt: '2026-08-03T08:00:00Z',
  workdir: '/users/dev/amagi-codebox',
  startedAt: '2026-08-03T07:30:00Z',
  earliestSeq: 0,
  latestSeq: 0,
};

/** 取 fetch mock 最近一次调用的 [url, init]。 */
function lastCall(fetchMock: ReturnType<typeof vi.fn>): [string, RequestInit] {
  return fetchMock.mock.calls[fetchMock.mock.calls.length - 1] as [string, RequestInit];
}

/** 断言 url/method 来自契约 manifest 对应 index（`{id}` 已被恰好替换一次）。 */
function expectEndpointUsed(call: [string, RequestInit], manifestIndex: number, id?: string) {
  const ep = V1_REST_ENDPOINTS[manifestIndex];
  const expectedPath = id === undefined ? ep.path : ep.path.replace('{id}', encodeURIComponent(id));
  expect(call[0]).toBe(`${REST_BASE_PATH}${expectedPath}`);
  expect(call[1].method).toBe(ep.method);
  expect(call[1].credentials).toBe('same-origin');
}

describe('listSessions / getSessionDetail', () => {
  it('GET /sessions 200 → 顶层数组（空列表为 []）', async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, []));
    vi.stubGlobal('fetch', fetchMock);

    const list = await listSessions();
    expectEndpointUsed(lastCall(fetchMock), 2);
    expect(list).toEqual([]);
  });

  it('GET /sessions/{id} 200 → SessionDetail；路径 {id} 恰好替换一次', async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, sessionDetailBody));
    vi.stubGlobal('fetch', fetchMock);

    const detail = await getSessionDetail(SESSION_ID);
    expectEndpointUsed(lastCall(fetchMock), 3, SESSION_ID);
    expect(lastCall(fetchMock)[0]).toContain(`/sessions/${SESSION_ID}`);
    expect(detail.workdir).toBe('/users/dev/amagi-codebox');
    expect(detail.earliestSeq).toBe(0);
  });

  it('404 session.not_found → 保留 code/layer（AC-24）', async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      jsonResponse(404, {
        requestId: 'req-404',
        code: 'session.not_found',
        layer: 'session',
        message: 'session not found',
        actionHint: 'retry',
      }),
    );
    vi.stubGlobal('fetch', fetchMock);

    const err = await getSessionDetail(SESSION_ID).catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiRequestError);
    expect((err as ApiRequestError).code).toBe('session.not_found');
    expect((err as ApiRequestError).layer).toBe('session');
  });
});

describe('createSession / 生命周期 / 控制权', () => {
  it('POST /sessions 201：body 恰为 {cliType}（不携带 provider/model/key）', async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(201, sessionDetailBody));
    vi.stubGlobal('fetch', fetchMock);

    await createSession({ cliType: 'claudecode' });
    const call = lastCall(fetchMock);
    expectEndpointUsed(call, 4);
    expect(JSON.parse(String(call[1].body))).toEqual({ cliType: 'claudecode' });
  });

  it('POST /sessions 可携带安全会话设置，但不携带密钥或 URL', async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(201, sessionDetailBody));
    vi.stubGlobal('fetch', fetchMock);

    await createSession({
      cliType: 'codex', workdir: '/workspace/project', providerRef: 'openai-main',
      presetRef: 'max', modelRef: 'gpt-5.6', shellRef: '/bin/zsh', useHeadroom: true,
    });
    const body = JSON.parse(String(lastCall(fetchMock)[1].body));
    expect(body).toEqual({
      cliType: 'codex', workdir: '/workspace/project', providerRef: 'openai-main',
      presetRef: 'max', modelRef: 'gpt-5.6', shellRef: '/bin/zsh', useHeadroom: true,
    });
    expect(JSON.stringify(body)).not.toMatch(/apiKey|baseURL|token|environment/i);
  });

  it('422 session.launch_failed 保留 details.cliType（AC-25）', async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      jsonResponse(422, {
        requestId: 'req-422',
        code: 'session.launch_failed',
        layer: 'session',
        message: 'CLI is unavailable on this host',
        actionHint: 'check-desktop',
        details: { cliType: 'opencode' },
      }),
    );
    vi.stubGlobal('fetch', fetchMock);

    const err = await createSession({ cliType: 'opencode' }).catch((e: unknown) => e);
    expect((err as ApiRequestError).code).toBe('session.launch_failed');
    expect((err as ApiRequestError).status).toBe(422);
    expect((err as ApiRequestError).details?.cliType).toBe('opencode');
  });

  it('stop/restart：POST {confirm:true}（PG-06 协议级 confirm），200 → SessionDetail', async () => {
    // 注意：Response body 只能消费一次，需每次调用构造新 Response。
    const fetchMock = vi
      .fn()
      .mockImplementation(() =>
        Promise.resolve(jsonResponse(200, { ...sessionDetailBody, state: 'stopped' })),
      );
    vi.stubGlobal('fetch', fetchMock);

    await stopSession(SESSION_ID);
    let call = lastCall(fetchMock);
    expectEndpointUsed(call, 5, SESSION_ID);
    expect(JSON.parse(String(call[1].body))).toEqual({ confirm: true });

    await restartSession(SESSION_ID);
    call = lastCall(fetchMock);
    expectEndpointUsed(call, 6, SESSION_ID);
    expect(JSON.parse(String(call[1].body))).toEqual({ confirm: true });
  });

  it('remove：DELETE {confirm:true} → 204 无 body（不做 JSON 解析）', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(null, { status: 204 }));
    vi.stubGlobal('fetch', fetchMock);

    const result = await removeSession(SESSION_ID);
    const call = lastCall(fetchMock);
    expectEndpointUsed(call, 7, SESSION_ID);
    expect(JSON.parse(String(call[1].body))).toEqual({ confirm: true });
    expect(result).toBeUndefined();
  });

  it('acquire/release：POST 空 body → 200 ControlSnapshot', async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(jsonResponse(200, { state: 'you' }))
      .mockResolvedValueOnce(jsonResponse(200, { state: 'none' }));
    vi.stubGlobal('fetch', fetchMock);

    const acquired = await acquireControl(SESSION_ID);
    let call = lastCall(fetchMock);
    expectEndpointUsed(call, 8, SESSION_ID);
    expect(call[1].body).toBeUndefined();
    expect(acquired).toEqual({ state: 'you' });

    const released = await releaseControl(SESSION_ID);
    call = lastCall(fetchMock);
    expectEndpointUsed(call, 9, SESSION_ID);
    expect(call[1].body).toBeUndefined();
    expect(released).toEqual({ state: 'none' });
  });

  it('409 control.busy（acquire 冲突）→ 保留 code/layer/actionHint，不笼统失败', async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      jsonResponse(409, {
        requestId: 'req-409',
        code: 'control.busy',
        layer: 'control',
        message: 'session control is held by another controller',
        actionHint: 'request-control',
      }),
    );
    vi.stubGlobal('fetch', fetchMock);

    const err = await acquireControl(SESSION_ID).catch((e: unknown) => e);
    expect((err as ApiRequestError).code).toBe('control.busy');
    expect((err as ApiRequestError).layer).toBe('control');
    expect((err as ApiRequestError).actionHint).toBe('request-control');
    expect((err as ApiRequestError).status).toBe(409);
  });

  it('403 control.forbidden（非 holder 写操作）→ 保留 code/layer', async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      jsonResponse(403, {
        requestId: 'req-403',
        code: 'control.forbidden',
        layer: 'control',
        message: 'device is not the session controller',
        actionHint: 'request-control',
      }),
    );
    vi.stubGlobal('fetch', fetchMock);

    const err = await stopSession(SESSION_ID).catch((e: unknown) => e);
    expect((err as ApiRequestError).code).toBe('control.forbidden');
    expect((err as ApiRequestError).layer).toBe('control');
  });
});
