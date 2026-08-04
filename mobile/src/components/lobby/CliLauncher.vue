<script setup lang="ts">
/**
 * CliLauncher — PG-02 CLI 启动器（M2-B）
 * ---------------------------------------------------------------------------
 * 契约（Task Contract M2-B / design §5.2 index 4 / §5.4）：
 *   · 四类 frozen CLI 卡片（claudecode/opencode/codex/pi），每类独立图标+文字；
 *   · available=false → 禁用并说明原因（宿主未就绪，不伪装可点）；
 *   · 提交中（launching）禁用防连点；
 *   · 启动失败分类由页面级面板呈现（AC-25），本组件只负责触发。
 * ---------------------------------------------------------------------------
 */
import { CLI_METAS, type CliMeta } from '../../stores/lobby';
import type { CLIAvailability, CLIType } from '../../lib/contract';

const props = defineProps<{
  availability: CLIAvailability[];
  /** 当前正在启动的 CLI（防连点）；null = 空闲。 */
  launching: CLIType | null;
}>();

const emit = defineEmits<{
  launch: [cliType: CLIType];
}>();

function isAvailable(cliType: CLIType): boolean {
  return props.availability.some((a) => a.cliType === cliType && a.available);
}

/** 每类 CLI 的独立图标（图形通道；不依赖颜色区分）。 */
const CLI_ICONS: Record<CLIType, string> = {
  // Claude Code：星号笔触
  claudecode: 'M12 3v18M5.6 7.5l12.8 9M18.4 7.5l-12.8 9',
  // OpenCode：尖括号
  opencode: 'M8 6l-6 6 6 6M16 6l6 6-6 6',
  // Codex：方块内光标
  codex: 'M4 4h16v16H4zM9 9h6M9 13h4',
  // Pi：希腊字母 π 笔触
  pi: 'M7 8h10M9 8c0 4-1 7-2 8M15 8c0 4 .5 6.5 2 8M5 8c0-2 1-3 3-3h8c2 0 3 1 3 3',
};

function cliIcon(meta: CliMeta): string {
  return CLI_ICONS[meta.cliType];
}
</script>

<template>
  <section class="cli-launcher" aria-label="启动新会话">
    <h2 class="launcher-title">启动新会话</h2>
    <div class="launcher-grid">
      <button
        v-for="meta in CLI_METAS"
        :key="meta.cliType"
        type="button"
        class="cli-card"
        :class="{ 'cli-card--disabled': !isAvailable(meta.cliType) }"
        :disabled="!isAvailable(meta.cliType) || launching !== null"
        :aria-disabled="!isAvailable(meta.cliType)"
        @click="emit('launch', meta.cliType)"
      >
        <span class="cli-icon" aria-hidden="true">
          <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
            <path :d="cliIcon(meta)" />
          </svg>
        </span>
        <span class="cli-label">{{ meta.label }}</span>
        <span v-if="launching === meta.cliType" class="cli-state">启动中…</span>
        <span v-else-if="!isAvailable(meta.cliType)" class="cli-state cli-state--unavailable">
          宿主不可用：未安装或未配置
        </span>
        <span v-else class="cli-state">点击启动</span>
      </button>
    </div>
  </section>
</template>

<style scoped>
.cli-launcher {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.launcher-title {
  margin: 0;
  font-size: 15px;
  font-weight: 700;
  color: var(--VT-text);
}

.launcher-grid {
  display: grid;
  grid-template-columns: repeat(2, 1fr);
  gap: 10px;
}

.cli-card {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 6px;
  min-height: 44px;
  padding: 12px 14px;
  border: 1px solid var(--VT-border);
  border-radius: 10px;
  background: var(--VT-surface);
  color: var(--VT-text);
  cursor: pointer;
  text-align: left;
}

.cli-card:active:not(:disabled) {
  background: var(--VT-surface-raised);
}

.cli-card:focus-visible {
  outline: 2px solid var(--VT-accent);
  outline-offset: 2px;
}

.cli-icon {
  color: var(--VT-accent-strong);
}

.cli-label {
  font-size: 14px;
  font-weight: 700;
}

.cli-state {
  font-size: 12px;
  color: var(--VT-text-secondary);
  line-height: 1.4;
}

.cli-state--unavailable {
  color: var(--VT-text-disabled);
}

.cli-card--disabled {
  cursor: not-allowed;
}

.cli-card--disabled .cli-icon {
  color: var(--VT-text-disabled);
}

.cli-card--disabled .cli-label {
  color: var(--VT-text-disabled);
}

@media (hover: hover) {
  .cli-card:hover:not(:disabled) {
    background: var(--VT-surface-raised);
    border-color: var(--VT-border-strong);
  }
}

@media (min-width: 480px) {
  .launcher-grid {
    grid-template-columns: repeat(4, 1fr);
  }
}

@media (prefers-reduced-motion: reduce) {
  .cli-card {
    transition: none;
  }
}
</style>
