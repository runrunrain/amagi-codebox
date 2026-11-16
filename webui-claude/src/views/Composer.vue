<template>
  <div class="input-dock">
    <div class="input-card">
      <textarea
        ref="textareaRef"
        class="composer-input"
        rows="3"
        :value="composer.text"
        :readonly="composer.mode === 'ended'"
        :placeholder="placeholder"
        aria-label="会话输入框"
        @input="onInput"
        @keydown.enter="onEnter"
        @keydown.escape="onEscape"
      ></textarea>

      <!-- 底部工具栏（pi input-foot 同构）：左 状态文字（单行占位防跳动：
           错误分流 > 超限 > busy 排队说明），右 按钮组 -->
      <div class="input-foot">
        <div class="composer-meta">
          <span v-if="composer.errorText" class="composer-error" role="alert">{{
            composer.errorText
          }}</span>
          <span v-else-if="composer.overLimit" class="composer-warn" role="alert">
            内容超过 128 KiB 上限，无法发送
          </span>
          <span v-else-if="composer.mode === 'busy'" class="composer-note">
            Claude 生成中——Enter 发送将排队，Esc 中断
          </span>
        </div>

        <div class="composer-actions">
          <!-- busy：主按钮中断 + 次按钮仍可发送（claude TUI 原生排队语义）；
               idle：主按钮发送；ended：均禁用。 -->
          <template v-if="composer.mode === 'busy'">
            <button
              type="button"
              class="composer-btn is-interrupt"
              :disabled="!composer.canInterrupt"
              @click="composer.interrupt()"
            >
              中断
            </button>
            <button
              type="button"
              class="composer-btn is-send"
              :disabled="!composer.canSubmit"
              title="Claude 正在生成，发送的消息将排队"
              @click="composer.submit()"
            >
              发送（将排队）
            </button>
          </template>
          <template v-else>
            <button
              type="button"
              class="composer-btn is-send"
              :disabled="!composer.canSubmit"
              @click="composer.submit()"
            >
              发送
            </button>
          </template>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
/**
 * Composer.vue — 输入面薄视图（design §7.2，t8）。
 *
 * 全部逻辑在 views/composerState.ts（createComposer 控制器：状态机三态/
 * 提交字节流/防抖草稿/错误分流/键位裁决——单测锚在那侧）；本组件只做：
 *   - 模板绑定（textarea + 按钮组 + 行内提示）；
 *   - 挂载恢复草稿、卸载 dispose；
 *   - 安装宿主桥（host-bridge.ts）：insert-input 追加进本输入框并聚焦，
 *     a[target=_blank] 点击上抛 open-url。桥凭证取自身 fragment（#/t=），
 *     与 props.token（API Bearer）同源但独立解析——接收端守卫以自身
 *     fragment 为准（冻结契约）。
 *
 * Enter 提交 / Shift+Enter 换行 / busy 态 Esc 中断在 composerKeyEvent
 * 裁决；preventDefault 在裁决通过后才调用（输入法组合态的 Enter 不拦截）。
 */
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import type { PlaneLocation } from '../data/basePath'
import { browserTransport, extractPlaneToken } from '../data/httpSource'
import type { Transport } from '../data/source'
import { installHostBridge } from '../host-bridge'
import { composerKeyEvent, createComposer, type Composer } from './composerState'

const props = withDefaults(
  defineProps<{
    /** 平面 capability token（API Bearer 用）。 */
    token: string
    /** 平面忙闲（doc.busy）：true 时主按钮变「中断」。 */
    busy?: boolean
    /** 会话已结束：输入禁用。 */
    ended?: boolean
    /** IO 缝注入（测试/开发态）；缺省 browserTransport()。 */
    transport?: Transport
    /** 位置面注入；缺省 window.location（双 base 推导输入）。 */
    loc?: PlaneLocation
  }>(),
  { busy: false, ended: false },
)

const textareaRef = ref<HTMLTextAreaElement | null>(null)
let uninstallBridge: (() => void) | null = null

const composer: Composer = createComposer({
  transport: props.transport ?? browserTransport(),
  loc: props.loc ?? window.location,
  // getter 而非值捕获：App.vue 在 onMounted 才给 planeToken 赋值，本组件
  // setup 先于父 mounted 执行（Vue 挂载顺序），值捕获会定格为空串——
  // 真机「发送失败，请重试」的根因（2026-11-14）。
  token: () => props.token,
  busy: () => props.busy === true,
  ended: () => props.ended === true,
  focusInput: () => textareaRef.value?.focus(),
})

const placeholder = computed(() =>
  composer.mode === 'ended' ? '会话已结束' : '输入消息，Enter 发送（Shift+Enter 换行）',
)

function onInput(e: Event): void {
  composer.setText((e.target as HTMLTextAreaElement).value)
}

/** 自动增高（pi 同规格）：高度随内容双向伸缩；min/max 由 CSS 钳制
 *（最小 3 行、最大 8 行，超出转内部滚动）。文本变化来源除键入外还有
 * 草稿恢复与宿主桥插入，统一经 watch 驱动。 */
function autoGrow(): void {
  const el = textareaRef.value
  if (!el) return
  el.style.height = 'auto'
  el.style.height = `${el.scrollHeight}px`
}

const stopGrowWatch = watch(
  () => composer.text,
  () => autoGrow(),
  { flush: 'post' },
)

function onEnter(e: KeyboardEvent): void {
  if (composerKeyEvent(e, composer.mode) !== 'submit') return
  e.preventDefault()
  void composer.submit()
}

function onEscape(e: KeyboardEvent): void {
  if (composerKeyEvent(e, composer.mode) !== 'interrupt') return
  e.preventDefault()
  void composer.interrupt()
}

// 草稿恢复等 token 就绪：本组件 mounted 先于父 App 的 onMounted（token
// 赋值点），立即恢复会带空 Bearer 401 被静默吞掉。watch immediate 兼容
// 两种时序——token 已就绪立即恢复，未就绪等 planeToken 赋值后一次恢复。
const stopDraftWatch = watch(
  () => props.token,
  (tok) => {
    if (!tok) return
    stopDraftWatch()
    void composer.restoreDraft()
  },
  { immediate: true },
)

onMounted(() => {
  uninstallBridge = installHostBridge(window, {
    getOwnToken: () => extractPlaneToken(window.location.hash),
    onInsertText: (text) => composer.insertText(text),
  })
})

onBeforeUnmount(() => {
  stopDraftWatch() // token 全程未就绪（异常时序）时回收 watcher
  stopGrowWatch()
  uninstallBridge?.()
  uninstallBridge = null
  composer.dispose()
})
</script>

<style scoped>
/* 输入坞（pi input-dock 同构）：流内常驻块，透皮（可读性由 input-card
   面板承担——全宽深底会把宿主皮肤盖成近黑，pi 嵌入模式同款取舍）。 */
.input-dock {
  flex: none;
  display: flex;
  flex-direction: column;
  padding: 8px clamp(24px, 5vw, 48px) 10px;
}

/* 输入卡（pi input-card 同构）：面板化——半透明底 + blur + 发丝线 +
   大圆角 + 阴影，760px 居中限宽（与消息流内容列同轴）。 */
.input-card {
  position: relative;
  box-sizing: border-box;
  width: 100%;
  max-width: 760px;
  margin: 0 auto;
  padding: 8px 10px;
  display: flex;
  flex-direction: column;
  background: var(--plane-panel);
  -webkit-backdrop-filter: blur(var(--plane-blur));
  backdrop-filter: blur(var(--plane-blur));
  border: 1px solid var(--plane-hairline);
  border-radius: var(--radius-card);
  box-shadow: var(--plane-shadow);
}

/* 输入框（pi input-row textarea 同构）：卡内独立圆角控件，比面板略实一档；
   最小 3 行、最大 8 行（1.45em 行高 × 行数 + 20px padding/border），
   JS autoGrow 双向伸缩，超出转内部滚动。 */
.composer-input {
  box-sizing: border-box;
  width: 100%;
  min-height: calc(1.45em * 3 + 20px);
  max-height: calc(1.45em * 8 + 20px);
  resize: none;
  overflow-y: auto;
  padding: 9px 11px;
  font: inherit;
  font-size: 14px;
  line-height: 1.45;
  color: var(--plane-text);
  background: var(--plane-field);
  border: 1px solid var(--plane-hairline);
  border-radius: var(--radius-md);
  outline: none;
}

.composer-input:focus {
  border-color: var(--plane-accent);
  box-shadow: 0 0 0 2px var(--plane-focus-ring);
}

.composer-input:focus-visible {
  outline: none;
}

.composer-input::placeholder {
  color: var(--plane-text-dim);
}

.composer-input[readonly] {
  color: var(--plane-text-dim);
  background: var(--plane-block);
}

/* 底部工具栏（pi input-foot 同构）：内容高 24px，栏上距 6px */
.input-foot {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-top: 6px;
  font-size: 12px;
  min-height: 24px;
}

.composer-meta {
  flex: 1;
  min-width: 0;
  line-height: 18px;
  display: flex;
  align-items: center;
}

.composer-error,
.composer-warn {
  color: var(--plane-danger);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.composer-note {
  color: var(--plane-text-dim);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.composer-actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  flex: none;
}

/* 发送钮（pi #input-send 同构）：accent 实底主操作 */
.composer-btn {
  flex: none;
  min-width: 64px;
  min-height: 24px;
  padding: 2px 12px;
  font-size: 13px;
  font-weight: 600;
  font-family: inherit;
  border-radius: var(--radius-md);
  cursor: pointer;
}

.composer-btn.is-send {
  color: var(--plane-accent-contrast);
  background: var(--plane-accent);
  border: 1px solid var(--plane-accent);
}

.composer-btn.is-send:hover:not(:disabled) {
  background: var(--plane-accent-hover);
}

.composer-btn.is-send:disabled {
  color: var(--plane-text-dim);
  background: var(--plane-block);
  border-color: var(--plane-hairline);
  cursor: not-allowed;
}

/* 中断钮：danger 描边次操作 */
.composer-btn.is-interrupt {
  color: var(--plane-danger);
  background: transparent;
  border: 1px solid var(--plane-danger);
}

.composer-btn.is-interrupt:hover:not(:disabled) {
  background: color-mix(in srgb, var(--plane-danger) 10%, transparent);
}

.composer-btn.is-interrupt:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.composer-btn:focus-visible {
  outline: 2px solid var(--plane-accent);
  outline-offset: 2px;
}
</style>
