<!-- 契约 §4 #7：诊断横幅。两数组皆空整条不渲染；折叠态一行摘要（真实计数），
     点击展开逐条列出；error/warning 色条沿用 ui/StatusBanner 配色。
     可测试性契约：expose toggleExpand()（与摘要行点击同一处理路径）。 -->
<template>
  <div v-if="hasDiagnostics" :class="['diagnostic-banner', severity, { expanded }]">
    <button type="button" class="db-summary" @click="toggleExpand">
      <span class="db-chevron" aria-hidden="true">{{ expanded ? '▾' : '▸' }}</span>
      <span class="db-summary-text">加载诊断：{{ errors.length }} 个错误，{{ warnings.length }} 个警告</span>
    </button>
    <div v-if="expanded" class="db-details">
      <p v-for="(error, index) in errors" :key="`error-${index}`" class="db-item db-error">
        {{ error }}
      </p>
      <p v-for="(warning, index) in warnings" :key="`warning-${index}`" class="db-item db-warning">
        {{ warning }}
      </p>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue';

interface Props {
  errors?: string[];
  warnings?: string[];
}

const props = withDefaults(defineProps<Props>(), {
  errors: () => [],
  warnings: () => [],
});

const expanded = ref(false);

const hasDiagnostics = computed(() => props.errors.length > 0 || props.warnings.length > 0);

// error 优先于 warning（与 StatusBanner 双色一致：红 255,59,48 / 橙 255,149,0）
const severity = computed<'has-error' | 'has-warning'>(() =>
  props.errors.length > 0 ? 'has-error' : 'has-warning',
);

function toggleExpand() {
  expanded.value = !expanded.value;
}

defineExpose({ toggleExpand });
</script>

<style scoped>
.diagnostic-banner {
  border-radius: 10px;
  overflow: hidden;
  font-size: 12px;
}

.diagnostic-banner.has-error {
  background: rgba(255, 59, 48, 0.08);
  border: 1px solid rgba(255, 59, 48, 0.3);
}

.diagnostic-banner.has-warning {
  background: rgba(255, 149, 0, 0.08);
  border: 1px solid rgba(255, 149, 0, 0.3);
}

.db-summary {
  display: flex;
  align-items: center;
  gap: 6px;
  width: 100%;
  border: none;
  background: none;
  cursor: pointer;
  font-family: inherit;
  font-size: 12px;
  padding: 8px 12px;
  text-align: left;
}

.diagnostic-banner.has-error .db-summary {
  color: var(--danger-strong);
}

.diagnostic-banner.has-warning .db-summary {
  color: var(--warning-strong);
}

.db-chevron {
  flex-shrink: 0;
  font-size: 10px;
}

.db-details {
  display: flex;
  flex-direction: column;
  gap: 6px;
  padding: 0 12px 10px;
}

.db-item {
  margin: 0;
  font-size: 12px;
  line-height: 1.5;
  border-radius: 6px;
  padding: 4px 8px;
}

.db-item.db-error {
  background: rgba(255, 59, 48, 0.08);
  color: var(--danger-strong);
}

.db-item.db-warning {
  background: rgba(255, 149, 0, 0.08);
  color: var(--warning-strong);
}
</style>
