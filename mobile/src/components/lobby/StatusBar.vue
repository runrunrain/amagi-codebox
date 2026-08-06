<script setup lang="ts">
/**
 * StatusBar — PG-02 五层状态芯片条（M2-B）
 * ---------------------------------------------------------------------------
 * 契约（P5 §5 状态矩阵 / §6 StatusBar 组件契约 / Task Contract M2-B）：
 *   · 五层：连接 / 授权 / 会话 / 控制 / 历史（与契约五 error layer 同构）；
 *   · 每层：图标 + 文字 + 颜色三通道，禁用彩色圆点；
 *   · 全部正常时可折叠为单行；任一层异常自动展开为明细列表。
 * 大厅语境（未 attach 任何会话）：连接/授权/会话层为真实诊断投影；
 * 控制层为聚合投影（你控制的会话数）；历史层诚实标注「附着会话后可见」，
 * 不伪造 replay 连续性。
 * ---------------------------------------------------------------------------
 */
import { computed, ref, watch } from 'vue';

export type LayerTone = 'ok' | 'warning' | 'danger' | 'neutral';

export interface StatusLayer {
  key: 'connection' | 'auth' | 'session' | 'control' | 'history';
  label: string;
  text: string;
  tone: LayerTone;
  /** 展开态的补充说明（可选）。 */
  detail?: string;
}

const props = defineProps<{
  layers: StatusLayer[];
}>();

const hasAbnormal = computed(() => props.layers.some((l) => l.tone === 'warning' || l.tone === 'danger'));

/** 用户手动折叠偏好；异常时强制展开（auto），恢复后回到手动态。 */
const manualExpanded = ref(false);
const expanded = computed(() => hasAbnormal.value || manualExpanded.value);

watch(hasAbnormal, (abnormal) => {
  // 异常出现时不改手动偏好，仅由 expanded 计算属性自动展开。
  if (!abnormal) manualExpanded.value = false;
});

const LAYER_ICONS: Record<StatusLayer['key'], string> = {
  // 信号波（连接）
  connection: 'M5 12.55a11 11 0 0 1 14.08 0M8.53 16.11a6 6 0 0 1 6.95 0M12 20h.01',
  // 钥匙（授权）
  auth: 'M21 2l-2 2m-7.61 7.61a5.5 5.5 0 1 1-7.778 7.778 5.5 5.5 0 0 1 7.777-7.777zm0 0L15.5 7.5m0 0l3 3L22 7l-3-3m-3.5 3.5L19 4',
  // 终端窗口（会话）
  session: 'M2 3h20v14H2zM8 21h8M12 17v4M7 8l3 3-3 3M13 14h4',
  // 手势控制（控制）
  control: 'M18 11V6a2 2 0 0 0-4 0v5M14 10V4a2 2 0 0 0-4 0v6M10 10.5V6a2 2 0 0 0-4 0v8M18 8a2 2 0 1 1 4 0v6a8 8 0 0 1-8 8h-2c-2.8 0-4.5-.86-5.99-2.34l-3.6-3.6a2 2 0 0 1 2.83-2.82L7 15',
  // 时间回卷（历史）
  history: 'M3 3v5h5M3.05 13A9 9 0 1 0 6 5.3L3 8M12 7v5l4 2',
};
</script>

<template>
  <section class="status-bar" aria-label="连接与授权状态">
    <div class="status-chips" :class="{ 'status-chips--expanded': expanded }" role="status" aria-live="polite">
      <span
        v-for="layer in layers"
        :key="layer.key"
        class="chip"
        :class="`chip--${layer.tone}`"
      >
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <path :d="LAYER_ICONS[layer.key]" />
        </svg>
        <span class="chip-text">{{ layer.label }}：{{ layer.text }}</span>
      </span>
      <button
        type="button"
        class="chip-toggle"
        :aria-expanded="expanded"
        :aria-label="expanded ? '收起状态明细' : '展开状态明细'"
        @click="manualExpanded = !manualExpanded"
      >
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true" :class="{ 'chevron--up': expanded }">
          <polyline points="6 9 12 15 18 9" />
        </svg>
      </button>
    </div>

    <!-- 展开明细：每层一行 + 补充说明；异常时自动呈现 -->
    <ul v-if="expanded" class="layer-details">
      <li v-for="layer in layers" :key="layer.key" class="layer-row">
        <span class="layer-name" :class="`layer-name--${layer.tone}`">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
            <path :d="LAYER_ICONS[layer.key]" />
          </svg>
          {{ layer.label }}
        </span>
        <span class="layer-value">
          {{ layer.text }}
          <span v-if="layer.detail" class="layer-detail">{{ layer.detail }}</span>
        </span>
      </li>
    </ul>
  </section>
</template>

<style scoped>
.status-bar {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

/* M4-A safe-area：横屏（刘海/圆角在左右）时芯片条不贴边；
   竖屏保持既有视觉（0 内边距）。 */
@media (orientation: landscape) {
  .status-bar {
    padding-left: env(safe-area-inset-left, 0px);
    padding-right: env(safe-area-inset-right, 0px);
  }
}

.status-chips {
  display: flex;
  align-items: center;
  flex-wrap: nowrap;
  gap: 8px;
  overflow-x: auto;
  scrollbar-width: none;
}

.status-chips::-webkit-scrollbar {
  display: none;
}

/* 展开时芯片换行堆叠，不再单行滚动 */
.status-chips--expanded {
  flex-wrap: wrap;
  overflow-x: visible;
}

.chip {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 6px 10px;
  border-radius: 999px;
  border: 1px solid var(--VT-border);
  background: var(--VT-surface);
  font-size: 12px;
  font-weight: 600;
  color: var(--VT-text);
  white-space: nowrap;
  flex-shrink: 0;
}

.chip svg {
  flex-shrink: 0;
}

.chip--ok {
  border-color: var(--VT-success);
}
.chip--ok svg {
  color: var(--VT-success);
}

.chip--warning {
  border-color: var(--VT-warning);
}
.chip--warning svg {
  color: var(--VT-warning);
}

.chip--danger {
  border-color: var(--VT-danger);
}
.chip--danger svg {
  color: var(--VT-danger);
}

.chip--neutral svg {
  color: var(--VT-secondary);
}

.chip-toggle {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-width: 44px;
  min-height: 44px;
  margin: -8px 0;
  border: none;
  background: transparent;
  color: var(--VT-text-secondary);
  cursor: pointer;
  flex-shrink: 0;
}

.chip-toggle:focus-visible {
  outline: 2px solid var(--VT-accent);
  outline-offset: 2px;
  border-radius: 8px;
}

.chevron--up {
  transform: rotate(180deg);
}

.layer-details {
  margin: 0;
  padding: 10px 12px;
  list-style: none;
  display: flex;
  flex-direction: column;
  gap: 8px;
  background: var(--VT-surface);
  border: 1px solid var(--VT-border);
  border-radius: 10px;
}

.layer-row {
  display: flex;
  align-items: baseline;
  gap: 10px;
  font-size: 13px;
}

.layer-name {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-weight: 700;
  color: var(--VT-text);
  flex-shrink: 0;
  min-width: 64px;
}

.layer-name--ok svg {
  color: var(--VT-success);
}
.layer-name--warning svg {
  color: var(--VT-warning);
}
.layer-name--danger svg {
  color: var(--VT-danger);
}
.layer-name--neutral svg {
  color: var(--VT-secondary);
}

.layer-value {
  color: var(--VT-text);
}

.layer-detail {
  display: block;
  margin-top: 2px;
  color: var(--VT-text-secondary);
  font-size: 12px;
  line-height: 1.5;
}

@media (prefers-reduced-motion: reduce) {
  .chevron--up {
    transition: none;
  }
}
</style>
