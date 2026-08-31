import type { DropdownOption } from '../ui/Dropdown.vue';
import type { ModelCatalog } from './useModelCatalog';

export interface ConcurrencyConfig {
  default?: number | string;
  providers?: Record<string, number | string>;
  models?: Record<string, number | string>;
  [key: string]: any;
}

/**
 * 归一化并发限制输入值（number | string | undefined | null）。
 * 在用户输入中间态过程中，空串或非数字暂存字符串，不打断输入链也不抛 TypeError。
 */
export function normalizeLimitInput(val: unknown): string | number {
  const raw = String(val ?? '').trim();
  if (raw === '') return '';
  const num = parseInt(raw, 10);
  if (!isNaN(num) && num > 0 && String(num) === raw) {
    return num;
  }
  return raw;
}

/**
 * 保存收口时清理 concurrency 配置（cleanEmptyLimits + cleanupConcurrency）。
 * - default：空串或非正整数移除；正整数保留。
 * - providers/models：空串/空白/非正整数键值剔除；正整数合法键值保留。
 * - 空子对象（providers/models 为空）剔除。
 * - 若 default/providers/models 均无，返回 undefined，从而从上层 amagi.json 中删除 concurrency 键。
 */
export function cleanConcurrencyConfig(
  concurrency: ConcurrencyConfig | undefined | null
): ConcurrencyConfig | undefined {
  if (!concurrency || typeof concurrency !== 'object') return undefined;

  const result: ConcurrencyConfig = { ...concurrency };

  // 1. 处理 default
  if (result.default !== undefined) {
    const raw = String(result.default ?? '').trim();
    if (!raw) {
      delete result.default;
    } else {
      const num = parseInt(raw, 10);
      if (!isNaN(num) && num > 0) {
        result.default = num;
      } else {
        delete result.default;
      }
    }
  }

  // 2. 处理 providers
  if (result.providers && typeof result.providers === 'object') {
    const cleanedProviders: Record<string, number> = {};
    for (const [k, v] of Object.entries(result.providers)) {
      const trimmedKey = k.trim();
      if (!trimmedKey) continue;
      const rawVal = String(v ?? '').trim();
      if (rawVal === '') continue; // 清空 = 剔除
      const num = parseInt(rawVal, 10);
      if (!isNaN(num) && num > 0) {
        cleanedProviders[trimmedKey] = num;
      }
    }
    if (Object.keys(cleanedProviders).length > 0) {
      result.providers = cleanedProviders;
    } else {
      delete result.providers;
    }
  }

  // 3. 处理 models
  if (result.models && typeof result.models === 'object') {
    const cleanedModels: Record<string, number> = {};
    for (const [k, v] of Object.entries(result.models)) {
      const trimmedKey = k.trim();
      if (!trimmedKey || !trimmedKey.includes('/')) continue;
      const rawVal = String(v ?? '').trim();
      if (rawVal === '') continue; // 清空 = 剔除
      const num = parseInt(rawVal, 10);
      if (!isNaN(num) && num > 0) {
        cleanedModels[trimmedKey] = num;
      }
    }
    if (Object.keys(cleanedModels).length > 0) {
      result.models = cleanedModels;
    } else {
      delete result.models;
    }
  }

  const hasDefault = typeof result.default === 'number' && result.default > 0;
  const hasProviders =
    result.providers && typeof result.providers === 'object' && Object.keys(result.providers).length > 0;
  const hasModels =
    result.models && typeof result.models === 'object' && Object.keys(result.models).length > 0;

  if (!hasDefault && !hasProviders && !hasModels) {
    return undefined;
  }

  return result;
}

/**
 * 构造 providers 键的标准 Dropdown 选项：
 * 选项 = 模型目录 providers ∪ 已配置 agents 的 model provider（spec '/' 前缀）∪ 已配置 concurrency.providers 键。
 * 去重、稳定排序，并在末尾附加自定义入口。
 */
export function buildProviderDropdownOptions(
  catalog: ModelCatalog | null | undefined,
  agentModels: string[],
  existingProviders?: Record<string, any> | null
): DropdownOption[] {
  const providersSet = new Set<string>();

  // 1. From catalog
  if (catalog?.providers && Array.isArray(catalog.providers)) {
    for (const p of catalog.providers) {
      if (p.name && typeof p.name === 'string') {
        const trimmed = p.name.trim();
        if (trimmed) providersSet.add(trimmed);
      }
    }
  }

  // 2. From configured agents' models (prefix before '/')
  if (Array.isArray(agentModels)) {
    for (const model of agentModels) {
      if (!model || typeof model !== 'string') continue;
      const slashIdx = model.indexOf('/');
      if (slashIdx > 0) {
        const pName = model.slice(0, slashIdx).trim();
        if (pName) providersSet.add(pName);
      }
    }
  }

  // 3. From concurrency.providers existing keys
  if (existingProviders && typeof existingProviders === 'object' && !Array.isArray(existingProviders)) {
    for (const k of Object.keys(existingProviders)) {
      const trimmed = k.trim();
      if (trimmed) providersSet.add(trimmed);
    }
  }

  const sorted = Array.from(providersSet).sort((a, b) => a.localeCompare(b));
  const options: DropdownOption[] = sorted.map((p) => ({ value: p, label: p }));
  options.push({ value: '__custom__', label: '＋ 自定义服务商...' });
  return options;
}

/**
 * 构造 models 键的标准 Dropdown 选项：
 * 选项 = 目录所有 provider/model spec ∪ agents 已用 spec（去 :thinking、须含 /）∪ 已配置键。
 * 去重、稳定排序，并在末尾附加自定义入口。
 */
export function buildModelDropdownOptions(
  catalog: ModelCatalog | null | undefined,
  agentModels: string[],
  existingModels?: Record<string, any> | null
): DropdownOption[] {
  const modelsSet = new Set<string>();

  // 1. From catalog
  if (catalog?.providers && Array.isArray(catalog.providers)) {
    for (const p of catalog.providers) {
      if (!p.name || !Array.isArray(p.models)) continue;
      const pName = p.name.trim();
      if (!pName) continue;
      for (const m of p.models) {
        if (m.id && typeof m.id === 'string') {
          const mId = m.id.trim();
          if (mId) {
            const spec = `${pName}/${mId}`;
            if (spec.includes('/')) modelsSet.add(spec);
          }
        }
      }
    }
  }

  // 2. From configured agents' models (strip :thinkingLevel, must have /)
  if (Array.isArray(agentModels)) {
    for (const model of agentModels) {
      if (!model || typeof model !== 'string') continue;
      const cleanSpec = (model.includes(':') ? model.slice(0, model.lastIndexOf(':')) : model).trim();
      if (cleanSpec && cleanSpec.includes('/')) {
        modelsSet.add(cleanSpec);
      }
    }
  }

  // 3. From concurrency.models existing keys
  if (existingModels && typeof existingModels === 'object' && !Array.isArray(existingModels)) {
    for (const k of Object.keys(existingModels)) {
      const trimmed = k.trim();
      if (trimmed && trimmed.includes('/')) modelsSet.add(trimmed);
    }
  }

  const sorted = Array.from(modelsSet).sort((a, b) => a.localeCompare(b));
  const options: DropdownOption[] = sorted.map((m) => ({ value: m, label: m }));
  options.push({ value: '__custom__', label: '＋ 自定义模型...' });
  return options;
}
