<template>
  <Teleport to="body">
    <section
      v-if="visible"
      ref="panelRef"
      class="git-panel"
      role="dialog"
      aria-modal="false"
      aria-labelledby="git-panel-heading"
      :style="panelStyle"
    >
      <header class="panel-header">
        <div class="panel-header-text">
          <h2 id="git-panel-heading">提交 / 推送</h2>
          <p class="panel-subtitle" :title="workDir">{{ workDir || '未知工作目录' }}</p>
        </div>
        <button type="button" class="panel-close" aria-label="关闭提交推送面板" @click="emitClose">×</button>
      </header>

      <div class="panel-body">
        <!-- 加载态 -->
        <div v-if="loading" class="panel-empty panel-empty--loading">正在读取仓库状态…</div>

        <!-- 非 git 仓库空态 -->
        <div v-else-if="!repo || !repo.isGitRepo" class="panel-empty">
          当前工作区不是 Git 仓库，无法使用提交/推送功能。
        </div>

        <template v-else>
          <!-- 手风琴：仓库状态 -->
          <section class="accordion" aria-label="仓库状态">
            <button
              type="button"
              class="accordion-head"
              :aria-expanded="statusExpanded"
              @click="statusExpanded = !statusExpanded"
            >
              <span class="accordion-arrow" :class="{ expanded: statusExpanded }">▸</span>
              <span class="accordion-title">仓库状态</span>
              <span v-if="!statusExpanded" class="accordion-summary">
                {{ repo.branch || '-' }} · 变更 {{ repo.staged + repo.unstaged + repo.untracked }}
              </span>
            </button>
            <div v-if="statusExpanded" class="accordion-content">
              <div class="status-grid">
                <div class="status-item">
                  <span>当前分支</span>
                  <strong>{{ repo.branch || '-' }}</strong>
                </div>
                <div class="status-item">
                  <span>上游分支</span>
                  <strong>{{ repo.upstream || '（无上游）' }}</strong>
                </div>
                <div class="status-item">
                  <span>领先 / 落后</span>
                  <strong>
                    <template v-if="repo.upstream">↑{{ repo.ahead }} / ↓{{ repo.behind }}</template>
                    <template v-else>-</template>
                  </strong>
                </div>
                <div class="status-item">
                  <span>变更</span>
                  <strong>暂存 {{ repo.staged }} · 未暂存 {{ repo.unstaged }} · 未跟踪 {{ repo.untracked }}</strong>
                </div>
                <div v-if="repo.remoteUrl" class="status-item status-item--wide">
                  <span>Remote</span>
                  <strong :title="repo.remoteUrl">{{ repo.remoteUrl }}</strong>
                </div>
              </div>
            </div>
          </section>

          <!-- 手风琴：分支切换 -->
          <section class="accordion" aria-label="分支切换">
            <button
              type="button"
              class="accordion-head"
              :aria-expanded="branchExpanded"
              @click="branchExpanded = !branchExpanded"
            >
              <span class="accordion-arrow" :class="{ expanded: branchExpanded }">▸</span>
              <span class="accordion-title">分支切换</span>
              <span v-if="!branchExpanded" class="accordion-summary">{{ repo.branch || '-' }}</span>
            </button>
            <div v-if="branchExpanded" class="accordion-content">
              <div class="section-row">
                <label class="section-label" for="git-branch-select">切换分支</label>
                <select
                  id="git-branch-select"
                  class="branch-select"
                  :value="pendingBranch || repo.branch"
                  :disabled="switching || branches.length === 0"
                  @change="onBranchSelect"
                >
                  <option
                    v-for="b in branches"
                    :key="b.name"
                    :value="b.name"
                  >{{ b.name }}{{ b.current ? '（当前）' : '' }}</option>
                </select>
                <button
                  type="button"
                  class="btn ghost"
                  :disabled="loading || anyActionRunning"
                  @click="refresh"
                >刷新</button>
              </div>
              <div v-if="pendingBranch" class="branch-confirm">
                <span>切换到分支 <strong>{{ pendingBranch }}</strong>？未提交的变更会随之携带。</span>
                <div class="branch-confirm-actions">
                  <button type="button" class="btn ghost" :disabled="switching" @click="pendingBranch = ''">取消</button>
                  <button type="button" class="btn primary" :disabled="switching" @click="confirmSwitch">
                    {{ switching ? '切换中…' : '确认切换' }}
                  </button>
                </div>
              </div>
            </div>
          </section>

          <!-- 手风琴：提交信息（默认展开，主操作路径） -->
          <section class="accordion" aria-label="提交信息">
            <button
              type="button"
              class="accordion-head"
              :aria-expanded="messageExpanded"
              @click="messageExpanded = !messageExpanded"
            >
              <span class="accordion-arrow" :class="{ expanded: messageExpanded }">▸</span>
              <span class="accordion-title">提交信息</span>
              <span v-if="!messageExpanded && message.trim()" class="accordion-summary" :title="message">
                {{ message.trim() }}
              </span>
            </button>
            <div v-if="messageExpanded" class="accordion-content">
              <div class="section-row section-row--head">
                <label class="section-label" for="git-commit-message">提交信息</label>
                <button
                  type="button"
                  class="btn ghost"
                  :disabled="summarizing"
                  @click="generateMessage"
                >{{ summarizing ? '生成中…' : '生成提交信息' }}</button>
              </div>
              <textarea
                id="git-commit-message"
                v-model="message"
                class="message-input"
                rows="4"
                placeholder="输入提交信息，或点击「生成提交信息」由 AI 生成"
                :disabled="committingAll || committingStaged"
              ></textarea>
              <div v-if="generateError" class="banner banner--error" role="alert">
                <span>{{ generateError }}</span>
                <button type="button" class="btn ghost banner-action" @click="goSettings">去设置</button>
              </div>
            </div>
          </section>

          <!-- 统一错误展示区 -->
          <div v-if="actionError" class="banner banner--error" role="alert">{{ actionError }}</div>
          <!-- 推送结果 -->
          <div v-if="pushResult" class="banner banner--success" role="status">{{ pushResult }}</div>

          <!-- 操作区 -->
          <div class="action-row">
            <button
              type="button"
              class="btn primary"
              :disabled="!canCommit || committingAll"
              @click="doCommitAll"
            >{{ committingAll ? '提交中…' : '提交全部变更' }}</button>
            <button
              type="button"
              class="btn ghost"
              :disabled="!canCommit || repo.staged === 0 || committingStaged"
              :title="repo.staged === 0 ? '当前没有已暂存的变更' : ''"
              @click="doCommitStaged"
            >{{ committingStaged ? '提交中…' : '仅提交已暂存' }}</button>
            <button
              type="button"
              class="btn ghost"
              :disabled="pushing || anyActionRunning"
              @click="doPush"
            >{{ pushing ? '推送中…' : '推送' }}</button>
          </div>
        </template>
      </div>
    </section>
  </Teleport>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import {
  getRepoInfo,
  listBranches,
  switchBranch,
  summarizeDiff,
  commitAll,
  commitStaged,
  push,
  type RepoStatus,
  type BranchInfo,
} from '../../api/gitassist'
import { useToast } from '../../composables/useToast'
import { useUIStore } from '../../stores/ui'

const props = defineProps<{
  visible: boolean
  workDir: string
  anchor: HTMLElement | null
}>()

const emit = defineEmits<{
  (event: 'close'): void
}>()

const { showSuccess } = useToast()
const uiStore = useUIStore()

const loading = ref(false)
const repo = ref<RepoStatus | null>(null)
const branches = ref<BranchInfo[]>([])
const message = ref('')
const summarizing = ref(false)
const generateError = ref('')
const actionError = ref('')
const pushResult = ref('')
const committingAll = ref(false)
const committingStaged = ref(false)
const pushing = ref(false)
const switching = ref(false)
const pendingBranch = ref('')

// 手风琴展开状态：仓库状态/分支切换默认收起，提交信息默认展开（主操作路径）
const statusExpanded = ref(false)
const branchExpanded = ref(false)
const messageExpanded = ref(true)

const panelRef = ref<HTMLElement | null>(null)
const panelStyle = ref<Record<string, string>>({})

const anyActionRunning = computed(
  () => committingAll.value || committingStaged.value || pushing.value || switching.value,
)
const canCommit = computed(
  () => message.value.trim().length > 0 && !anyActionRunning.value,
)

function errText(err: unknown): string {
  if (err instanceof Error) return err.message
  return String(err)
}

// ---- 浮层定位：锚定「提交/推送」按钮，正下方右对齐，clamp 在视口内 ----
const VIEWPORT_PAD = 12
const PANEL_WIDTH = 400

function updatePosition() {
  const anchor = props.anchor
  if (!anchor) return
  const rect = anchor.getBoundingClientRect()
  const width = Math.min(PANEL_WIDTH, window.innerWidth - VIEWPORT_PAD * 2)
  // 右对齐：面板右缘与按钮右缘对齐；窄屏时向左 clamp 进视口
  const left = Math.max(
    VIEWPORT_PAD,
    Math.min(rect.right - width, window.innerWidth - width - VIEWPORT_PAD),
  )
  const top = Math.min(rect.bottom + 8, window.innerHeight - VIEWPORT_PAD)
  panelStyle.value = {
    left: `${left}px`,
    top: `${top}px`,
    width: `${width}px`,
  }
}

// ---- 非阻断关闭：浮层外按下即关闭，但绝不拦截事件（不 preventDefault，
// 事件照常落到终端，终端保持可输入/滚动/右键）----
function onDocumentPointerDown(event: Event) {
  const target = event.target as Node | null
  if (!target) return
  if (panelRef.value?.contains(target)) return
  if (props.anchor?.contains(target)) return
  emitClose()
}

function onDocumentKeydown(event: KeyboardEvent) {
  if (event.key === 'Escape') emitClose()
}

function attachGlobalListeners() {
  updatePosition()
  window.addEventListener('resize', updatePosition)
  document.addEventListener('pointerdown', onDocumentPointerDown, true)
  document.addEventListener('mousedown', onDocumentPointerDown, true)
  document.addEventListener('keydown', onDocumentKeydown, true)
}

function detachGlobalListeners() {
  window.removeEventListener('resize', updatePosition)
  document.removeEventListener('pointerdown', onDocumentPointerDown, true)
  document.removeEventListener('mousedown', onDocumentPointerDown, true)
  document.removeEventListener('keydown', onDocumentKeydown, true)
}

onBeforeUnmount(detachGlobalListeners)

async function loadAll() {
  if (!props.workDir) {
    repo.value = null
    return
  }
  loading.value = true
  actionError.value = ''
  try {
    const info = await getRepoInfo(props.workDir)
    repo.value = info
    if (info.isGitRepo) {
      branches.value = (await listBranches(props.workDir)) || []
    } else {
      branches.value = []
    }
  } catch (err) {
    repo.value = null
    branches.value = []
    actionError.value = '读取仓库状态失败: ' + errText(err)
  } finally {
    loading.value = false
  }
}

async function refresh() {
  pushResult.value = ''
  await loadAll()
}

function onBranchSelect(event: Event) {
  const target = event.target as HTMLSelectElement
  const name = target.value
  if (!name || name === repo.value?.branch) {
    pendingBranch.value = ''
    return
  }
  pendingBranch.value = name
}

async function confirmSwitch() {
  const branch = pendingBranch.value
  if (!branch || !props.workDir) return
  switching.value = true
  actionError.value = ''
  try {
    await switchBranch(props.workDir, branch)
    showSuccess(`已切换到分支 ${branch}`)
    pendingBranch.value = ''
    await loadAll()
  } catch (err) {
    actionError.value = errText(err)
  } finally {
    switching.value = false
  }
}

async function generateMessage() {
  if (!props.workDir) return
  summarizing.value = true
  generateError.value = ''
  try {
    const text = await summarizeDiff(props.workDir)
    message.value = (text || '').trim()
  } catch (err) {
    generateError.value = errText(err)
  } finally {
    summarizing.value = false
  }
}

async function doCommit(kind: 'all' | 'staged') {
  const msg = message.value.trim()
  if (!msg || !props.workDir) return
  const flag = kind === 'all' ? committingAll : committingStaged
  flag.value = true
  actionError.value = ''
  try {
    if (kind === 'all') await commitAll(props.workDir, msg)
    else await commitStaged(props.workDir, msg)
    showSuccess('提交成功')
    message.value = ''
    await loadAll()
  } catch (err) {
    actionError.value = (kind === 'all' ? '提交全部变更失败: ' : '提交已暂存变更失败: ') + errText(err)
  } finally {
    flag.value = false
  }
}

function doCommitAll() {
  void doCommit('all')
}

function doCommitStaged() {
  void doCommit('staged')
}

async function doPush() {
  if (!props.workDir) return
  pushing.value = true
  actionError.value = ''
  pushResult.value = ''
  try {
    const summary = await push(props.workDir)
    pushResult.value = summary || '推送成功'
    await loadAll()
  } catch (err) {
    actionError.value = '推送失败: ' + errText(err)
  } finally {
    pushing.value = false
  }
}

function goSettings() {
  emit('close')
  uiStore.enterSettingsMode()
}

function emitClose() {
  emit('close')
}

watch(
  () => props.visible,
  (visible) => {
    if (visible) {
      // 重置瞬态状态，重新拉取仓库信息
      message.value = ''
      generateError.value = ''
      actionError.value = ''
      pushResult.value = ''
      pendingBranch.value = ''
      statusExpanded.value = false
      branchExpanded.value = false
      messageExpanded.value = true
      void loadAll()
      nextTick(() => attachGlobalListeners())
      return
    }
    detachGlobalListeners()
  },
)
</script>

<style scoped>
/* 锚定下拉浮层：无全屏遮罩，fixed 定位由 JS 计算（left/top/width 内联） */
.git-panel {
  position: fixed;
  z-index: 3000;
  max-height: 70vh;
  overflow: hidden;
  display: flex;
  flex-direction: column;
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

.panel-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 12px 14px;
  border-bottom: 1px solid rgba(58, 74, 94, 0.64);
}

.panel-header-text {
  min-width: 0;
}

.panel-header h2 {
  margin: 0 0 2px;
  color: #edf6ff;
  font-size: 15px;
}

.panel-subtitle {
  margin: 0;
  color: #71869b;
  font-size: 11px;
  font-family: 'Cascadia Code', 'SFMono-Regular', Consolas, monospace;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  max-width: 320px;
}

.panel-close {
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

.panel-close:hover,
.panel-close:focus-visible {
  border-color: #89ddff;
  color: #89ddff;
  outline: none;
}

.panel-body {
  overflow: auto;
  padding: 12px 14px 14px;
}

.panel-empty {
  padding: 26px 16px;
  border: 1px dashed rgba(113, 134, 155, 0.42);
  border-radius: 12px;
  background: rgba(26, 31, 46, 0.52);
  color: #9aabba;
  font-size: 13px;
  line-height: 1.6;
  text-align: center;
}

.panel-empty--loading {
  border-color: rgba(137, 221, 255, 0.34);
  color: #89ddff;
}

/* ---- 手风琴 ---- */
.accordion {
  margin-top: 10px;
  border: 1px solid rgba(58, 74, 94, 0.64);
  border-radius: 12px;
  background: rgba(10, 14, 22, 0.64);
  overflow: hidden;
}

.accordion:first-child {
  margin-top: 0;
}

.accordion-head {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
  padding: 10px 12px;
  border: none;
  background: transparent;
  color: #a9bbcc;
  font-family: inherit;
  font-size: 13px;
  font-weight: 600;
  cursor: pointer;
  text-align: left;
}

.accordion-head:hover {
  color: #d9e2ec;
  background: rgba(137, 221, 255, 0.05);
}

.accordion-head:focus-visible {
  outline: none;
  color: #89ddff;
  box-shadow: inset 0 0 0 2px rgba(137, 221, 255, 0.35);
}

.accordion-arrow {
  flex-shrink: 0;
  color: #71869b;
  font-size: 11px;
  transition: transform 0.15s;
}

.accordion-arrow.expanded {
  transform: rotate(90deg);
  color: #89ddff;
}

.accordion-title {
  flex-shrink: 0;
}

.accordion-summary {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  text-align: right;
  color: #6f8194;
  font-size: 11px;
  font-weight: 400;
}

.accordion-content {
  padding: 0 12px 12px;
}

.status-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 10px;
}

.status-item {
  min-width: 0;
  padding: 10px 12px;
  border: 1px solid rgba(58, 74, 94, 0.7);
  border-radius: 12px;
  background: rgba(26, 31, 46, 0.72);
}

.status-item--wide {
  grid-column: 1 / -1;
}

.status-item span {
  display: block;
  margin-bottom: 6px;
  color: #6f8194;
  font-size: 11px;
}

.status-item strong {
  display: block;
  overflow: hidden;
  color: #d9e2ec;
  font-size: 13px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.section-row {
  display: flex;
  align-items: center;
  gap: 10px;
}

.section-row--head {
  justify-content: space-between;
  margin-bottom: 10px;
}

.section-label {
  flex-shrink: 0;
  color: #a9bbcc;
  font-size: 13px;
}

.branch-select {
  flex: 1;
  min-width: 0;
  appearance: none;
  -webkit-appearance: none;
  padding: 7px 30px 7px 12px;
  font-size: 13px;
  font-family: inherit;
  color: #d9e2ec;
  background: rgba(26, 31, 46, 0.9);
  border: 1px solid rgba(58, 74, 94, 0.9);
  border-radius: 8px;
  background-image: linear-gradient(45deg, transparent 50%, #71869b 50%),
    linear-gradient(135deg, #71869b 50%, transparent 50%);
  background-position: calc(100% - 16px) center, calc(100% - 11px) center;
  background-size: 5px 5px, 5px 5px;
  background-repeat: no-repeat;
  cursor: pointer;
}

.branch-select:focus-visible {
  outline: none;
  border-color: #89ddff;
}

.branch-select:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.branch-confirm {
  margin-top: 10px;
  padding: 10px 12px;
  border: 1px solid rgba(255, 203, 107, 0.36);
  border-radius: 10px;
  background: rgba(255, 203, 107, 0.07);
  color: #ffcb6b;
  font-size: 12px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
}

.branch-confirm-actions {
  display: flex;
  gap: 8px;
}

.message-input {
  width: 100%;
  box-sizing: border-box;
  resize: vertical;
  min-height: 72px;
  padding: 10px 12px;
  border: 1px solid rgba(58, 74, 94, 0.9);
  border-radius: 10px;
  background: #080d14;
  color: #d6e2ee;
  font-family: inherit;
  font-size: 13px;
  line-height: 1.55;
}

.message-input:focus-visible {
  outline: none;
  border-color: #89ddff;
}

.message-input:disabled {
  opacity: 0.6;
}

.banner {
  margin-top: 12px;
  padding: 10px 12px;
  border-radius: 10px;
  font-size: 12px;
  line-height: 1.55;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}

.banner--error {
  border: 1px solid rgba(255, 83, 112, 0.42);
  background: rgba(255, 83, 112, 0.08);
  color: #ff8aa0;
}

.banner--success {
  border: 1px solid rgba(102, 187, 106, 0.46);
  background: rgba(102, 187, 106, 0.08);
  color: #9be69f;
}

.banner-action {
  flex-shrink: 0;
}

.action-row {
  margin-top: 14px;
  display: flex;
  gap: 10px;
  flex-wrap: wrap;
}

.btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 6px;
  border-radius: 10px;
  cursor: pointer;
  font-size: 12px;
  font-weight: 500;
  padding: 7px 14px;
  font-family: inherit;
  transition: background 0.15s, opacity 0.15s;
  border: 1px solid transparent;
}

.btn.primary {
  background: #89ddff;
  color: #071018;
  border-color: rgba(137, 221, 255, 0.72);
}

.btn.primary:hover:not(:disabled) {
  box-shadow: 0 0 18px rgba(79, 195, 247, 0.22);
}

.btn.ghost {
  background: rgba(15, 23, 42, 0.72);
  color: #c8d5e3;
  border-color: #334155;
}

.btn.ghost:hover:not(:disabled) {
  border-color: #89ddff;
  color: #89ddff;
}

.btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.btn:focus-visible {
  outline: none;
  border-color: #89ddff;
  box-shadow: 0 0 0 2px rgba(137, 221, 255, 0.25);
}

@media (max-width: 640px) {
  .status-grid {
    grid-template-columns: 1fr;
  }
}
</style>
