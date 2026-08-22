/**
 * 远程配置掩码纪律（RC4-2 · 验收 5 第二道防线）
 *
 * 与 Go 侧净化层（internal/remoteclient/legacyconfig.go）配套的前端防线：
 * - 下行：Go 已把疑似密钥字段值掩码为 «remote-managed» 占位，前端渲染为
 *   MaskedValue（toggleable=false），永不展开/复制明文；
 * - 上行：提交前剔除值仍为掩码占位的密钥字段，禁止把占位当明文回传。
 *
 * 字段识别规则镜像 Go looksLikeCredentialField（冻结清单见 RC4-go 报告 §2）：
 * 归一化（小写、去 _ - 空格）后按子串模式 + 全等 token 族匹配，
 * max_tokens 是模型上限参数而非凭据，白名单排除。
 */

/** Go 导出常量 remoteclient.RemoteManagedMask 的前端镜像。 */
export const REMOTE_MANAGED_MASK = '«remote-managed»';

/** 子串模式：命中即疑似凭据字段（镜像 credentialFieldMarkers）。 */
const CREDENTIAL_MARKERS = ['apikey', 'authkey', 'secret', 'password', 'passwd', 'authorization', 'credential'];

/** 全等 token 族（镜像 exactCredentialFields；不用裸 token 子串防误伤 max_tokens）。 */
const EXACT_CREDENTIAL_FIELDS = new Set([
  'token',
  'remotetoken',
  'accesstoken',
  'refreshtoken',
  'idtoken',
  'devicetoken',
  'apitoken',
  'authtoken',
  'bearertoken',
]);

/** 白名单排除（镜像 knownNonCredentialFields）。 */
const NON_CREDENTIAL_FIELDS = new Set(['maxtokens']);

/** 字段名归一化：小写 + 去 `_`/`-`/空格（镜像 normalizeFieldName）。 */
export function normalizeCredentialFieldName(name: string): string {
  return name.toLowerCase().replace(/[_\-\s]/g, '');
}

/** 字段是否疑似凭据承载字段（镜像 Go looksLikeCredentialField）。 */
export function looksLikeCredentialField(name: string): boolean {
  const n = normalizeCredentialFieldName(name);
  if (!n || NON_CREDENTIAL_FIELDS.has(n)) return false;
  if (EXACT_CREDENTIAL_FIELDS.has(n)) return true;
  return CREDENTIAL_MARKERS.some((m) => n.includes(m));
}

/**
 * 是否为「空值=保留宿主现值」语义的统一密钥字段（镜像 isUnifiedKeyField）：
 * 顶层 api_key 与 anthropic/openai 嵌套 api_key。这些字段剔除后宿主不动 secrets。
 */
export function isUnifiedKeyField(name: string): boolean {
  return normalizeCredentialFieldName(name) === 'apikey';
}

/** 掩码扫描结果：按「可安全剔除」与「阻断保存」分组（见 prepareRemoteUpload）。 */
export interface MaskScan {
  /** 值为掩码占位的统一密钥字段路径（剔除 = 保留宿主现值，非破坏）。 */
  apiKeyMasked: string[];
  /** 值为掩码占位的其他凭据字段路径（剔除会在全量替换语义下静默清空宿主值，须阻断）。 */
  otherMasked: string[];
}

function joinPath(path: string, key: string): string {
  return path ? `${path}.${key}` : key;
}

/** 递归扫描文档中值为掩码占位的凭据字段（含 headers map 键与数组内对象）。 */
export function scanMaskedCredentialFields(value: unknown, path = ''): MaskScan {
  const result: MaskScan = { apiKeyMasked: [], otherMasked: [] };
  walk(value, path, result);
  return result;
}

function walk(value: unknown, path: string, result: MaskScan): void {
  if (Array.isArray(value)) {
    value.forEach((item) => walk(item, path, result));
    return;
  }
  if (value && typeof value === 'object') {
    for (const [k, v] of Object.entries(value as Record<string, unknown>)) {
      const p = joinPath(path, k);
      if (typeof v === 'string' && v === REMOTE_MANAGED_MASK && looksLikeCredentialField(k)) {
        if (isUnifiedKeyField(k)) {
          result.apiKeyMasked.push(p);
        } else {
          result.otherMasked.push(p);
        }
        continue;
      }
      walk(v, p, result);
    }
  }
}

/**
 * 就地剔除文档中值为掩码占位的凭据字段（递归，含 headers map 键）。
 * 调用方须先经 scanMaskedCredentialFields 确认无阻断项（otherMasked 为空）。
 */
export function stripMaskedCredentialFields(value: unknown): void {
  if (Array.isArray(value)) {
    value.forEach((item) => stripMaskedCredentialFields(item));
    return;
  }
  if (value && typeof value === 'object') {
    const obj = value as Record<string, unknown>;
    for (const k of Object.keys(obj)) {
      const v = obj[k];
      if (typeof v === 'string' && v === REMOTE_MANAGED_MASK && looksLikeCredentialField(k)) {
        delete obj[k];
        continue;
      }
      stripMaskedCredentialFields(v);
    }
  }
}

/**
 * 净化远程提供商上传体（PUT /api/providers/{name}，全量替换语义）：
 * 1. 含掩码占位的非统一密钥字段（auth_key / headers.Authorization 等）→ 抛错阻断：
 *    剔除会在宿主全量替换时静默清空该凭据，拒绝是唯一非破坏选项
 *    （与 Go 上行拦截同一设计取舍，前端为第一道、Go 为兜底）；
 * 2. 统一密钥字段（api_key 族）掩码占位 → 剔除不上传（宿主缺失=保留现值）；
 * 3. 其余字段原样保留（presets/max_tokens 等非密钥字段保真）。
 * 返回可直接上传的 JSON 字符串。
 */
export function prepareRemoteProviderUpload(rawDoc: string): string {
  const doc = JSON.parse(rawDoc) as unknown;
  const scan = scanMaskedCredentialFields(doc);
  if (scan.otherMasked.length > 0) {
    throw new Error(
      `远程编辑过渡期限制：该提供商含由宿主管辖的凭据字段（${scan.otherMasked.join(', ')}），远程保存会清空宿主值，请在宿主本地修改这些字段`,
    );
  }
  stripMaskedCredentialFields(doc);
  return JSON.stringify(doc);
}

/**
 * 净化远程设置上传体（PUT /api/settings）。宿主端 PUT 仅消费 remotePort
 * 等非密钥字段、忽略未知字段，因此掩码占位一律剔除即可（含 remoteToken——
 * 该字段本不应上行，剔除=宿主不动 token）。
 */
export function prepareRemoteSettingsUpload(rawDoc: string): string {
  const doc = JSON.parse(rawDoc) as unknown;
  stripMaskedCredentialFields(doc);
  return JSON.stringify(doc);
}

/** 文档中全部凭据字段路径（含已掩码与空值），供详情页渲染「宿主管辖」行。 */
export function listCredentialEntries(value: unknown, path = ''): string[] {
  const paths: string[] = [];
  function collect(v: unknown, p: string): void {
    if (Array.isArray(v)) {
      v.forEach((item) => collect(item, p));
      return;
    }
    if (v && typeof v === 'object') {
      for (const [k, val] of Object.entries(v as Record<string, unknown>)) {
        const keyPath = p ? `${p}.${k}` : k;
        if (looksLikeCredentialField(k)) {
          if (val && typeof val === 'object') {
            collect(val, keyPath);
          } else {
            paths.push(keyPath);
          }
          continue;
        }
        collect(val, keyPath);
      }
    }
  }
  collect(value, path);
  return paths;
}
