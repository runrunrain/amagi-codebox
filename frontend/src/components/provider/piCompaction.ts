// amagi-pi compaction.model：未设置继承、显式 null 使用原生模型、字符串统一选模。
import { parseModelSpec } from './useModelCatalog';

export type CompactionMode = 'unset' | 'custom' | 'native';

function isObject(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value);
}

export function compactionModeOf(value: unknown): CompactionMode {
  if (value === undefined || (isObject(value) && !('model' in value))) return 'unset';
  if (isObject(value) && value.model === null) return 'native';
  return 'custom';
}

export function compactionModelOf(value: unknown): string {
  return isObject(value) && typeof value.model === 'string' ? value.model : '';
}

/** 只修改 model；未知字段留给源码修复，不能被模式切换静默清除。 */
export function applyCompactionMode(config: Record<string, unknown>, mode: CompactionMode, model = ''): void {
  const current = config.compaction;
  if (current !== undefined && !isObject(current)) {
    throw new Error('compaction 必须是对象，请在 JSON 模式修复');
  }
  const next = { ...(isObject(current) ? current : {}) };
  if (mode === 'unset') {
    delete next.model;
    if (Object.keys(next).length) config.compaction = next;
    else delete config.compaction;
  } else {
    next.model = mode === 'native' ? null : model;
    config.compaction = next;
  }
}

/** 与插件 resolveCompactionConfig 对齐；不以本地目录缺项否定原生动态注册模型。 */
export function validateCompactionConfig(config: Record<string, unknown>): string {
  if (!('compaction' in config)) return '';
  const value = config.compaction;
  if (!isObject(value)) return 'compaction 必须是对象';
  if (Object.keys(value).some(key => key !== 'model')) return 'compaction 只允许 model 字段，请在 JSON 模式修复';
  if (!('model' in value) || value.model === null) return '';
  if (typeof value.model !== 'string' || !parseModelSpec(value.model)) {
    return 'compaction.model 必须是完整的 provider/model[:thinking] 或 null';
  }
  return '';
}
