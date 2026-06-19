<template>
  <Teleport to="body">
    <div
      v-show="visible"
      class="ctx-menu"
      :style="{ left: x + 'px', top: y + 'px' }"
      role="menu"
      aria-label="终端操作"
    >
      <div
        class="ctx-item"
        :class="{ 'ctx-disabled': !hasSelection }"
        role="menuitem"
        @mousedown.prevent="onCopy"
      >
        <span>复制</span>
        <span class="ctx-shortcut">Ctrl+Shift+C</span>
      </div>
      <div
        class="ctx-item"
        role="menuitem"
        @mousedown.prevent="emit('paste')"
      >
        <span>粘贴</span>
        <span class="ctx-shortcut">Ctrl+Shift+V</span>
      </div>
      <div class="ctx-sep"></div>
      <div
        class="ctx-item"
        role="menuitem"
        @mousedown.prevent="emit('select-all')"
      >
        <span>全选</span>
        <span class="ctx-shortcut">Ctrl+Shift+A</span>
      </div>
    </div>
    <!-- invisible catcher: clicking anywhere closes the menu -->
    <div
      v-show="visible"
      class="ctx-catcher"
      @mousedown.prevent="emit('close')"
      @contextmenu.prevent="emit('close')"
    ></div>
  </Teleport>
</template>

<script setup lang="ts">
/**
 * TerminalContextMenu — right-click menu for the xterm surface.
 *
 * Rendered via Teleport to body so it is never clipped by the terminal's
 * overflow-hidden container. A full-screen catcher behind the menu closes it
 * on any outside click / right-click. Mirrors the legacy Terminals.vue menu
 * (copy / paste / select-all with keyboard shortcuts).
 */
const emit = defineEmits<{
  (e: 'copy'): void
  (e: 'paste'): void
  (e: 'select-all'): void
  (e: 'close'): void
}>()

const props = defineProps<{
  visible: boolean
  x: number
  y: number
  hasSelection: boolean
}>()

// copy is a no-op when nothing is selected; visual cue handled via ctx-disabled.
function onCopy() {
  if (!props.hasSelection) return
  emit('copy')
}
</script>

<style scoped>
.ctx-menu {
  position: fixed;
  z-index: 10000;
  background: #252a3a;
  border: 1px solid #3a4a5e;
  border-radius: 6px;
  padding: 4px 0;
  min-width: 180px;
  box-shadow: 0 6px 20px rgba(0, 0, 0, 0.45);
}

.ctx-item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 6px 14px;
  font-size: 13px;
  color: #d0d8e0;
  cursor: pointer;
  transition: background 0.1s;
}

.ctx-item:hover {
  background: #3a4a6a;
}

.ctx-item.ctx-disabled,
.ctx-item.ctx-disabled:hover {
  opacity: 0.4;
  cursor: not-allowed;
  background: transparent;
}

.ctx-shortcut {
  color: #667788;
  font-size: 11px;
  margin-left: 24px;
}

.ctx-sep {
  height: 1px;
  background: #3a4a5e;
  margin: 4px 8px;
}

.ctx-catcher {
  position: fixed;
  inset: 0;
  z-index: 9999;
  /* transparent: purely an event surface */
  background: transparent;
}
</style>
