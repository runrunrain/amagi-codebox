/**
 * Pi 启动选择协调（R4）。
 *
 * 会话页在刷新提供商/预设（含登录状态）后需要重新校验当前选择。既有
 * validatePiPreset 对任何失配都会自动改选列表第一项——这对公共预设是既有
 * 行为，但对登录选择是账号级变更：退出登录或登录状态读取失败后，绝不能
 * 静默改选公共 API 预设并照常启动。
 *
 * 本模块是 SessionSettingsView watcher 与启动闸门共用的纯函数契约：
 *  - 登录选择失效/登录状态读取失败 → 保留原选择、标记 stale、给出明确错误，
 *    由 UI 禁用启动并要求用户显式选择新的认证来源；
 *  - 公共选择失配 → 维持既有自动改选行为，不改普通 API 语义。
 */

/** 登录来源 id 形态：pi-login/<base64url(provider)>（与后端 ParsePiLoginProviderID 对齐）。 */
const PI_LOGIN_SEGMENT = 'pi-login';
const BASE64URL = /^[A-Za-z0-9_-]+$/;

/** 镜像 Go decodeLoginSegment：charset 合法且长度可解码（%4!==1）。 */
function decodableBase64Url(s: string): boolean {
  return s.length > 0 && BASE64URL.test(s) && s.length % 4 !== 1;
}

export function isPiLoginProviderId(id: string): boolean {
  const parts = (id || '').split('/');
  return parts.length === 2 && parts[0] === PI_LOGIN_SEGMENT && decodableBase64Url(parts[1]);
}

/** 登录预设 key 形态：pi-login/<b64>/<b64>。仅用于把 key 关联到登录来源。 */
export function isPiLoginPresetKey(key: string): boolean {
  const parts = (key || '').split('/');
  return parts.length === 3 && parts[0] === PI_LOGIN_SEGMENT && decodableBase64Url(parts[1]) && decodableBase64Url(parts[2]);
}

export interface PiChoice {
  key: string;
  provider: string;
}

export interface PiSelectionInput {
  /** 当前选择（dashState.piProvider / piModel） */
  provider: string;
  model: string;
  /** 刷新后的候选列表（公共 + 登录投影合并后的 piPresets） */
  choices: PiChoice[];
  /** 登录状态读取错误（非空表示本轮无法确认登录可用性） */
  loginError?: string | null;
}

export interface PiSelectionResult {
  provider: string;
  model: string;
  /** true = 登录选择已失效，必须禁启并提示用户显式重新选择 */
  stale: boolean;
  /** stale/错误时给用户的明确提示；正常路径为空串 */
  message: string;
}

export const PI_LOGIN_STALE_MESSAGE =
  '所选 Pi 登录认证已失效（可能已退出登录或预设被删除）。请刷新登录状态、重新登录，或显式选择其它服务提供商后再启动。';
export const PI_LOGIN_LOAD_ERROR_MESSAGE =
  'Pi 登录状态读取失败，当前登录选择不可用。请修复后刷新，或显式选择其它服务提供商。';

/**
 * 协调规则：
 *  1. loginError 非空：不改选任何项。登录选择标记 stale；公共选择原样保留
 *     （候选中的公共项未被移除，不存在自动改选触发条件）。
 *  2. 当前选择仍在候选中（key 且 provider 匹配）→ 原样返回。
 *  3. 当前 provider 是登录来源 id 且其预设不在候选 → 保留选择 + stale。
 *  4. 其余（公共选择失配/为空）→ 既有自动改选：同 provider 首个 → 列表
 *     首个 → 清空 model。
 */
export function reconcilePiLaunchSelection(input: PiSelectionInput): PiSelectionResult {
  const { provider, model, choices, loginError } = input;

  if (loginError) {
    if (isPiLoginProviderId(provider)) {
      return { provider, model, stale: true, message: PI_LOGIN_LOAD_ERROR_MESSAGE };
    }
    return { provider, model, stale: false, message: '' };
  }

  if (model) {
    const entry = choices.find((c) => c.key === model);
    if (entry && entry.provider === provider) {
      return { provider, model, stale: false, message: '' };
    }
  }

  if (isPiLoginProviderId(provider)) {
    // 登录选择失效：保留失效引用交由后端明确拒绝/前端禁启，绝不回退 API。
    return { provider, model, stale: true, message: PI_LOGIN_STALE_MESSAGE };
  }

  if (choices.length > 0) {
    const match = choices.find((c) => c.provider === provider);
    if (match) {
      return { provider, model: match.key, stale: false, message: '' };
    }
    const first = choices[0];
    return { provider: first.provider, model: first.key, stale: false, message: '' };
  }
  return { provider, model: '', stale: false, message: '' };
}
