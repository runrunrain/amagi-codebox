/**
 * lib/api.ts — Remote REST v1 client（M1-D1 PG-01）
 * ---------------------------------------------------------------------------
 * 契约来源：mobile/src/lib/contract（M0-03 冻结）。本文件只 import 契约类型与
 * 常量，不复制任何路径/错误码字符串。
 *
 * 边界：
 *   · BASE 为契约冻结的相对路径 REST_BASE_PATH（/api/remote/v1），页面由宿主
 *     同源伺服；跨宿主切换通过整页导航完成，不在 client 内拼绝对地址。
 *   · 唯一无凭据端点：POST /pairing/complete；其余请求一律 credentials:'same-origin'
 *     （HttpOnly Cookie 是唯一凭据载体，前端不可读、不存储）。
 *   · 禁止记录 code / token / cookie：本文件不 console 任何请求体与凭据材料。
 * ---------------------------------------------------------------------------
 */

import {
  REST_BASE_PATH,
  V1_ENDPOINT_PAIRING_COMPLETE,
  V1_ENDPOINT_HOST_SUMMARY,
  ERROR_CODE_NET_UNREACHABLE,
  ERROR_CODE_SERVICE_DOWN,
  ERROR_CODE_AUTH_UNPAIRED,
  type ApiError,
  type ErrorCode,
  type ErrorLayer,
  type ActionHint,
  type PairingCompleteResponse,
  type HostSummary,
  type RequestID,
  type RestMethod,
} from './contract';

/** 客户端侧结构化错误：网络层失败也会被综合为契约错误形态（code=net.unreachable）。 */
export class ApiRequestError extends Error {
  readonly code: ErrorCode;
  readonly layer: ErrorLayer;
  readonly actionHint: ActionHint;
  readonly status: number | null;
  readonly requestId: RequestID | null;
  readonly details?: Record<string, unknown>;

  constructor(init: {
    message: string;
    code: ErrorCode;
    layer: ErrorLayer;
    actionHint: ActionHint;
    status: number | null;
    requestId: RequestID | null;
    details?: Record<string, unknown>;
  }) {
    super(init.message);
    this.name = 'ApiRequestError';
    this.code = init.code;
    this.layer = init.layer;
    this.actionHint = init.actionHint;
    this.status = init.status;
    this.requestId = init.requestId;
    this.details = init.details;
  }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null;
}

/** 把未知异常规整为 ApiRequestError；已是 ApiRequestError 则原样返回。 */
export function toApiRequestError(err: unknown): ApiRequestError {
  if (err instanceof ApiRequestError) return err;
  return new ApiRequestError({
    message: err instanceof Error ? err.message : 'Unknown error',
    code: ERROR_CODE_NET_UNREACHABLE,
    layer: 'connection',
    actionHint: 'retry',
    status: null,
    requestId: null,
  });
}

async function request<T>(method: RestMethod, path: string, body?: unknown): Promise<T> {
  let response: Response;
  try {
    response = await fetch(`${REST_BASE_PATH}${path}`, {
      method,
      credentials: 'same-origin',
      headers: body === undefined ? undefined : { 'Content-Type': 'application/json' },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
  } catch (err) {
    // fetch 拒绝（DNS/TCP/离线/CORS）：统一映射为契约 net.unreachable，不透出宿主细节。
    throw toApiRequestError(err);
  }

  if (response.ok) {
    // 201 pairing/complete 与 200 host/summary 均有 JSON 体；
    // 解析失败（如开发服务器 SPA fallback 返回 HTML）如实映射为 service.down，不伪装成功。
    try {
      return (await response.json()) as T;
    } catch {
      throw new ApiRequestError({
        message: `Unexpected non-JSON response (status ${response.status})`,
        code: ERROR_CODE_SERVICE_DOWN,
        layer: 'connection',
        actionHint: 'check-desktop',
        status: response.status,
        requestId: null,
      });
    }
  }

  // 统一错误映射：契约错误体为顶层 ApiError 对象（无 {error:{}} 信封）。
  let apiError: ApiError | null = null;
  try {
    const parsed: unknown = await response.json();
    if (isRecord(parsed) && typeof parsed.code === 'string' && typeof parsed.message === 'string') {
      apiError = parsed as unknown as ApiError;
    }
  } catch {
    // 非 JSON 错误体（如代理/网关拦截）：落到下方的状态码兜底分类。
  }

  if (apiError) {
    throw new ApiRequestError({
      message: apiError.message,
      code: apiError.code,
      layer: apiError.layer,
      actionHint: apiError.actionHint,
      status: response.status,
      requestId: apiError.requestId,
      details: apiError.details,
    });
  }

  // 状态码兜底：无契约错误体时仍给出可分类的 code，而不是笼统失败。
  const fallbackCode: ErrorCode =
    response.status === 401 || response.status === 403
      ? ERROR_CODE_AUTH_UNPAIRED
      : ERROR_CODE_NET_UNREACHABLE;
  const fallbackLayer: ErrorLayer =
    response.status === 401 || response.status === 403 ? 'auth' : 'connection';
  throw new ApiRequestError({
    message: `Request failed with status ${response.status}`,
    code: fallbackCode,
    layer: fallbackLayer,
    actionHint: fallbackLayer === 'auth' ? 're-pair' : 'retry',
    status: response.status,
    requestId: null,
  });
}

/**
 * 一次性配对（唯一无凭据端点）。
 * code 为一次性配对材料：不入日志、不入持久化，仅随请求体发送一次。
 * method/path 一律消费契约 manifest 具名常量（Minor-01），不复制字符串。
 */
export async function completePairing(code: string, deviceName: string): Promise<PairingCompleteResponse> {
  return request<PairingCompleteResponse>(
    V1_ENDPOINT_PAIRING_COMPLETE.method,
    V1_ENDPOINT_PAIRING_COMPLETE.path,
    { code, deviceName },
  );
}

/** 宿主摘要（需设备 Cookie 凭据）。 */
export async function getHostSummary(): Promise<HostSummary> {
  return request<HostSummary>(V1_ENDPOINT_HOST_SUMMARY.method, V1_ENDPOINT_HOST_SUMMARY.path);
}
