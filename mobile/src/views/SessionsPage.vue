<script setup lang="ts">
import { ref, onMounted, computed } from 'vue'
import { useRouter } from 'vue-router'
import { useConnection } from '../stores/connection'
import { apiClient, type SessionInfo, type ProviderSummary } from '../api/client'

const router = useRouter()
const { isConnected } = useConnection()

const sessions = ref<SessionInfo[]>([])
const providers = ref<ProviderSummary[]>([])
const loading = ref(false)
const showLaunchDialog = ref(false)
const availablePaths = ref<string[]>([])

const launchForm = ref({
  mode: 'claude' as 'claude' | 'opencode' | 'codex',
  providerName: '',
  presetName: '',
  workDir: '',
  useProxy: false,
  shellPath: '',
  modelName: '',
  providerID: '',
})

const sortedSessions = computed(() => {
  return [...sessions.value].sort((a, b) => {
    if (a.status === 'running' && b.status !== 'running') return -1
    if (a.status !== 'running' && b.status === 'running') return 1
    return new Date(b.startedAt).getTime() - new Date(a.startedAt).getTime()
  })
})

// Claude/Anthropic 类提供商（非 OpenAI 类型）
const anthropicProviders = computed(() =>
  providers.value.filter(p => p.type !== 'openai')
)

// OpenCode/Codex 类提供商（OpenAI 兼容类型）
const openaiProviders = computed(() =>
  providers.value.filter(p => p.type === 'openai')
)

async function refresh() {
  loading.value = true
  try {
    sessions.value = await apiClient.getSessions()
    providers.value = await apiClient.getProviders()
    availablePaths.value = await apiClient.getPaths().catch(() => [])
  } catch {
    sessions.value = []
  } finally {
    loading.value = false
  }
}

// 切换模式时重置提供商选择，避免 Claude 提供商传给 OpenCode/Codex
function onModeChange() {
  launchForm.value.providerName = ''
  launchForm.value.providerID = ''
  launchForm.value.presetName = ''
  launchForm.value.modelName = ''
}

async function launchSession() {
  try {
    let session: SessionInfo
    const mode = launchForm.value.mode

    // mode 变量是应用类型（claude/opencode/codex），用于选择 API 路由
    // 启动模式始终使用 'embedded'（内嵌终端），否则后端会默认启动独立终端窗口，
    // 移动端无法访问独立终端窗口的内容
    const launchMode = 'embedded'

    if (mode === 'codex') {
      session = await apiClient.launchCodexSession({
        modelName: launchForm.value.modelName,
        providerID: launchForm.value.providerID,
        mode: launchMode,
        workDir: launchForm.value.workDir,
        shellPath: launchForm.value.shellPath,
      })
    } else if (mode === 'opencode') {
      session = await apiClient.launchOpenCodeSession({
        providerName: launchForm.value.providerName,
        mode: launchMode,
        workDir: launchForm.value.workDir,
        shellPath: launchForm.value.shellPath,
      })
    } else {
      session = await apiClient.launchSession({
        providerName: launchForm.value.providerName,
        presetName: launchForm.value.presetName,
        mode: launchMode,
        workDir: launchForm.value.workDir,
        useProxy: launchForm.value.useProxy,
        shellPath: launchForm.value.shellPath,
      })
    }

    showLaunchDialog.value = false
    await refresh()
    router.push(`/terminal/${session.id}`)
  } catch (err) {
    alert(err instanceof Error ? err.message : 'Failed to launch session')
  }
}

async function stopSession(id: string) {
  try {
    await apiClient.stopSession(id)
    await refresh()
  } catch (err) {
    alert(err instanceof Error ? err.message : 'Failed to stop session')
  }
}

async function removeSession(id: string) {
  try {
    await apiClient.removeSession(id)
    await refresh()
  } catch (err) {
    alert(err instanceof Error ? err.message : 'Failed to remove session')
  }
}

async function clearStopped() {
  try {
    await apiClient.clearStoppedSessions()
    await refresh()
  } catch (err) {
    alert(err instanceof Error ? err.message : 'Failed to clear stopped sessions')
  }
}

function formatDuration(startedAt: string): string {
  const start = new Date(startedAt).getTime()
  const now = Date.now()
  const diff = Math.floor((now - start) / 1000)
  if (diff < 60) return `${diff}s`
  if (diff < 3600) return `${Math.floor(diff / 60)}m`
  return `${Math.floor(diff / 3600)}h ${Math.floor((diff % 3600) / 60)}m`
}

function statusColor(status: string): string {
  switch (status) {
    case 'running': return '#3fb950'
    case 'stopped': return '#8b949e'
    case 'error': return '#f85149'
    default: return '#d29922'
  }
}

onMounted(() => {
  if (!isConnected.value) {
    router.replace('/')
    return
  }
  refresh()
})
</script>

<template>
  <div class="sessions-page">
    <div class="page-header">
      <h2 class="page-title">Sessions</h2>
      <div class="header-actions">
        <button class="icon-btn" @click="refresh" :disabled="loading">
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"
            :class="{ spinning: loading }">
            <polyline points="23 4 23 10 17 10" />
            <path d="M20.49 15a9 9 0 1 1-2.12-9.36L23 10" />
          </svg>
        </button>
        <button class="launch-btn" @click="showLaunchDialog = true">
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <line x1="12" y1="5" x2="12" y2="19" />
            <line x1="5" y1="12" x2="19" y2="12" />
          </svg>
          New
        </button>
      </div>
    </div>

    <div v-if="sortedSessions.length === 0 && !loading" class="empty-state">
      <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="#30363d" stroke-width="1.5">
        <rect x="2" y="3" width="20" height="14" rx="2" />
        <line x1="8" y1="21" x2="16" y2="21" />
        <line x1="12" y1="17" x2="12" y2="21" />
      </svg>
      <p>No sessions</p>
      <button class="launch-btn" @click="showLaunchDialog = true">Launch Session</button>
    </div>

    <div class="session-list">
      <div
        v-for="session in sortedSessions"
        :key="session.id"
        class="session-card"
        @click="session.status === 'running' && router.push(`/terminal/${session.id}`)"
      >
        <div class="session-top">
          <div class="session-status">
            <div class="status-indicator" :style="{ background: statusColor(session.status) }"></div>
            <span class="session-mode">{{ session.mode }}</span>
          </div>
          <span class="session-time">{{ formatDuration(session.startedAt) }}</span>
        </div>
        <div class="session-meta">
          <span class="meta-item">{{ session.provider }}</span>
          <span v-if="session.preset" class="meta-item">{{ session.preset }}</span>
        </div>
        <div class="session-dir" v-if="session.workDir">{{ session.workDir }}</div>
        <div class="session-actions">
          <button
            v-if="session.status === 'running'"
            class="action-btn action-btn--terminal"
            @click.stop="router.push(`/terminal/${session.id}`)"
          >
            Terminal
          </button>
          <button
            v-if="session.status === 'running'"
            class="action-btn action-btn--stop"
            @click.stop="stopSession(session.id)"
          >
            Stop
          </button>
          <button
            v-if="session.status !== 'running'"
            class="action-btn action-btn--remove"
            @click.stop="removeSession(session.id)"
          >
            Remove
          </button>
        </div>
      </div>
    </div>

    <button
      v-if="sortedSessions.some(s => s.status !== 'running')"
      class="clear-stopped-btn"
      @click="clearStopped"
    >
      Clear Stopped Sessions
    </button>

    <!-- Launch Dialog -->
    <Teleport to="body">
      <div v-if="showLaunchDialog" class="dialog-overlay" @click.self="showLaunchDialog = false">
        <div class="dialog">
          <h3 class="dialog-title">Launch Session</h3>

          <div class="form-group">
            <label class="form-label">Mode</label>
            <select v-model="launchForm.mode" class="form-select" @change="onModeChange">
              <option value="claude">Claude</option>
              <option value="opencode">OpenCode</option>
              <option value="codex">Codex</option>
            </select>
          </div>

          <!-- Claude mode fields -->
          <template v-if="launchForm.mode === 'claude'">
            <div class="form-group">
              <label class="form-label">Provider Name</label>
              <select v-model="launchForm.providerName" class="form-select">
                <option value="">Default</option>
                <option v-for="p in anthropicProviders" :key="p.id" :value="p.name">{{ p.name }}</option>
              </select>
            </div>
            <div class="form-group">
              <label class="form-label">Preset Name</label>
              <input v-model="launchForm.presetName" class="form-input" placeholder="Optional preset" />
            </div>
            <div class="form-group toggle-row">
              <label class="form-label">Use Proxy</label>
              <label class="toggle">
                <input type="checkbox" v-model="launchForm.useProxy" />
                <span class="toggle-slider"></span>
              </label>
            </div>
          </template>

          <!-- OpenCode mode fields -->
          <template v-if="launchForm.mode === 'opencode'">
            <div class="form-group">
              <label class="form-label">Provider Name</label>
              <select v-model="launchForm.providerName" class="form-select">
                <option value="">Default</option>
                <option v-for="p in openaiProviders" :key="p.id" :value="p.name">{{ p.name }}</option>
              </select>
            </div>
          </template>

          <!-- Codex mode fields -->
          <template v-if="launchForm.mode === 'codex'">
            <div class="form-group">
              <label class="form-label">Model Name</label>
              <input v-model="launchForm.modelName" class="form-input" placeholder="e.g. o4-mini" />
            </div>
            <div class="form-group">
              <label class="form-label">Provider ID</label>
              <select v-model="launchForm.providerID" class="form-select">
                <option value="">Default</option>
                <option v-for="p in openaiProviders" :key="p.id" :value="p.id">{{ p.name }}</option>
              </select>
            </div>
          </template>

          <!-- Common fields -->
          <div class="form-group">
            <label class="form-label">Working Directory</label>
            <select v-if="availablePaths.length > 0" v-model="launchForm.workDir" class="form-select">
              <option value="">Select directory...</option>
              <option v-for="p in availablePaths" :key="p" :value="p">{{ p }}</option>
            </select>
            <input v-else v-model="launchForm.workDir" class="form-input" placeholder="/path/to/project" />
          </div>
          <div class="form-group">
            <label class="form-label">Shell Path</label>
            <input v-model="launchForm.shellPath" class="form-input" placeholder="Optional (e.g. /bin/bash)" />
          </div>

          <div class="dialog-actions">
            <button class="btn btn--secondary" @click="showLaunchDialog = false">Cancel</button>
            <button class="btn btn--primary" @click="launchSession">Launch</button>
          </div>
        </div>
      </div>
    </Teleport>
  </div>
</template>

<style scoped>
.sessions-page {
  padding: 16px;
}

.page-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 16px;
}

.page-title {
  font-size: 18px;
  font-weight: 600;
  color: #f0f6fc;
  margin: 0;
}

.header-actions {
  display: flex;
  gap: 8px;
  align-items: center;
}

.icon-btn {
  background: none;
  border: none;
  color: #8b949e;
  cursor: pointer;
  padding: 6px;
  border-radius: 6px;
}

.icon-btn:active {
  background: #30363d;
}

.spinning {
  animation: spin 1s linear infinite;
}

@keyframes spin {
  to { transform: rotate(360deg); }
}

.launch-btn {
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 6px 12px;
  background: #238636;
  color: #fff;
  border: none;
  border-radius: 6px;
  font-size: 13px;
  font-weight: 500;
  cursor: pointer;
}

.launch-btn:active {
  background: #2ea043;
}

.empty-state {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 12px;
  padding: 48px 24px;
  color: #8b949e;
}

.session-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.session-card {
  background: #161b22;
  border: 1px solid #30363d;
  border-radius: 8px;
  padding: 12px;
  cursor: pointer;
}

.session-card:active {
  border-color: #58a6ff;
}

.session-top {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 8px;
}

.session-status {
  display: flex;
  align-items: center;
  gap: 6px;
}

.status-indicator {
  width: 8px;
  height: 8px;
  border-radius: 50%;
}

.session-mode {
  font-size: 14px;
  font-weight: 600;
  color: #f0f6fc;
  text-transform: capitalize;
}

.session-time {
  font-size: 12px;
  color: #8b949e;
}

.session-meta {
  display: flex;
  gap: 8px;
  margin-bottom: 4px;
}

.meta-item {
  font-size: 12px;
  color: #8b949e;
  background: #21262d;
  padding: 2px 6px;
  border-radius: 4px;
}

.session-dir {
  font-size: 12px;
  color: #8b949e;
  margin-bottom: 8px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.session-actions {
  display: flex;
  gap: 8px;
}

.action-btn {
  padding: 6px 12px;
  border-radius: 4px;
  font-size: 12px;
  font-weight: 500;
  cursor: pointer;
  border: 1px solid;
}

.action-btn--terminal {
  background: rgba(88, 166, 255, 0.1);
  color: #58a6ff;
  border-color: rgba(88, 166, 255, 0.3);
}

.action-btn--stop {
  background: rgba(248, 81, 73, 0.1);
  color: #f85149;
  border-color: rgba(248, 81, 73, 0.3);
}

.action-btn--remove {
  background: rgba(139, 148, 158, 0.1);
  color: #8b949e;
  border-color: rgba(139, 148, 158, 0.3);
}

.action-btn:active {
  opacity: 0.8;
}

.clear-stopped-btn {
  width: 100%;
  padding: 10px;
  margin-top: 12px;
  background: rgba(139, 148, 158, 0.1);
  color: #8b949e;
  border: 1px solid rgba(139, 148, 158, 0.2);
  border-radius: 6px;
  font-size: 13px;
  cursor: pointer;
}

.clear-stopped-btn:active {
  background: rgba(139, 148, 158, 0.2);
}

/* Dialog */
.dialog-overlay {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.6);
  display: flex;
  align-items: flex-end;
  z-index: 200;
}

.dialog {
  width: 100%;
  background: #161b22;
  border-radius: 16px 16px 0 0;
  padding: 24px 16px;
  max-height: 80vh;
  overflow-y: auto;
  padding-bottom: calc(24px + env(safe-area-inset-bottom, 0));
}

.dialog-title {
  font-size: 18px;
  font-weight: 600;
  color: #f0f6fc;
  margin: 0 0 20px;
}

.form-group {
  margin-bottom: 16px;
}

.form-label {
  display: block;
  font-size: 13px;
  color: #8b949e;
  margin-bottom: 6px;
}

.form-input,
.form-select {
  width: 100%;
  padding: 10px 12px;
  background: #0d1117;
  border: 1px solid #30363d;
  border-radius: 6px;
  color: #c9d1d9;
  font-size: 15px;
  outline: none;
  box-sizing: border-box;
  -webkit-appearance: none;
}

.form-input:focus,
.form-select:focus {
  border-color: #58a6ff;
}

.form-select {
  background-image: url("data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='12' height='12' viewBox='0 0 24 24' fill='none' stroke='%238b949e' stroke-width='2'%3E%3Cpolyline points='6 9 12 15 18 9'%3E%3C/polyline%3E%3C/svg%3E");
  background-repeat: no-repeat;
  background-position: right 12px center;
  padding-right: 36px;
}

.dialog-actions {
  display: flex;
  gap: 8px;
  margin-top: 20px;
}

.btn {
  flex: 1;
  padding: 12px;
  border: none;
  border-radius: 6px;
  font-size: 15px;
  font-weight: 600;
  cursor: pointer;
}

.btn--secondary {
  background: #21262d;
  color: #c9d1d9;
}

.btn--primary {
  background: #238636;
  color: #fff;
}

.btn:active {
  opacity: 0.8;
}

.toggle-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.toggle-row .form-label {
  margin-bottom: 0;
}

.toggle {
  position: relative;
  display: inline-block;
  width: 44px;
  height: 24px;
}

.toggle input {
  opacity: 0;
  width: 0;
  height: 0;
}

.toggle-slider {
  position: absolute;
  cursor: pointer;
  inset: 0;
  background: #30363d;
  border-radius: 12px;
  transition: 0.2s;
}

.toggle-slider::before {
  content: '';
  position: absolute;
  height: 18px;
  width: 18px;
  left: 3px;
  bottom: 3px;
  background: #c9d1d9;
  border-radius: 50%;
  transition: 0.2s;
}

.toggle input:checked + .toggle-slider {
  background: #238636;
}

.toggle input:checked + .toggle-slider::before {
  transform: translateX(20px);
}
</style>
