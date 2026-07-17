<template>
  <div class="pricing-table-wrap">
    <!-- 未知模型快捷入口 / Unknown-model quick-add chips -->
    <div v-if="unknownModels.length > 0" class="unknown-block">
      <div class="unknown-head">
        <span class="unknown-title">未配置价格的模型（{{ unknownModels.length }}）</span>
        <span class="unknown-hint">点击「+」为该模型新增价格</span>
      </div>
      <div class="unknown-chips">
        <button
          v-for="u in unknownModels"
          :key="u.normalizedModel"
          class="unknown-chip"
          :title="`样例：${u.sampleRaw || u.normalizedModel}（${u.requests} 次）`"
          @click="emit('add-for-unknown', u.normalizedModel, u.sampleRaw || u.normalizedModel)"
        >
          <span class="uc-plus">+</span>
          <span class="uc-name mono">{{ u.displayName || u.normalizedModel }}</span>
          <span class="uc-count">{{ formatCount(u.requests) }}</span>
        </button>
      </div>
    </div>

    <!-- 工具栏 / Toolbar -->
    <div class="toolbar">
      <input
        v-model="keyword"
        type="text"
        class="search-input"
        placeholder="搜索模型 ID / 显示名 / 供应商..."
      />
      <span class="entry-count">{{ filtered.length }} / {{ entries.length }} 条</span>
      <AppButton variant="ghost" size="small" @click="emit('reset')">恢复内置</AppButton>
      <AppButton variant="primary" size="small" @click="emit('add')">+ 新增</AppButton>
    </div>

    <div v-if="filtered.length === 0" class="empty-table">
      <span v-if="entries.length === 0">价格表为空，点击「+ 新增」添加第一条</span>
      <span v-else>没有匹配「{{ keyword }}」的条目</span>
    </div>

    <div v-else class="table-scroll">
      <table class="pricing-table">
        <thead>
          <tr>
            <th class="col-pattern">模型 ID</th>
            <th class="col-name">显示名</th>
            <th class="col-provider">供应商</th>
            <th class="col-currency">币种</th>
            <th class="col-price">Input</th>
            <th class="col-price">Output</th>
            <th class="col-price">Cache R</th>
            <th class="col-price">Cache W</th>
            <th class="col-tag">标记</th>
            <th class="col-actions">操作</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="entry in filtered" :key="entry.id">
            <td class="col-pattern mono" :title="entry.modelPattern">{{ entry.modelPattern }}</td>
            <td class="col-name">{{ entry.displayName || '—' }}</td>
            <td class="col-provider">{{ entry.provider || '—' }}</td>
            <td class="col-currency mono">{{ entry.currencyCode }}</td>
            <td class="col-price mono">{{ formatPerMillion(entry.inputPerMillion, entry.currencyCode) }}</td>
            <td class="col-price mono">{{ formatPerMillion(entry.outputPerMillion, entry.currencyCode) }}</td>
            <td class="col-price mono">{{ formatPerMillion(entry.cacheReadPerMillion, entry.currencyCode) }}</td>
            <td class="col-price mono">{{ formatPerMillion(entry.cacheCreationPerMillion, entry.currencyCode) }}</td>
            <td class="col-tag">
              <span v-if="entry.isBuiltin" class="tag tag-builtin">内置</span>
              <span v-else class="tag tag-custom">自定义</span>
            </td>
            <td class="col-actions">
              <button class="row-btn" title="编辑" @click="emit('edit', entry)">编辑</button>
              <button
                class="row-btn row-btn-danger"
                :disabled="entry.isBuiltin"
                :title="entry.isBuiltin ? '内置模型不可删除' : '删除'"
                @click="handleDelete(entry)"
              >
                删除
              </button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed } from 'vue';
import AppButton from '../ui/AppButton.vue';
import { formatPerMillion, formatCount } from '../../utils/usage-format';
import type { ModelPricing, UnknownModel } from '../../api/usage';

interface Props {
  entries: ModelPricing[];
  unknownModels?: UnknownModel[];
}

const props = withDefaults(defineProps<Props>(), {
  unknownModels: () => [],
});

const emit = defineEmits<{
  (e: 'edit', entry: ModelPricing): void;
  (e: 'delete', entry: ModelPricing): void;
  (e: 'add'): void;
  (e: 'add-for-unknown', normalizedModel: string, sampleRaw: string): void;
  (e: 'reset'): void;
}>();

// 与 UnknownModel.displayName 兼容：models.ts 中 UnknownModel 无 displayName 字段，
// 这里把样例展示归一到 sampleRaw（已在模板的 title/默认值中处理）。
// 启用 displayName 字段透传，方便后续 backend 扩展。
type UnknownModelLike = UnknownModel & { displayName?: string };
const unknownList = computed<UnknownModelLike[]>(() => props.unknownModels as UnknownModelLike[]);

// 重新映射以便模板用 u.displayName（如果没有则回退到 normalizedModel）。
// 通过 computed 透出 displayName 字段，保持模板简洁。
const unknownModels = computed(() =>
  unknownList.value.map((u) => ({
    ...u,
    displayName: u.displayName || u.normalizedModel,
  })),
);

const keyword = ref('');

const filtered = computed(() => {
  const kw = keyword.value.trim().toLowerCase();
  if (!kw) return props.entries;
  return props.entries.filter((e) =>
    [e.modelPattern, e.displayName, e.provider]
      .filter(Boolean)
      .some((s) => s.toLowerCase().includes(kw)),
  );
});

function handleDelete(entry: ModelPricing) {
  if (entry.isBuiltin) return;
  if (!confirm(`确认删除价格「${entry.displayName || entry.modelPattern}」？`)) return;
  emit('delete', entry);
}
</script>

<style scoped>
.pricing-table-wrap {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.unknown-block {
  background: rgba(255, 149, 0, 0.06);
  border: 1px solid rgba(255, 149, 0, 0.2);
  border-radius: 10px;
  padding: 10px 12px;
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.unknown-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}

.unknown-title {
  font-size: 12px;
  font-weight: 600;
  color: var(--warning-strong);
}

.unknown-hint {
  font-size: 11px;
  color: var(--tertiary);
}

.unknown-chips {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}

.unknown-chip {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 4px 9px;
  border-radius: 999px;
  background: var(--card);
  border: 1px solid var(--separator);
  font-size: 12px;
  color: var(--secondary);
  cursor: pointer;
  transition: border-color 0.12s, color 0.12s;
}

.unknown-chip:hover {
  border-color: var(--accent);
  color: var(--accent);
}

.uc-plus {
  color: var(--accent);
  font-weight: 600;
}

.uc-name {
  color: var(--label);
  font-weight: 500;
}

.uc-count {
  color: var(--tertiary);
  font-size: 11px;
  padding-left: 4px;
  border-left: 1px solid var(--separator);
}

.toolbar {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}

.search-input {
  flex: 1;
  min-width: 180px;
  appearance: none;
  background: var(--control);
  border: 1px solid transparent;
  border-radius: 7px;
  padding: 6px 10px;
  font-size: 13px;
  color: var(--label);
  font-family: inherit;
  outline: none;
  transition: box-shadow 0.12s, background-color 0.12s;
}

.search-input:focus {
  box-shadow: 0 0 0 2px rgba(0, 122, 255, 0.25);
  background: var(--card);
}

.entry-count {
  font-size: 11px;
  color: var(--tertiary);
  margin-right: auto;
}

.empty-table {
  padding: 24px 12px;
  text-align: center;
  font-size: 13px;
  color: var(--tertiary);
  background: var(--sidebar);
  border-radius: 8px;
}

.table-scroll {
  max-height: 420px;
  overflow-y: auto;
  border: 1px solid var(--separator);
  border-radius: 10px;
}

.pricing-table {
  width: 100%;
  border-collapse: collapse;
  font-size: 12.5px;
}

.pricing-table thead {
  position: sticky;
  top: 0;
  z-index: 1;
}

.pricing-table th {
  background: var(--sidebar);
  color: var(--secondary);
  font-size: 11px;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.4px;
  padding: 8px 10px;
  text-align: left;
  border-bottom: 1px solid var(--separator);
}

.pricing-table td {
  padding: 7px 10px;
  border-bottom: 1px solid var(--separator);
  color: var(--label);
  vertical-align: top;
}

.pricing-table tr:last-child td {
  border-bottom: none;
}

.pricing-table tr:hover td {
  background: color-mix(in srgb, var(--accent) 4%, transparent);
}

.mono {
  font-family: var(--mono);
  font-size: 12px;
}

.col-pattern {
  min-width: 160px;
  max-width: 220px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.col-name { min-width: 110px; }
.col-provider { width: 90px; }
.col-currency { width: 60px; }
.col-price { white-space: nowrap; }
.col-tag { width: 70px; }
.col-actions { width: 110px; white-space: nowrap; }

.tag {
  display: inline-block;
  padding: 2px 8px;
  border-radius: 4px;
  font-size: 10px;
  font-weight: 600;
  letter-spacing: 0.3px;
}

.tag-builtin {
  background: rgba(0, 122, 255, 0.1);
  color: var(--accent);
}

.tag-custom {
  background: rgba(175, 82, 222, 0.1);
  color: var(--purple);
}

.row-btn {
  background: transparent;
  border: 1px solid var(--separator);
  border-radius: 6px;
  padding: 3px 8px;
  margin-right: 4px;
  font-size: 11px;
  color: var(--secondary);
  cursor: pointer;
  transition: all 0.12s;
}

.row-btn:hover {
  border-color: var(--accent);
  color: var(--accent);
}

.row-btn-danger:hover {
  border-color: var(--danger);
  color: var(--danger);
}

.row-btn:disabled {
  opacity: 0.4;
  cursor: not-allowed;
}

.row-btn:disabled:hover {
  border-color: var(--separator);
  color: var(--secondary);
}
</style>
