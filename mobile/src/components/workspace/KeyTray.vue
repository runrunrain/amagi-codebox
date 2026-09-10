<script setup lang="ts">
/**
 * KeyTray.vue — 终端按键快捷托盘
 * ---------------------------------------------------------------------------
 * 功能与规范：
 *   · 提供 Esc, Tab, ^C, ^D, ^L, Enter, 方向键及 Alt 快捷键（⌥W, ⌥T, ⌥R 等）；
 *   · 权限受控：当 canWrite=false（无控制权或连接中断）时全部禁用，
 *     不扩充任何写入权限（对齐 P-04 语义）；
 *   · 支持 Alt 修饰键轻触切换：高亮激活后，下次按键将带 ESC (\x1b) 前缀；
 *   · 触屏友好：窄视口自动换行多行展示（M4-A 几何契约：所有按键须完整
 *     落在视口内，禁横向滚动裁切——滚动条内按键的 bounding rect 会越界）；
 *   · 契约：带显式 data-testid 供 E2E 与组件测试断言。
 * ---------------------------------------------------------------------------
 */
import { ref } from 'vue';
import { TERMINAL_KEY_DEFS, type SpecialKeyDef } from '../../lib/specialKeys';

const props = defineProps<{
  /** 是否具备输入控制权（由 workspaceStore.canWrite 传入）。 */
  canWrite: boolean;
}>();

const emit = defineEmits<{
  /** 向上层派发待发送的 ANSI 序列。 */
  sendKey: [sequence: string];
}>();

/** Alt 键是否处于单次激活态。 */
const altActive = ref(false);

function handleKeyClick(def: SpecialKeyDef): void {
  if (!props.canWrite) return;

  if (def.kind === 'modifier') {
    altActive.value = !altActive.value;
    return;
  }

  let seq = def.sequence;
  if (!seq) return;

  // 若用户处于 Alt 激活态，且当前键不是已有 Alt 前缀的键（非以 \x1b 开头）
  if (altActive.value) {
    if (!seq.startsWith('\x1b')) {
      seq = `\x1b${seq}`;
    }
    altActive.value = false;
  }

  emit('sendKey', seq);
}
</script>

<template>
  <div
    class="key-tray"
    role="toolbar"
    aria-label="终端按键托盘"
    data-testid="terminal-key-tray"
  >
    <button
      v-for="keyDef in TERMINAL_KEY_DEFS"
      :key="keyDef.id"
      type="button"
      class="key-tray-btn"
      :class="{
        'key-tray-btn--active': keyDef.kind === 'modifier' && altActive,
        'key-tray-btn--arrow': keyDef.kind === 'arrow',
        'key-tray-btn--alt': keyDef.kind === 'alt',
        'key-tray-btn--control': keyDef.kind === 'control',
      }"
      :disabled="!canWrite"
      :aria-label="keyDef.ariaLabel"
      :data-testid="`key-${keyDef.id}`"
      @click="handleKeyClick(keyDef)"
    >
      {{ keyDef.label }}
    </button>
  </div>
</template>

<style scoped>
.key-tray {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px;
  padding: 4px 0 6px;
}

.key-tray-btn {
  flex-shrink: 0;
  /* M4-A 44px 触控目标契约（a11y-m4:367 权威判定）：与主 Composer 按钮同规格，
     不得低于 44×44（P1-A 初版 34px 不达标，Leader 直修）。 */
  min-height: 44px;
  min-width: 44px;
  padding: 4px 8px;
  border: 1px solid var(--VT-border-strong);
  border-radius: 6px;
  background: var(--VT-surface);
  color: var(--VT-text);
  font-size: 13px;
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-weight: 600;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  cursor: pointer;
  user-select: none;
  transition: background-color 0.12s ease, border-color 0.12s ease;
}

@media (hover: hover) {
  .key-tray-btn:hover:not(:disabled) {
    background: var(--VT-surface-raised);
    border-color: var(--VT-accent);
  }
}

.key-tray-btn:focus-visible {
  outline: 2px solid var(--VT-accent);
  outline-offset: 1px;
}

.key-tray-btn:disabled {
  color: var(--VT-text-disabled);
  border-color: var(--VT-border);
  background: var(--VT-surface);
  cursor: not-allowed;
  opacity: 0.6;
}

/* Alt 激活修饰态高亮 */
.key-tray-btn--active {
  background: var(--VT-accent-strong);
  color: var(--VT-canvas);
  border-color: var(--VT-accent-strong);
}

.key-tray-btn--arrow {
  font-size: 14px;
  padding: 2px 8px;
}

.key-tray-btn--control {
  font-weight: 700;
}

.key-tray-btn--alt {
  color: var(--VT-accent-strong);
}

.key-tray-btn--alt:disabled {
  color: var(--VT-text-disabled);
}
</style>
