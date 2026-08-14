// useModelCatalog - provider→model 目录的加载与 spec 拆装逻辑。
// pi 与 omp 的目录 JSON 形态一致（providers[].{name,api,models[]}），
// 两端的可视化配置组件共用本 composable 实现「provider → model →
// thinking level」三级下拉关联，避免手写 provider/model:level spec 出错。
import { ref } from 'vue';

export interface CatalogModel {
  id: string;
  name?: string;
  reasoning: boolean;
  thinkingLevels?: string[];
  contextWindow?: number;
}

export interface CatalogProvider {
  name: string;
  api?: string;
  models: CatalogModel[];
  /** 该提供商是否已有可用凭据（auth.json 条目或注册表内联 apiKey） */
  hasAuth?: boolean;
}

export interface ModelCatalog {
  providers: CatalogProvider[];
}

/** 无 thinkingLevelMap 元数据时的兜底级别集合（与 amagi-pi 校验集一致） */
export const DEFAULT_THINKING_LEVELS = ['off', 'minimal', 'low', 'medium', 'high', 'xhigh', 'max'];

export interface ModelSpec {
  provider: string;
  model: string;
  level: string;
}

/**
 * 拆解 `provider/model[:level]` spec。
 * 非常规形态（无 provider、多斜杠等）返回 null，调用方回退手动输入。
 */
export function parseModelSpec(spec: string): ModelSpec | null {
  if (!spec) return null;
  const levelIdx = spec.lastIndexOf(':');
  const base = levelIdx > 0 ? spec.slice(0, levelIdx) : spec;
  const level = levelIdx > 0 ? spec.slice(levelIdx + 1) : '';
  const slashIdx = base.indexOf('/');
  if (slashIdx <= 0 || slashIdx === base.length - 1) return null;
  return { provider: base.slice(0, slashIdx), model: base.slice(slashIdx + 1), level };
}

/** 拼装 `provider/model[:level]` spec（level 为空时不带冒号段） */
export function buildModelSpec(provider: string, model: string, level: string): string {
  const base = `${provider}/${model}`;
  return level ? `${base}:${level}` : base;
}

export function useModelCatalog() {
  const catalog = ref<ModelCatalog>({ providers: [] });
  const catalogLoading = ref(false);
  const catalogError = ref('');

  async function loadCatalog(loader: () => Promise<string>) {
    catalogLoading.value = true;
    catalogError.value = '';
    try {
      const raw = await loader();
      const parsed = JSON.parse(raw || '{}') as ModelCatalog;
      catalog.value = parsed && Array.isArray(parsed.providers) ? parsed : { providers: [] };
    } catch (err) {
      catalogError.value = String(err);
      catalog.value = { providers: [] };
    } finally {
      catalogLoading.value = false;
    }
  }

  return { catalog, catalogLoading, catalogError, loadCatalog };
}
