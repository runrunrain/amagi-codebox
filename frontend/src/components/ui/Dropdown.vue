<template>
  <!--
    真正可用的下拉选择组件（苹果风）。
    两种模式：
      1. options + modelValue（v-model 受控，标准 select 行为）
      2. 无 options（兼容旧调用方：作为带 caret 的视觉容器，slot 内自定义触发器）

    菜单渲染：Teleport 到 document.body + position:fixed，
    彻底脱离祖先 overflow 裁剪（AppShell 主滚动容器 overflow:auto /
    壳层 overflow:hidden 曾导致列表尾部行的菜单被裁掉）。
  -->
  <div v-if="options.length > 0" class="dropdown-root" :class="{ disabled }" ref="rootEl">
    <button
      type="button"
      class="dropdown-trigger"
      :disabled="disabled"
      @click="open = !open"
    >
      <span :class="['trigger-label', { placeholder: !hasSelection }]">{{ displayLabel }}</span>
      <span class="caret">▾</span>
    </button>
    <Teleport to="body">
      <transition :name="dropUp ? 'dropdown-fade-up' : 'dropdown-fade'">
        <ul
          v-if="open"
          ref="menuEl"
          class="dropdown-menu"
          role="listbox"
          :style="menuStyle"
        >
          <li
            v-for="opt in options"
            :key="opt.value"
            :class="['dropdown-item', { selected: opt.value === modelValue }]"
            role="option"
            :aria-selected="opt.value === modelValue"
            @click="choose(opt)"
          >
            <span class="item-label">{{ opt.label }}</span>
            <svg v-if="opt.value === modelValue" class="item-check" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
  <polyline points="20 6 9 17 4 12"/>
</svg>
          </li>
        </ul>
      </transition>
    </Teleport>
  </div>
  <!-- 兼容旧调用：作为视觉容器，slot 内容为触发器 -->
  <div
    v-else
    :class="{ dropdown: true, disabled }"
    @click="$emit('click', $event)"
  >
    <slot />
    <span v-if="showCaret" class="caret">▾</span>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onBeforeUnmount, watch } from 'vue';

export interface DropdownOption {
  value: string;
  label: string;
  disabled?: boolean;
}

interface Props {
  /** 受控值（v-model）。为空时进入兼容模式 */
  modelValue?: string;
  /** 选项列表；非空时启用标准 select 模式 */
  options?: DropdownOption[];
  placeholder?: string;
  disabled?: boolean;
  /** 兼容旧调用：是否显示 caret */
  showCaret?: boolean;
}

const props = withDefaults(defineProps<Props>(), {
  modelValue: '',
  options: () => [],
  placeholder: '请选择',
  disabled: false,
  showCaret: true,
});

const emit = defineEmits<{
  'update:modelValue': [value: string];
  click: [event: MouseEvent];
}>();

const open = ref(false);
const rootEl = ref<HTMLElement | null>(null);
const menuEl = ref<HTMLElement | null>(null);

/** 向上展开（空间不足时翻转方向） */
const dropUp = ref(false);
/** teleport 菜单的 fixed 定位样式 */
const menuStyle = ref<Record<string, string>>({});

/** 菜单最大高度上限（px） */
const MENU_MAX_HEIGHT = 320;
/** 菜单与触发器的间距（px） */
const MENU_GAP = 4;
/** 菜单与视口边缘的最小留白（px） */
const VIEWPORT_MARGIN = 8;
/** 菜单最小宽度：与 .dropdown-trigger 的 min-width 保持一致 */
const MENU_MIN_WIDTH = 140;

const hasSelection = computed(
  () => !!props.modelValue && props.options.some((o) => o.value === props.modelValue)
);

const displayLabel = computed(() => {
  const matched = props.options.find((o) => o.value === props.modelValue);
  return matched ? matched.label : props.placeholder;
});

function choose(opt: DropdownOption) {
  if (opt.disabled) return;
  emit('update:modelValue', opt.value);
  open.value = false;
}

/**
 * 依据触发器在视口中的位置计算菜单 fixed 定位。
 * 返回 false 表示触发器已滚出视口（调用方应直接关闭菜单）。
 */
function updatePosition(): boolean {
  const root = rootEl.value;
  if (!root) return false;
  const rect = root.getBoundingClientRect();
  const vh = window.innerHeight;
  const vw = window.innerWidth;

  // 触发器滚出视口（滚动发生在 AppShell 内层容器，捕获阶段重定位后判定）
  if (rect.bottom <= 0 || rect.top >= vh) return false;

  const spaceBelow = vh - rect.bottom - VIEWPORT_MARGIN;
  const spaceAbove = rect.top - VIEWPORT_MARGIN;
  // 下方空间足够（≥ 上限）仍默认向下；否则取更大的一侧
  const up = spaceBelow < MENU_MAX_HEIGHT && spaceAbove > spaceBelow;
  dropUp.value = up;

  const available = Math.max(0, up ? spaceAbove : spaceBelow);
  const maxHeight = Math.min(MENU_MAX_HEIGHT, available);
  const width = Math.max(rect.width, MENU_MIN_WIDTH);
  // 宽度保底后可能超出右边缘，向左钳制（左侧越界同理）
  const left = Math.min(Math.max(rect.left, VIEWPORT_MARGIN), Math.max(vw - width - VIEWPORT_MARGIN, VIEWPORT_MARGIN));

  const style: Record<string, string> = {
    left: `${left}px`,
    width: `${width}px`,
    maxHeight: `${maxHeight}px`,
  };
  if (up) {
    style.bottom = `${vh - rect.top + MENU_GAP}px`;
  } else {
    style.top = `${rect.bottom + MENU_GAP}px`;
  }
  menuStyle.value = style;
  return true;
}

/** 滚动/缩放时重定位；触发器已滚出视口则直接关闭 */
function onReposition() {
  if (!open.value) return;
  if (!updatePosition()) open.value = false;
}

function addRepositionListeners() {
  // capture=true：滚动发生在 AppShell 内层 overflow 容器，不捕获收不到
  window.addEventListener('scroll', onReposition, true);
  window.addEventListener('resize', onReposition);
}

function removeRepositionListeners() {
  window.removeEventListener('scroll', onReposition, true);
  window.removeEventListener('resize', onReposition);
}

function onDocClick(e: MouseEvent) {
  const target = e.target as Node;
  // teleport 后菜单不在 root 内：包含性判断需覆盖触发器 root + 菜单元素
  if (rootEl.value?.contains(target)) return;
  if (menuEl.value?.contains(target)) return;
  open.value = false;
}

function onKey(e: KeyboardEvent) {
  if (e.key === 'Escape') open.value = false;
}

onMounted(() => {
  document.addEventListener('mousedown', onDocClick);
  document.addEventListener('keydown', onKey);
});
onBeforeUnmount(() => {
  document.removeEventListener('mousedown', onDocClick);
  document.removeEventListener('keydown', onKey);
  removeRepositionListeners();
});

// 打开时定位并挂监听；关闭时清理（Teleport 的 body 节点随 v-if 卸载自动移除）
watch(open, (val) => {
  if (val) {
    updatePosition();
    addRepositionListeners();
  } else {
    removeRepositionListeners();
  }
});

// 切换 disabled 时收起菜单
watch(
  () => props.disabled,
  (val) => {
    if (val) open.value = false;
  }
);
</script>

<style scoped>
/* ---- 标准 select 模式 ---- */
.dropdown-root {
  position: relative;
  display: inline-flex;
  flex-direction: column;
  font-size: 14px;
}

.dropdown-trigger {
  display: inline-flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  background: var(--control);
  border: 1px solid var(--separator);
  border-radius: 8px;
  padding: 7px 11px;
  cursor: pointer;
  color: var(--label);
  font-family: inherit;
  font-size: 13px;
  min-width: 140px;
  transition: background 0.12s, border-color 0.12s;
  text-align: left;
}

.dropdown-trigger:hover:not(:disabled) {
  background: var(--controlHover);
}

.dropdown-trigger:disabled,
.dropdown-root.disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.trigger-label {
  flex: 1;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.trigger-label.placeholder {
  color: var(--tertiary);
}

.caret {
  color: var(--tertiary);
  font-size: 10px;
  flex-shrink: 0;
}

/*
 * 菜单经 Teleport 挂到 document.body，position:fixed（left/top/bottom/width/max-height 由 JS 内联注入）。
 * z-index=2000：高于 Dialog(1000)，保证对话框内的下拉不被遮挡；
 * 低于 GitPanel(3000)/Toast(9999)/TerminalContextMenu(10000)，全局提示与终端右键菜单仍居顶。
 */
.dropdown-menu {
  position: fixed;
  z-index: 2000;
  list-style: none;
  margin: 0;
  padding: 4px;
  background: #FFFFFF;
  --card: #FFFFFF;
  --control: #F2F2F5;
  --controlHover: #E5E5EA;
  border: 1px solid var(--separator);
  border-radius: 9px;
  box-shadow: 0 8px 24px rgba(0, 0, 0, 0.16);
  overflow-y: auto;
}

.dropdown-item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 7px 9px;
  border-radius: 6px;
  cursor: pointer;
  font-size: 13px;
  color: var(--label);
  transition: background 0.1s;
}

.dropdown-item:hover {
  background: #F2F2F5;
}

.dropdown-item.selected {
  background: rgba(0, 122, 255, 0.08);
  color: var(--accent);
  font-weight: 500;
}

.item-label {
  flex: 1;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.item-check {
  color: var(--accent);
  width: 14px;
  height: 14px;
  flex-shrink: 0;
}

/* 向下展开：菜单在触发器下方，入场自上方落入 */
.dropdown-fade-enter-active,
.dropdown-fade-leave-active,
.dropdown-fade-up-enter-active,
.dropdown-fade-up-leave-active {
  transition: opacity 0.12s, transform 0.12s;
}

.dropdown-fade-enter-from,
.dropdown-fade-leave-to {
  opacity: 0;
  transform: translateY(-4px);
}

/* 向上展开：方向反向，入场自下方升起 */
.dropdown-fade-up-enter-from,
.dropdown-fade-up-leave-to {
  opacity: 0;
  transform: translateY(4px);
}

@media (prefers-reduced-motion: reduce) {
  .dropdown-fade-enter-active,
  .dropdown-fade-leave-active,
  .dropdown-fade-up-enter-active,
  .dropdown-fade-up-leave-active {
    transition: none;
  }
}

/* ---- 兼容旧容器模式 ---- */
.dropdown {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  background: var(--control);
  border-radius: 7px;
  padding: 6px 10px;
  cursor: pointer;
  font-size: 14px;
  color: var(--label);
  transition: background 0.12s;
  user-select: none;
}

.dropdown:hover:not(.disabled) {
  background: var(--controlHover);
}

.dropdown.disabled {
  opacity: 0.5;
  cursor: not-allowed;
}
</style>
