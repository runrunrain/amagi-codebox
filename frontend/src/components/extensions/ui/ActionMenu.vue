<!-- 契约 §4 #11：「添加 ▾」菜单。受控展开状态（内部 open ref，经触发器/choose 收起）
     + Teleport 到 body + position:fixed 弹层（参照 ui/Dropdown.vue 的 Teleport 模式，
     但为菜单行为、不渲染选中项；不改 ui/*）。node/SSR 环境无 window/document，
     定位与全局监听均做环境守卫，open 仅在用户点击（客户端）时置真。
     可测试性契约：expose choose(key)（emit select 并收起）。 -->
<template>
  <div ref="rootEl" class="action-menu">
    <AppButton variant="ghost" size="small" :disabled="disabled" @click="toggleOpen">
      <span>{{ triggerText }}</span>
      <span class="am-caret" aria-hidden="true">▾</span>
    </AppButton>
    <Teleport to="body">
      <ul v-if="open" class="am-menu" role="menu" :style="menuStyle">
        <li v-for="item in items" :key="item.key" role="none">
          <button
            type="button"
            role="menuitem"
            class="am-item"
            :disabled="item.disabled"
            @click="choose(item.key)"
          >
            {{ item.label }}
          </button>
        </li>
      </ul>
    </Teleport>
  </div>
</template>

<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue';
import AppButton from '../../ui/AppButton.vue';
import type { ActionMenuItem } from '../pluginViewTypes';

interface Props {
  triggerText: string;
  items?: ActionMenuItem[];
  disabled?: boolean;
}

const props = withDefaults(defineProps<Props>(), {
  items: () => [],
  disabled: false,
});

const emit = defineEmits<{
  select: [key: string];
}>();

const open = ref(false);
const rootEl = ref<HTMLElement | null>(null);
const menuStyle = ref<Record<string, string>>({});

function toggleOpen() {
  if (!props.disabled) {
    open.value = !open.value;
  }
}

function choose(key: string) {
  const item = props.items.find((entry) => entry.key === key);
  if (item?.disabled) return;
  emit('select', key);
  open.value = false;
}

defineExpose({ choose });

/** 菜单与触发器的间距（px） */
const MENU_GAP = 4;
/** z-index=2000：与 ui/Dropdown 菜单同级（高于 Dialog(1000)） */
const MENU_Z_INDEX = '2000';

function updatePosition() {
  if (typeof window === 'undefined' || !rootEl.value) return;
  const rect = rootEl.value.getBoundingClientRect();
  menuStyle.value = {
    position: 'fixed',
    left: `${rect.left}px`,
    top: `${rect.bottom + MENU_GAP}px`,
    minWidth: '140px',
    zIndex: MENU_Z_INDEX,
  };
}

function onDocClick(event: MouseEvent) {
  const target = event.target as Node;
  // 触发器区点击交给 toggleOpen，不在此处关闭
  if (rootEl.value?.contains(target)) return;
  // teleport 后菜单不在 root 内：菜单内点击交给 choose，不视为外部关闭
  if (target instanceof Element && target.closest('.am-menu')) return;
  open.value = false;
}

function onKey(event: KeyboardEvent) {
  if (event.key === 'Escape') {
    open.value = false;
  }
}

function addListeners() {
  if (typeof document === 'undefined') return;
  document.addEventListener('mousedown', onDocClick);
  document.addEventListener('keydown', onKey);
}

function removeListeners() {
  if (typeof document === 'undefined') return;
  document.removeEventListener('mousedown', onDocClick);
  document.removeEventListener('keydown', onKey);
}

watch(open, (value) => {
  if (value) {
    updatePosition();
    addListeners();
  } else {
    removeListeners();
  }
});

watch(
  () => props.disabled,
  (value) => {
    if (value) {
      open.value = false;
    }
  },
);

onBeforeUnmount(() => {
  removeListeners();
});
</script>

<style scoped>
.action-menu {
  display: inline-flex;
  position: relative;
}

.am-caret {
  font-size: 10px;
  color: inherit;
  margin-left: 2px;
}

.am-menu {
  position: fixed;
  z-index: 2000;
  list-style: none;
  margin: 0;
  padding: 4px;
  background: var(--popover, #ffffff);
  border: 1px solid var(--separator);
  border-radius: 9px;
  box-shadow: var(--shadow-pop, 0 8px 24px rgba(0, 0, 0, 0.16));
}

.am-item {
  display: block;
  width: 100%;
  border: none;
  background: none;
  text-align: left;
  padding: 7px 9px;
  border-radius: 6px;
  cursor: pointer;
  font-size: 13px;
  color: var(--label);
  font-family: inherit;
  transition: background 0.1s;
}

.am-item:hover:not(:disabled) {
  background: var(--control);
}

.am-item:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}
</style>
