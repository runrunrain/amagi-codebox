<template>
  <div class="set-card">
    <h2>命令行访问</h2>
    <p class="set-sub">
      供本机 AI agent 使用的 CLI（codebox-agent）：在应用运行且开启时，agent 可经 shim
      查询与操作本机配置、会话与用量。应用未运行或已关闭时 CLI 不可用（业务命令退出码 3）。
    </p>

    <div v-if="c.actionError.value" class="action-error" role="alert">
      <span>{{ c.actionError.value }}</span>
      <button type="button" class="link-btn" @click="c.actionError.value = ''">知道了</button>
    </div>

    <div class="setting-list">
      <div class="setting-row">
        <label>启用命令行访问</label>
        <div class="inline-toggle">
          <span class="state-text">{{ c.statusLoading.value && !c.status.value ? '读取中…' : (enabled ? '已开启' : '已关闭') }}</span>
          <Switch :model-value="enabled" :disabled="c.toggling.value || c.statusLoading.value" @update:model-value="onToggle" />
        </div>
      </div>

      <div class="setting-row">
        <label>运行状态</label>
        <div class="row-value">
          <template v-if="c.statusLoading.value && !c.status.value">
            <span class="state-text">读取中…</span>
          </template>
          <template v-else-if="running">
            <span class="badge badge-ok">运行中</span>
            <span class="mono state-text">{{ c.status.value?.url }}</span>
          </template>
          <span v-else-if="enabled" class="state-text">服务未运行</span>
          <span v-else class="state-text">已关闭</span>
        </div>
      </div>

      <div v-if="running" class="setting-row">
        <label>进程</label>
        <div class="row-value mono state-text">
          PID {{ c.status.value?.pid }}<template v-if="formattedStartedAt"> · 启动于 {{ formattedStartedAt }}</template>
        </div>
      </div>

      <!-- 最近一次启动/发布失败原因（如端点被其它实例占用）；空=无异常，非空以告警展示 -->
      <div v-if="lastError" class="last-error" role="alert">
        <span class="last-error-label">最近启动/发布失败</span>
        <span>{{ lastError }}</span>
      </div>

      <div class="setting-row">
        <label>CLI 令牌</label>
        <div class="row-value">
          <span class="state-text">界面不显示令牌（每次启动重新生成）</span>
          <AppButton
            variant="primary"
            size="small"
            :disabled="rotateDisabled"
            @click="onRotateClick"
          >
            {{ c.rotating.value ? '轮换中…' : '轮换令牌' }}
          </AppButton>
        </div>
      </div>
    </div>

    <ul class="security-note">
      <li>仅接受本机回环连接（127.0.0.1），不监听局域网与外网，也无法跨机器访问。</li>
      <li>访问令牌每次应用启动重新生成，不出现在界面、日志与 CLI 输出中；轮换后旧令牌立即失效。</li>
      <li>会话启动/输入、安装等 exec 与删除类 destructive 命令需要附加 <code>--yes</code> 显式确认。</li>
    </ul>
  </div>

  <div class="set-card">
    <h2>本机分发（shim 与 agent 技能）</h2>
    <p class="set-sub">
      shim 是指向本应用可执行文件的转发脚本（{{ c.install.value?.command || 'codebox-agent' }}），
      应用每次启动会自动刷新；agent 技能（amagi-codebox）教会各 CLI 内的 agent 如何使用它，
      含全部 {{ c.install.value?.commandCount ?? '…' }} 条命令参考。
    </p>

    <div v-if="!c.install.value" class="state-text">正在读取本机分发状态…</div>

    <div v-else class="setting-list">
      <div class="setting-row">
        <div class="row-label-block">
          <label>CLI shim</label>
          <span class="mono path-text">{{ c.install.value.shimPath || '（路径不可用）' }}</span>
        </div>
        <div class="row-value">
          <span class="badge" :class="shimMeta.badgeClass">{{ shimMeta.text }}</span>
          <AppButton
            variant="primary"
            size="small"
            :disabled="c.shimBusy.value !== null"
            @click="c.installShim()"
          >
            {{ c.shimBusy.value === 'install' ? '处理中…' : shimMeta.installLabel }}
          </AppButton>
          <AppButton
            variant="ghost"
            size="small"
            :disabled="c.shimBusy.value !== null"
            @click="c.removeShim()"
          >
            {{ c.shimBusy.value === 'remove' ? '清理中…' : '清理' }}
          </AppButton>
        </div>
      </div>
      <p v-if="shimMeta.hint" class="state-hint">{{ shimMeta.hint }}</p>

      <div v-for="t in SKILL_TARGETS" :key="t.target" class="setting-row">
        <div class="row-label-block">
          <label>{{ t.label }}</label>
          <span class="mono path-text">{{ skillPath(t.target) }}</span>
        </div>
        <div class="row-value">
          <span class="badge" :class="skillMeta(t.target).badgeClass">{{ skillMeta(t.target).text }}</span>
          <AppButton
            variant="primary"
            size="small"
            :disabled="c.skillBusy[t.target] !== null"
            @click="c.installSkill(t.target)"
          >
            {{ c.skillBusy[t.target] === 'install' ? '处理中…' : skillMeta(t.target).installLabel }}
          </AppButton>
          <AppButton
            variant="ghost"
            size="small"
            :disabled="c.skillBusy[t.target] !== null"
            @click="c.removeSkill(t.target)"
          >
            {{ c.skillBusy[t.target] === 'remove' ? '清理中…' : '清理' }}
          </AppButton>
        </div>
      </div>
      <p v-for="h in skillHints" :key="`hint-${h.target}`" class="state-hint">
        {{ skillTargetName(h.target) }}：{{ h.hint }}
      </p>
    </div>

    <div class="usage-row">
      <code class="mono usage-text">{{ usageLine }}</code>
      <AppButton size="small" :disabled="copying" @click="copyUsage">
        {{ copied ? '已复制' : '复制示例' }}
      </AppButton>
    </div>
    <p class="state-hint">把整行复制给 agent 即可开始使用（更多命令见技能内的命令参考）。</p>
  </div>

  <!-- 轮换令牌二次确认（旧令牌立即失效，不可撤销） -->
  <ConfirmDialog
    :open="rotateDialogOpen"
    title="轮换 CLI 令牌"
    :message="ROTATE_CONFIRM_MESSAGE"
    confirm-text="轮换"
    @update:open="onRotateDialogOpen"
    @confirm="onRotateConfirmed"
  />
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useToast } from '../../composables/useToast'
import AppButton from '../../components/ui/AppButton.vue'
import Switch from '../../components/ui/Switch.vue'
import ConfirmDialog from '../../components/ui/ConfirmDialog.vue'
import {
  SKILL_TARGETS,
  ROTATE_CONFIRM_MESSAGE,
  installStateMeta,
  skillTargetName,
  usageExample,
  formatStartedAt,
  lastErrorText,
  errMessage,
  createAgentCliSettingsController,
  type AgentCLISkillTarget,
} from './agentCliSettings'

const { showSuccess, showError } = useToast()

// ---- 轮换令牌二次确认（promise 门：确认/取消都解析，取消即放弃操作） ----
const rotateDialogOpen = ref(false)
let resolveRotate: ((ok: boolean) => void) | null = null

function confirmRotate(): Promise<boolean> {
  return new Promise((resolve) => {
    resolveRotate = resolve
    rotateDialogOpen.value = true
  })
}

function settleRotate(ok: boolean) {
  rotateDialogOpen.value = false
  resolveRotate?.(ok)
  resolveRotate = null
}

function onRotateConfirmed() {
  settleRotate(true)
}

function onRotateDialogOpen(open: boolean) {
  if (!open) settleRotate(false)
}

// ---- 控制器（交互逻辑全部在 agentCliSettings.ts，测试在 wailsjs 边界打桩） ----
const c = createAgentCliSettingsController({
  confirm: confirmRotate,
  notify: (kind, message) => {
    if (kind === 'success') showSuccess(message)
    else showError(message)
  },
})

const enabled = computed(() => c.status.value?.enabled ?? false)
const running = computed(() => c.status.value?.running ?? false)
const rotateDisabled = computed(
  () => !enabled.value || !running.value || c.rotating.value || c.toggling.value,
)
const formattedStartedAt = computed(() => formatStartedAt(c.status.value?.startedAt))
const lastError = computed(() => lastErrorText(c.status.value?.lastError))
const usageLine = computed(() => usageExample(c.install.value?.shimPath, c.install.value?.command))

const shimMeta = computed(() => installStateMeta(c.install.value?.shimStatus ?? 'not-installed'))

function skillStatusOf(target: AgentCLISkillTarget): string {
  return c.install.value?.skills?.find((s) => s.target === target)?.status ?? 'not-installed'
}

function skillMeta(target: AgentCLISkillTarget) {
  return installStateMeta(skillStatusOf(target))
}

/** 有提示的技能目标（modified-by-user/outdated/invalid），避免渲染空行。 */
const skillHints = computed(() =>
  SKILL_TARGETS.map((t) => ({ target: t.target, hint: skillMeta(t.target).hint })).filter(
    (h) => h.hint,
  ),
)

function skillPath(target: AgentCLISkillTarget): string {
  return c.install.value?.skills?.find((s) => s.target === target)?.path || '（路径不可用）'
}

function onToggle(value: boolean) {
  void c.toggleEnabled(value)
}

function onRotateClick() {
  void c.rotateToken()
}

// ---- 使用示例复制（clipboard API 优先，WebView 权限缺失时 execCommand 降级） ----
const copied = ref(false)
const copying = ref(false)
let copiedTimer: ReturnType<typeof setTimeout> | null = null

async function copyUsage() {
  if (copying.value) return
  copying.value = true
  try {
    await copyText(usageLine.value)
    copied.value = true
    if (copiedTimer) clearTimeout(copiedTimer)
    copiedTimer = setTimeout(() => (copied.value = false), 1600)
  } catch (err) {
    showError('复制失败: ' + errMessage(err))
  } finally {
    copying.value = false
  }
}

async function copyText(text: string): Promise<void> {
  try {
    await navigator.clipboard.writeText(text)
    return
  } catch {
    // 降级：临时 textarea + execCommand（对齐 OpenCodeGlobalConfig 的做法）
    const ta = document.createElement('textarea')
    ta.value = text
    ta.style.position = 'fixed'
    ta.style.opacity = '0'
    document.body.appendChild(ta)
    ta.select()
    try {
      if (!document.execCommand('copy')) throw new Error('execCommand copy 返回 false')
    } finally {
      document.body.removeChild(ta)
    }
  }
}

onMounted(() => {
  void c.refresh()
})
</script>

<style scoped>
.set-card {
  background: var(--card);
  border: 1px solid var(--separator);
  border-radius: 14px;
  padding: 20px 24px;
  box-shadow: var(--shadow);
}

.set-card h2 {
  font-size: 17px;
  font-weight: 600;
  color: var(--label);
  margin-bottom: 4px;
}

.set-sub {
  font-size: 12px;
  color: var(--tertiary);
  margin-bottom: 14px;
  line-height: 1.6;
}

.setting-list {
  display: flex;
  flex-direction: column;
}

.setting-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 12px 0;
  border-top: 1px solid var(--separator);
}

.setting-row:first-child {
  border-top: none;
}

.setting-row label {
  font-size: 14px;
  color: var(--secondary);
}

.row-label-block {
  display: flex;
  flex-direction: column;
  gap: 3px;
  min-width: 0;
}

.row-label-block label {
  font-size: 14px;
  color: var(--secondary);
}

.path-text {
  max-width: 420px;
  overflow-wrap: anywhere;
}

.row-value {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-shrink: 0;
}

.inline-toggle {
  display: flex;
  align-items: center;
  gap: 12px;
}

.state-text {
  font-size: 12px;
  color: var(--tertiary);
}

.state-hint {
  font-size: 11px;
  color: var(--tertiary);
  margin: 2px 0 0;
  line-height: 1.5;
}

.mono {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 12px;
  color: var(--secondary);
  word-break: break-all;
}

.badge {
  font-size: 12px;
  padding: 2px 8px;
  border-radius: 6px;
  white-space: nowrap;
}

.badge-ok {
  color: var(--success, #2ea043);
  background: color-mix(in srgb, var(--success, #2ea043) 14%, transparent);
}

.badge-warn {
  color: #d08770;
  background: color-mix(in srgb, #d08770 22%, transparent);
}

.badge-missing {
  color: var(--tertiary);
  background: var(--control);
}

.last-error {
  display: flex;
  align-items: baseline;
  gap: 8px;
  font-size: 12px;
  color: #d08770;
  background: color-mix(in srgb, #d08770 12%, transparent);
  border: 1px solid color-mix(in srgb, #d08770 30%, transparent);
  border-radius: 8px;
  padding: 8px 12px;
  margin: 8px 0 4px;
  line-height: 1.5;
}

.last-error-label {
  font-weight: 600;
  flex-shrink: 0;
}

.action-error {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  font-size: 12px;
  color: #d08770;
  background: color-mix(in srgb, #d08770 12%, transparent);
  border-radius: 8px;
  padding: 8px 12px;
  margin-bottom: 12px;
}

.link-btn {
  background: none;
  border: none;
  color: inherit;
  font-size: 12px;
  cursor: pointer;
  text-decoration: underline;
  flex-shrink: 0;
}

.security-note {
  margin: 14px 0 0;
  padding: 10px 0 0;
  border-top: 1px solid var(--separator);
  display: flex;
  flex-direction: column;
  gap: 4px;
  font-size: 11px;
  color: var(--tertiary);
  list-style: none;
}

.security-note li::before {
  content: '· ';
}

.security-note code {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 11px;
  color: var(--secondary);
}

.usage-row {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-top: 14px;
}

.usage-text {
  flex: 1;
  background: var(--control);
  border: 1px solid var(--separator);
  border-radius: 8px;
  padding: 8px 12px;
  font-size: 12px;
}
</style>
