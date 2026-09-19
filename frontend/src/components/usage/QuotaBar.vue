<template>
  <div class="quota-bar" role="progressbar" :aria-valuenow="Math.round(clamped)" aria-valuemin="0" aria-valuemax="100">
    <div class="quota-track">
      <div class="quota-fill" :class="`tone-${tone}`" :style="{ width: `${clamped}%` }">
        <!-- 深底浅字：填充足够宽时百分比在色条内白字呈现 -->
        <span v-if="label && clamped >= 18" class="quota-fill-text">{{ label }}</span>
      </div>
    </div>
    <!-- 填充过窄时白字会被裁切，改在条外以同档强调色显示，保证可读 -->
    <span v-if="label && clamped < 18" class="quota-side-text" :class="`tone-text-${tone}`">{{ label }}</span>
  </div>
</template>

<script setup lang="ts">
/**
 * QuotaBar — 可视化额度条（设计 §6：used_percent 驱动，阈值色三档）。
 * 纯展示组件：分档/取整决策在 quotaModel.ts（可单测），此处只做绑定。
 */
import { computed } from 'vue';
import { clampPercent, quotaTone } from './quotaModel';

const props = withDefaults(defineProps<{ usedPercent: number; label?: string }>(), {
  label: '',
});

const clamped = computed(() => clampPercent(props.usedPercent));
const tone = computed(() => quotaTone(props.usedPercent));
</script>

<style scoped>
.quota-bar {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
}

.quota-track {
  flex: 1;
  height: 14px;
  border-radius: 7px;
  background: var(--control);
  overflow: hidden;
}

.quota-fill {
  height: 100%;
  min-width: 2px; /* 极低占用也保留可见薄条 */
  border-radius: 7px;
  display: flex;
  align-items: center;
  justify-content: flex-end;
  padding-right: 6px;
  box-sizing: border-box;
  overflow: hidden;
  transition: width 0.3s ease;
}

/* 阈值色优先复用 tokens.css 既有语义 token（--accent/--warning/--danger），
   与设计稿建议的 #ffa726/#ef5350 同族，不另造字面量 */
.tone-normal { background: var(--accent, #007AFF); }
.tone-warn { background: var(--warning, #FF9500); }
.tone-danger { background: var(--danger, #FF3B30); }

.quota-fill-text {
  color: #fff;
  font-size: 10px;
  font-weight: 600;
  font-family: var(--mono, monospace);
  line-height: 1;
  white-space: nowrap;
  text-shadow: 0 1px 2px rgba(0, 0, 0, 0.25);
}

.quota-side-text {
  flex: 0 0 auto;
  font-size: 11px;
  font-weight: 600;
  font-family: var(--mono, monospace);
}

/* 条外文字用同色系深色变体（tokens.css *-strong），浅底上可读 */
.tone-text-normal { color: var(--accent-strong, #0a5cb8); }
.tone-text-warn { color: var(--warning-strong, #b25000); }
.tone-text-danger { color: var(--danger-strong, #b3261e); }
</style>
