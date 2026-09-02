<template>
  <Teleport to="body">
    <Transition name="pp-fade">
      <div v-if="visible" class="pp-overlay" @click.self="emitClose" @contextmenu.self.prevent>
        <section
          ref="dialogRef"
          class="pp-dialog"
          role="dialog"
          aria-modal="true"
          aria-labelledby="pp-heading"
          tabindex="-1"
        >
          <header class="pp-header">
            <div class="pp-header-text">
              <h2 id="pp-heading">选择工作路径</h2>
              <p class="pp-subtitle">勾选一个或多个目录，确认后插入当前终端输入框</p>
            </div>
            <button type="button" class="pp-close" aria-label="关闭路径选择器" @click="emitClose">×</button>
          </header>

          <div class="pp-body">
            <!-- 面包屑：当前 root 的全部祖先 + 当前层，可点祖先直达 -->
            <nav v-if="model.listing" class="pp-breadcrumb" aria-label="目录层级">
              <template v-for="(seg, i) in model.breadcrumb" :key="seg.path">
                <button
                  v-if="i < model.breadcrumb.length - 1"
                  type="button"
                  class="pp-crumb"
                  :title="seg.path"
                  @click="model.navigate(seg.path)"
                >{{ seg.name }}</button>
                <span v-else class="pp-crumb pp-crumb--current" :title="seg.path">{{ seg.name }}</span>
                <span v-if="i < model.breadcrumb.length - 1" class="pp-crumb-sep">›</span>
              </template>
              <button
                v-if="model.listing.parent"
                type="button"
                class="pp-up"
                :title="model.listing.parent"
                @click="model.goParent()"
              >上一级</button>
            </nav>

            <!-- 加载态 -->
            <div v-if="model.loading" class="pp-state pp-state--loading">正在读取目录…</div>

            <!-- 错误态：行内提示 + 重试 -->
            <div v-else-if="model.errorMsg" class="pp-state pp-state--error">
              <span class="pp-state-text">{{ model.errorMsg }}</span>
              <button type="button" class="pp-mini-btn" @click="model.retry()">重试</button>
            </div>

            <!-- 当前层目录列表（含展开的子层缩进行） -->
            <ul v-else-if="model.rows.length" class="pp-list" role="listbox" aria-multiselectable="true" aria-label="子目录列表">
              <li
                v-for="row in model.rows"
                :key="row.kind === 'dir' ? row.path : row.kind === 'loading' ? row.key : `${row.path}#error`"
                :class="['pp-row', { 'pp-row--child': row.depth > 0 }]"
                :style="{ '--depth': row.depth }"
              >
                <template v-if="row.kind === 'dir'">
                  <input
                    :id="`pp-check-${row.path}`"
                    type="checkbox"
                    class="pp-check"
                    :checked="model.isSelected(row.path)"
                    :aria-label="`选择目录 ${row.name}`"
                    @change="model.toggleCheck(row.path)"
                  />
                  <button
                    type="button"
                    class="pp-name"
                    :title="row.path"
                    @click="model.navigate(row.path)"
                  >{{ row.name }}</button>
                  <button
                    type="button"
                    class="pp-mini-btn pp-expand"
                    :aria-expanded="row.expanded"
                    @click="model.toggleExpand(row.path)"
                  >{{ row.expanded ? '收起' : '展开' }}</button>
                </template>
                <span v-else-if="row.kind === 'loading'" class="pp-inline-state pp-inline-state--loading">加载中…</span>
                <span v-else class="pp-inline-state pp-inline-state--error">
                  {{ row.message }}
                  <button type="button" class="pp-mini-btn" @click="model.toggleExpand(row.path, true)">重试</button>
                </span>
              </li>
              <li v-if="model.listing?.truncated" class="pp-truncated">已截断至 500 条</li>
            </ul>

            <!-- 空态：当前层没有可见子目录 -->
            <div v-else class="pp-state pp-state--empty">当前目录没有可见的子目录</div>
          </div>

          <footer class="pp-footer">
            <div class="pp-chips" aria-label="已选路径">
              <span v-for="chip in model.selected" :key="chip" class="pp-chip" :title="chip">
                <span class="pp-chip-text">{{ chipLabelOf(chip) }}</span>
                <button
                  type="button"
                  class="pp-chip-x"
                  :aria-label="`移除 ${chip}`"
                  @click="model.toggleCheck(chip)"
                >×</button>
              </span>
            </div>
            <div class="pp-actions">
              <button type="button" class="pp-btn" @click="emitClose">取消</button>
              <button
                type="button"
                class="pp-btn pp-btn--primary"
                :disabled="!model.canConfirm"
                @click="emitConfirm"
              >{{ model.confirmLabel }}</button>
            </div>
          </footer>
        </section>
      </div>
    </Transition>
  </Teleport>
</template>

<script setup lang="ts">
/**
 * PathPickerDialog — 快捷功能「输入工作路径」的目录多选浮层（渲染接线层）。
 *
 * 可测逻辑全部在 pathPickerModel.ts（纯 TS，先例 piConcurrency.ts）：
 * 初始 root = dirname(workDir)、失败降级空串（后端 home 兜底）、面包屑祖先
 * 链、展开下一层、有序勾选。本组件只负责挂载模型 + 渲染 + Esc 关闭 + 焦点。
 */
import { nextTick, onBeforeUnmount, reactive, ref, watch } from 'vue'
import { listDirectories } from '../../api/paths'
import { PathPickerModel, chipLabelOf } from './pathPickerModel'

const props = defineProps<{
  visible: boolean
  /** 当前会话工作目录（用于推导初始 root）。 */
  workDir: string
}>()

const emit = defineEmits<{
  (event: 'close'): void
  (event: 'confirm', paths: string[]): void
}>()

const dialogRef = ref<HTMLElement | null>(null)

// reactive 包装纯模型实例：字段/Set 访问自动响应式，模板直绑。
const model = reactive(new PathPickerModel(listDirectories))

// ---- 打开：重置状态并加载初始 root（workDir 父目录 / home 兜底） ----
watch(
  () => props.visible,
  async (visible) => {
    if (!visible) return
    await model.resetAndLoad(props.workDir)
    await nextTick()
    dialogRef.value?.focus()
  },
  { immediate: true },
)

// ---- Esc 关闭 ----
function onDocumentKeydown(event: KeyboardEvent) {
  if (event.key === 'Escape' && props.visible) emitClose()
}

document.addEventListener('keydown', onDocumentKeydown, true)
onBeforeUnmount(() => {
  document.removeEventListener('keydown', onDocumentKeydown, true)
})

function emitClose() {
  emit('close')
}

function emitConfirm() {
  if (!model.canConfirm) return
  emit('confirm', [...model.selected])
}
</script>

<style scoped>
/* 阻断式居中浮层（对齐 ui/Dialog.vue 的遮罩语言），容器贴合终端区暗色风格 */
.pp-overlay {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.5);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 1000;
  padding: 20px;
}

.pp-dialog {
  width: 100%;
  max-width: 560px;
  max-height: 85vh;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  border: 1px solid rgba(137, 221, 255, 0.24);
  border-radius: 14px;
  background:
    radial-gradient(circle at 12% 0%, rgba(137, 221, 255, 0.16), transparent 32%),
    radial-gradient(circle at 92% 12%, rgba(102, 187, 106, 0.11), transparent 28%),
    #0b1018;
  box-shadow: 0 16px 48px rgba(0, 0, 0, 0.5);
  color: #d9e2ec;
  outline: none;
}

.pp-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 12px 14px;
  border-bottom: 1px solid rgba(58, 74, 94, 0.64);
}

.pp-header-text {
  min-width: 0;
}

.pp-header h2 {
  margin: 0 0 2px;
  color: #edf6ff;
  font-size: 15px;
}

.pp-subtitle {
  margin: 0;
  color: #71869b;
  font-size: 11px;
}

.pp-close {
  flex-shrink: 0;
  width: 26px;
  height: 26px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border: 1px solid #334155;
  border-radius: 999px;
  background: rgba(15, 23, 42, 0.72);
  color: #c8d5e3;
  font-size: 14px;
  line-height: 1;
  cursor: pointer;
}

.pp-close:hover,
.pp-close:focus-visible {
  border-color: #89ddff;
  color: #89ddff;
  outline: none;
}

.pp-body {
  flex: 1;
  min-height: 220px;
  overflow-y: auto;
  padding: 10px 14px 12px;
  display: flex;
  flex-direction: column;
  gap: 8px;
}

/* 面包屑 */
.pp-breadcrumb {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 4px;
  font-size: 12px;
  color: #71869b;
}

.pp-crumb {
  border: none;
  background: none;
  color: #89ddff;
  font-size: 12px;
  cursor: pointer;
  padding: 2px 4px;
  border-radius: 4px;
  max-width: 160px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-family: inherit;
}

.pp-crumb:hover {
  background: rgba(137, 221, 255, 0.12);
}

.pp-crumb--current {
  color: #d9e2ec;
  cursor: default;
  font-weight: 600;
}

.pp-crumb--current:hover {
  background: none;
}

.pp-crumb-sep {
  color: #46586b;
}

.pp-up {
  margin-left: auto;
  border: 1px solid #334155;
  background: rgba(15, 23, 42, 0.72);
  color: #c8d5e3;
  font-size: 11px;
  border-radius: 6px;
  padding: 3px 8px;
  cursor: pointer;
  flex-shrink: 0;
  font-family: inherit;
}

.pp-up:hover {
  border-color: #89ddff;
  color: #89ddff;
}

/* 加载 / 错误 / 空态 */
.pp-state {
  padding: 28px 12px;
  text-align: center;
  color: #71869b;
  font-size: 13px;
}

.pp-state--error {
  color: #ff8a80;
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 10px;
  flex-wrap: wrap;
}

.pp-state-text {
  max-width: 380px;
  overflow-wrap: anywhere;
}

/* 目录列表 */
.pp-list {
  list-style: none;
  margin: 0;
  padding: 0;
}

.pp-row {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 4px 6px;
  border-radius: 8px;
  padding-left: calc(6px + var(--depth, 0) * 22px);
}

.pp-row:hover {
  background: rgba(137, 221, 255, 0.06);
}

.pp-row--child {
  background: rgba(137, 221, 255, 0.03);
}

.pp-check {
  flex-shrink: 0;
  accent-color: #89ddff;
  cursor: pointer;
}

.pp-name {
  flex: 1;
  min-width: 0;
  border: none;
  background: none;
  color: #d9e2ec;
  font-size: 13px;
  text-align: left;
  cursor: pointer;
  padding: 4px 2px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-family: 'Cascadia Code', 'SFMono-Regular', Consolas, monospace;
}

.pp-name:hover {
  color: #89ddff;
}

.pp-mini-btn {
  flex-shrink: 0;
  border: 1px solid #334155;
  background: rgba(15, 23, 42, 0.72);
  color: #c8d5e3;
  font-size: 11px;
  border-radius: 6px;
  padding: 2px 8px;
  cursor: pointer;
  font-family: inherit;
}

.pp-mini-btn:hover {
  border-color: #89ddff;
  color: #89ddff;
}

.pp-expand {
  min-width: 48px;
  text-align: center;
}

.pp-inline-state {
  font-size: 12px;
  padding: 2px 0;
}

.pp-inline-state--loading {
  color: #71869b;
}

.pp-inline-state--error {
  color: #ff8a80;
  display: inline-flex;
  align-items: center;
  gap: 8px;
}

.pp-truncated {
  padding: 6px 6px 2px;
  font-size: 11px;
  color: #71869b;
}

/* 底部：已选 chips + 操作 */
.pp-footer {
  border-top: 1px solid rgba(58, 74, 94, 0.64);
  padding: 10px 14px;
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.pp-chips {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  min-height: 24px;
}

.pp-chip {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  border: 1px solid #334155;
  background: rgba(15, 23, 42, 0.72);
  color: #c8d5e3;
  border-radius: 999px;
  padding: 2px 4px 2px 10px;
  font-size: 12px;
  max-width: 240px;
}

.pp-chip-text {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.pp-chip-x {
  border: none;
  background: none;
  color: #71869b;
  cursor: pointer;
  font-size: 13px;
  line-height: 1;
  padding: 2px 4px;
  border-radius: 999px;
}

.pp-chip-x:hover {
  color: #ff8a80;
}

.pp-actions {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
}

.pp-btn {
  border: 1px solid #334155;
  background: rgba(15, 23, 42, 0.72);
  color: #c8d5e3;
  border-radius: 8px;
  padding: 6px 16px;
  font-size: 13px;
  cursor: pointer;
  font-family: inherit;
}

.pp-btn:hover:not(:disabled) {
  border-color: #89ddff;
  color: #89ddff;
}

.pp-btn--primary {
  background: #89ddff;
  border-color: #89ddff;
  color: #0b1018;
  font-weight: 600;
}

.pp-btn--primary:hover:not(:disabled) {
  background: #a8e6ff;
  color: #0b1018;
}

.pp-btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

/* Transition（对齐 ui/Dialog.vue 节奏） */
.pp-fade-enter-active,
.pp-fade-leave-active {
  transition: opacity 0.2s;
}

.pp-fade-enter-active .pp-dialog,
.pp-fade-leave-active .pp-dialog {
  transition: transform 0.2s, opacity 0.2s;
}

.pp-fade-enter-from,
.pp-fade-leave-to {
  opacity: 0;
}

.pp-fade-enter-from .pp-dialog,
.pp-fade-leave-to .pp-dialog {
  transform: scale(0.95);
  opacity: 0;
}
</style>
