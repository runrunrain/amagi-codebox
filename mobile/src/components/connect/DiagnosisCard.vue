<script setup lang="ts">
/**
 * DiagnosisCard — PG-01 连接诊断卡片（AC-23：分类文案 + 可执行动作，禁笼统失败）
 * 四类分类（E-01/E-02 家族）：
 *   net-unreachable     网络不可达
 *   service-down        服务未开启或版本不兼容
 *   unpaired            未配对（引导完成配对，不是错误红）
 *   authorized          已授权可进入（成功态）
 * 三通道呈现：固定语义图标 + 文字 + VT 语义色；颜色绝不单独承载含义。
 */
import { computed } from 'vue';

export type DiagnosisKind =
  | 'checking'
  | 'authorized'
  | 'unpaired'
  | 'net-unreachable'
  | 'service-down'
  | 'unknown';

interface Props {
  kind: DiagnosisKind;
  /** 补充细节（如服务端 message / 版本信息），可为空。 */
  detail?: string;
  /** 错误时是否正在重试。 */
  retrying?: boolean;
}

const props = withDefaults(defineProps<Props>(), { detail: '', retrying: false });

const emit = defineEmits<{
  (e: 'retry'): void;
}>();

interface Copy {
  title: string;
  description: string;
  actionLabel: string | null;
  tone: 'accent' | 'success' | 'warning' | 'danger' | 'secondary';
  icon: 'signal' | 'key' | 'check' | 'warning' | 'spinner';
}

const COPY: Record<DiagnosisKind, Copy> = {
  checking: {
    title: '正在诊断连接',
    description: '正在确认宿主服务与这台设备的授权状态…',
    actionLabel: null,
    tone: 'secondary',
    icon: 'spinner',
  },
  authorized: {
    title: '已授权，可以进入',
    description: '这台设备已完成配对，连接与授权均正常。',
    actionLabel: null,
    tone: 'success',
    icon: 'check',
  },
  unpaired: {
    title: '这台设备还没有配对',
    description: '服务可以到达。请使用桌面端展示的二维码或配对码完成短时配对。',
    actionLabel: null,
    tone: 'accent',
    icon: 'key',
  },
  'net-unreachable': {
    title: '网络不可达',
    description: '无法联系到宿主。请确认手机与桌面在同一局域网，并核对地址是否正确。',
    actionLabel: '重试诊断',
    tone: 'danger',
    icon: 'signal',
  },
  'service-down': {
    title: '远程服务未开启或版本不兼容',
    description: '地址可以到达，但远程服务没有响应。请到桌面端「设置 › 远程访问」开启服务；若刚升级，请确认两端版本匹配。',
    actionLabel: '重试诊断',
    tone: 'warning',
    icon: 'warning',
  },
  unknown: {
    title: '收到无法识别的响应',
    description: '宿主返回了无法分类的结果。请重试；若持续出现，请检查两端版本。',
    actionLabel: '重试诊断',
    tone: 'warning',
    icon: 'warning',
  },
};

const copy = computed(() => COPY[props.kind]);
</script>

<template>
  <section class="diagnosis-card" :class="`diagnosis-card--${copy.tone}`" aria-live="polite">
    <span class="diagnosis-icon" aria-hidden="true">
      <!-- 信号波：连接层 -->
      <svg v-if="copy.icon === 'signal'" width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round">
        <path d="M5 12.55a11 11 0 0 1 14.08 0" />
        <path d="M8.53 16.11a6 6 0 0 1 6.95 0" />
        <line x1="12" y1="20" x2="12.01" y2="20" />
        <line x1="2" y1="2" x2="22" y2="22" v-if="false" />
      </svg>
      <!-- 钥匙：授权层 -->
      <svg v-else-if="copy.icon === 'key'" width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
        <path d="M21 2l-2 2m-7.61 7.61a5.5 5.5 0 1 1-7.778 7.778 5.5 5.5 0 0 1 7.777-7.777zm0 0L15.5 7.5m0 0l3 3L22 7l-3-3m-3.5 3.5L19 4" />
      </svg>
      <!-- 对勾：成功 -->
      <svg v-else-if="copy.icon === 'check'" width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round">
        <polyline points="20 6 9 17 4 12" />
      </svg>
      <!-- 三角警告 -->
      <svg v-else-if="copy.icon === 'warning'" width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
        <path d="M10.29 3.86L1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z" />
        <line x1="12" y1="9" x2="12" y2="13" />
        <line x1="12" y1="17" x2="12.01" y2="17" />
      </svg>
      <!-- spinner -->
      <svg v-else width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" class="spin">
        <path d="M21 12a9 9 0 1 1-6.219-8.56" />
      </svg>
    </span>
    <div class="diagnosis-body">
      <h2 class="diagnosis-title">{{ copy.title }}</h2>
      <p class="diagnosis-description">{{ copy.description }}</p>
      <p v-if="detail" class="diagnosis-detail">{{ detail }}</p>
      <button
        v-if="copy.actionLabel"
        type="button"
        class="diagnosis-action"
        :disabled="retrying"
        @click="emit('retry')"
      >
        {{ retrying ? '正在重试…' : copy.actionLabel }}
      </button>
    </div>
  </section>
</template>

<style scoped>
.diagnosis-card {
  display: flex;
  gap: 12px;
  align-items: flex-start;
  padding: 16px;
  background: var(--VT-surface);
  border: 1px solid var(--VT-border);
  border-left: 4px solid var(--VT-secondary);
  border-radius: 10px;
}

.diagnosis-card--accent { border-left-color: var(--VT-accent); }
.diagnosis-card--accent .diagnosis-icon { color: var(--VT-accent); }
.diagnosis-card--success { border-left-color: var(--VT-success); }
.diagnosis-card--success .diagnosis-icon { color: var(--VT-success); }
.diagnosis-card--warning { border-left-color: var(--VT-warning); }
.diagnosis-card--warning .diagnosis-icon { color: var(--VT-warning); }
.diagnosis-card--danger { border-left-color: var(--VT-danger); }
.diagnosis-card--danger .diagnosis-icon { color: var(--VT-danger); }
.diagnosis-card--secondary .diagnosis-icon { color: var(--VT-secondary); }

.diagnosis-icon {
  flex-shrink: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  width: 44px;
  height: 44px;
}

.diagnosis-body {
  flex: 1;
  min-width: 0;
}

.diagnosis-title {
  margin: 0 0 4px;
  font-size: 16px;
  font-weight: 700;
  color: var(--VT-text);
}

.diagnosis-description {
  margin: 0;
  font-size: 14px;
  line-height: 1.55;
  color: var(--VT-text);
}

.diagnosis-detail {
  margin: 6px 0 0;
  font-size: 12px;
  line-height: 1.5;
  color: var(--VT-text-secondary);
  word-break: break-word;
}

.diagnosis-action {
  margin-top: 12px;
  min-height: 44px;
  min-width: 44px;
  padding: 0 20px;
  border: 1px solid var(--VT-border-strong);
  border-radius: 8px;
  background: transparent;
  color: var(--VT-text);
  font-size: 15px;
  font-weight: 600;
  cursor: pointer;
}

.diagnosis-action:disabled {
  color: var(--VT-text-disabled);
  border-color: var(--VT-border);
  cursor: not-allowed;
}

.diagnosis-action:focus-visible {
  outline: 2px solid var(--VT-accent);
  outline-offset: 2px;
}

@media (hover: hover) {
  .diagnosis-action:hover:not(:disabled) {
    background: var(--VT-surface-raised);
  }
}

.spin {
  animation: diagnosis-rotate 1s linear infinite;
}

@keyframes diagnosis-rotate {
  to { transform: rotate(360deg); }
}

@media (prefers-reduced-motion: reduce) {
  .spin {
    animation: none;
  }
}
</style>
