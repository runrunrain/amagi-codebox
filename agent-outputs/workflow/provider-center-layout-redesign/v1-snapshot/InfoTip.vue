<!--
  InfoTip - 可聚焦的说明按钮（toggletip 模式，provider-center-layout-redesign §6.1）

  把「背景 / 优先级 / 来源 / 次要后果」这类不必常驻的说明收进一个 ⓘ 按钮：
  - 键盘：Tab 聚焦只读出 aria-label + aria-describedby 的说明，不自动弹出；
    Enter / Space 打开，Esc 关闭并把焦点还给按钮；
  - 指针：悬停 250ms 打开（hover 来源，离开 120ms 后收起），点击固定（pinned 来源）；
  - 浮层 Teleport 到 body + position:fixed 实时测量定位：祖先（如
    .config-cat-card 的 overflow:hidden）不会裁切它，下方放不下自动向上翻转；
  - 浮层 role=tooltip、不含可聚焦内容；z-index 2000（与 Dropdown 菜单同级）。
-->
<template>
  <span class="infotip">
    <button
      ref="anchor"
      type="button"
      class="infotip-btn"
      :class="{ open }"
      :aria-label="`说明：${label}`"
      :aria-expanded="open"
      :aria-describedby="popId"
      @click="togglePinned"
      @mouseenter="hoverIn"
      @mouseleave="hoverOut"
      @blur="onBlur"
    >
      <svg
        viewBox="0 0 16 16"
        width="14"
        height="14"
        fill="none"
        stroke="currentColor"
        stroke-width="1.4"
        aria-hidden="true"
      >
        <circle cx="8" cy="8" r="6.5" />
        <line x1="8" y1="7.2" x2="8" y2="11.2" stroke-linecap="round" />
        <circle cx="8" cy="4.9" r=".85" fill="currentColor" stroke="none" />
      </svg>
    </button>
    <Teleport to="body">
      <!-- v-show 而非 v-if：aria-describedby 指向的节点始终存在，读屏在按钮获得焦点时即可读到说明 -->
      <div
        v-show="open"
        :id="popId"
        ref="pop"
        class="infotip-pop"
        role="tooltip"
        :style="popStyle"
        @mouseenter="cancelClose"
        @mouseleave="hoverOut"
        @pointerdown="onPopPointerDown"
      >
        <p v-for="(t, i) in paragraphs" :key="i">{{ t }}</p>
        <dl v-if="items && items.length">
          <template v-for="it in items" :key="it.term">
            <dt>{{ it.term }}</dt>
            <dd>{{ it.desc }}</dd>
          </template>
        </dl>
        <slot />
      </div>
    </Teleport>
  </span>
</template>

<script lang="ts">
// 模块级计数器：项目 vue ^3.2.37，不能依赖 3.5 的 useId。
let infoTipSeq = 0;
</script>

<script setup lang="ts">
import { ref, computed, nextTick, onBeforeUnmount } from 'vue';

export interface InfoTipItem {
  term: string;
  desc: string;
}

interface Props {
  /** 主题名，用于按钮 aria-label="说明：{label}" */
  label: string;
  /** 段落；数组时每项一段 */
  text?: string | string[];
  /** 术语对照（三态等），渲染为 <dl> */
  items?: InfoTipItem[];
  /** 首选方向，空间不足时自动翻转 */
  placement?: 'bottom' | 'top';
}

const props = withDefaults(defineProps<Props>(), {
  placement: 'bottom',
});

const popId = `infotip-${++infoTipSeq}`;

const open = ref(false);
const anchor = ref<HTMLButtonElement | null>(null);
const pop = ref<HTMLElement | null>(null);
const popStyle = ref<Record<string, string>>({ left: '0px', top: '0px' });

const paragraphs = computed<string[]>(() => {
  const t = props.text;
  if (Array.isArray(t)) return t.filter(Boolean);
  return t ? [t] : [];
});

/** 打开来源：hover 来源离开即收起；pinned 来源需显式关闭（再次点击 / Esc / 外点 / 失焦） */
type Source = 'hover' | 'pinned';
let source: Source | null = null;
let openTimer = 0;
let closeTimer = 0;
/** 浮层内按下指针（如选中文字）会让按钮失焦，此时不能因 blur 收起 */
let pointerInPop = false;

const HOVER_OPEN_DELAY = 250;
const HOVER_CLOSE_DELAY = 120;
const GAP = 6;
const MARGIN = 8;

function clearOpenTimer() {
  if (openTimer) {
    window.clearTimeout(openTimer);
    openTimer = 0;
  }
}

function clearCloseTimer() {
  if (closeTimer) {
    window.clearTimeout(closeTimer);
    closeTimer = 0;
  }
}

function clearTimers() {
  clearOpenTimer();
  clearCloseTimer();
}

function openWith(src: Source) {
  source = src;
  if (open.value) return;
  // 重置到原点再测量：避免上一次的 left 使浮层按「剩余宽度」被压窄
  popStyle.value = { left: '0px', top: '0px' };
  open.value = true;
  addGlobalListeners();
  void nextTick(updatePosition);
}

function close() {
  clearTimers();
  source = null;
  if (!open.value) return;
  open.value = false;
  removeGlobalListeners();
}

function togglePinned() {
  clearTimers();
  if (open.value && source === 'pinned') close();
  else openWith('pinned');
}

function hoverIn() {
  clearCloseTimer();
  if (open.value || source === 'pinned') return;
  clearOpenTimer();
  openTimer = window.setTimeout(() => {
    openTimer = 0;
    openWith('hover');
  }, HOVER_OPEN_DELAY);
}

function hoverOut() {
  clearOpenTimer();
  if (open.value && source === 'hover') {
    clearCloseTimer();
    closeTimer = window.setTimeout(() => {
      closeTimer = 0;
      if (source === 'hover') close();
    }, HOVER_CLOSE_DELAY);
  }
}

function cancelClose() {
  clearCloseTimer();
}

function onBlur() {
  if (pointerInPop) return;
  if (open.value && source === 'pinned') close();
}

function onPopPointerDown() {
  pointerInPop = true;
  window.setTimeout(() => {
    pointerInPop = false;
  }, 0);
}

/** 在 nextTick 中测量：left 居中于按钮并钳制到视口内；放不下时翻转方向 */
function updatePosition() {
  const a = anchor.value;
  const p = pop.value;
  if (!a || !p || !open.value) return;
  const r = a.getBoundingClientRect();
  const pw = p.offsetWidth;
  const ph = p.offsetHeight;
  const vw = window.innerWidth;
  const vh = window.innerHeight;

  const rawLeft = r.left + r.width / 2 - pw / 2;
  const left = Math.min(Math.max(rawLeft, MARGIN), Math.max(vw - pw - MARGIN, MARGIN));

  const below = r.bottom + GAP;
  const above = r.top - GAP - ph;
  const fitsBelow = below + ph <= vh - MARGIN;
  const fitsAbove = above >= MARGIN;
  let top: number;
  if (props.placement === 'top') top = fitsAbove || !fitsBelow ? above : below;
  else top = fitsBelow || !fitsAbove ? below : above;
  top = Math.min(Math.max(top, MARGIN), Math.max(vh - ph - MARGIN, MARGIN));

  popStyle.value = { left: `${Math.round(left)}px`, top: `${Math.round(top)}px` };
}

// ---- 打开期间的全局监听（对齐 SessionQuotaStrip：关闭 / 卸载时移除） ----

function onDocKeydown(e: KeyboardEvent) {
  if (e.key !== 'Escape' || !open.value) return;
  const wasPinned = source === 'pinned';
  close();
  e.stopPropagation();
  // 仅键盘 / 点击固定的浮层把焦点还给按钮；悬停打开的不抢焦点
  if (wasPinned) anchor.value?.focus();
}

function onDocPointerdown(e: PointerEvent) {
  const t = e.target as Node | null;
  if (!t) return;
  if (anchor.value?.contains(t) || pop.value?.contains(t)) return;
  close();
}

function onReposition() {
  close();
}

function addGlobalListeners() {
  document.addEventListener('keydown', onDocKeydown, true);
  document.addEventListener('pointerdown', onDocPointerdown, true);
  window.addEventListener('scroll', onReposition, true);
  window.addEventListener('resize', onReposition);
}

function removeGlobalListeners() {
  document.removeEventListener('keydown', onDocKeydown, true);
  document.removeEventListener('pointerdown', onDocPointerdown, true);
  window.removeEventListener('scroll', onReposition, true);
  window.removeEventListener('resize', onReposition);
}

onBeforeUnmount(() => {
  clearTimers();
  removeGlobalListeners();
});
</script>

<style scoped>
.infotip {
  display: inline-flex;
  vertical-align: middle;
}

.infotip-btn {
  width: 18px;
  height: 18px;
  padding: 0;
  border: none;
  border-radius: 50%;
  background: transparent;
  color: var(--tertiary);
  display: inline-flex;
  align-items: center;
  justify-content: center;
  cursor: pointer;
  flex: 0 0 auto;
}

.infotip-btn:hover,
.infotip-btn.open {
  color: var(--accent);
}

.infotip-btn:focus-visible {
  outline: 2px solid var(--accent);
  outline-offset: 1px;
  color: var(--accent);
}

.infotip-pop {
  position: fixed;
  z-index: 2000;
  min-width: 160px;
  max-width: 280px;
  padding: 10px 12px;
  background: var(--popover);
  border: 1px solid var(--separator);
  border-radius: var(--radius-md);
  box-shadow: var(--shadow-pop);
  font-size: 12px;
  font-weight: 400;
  line-height: 1.55;
  color: var(--label);
  text-align: left;
  white-space: normal;
}

.infotip-pop p {
  margin: 0;
}

.infotip-pop p + p {
  margin-top: 6px;
}

.infotip-pop dl {
  margin: 6px 0 0;
  display: grid;
  grid-template-columns: auto 1fr;
  gap: 4px 10px;
}

.infotip-pop dl:first-child {
  margin-top: 0;
}

.infotip-pop dt {
  font-weight: 600;
  white-space: nowrap;
}

.infotip-pop dd {
  margin: 0;
  color: var(--secondary);
}
</style>
