<template>
  <div class="composer">
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

    <!-- 行内提示区（单行占位，避免布局跳动）：错误分流 > 超限 > busy 排队说明 -->
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
  uninstallBridge?.()
  uninstallBridge = null
  composer.dispose()
})
</script>

<style scoped>
.composer {
  flex: none;
  display: flex;
  flex-direction: column;
  gap: 6px;
  padding: 8px 12px 10px;
  border-top: 1px solid var(--plane-border);
}

.composer-input {
  resize: vertical;
  min-height: 56px;
  max-height: 40vh;
  padding: 8px 10px;
  font: inherit;
  color: var(--plane-text);
  background: var(--plane-card);
  border: 1px solid var(--plane-border);
  border-radius: var(--radius-sm);
}

.composer-input:focus-visible {
  outline: none;
  border-color: var(--plane-accent);
}

.composer-input::placeholder {
  color: var(--plane-text-dim);
}

.composer-meta {
  min-height: 18px;
  font-size: 12px;
  line-height: 18px;
}

.composer-error {
  color: var(--plane-danger);
}

.composer-warn {
  color: var(--plane-danger);
}

.composer-note {
  color: var(--plane-text-dim);
}

.composer-actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
}

.composer-btn {
  padding: 6px 18px;
  font-size: 13px;
  font-family: inherit;
  color: var(--plane-text);
  background: var(--plane-card);
  border: 1px solid var(--plane-border);
  border-radius: var(--radius-sm);
  cursor: pointer;
}

.composer-btn:hover:not(:disabled) {
  border-color: var(--plane-accent);
  color: var(--plane-accent);
}

.composer-btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.composer-btn.is-interrupt {
  color: var(--plane-danger);
  border-color: var(--plane-danger);
}

.composer-btn.is-interrupt:hover:not(:disabled) {
  color: var(--plane-danger);
  border-color: var(--plane-danger);
}
</style>
