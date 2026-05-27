<script setup lang="ts">
import { ref, onMounted, computed } from 'vue'
import { useRouter } from 'vue-router'
import { useConnection } from '../stores/connection'
import {
  apiClient,
  type LaunchMetadataResponse,
  type LaunchPresetOption,
  type SessionInfo,
} from '../api/client'

const router = useRouter()
const { isConnected } = useConnection()

const sessions = ref<SessionInfo[]>([])
const launchMeta = ref<LaunchMetadataResponse | null>(null)
const loading = ref(false)
const showLaunchDialog = ref(false)

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

const availablePaths = computed(() => launchMeta.value?.paths || [])
const claudeProviders = computed(() => launchMeta.value?.claude.providers || [])
const claudePresets = computed(() => launchMeta.value?.claude.presets || [])
const openCodeProviders = computed(() => launchMeta.value?.opencode.providers || [])
const openCodePresets = computed(() => launchMeta.value?.opencode.presets || [])
const codexProviders = computed(() => launchMeta.value?.codex.providers || [])
const codexPresets = computed(() => launchMeta.value?.codex.presets || [])

const selectedClaudePreset = computed(() => claudePresets.value.find((item) => item.key === launchForm.value.presetName) || null)
const selectedCodexPreset = computed(() => codexPresets.value.find((item) => item.key === launchForm.value.modelName) || null)

// --- Grouped sessions: running first, then stopped ---
const runningSessions = computed(() =>
  sessions.value.filter(s => s.status === 'running').sort((a, b) =>
    new Date(b.startedAt).getTime() - new Date(a.startedAt).getTime()
  )
)

const stoppedSessions = computed(() =>
  sessions.value.filter(s => s.status !== 'running').sort((a, b) =>
    new Date(b.startedAt).getTime() - new Date(a.startedAt).getTime()
  )
)

// --- App type visual config ---
interface AppTypeConfig {
  label: string
  color: string
  bg: string
  border: string
}

const appTypeMap: Record<string, AppTypeConfig> = {
  claude:    { label: 'Claude',    color: '#d4a574', bg: 'rgba(212, 165, 116, 0.12)', border: 'rgba(212, 165, 116, 0.25)' },
  opencode:  { label: 'OpenCode',  color: '#7ee787', bg: 'rgba(126, 231, 135, 0.10)', border: 'rgba(126, 231, 135, 0.22)' },
  codex:     { label: 'Codex',     color: '#79c0ff', bg: 'rgba(121, 192, 255, 0.10)', border: 'rgba(121, 192, 255, 0.22)' },
}

function getAppType(appType: string): AppTypeConfig {
  return appTypeMap[appType] || { label: appType, color: '#8b949e', bg: 'rgba(139, 148, 158, 0.10)', border: 'rgba(139, 148, 158, 0.20)' }
}

// --- Status visual config ---
interface StatusConfig {
  label: string
  color: string
  bg: string
}

const statusMap: Record<string, StatusConfig> = {
  running: { label: 'Running', color: '#3fb950', bg: 'rgba(63, 185, 80, 0.08)' },
  stopped: { label: 'Stopped', color: '#8b949e', bg: 'transparent' },
  error:   { label: 'Error',   color: '#f85149', bg: 'rgba(248, 81, 73, 0.06)' },
}

function getStatus(status: string): StatusConfig {
  return statusMap[status] || { label: status, color: '#d29922', bg: 'transparent' }
}

// --- Mode selector for dialog ---
const modeOptions = [
  { key: 'claude' as const, label: 'Claude',    color: '#d4a574', bg: 'rgba(212, 165, 116, 0.12)', border: 'rgba(212, 165, 116, 0.25)', icon: 'M12 2a5 5 0 0 1 5 5v3a5 5 0 0 1-10 0V7a5 5 0 0 1 5-5zm-8 8h2m12 0h2m-12 4a6 6 0 0 0 6 0' },
  { key: 'opencode' as const, label: 'OpenCode',  color: '#7ee787', bg: 'rgba(126, 231, 135, 0.10)', border: 'rgba(126, 231, 135, 0.22)', icon: 'M16 18l6-6-6-6M8 6l-6 6 6 6' },
  { key: 'codex' as const, label: 'Codex',     color: '#79c0ff', bg: 'rgba(121, 192, 255, 0.10)', border: 'rgba(121, 192, 255, 0.22)', icon: 'M4 4h16v16H4zM9 9h6M9 13h4' },
]

function ensureDefaultWorkDir() {
  if (!launchForm.value.workDir && availablePaths.value.length > 0) {
    launchForm.value.workDir = availablePaths.value[0]
  }
}

async function refresh() {
  loading.value = true
  try {
    const [nextSessions, nextLaunchMeta] = await Promise.all([
      apiClient.getSessions(),
      apiClient.getLaunchMetadata(),
    ])
    sessions.value = nextSessions
    launchMeta.value = nextLaunchMeta
    ensureDefaultWorkDir()
  } catch {
    sessions.value = []
    launchMeta.value = null
  } finally {
    loading.value = false
  }
}

function onModeChange() {
  launchForm.value.providerName = ''
  launchForm.value.providerID = ''
  launchForm.value.presetName = ''
  launchForm.value.modelName = ''
}

function presetLabel(option: LaunchPresetOption): string {
  const parts = [option.label]
  if (option.provider) parts.push(option.provider)
  if (option.model) parts.push(option.model)
  return parts.join(' · ')
}

async function launchSession() {
  try {
    let session: SessionInfo
    const mode = launchForm.value.mode
    const launchMode = 'embedded'

    if (mode === 'codex') {
      session = await apiClient.launchCodexSession({
        modelName: launchForm.value.modelName,
        providerID: launchForm.value.providerID || selectedCodexPreset.value?.provider || '',
        mode: launchMode,
        workDir: launchForm.value.workDir,
        shellPath: launchForm.value.shellPath,
      })
    } else if (mode === 'opencode') {
      session = await apiClient.launchOpenCodeSession({
        providerName: launchForm.value.providerName,
        presetName: launchForm.value.presetName,
        mode: launchMode,
        workDir: launchForm.value.workDir,
        shellPath: launchForm.value.shellPath,
      })
    } else {
      session = await apiClient.launchSession({
        providerName: launchForm.value.providerName || selectedClaudePreset.value?.provider || '',
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

// Pulsing animation for running status dot
function statusDotClass(status: string): string {
  return status === 'running' ? 'status-dot--pulse' : ''
}

// Short dir display
function shortDir(dir: string): string {
  if (!dir) return ''
  const parts = dir.replace(/\\/g, '/').split('/')
  // Show last 2 segments
  if (parts.length <= 2) return dir
  return '.../' + parts.slice(-2).join('/')
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
    <!-- Header -->
    <div class="page-header">
      <div class="header-left">
        <h2 class="page-title">Sessions</h2>
        <span v-if="sessions.length > 0" class="session-count">{{ sessions.length }}</span>
      </div>
      <div class="header-actions">
        <button class="icon-btn" @click="refresh" :disabled="loading" title="Refresh">
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"
            :class="{ spinning: loading }">
            <polyline points="23 4 23 10 17 10" />
            <path d="M20.49 15a9 9 0 1 1-2.12-9.36L23 10" />
          </svg>
        </button>
        <button class="launch-btn" @click="showLaunchDialog = true">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5">
            <line x1="12" y1="5" x2="12" y2="19" />
            <line x1="5" y1="12" x2="19" y2="12" />
          </svg>
          New
        </button>
      </div>
    </div>

    <!-- Empty State -->
    <div v-if="sessions.length === 0 && !loading" class="empty-state">
      <div class="empty-icon">
        <svg width="40" height="40" viewBox="0 0 24 24" fill="none" stroke="#30363d" stroke-width="1.2">
          <rect x="2" y="3" width="20" height="14" rx="2" />
          <line x1="8" y1="21" x2="16" y2="21" />
          <line x1="12" y1="17" x2="12" y2="21" />
        </svg>
      </div>
      <div class="empty-text">
        <p class="empty-title">No active sessions</p>
        <p class="empty-desc">Launch a new session to start coding</p>
      </div>
      <button class="launch-btn launch-btn--large" @click="showLaunchDialog = true">
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <line x1="12" y1="5" x2="12" y2="19" />
          <line x1="5" y1="12" x2="19" y2="12" />
        </svg>
        Launch Session
      </button>
    </div>

    <!-- Loading skeleton -->
    <div v-if="loading && sessions.length === 0" class="loading-skeleton">
      <div v-for="i in 3" :key="i" class="skeleton-card">
        <div class="skeleton-line skeleton-line--title"></div>
        <div class="skeleton-line skeleton-line--meta"></div>
        <div class="skeleton-line skeleton-line--short"></div>
      </div>
    </div>

    <!-- Session list with grouped sections -->
    <div v-if="sessions.length > 0" class="session-list">
      <!-- Running section -->
      <template v-if="runningSessions.length > 0">
        <div class="section-label">
          <span class="section-dot section-dot--active"></span>
          Active
          <span class="section-count">{{ runningSessions.length }}</span>
        </div>
        <div class="session-group">
          <div
            v-for="session in runningSessions"
            :key="session.id"
            class="session-card"
            :style="{ borderLeftColor: getAppType(session.appType).color }"
            @click="router.push(`/terminal/${session.id}`)"
          >
            <!-- Card top row: appType badge + status + duration -->
            <div class="card-top">
              <span
                class="app-type-badge"
                :style="{
                  color: getAppType(session.appType).color,
                  background: getAppType(session.appType).bg,
                  borderColor: getAppType(session.appType).border,
                }"
              >{{ getAppType(session.appType).label }}</span>
              <div class="card-status">
                <span
                  class="status-dot"
                  :class="statusDotClass(session.status)"
                  :style="{ background: getStatus(session.status).color }"
                ></span>
                <span class="status-text" :style="{ color: getStatus(session.status).color }">
                  {{ getStatus(session.status).label }}
                </span>
                <span class="session-time">{{ formatDuration(session.startedAt) }}</span>
              </div>
            </div>

            <!-- Card meta: provider > preset > model -->
            <div class="card-meta" v-if="session.provider || session.preset || session.model">
              <span v-if="session.provider" class="meta-tag meta-tag--primary">{{ session.provider }}</span>
              <span v-if="session.preset" class="meta-tag meta-tag--secondary">{{ session.preset }}</span>
              <span v-else-if="session.model" class="meta-tag meta-tag--secondary">{{ session.model }}</span>
            </div>

            <!-- Workdir -->
            <div class="card-dir" v-if="session.workDir">
              <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z" />
              </svg>
              {{ shortDir(session.workDir) }}
            </div>

            <!-- Actions -->
            <div class="card-actions">
              <button
                class="action-btn action-btn--terminal"
                @click.stop="router.push(`/terminal/${session.id}`)"
              >
                <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                  <polyline points="4 17 10 11 4 5" />
                  <line x1="12" y1="19" x2="20" y2="19" />
                </svg>
                Terminal
              </button>
              <button
                class="action-btn action-btn--stop"
                @click.stop="stopSession(session.id)"
              >
                <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                  <rect x="6" y="6" width="12" height="12" />
                </svg>
                Stop
              </button>
            </div>
          </div>
        </div>
      </template>

      <!-- Stopped section -->
      <template v-if="stoppedSessions.length > 0">
        <div class="section-label section-label--dim">
          <span class="section-dot section-dot--dim"></span>
          History
          <span class="section-count section-count--dim">{{ stoppedSessions.length }}</span>
        </div>
        <div class="session-group">
          <div
            v-for="session in stoppedSessions"
            :key="session.id"
            class="session-card session-card--stopped"
            :style="{ borderLeftColor: getAppType(session.appType).color + '44' }"
          >
            <!-- Card top row -->
            <div class="card-top">
              <span
                class="app-type-badge app-type-badge--dim"
                :style="{
                  color: getAppType(session.appType).color + 'bb',
                  background: getAppType(session.appType).bg,
                  borderColor: getAppType(session.appType).border,
                }"
              >{{ getAppType(session.appType).label }}</span>
              <div class="card-status">
                <span class="status-dot" :style="{ background: getStatus(session.status).color }"></span>
                <span class="status-text" :style="{ color: getStatus(session.status).color }">
                  {{ getStatus(session.status).label }}
                </span>
                <span class="session-time">{{ formatDuration(session.startedAt) }}</span>
              </div>
            </div>

            <!-- Meta -->
            <div class="card-meta" v-if="session.provider || session.preset || session.model">
              <span v-if="session.provider" class="meta-tag meta-tag--primary meta-tag--dim">{{ session.provider }}</span>
              <span v-if="session.preset" class="meta-tag meta-tag--secondary meta-tag--dim">{{ session.preset }}</span>
              <span v-else-if="session.model" class="meta-tag meta-tag--secondary meta-tag--dim">{{ session.model }}</span>
            </div>

            <!-- Workdir -->
            <div class="card-dir card-dir--dim" v-if="session.workDir">
              <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z" />
              </svg>
              {{ shortDir(session.workDir) }}
            </div>

            <!-- Actions -->
            <div class="card-actions">
              <button
                class="action-btn action-btn--remove"
                @click.stop="removeSession(session.id)"
              >
                <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                  <polyline points="3 6 5 6 21 6" />
                  <path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2" />
                </svg>
                Remove
              </button>
            </div>
          </div>
        </div>

        <button class="clear-stopped-btn" @click="clearStopped">
          Clear History
        </button>
      </template>
    </div>

    <!-- Launch Dialog -->
    <Teleport to="body">
      <Transition name="dialog">
        <div v-if="showLaunchDialog" class="dialog-overlay" @click.self="showLaunchDialog = false">
          <div class="dialog">
            <!-- Dialog handle (mobile affordance) -->
            <div class="dialog-handle"></div>

            <h3 class="dialog-title">New Session</h3>

            <!-- App Type Selector -->
            <div class="section-header">App Type</div>
            <div class="mode-selector">
              <button
                v-for="opt in modeOptions"
                :key="opt.key"
                class="mode-chip"
                :class="{ 'mode-chip--active': launchForm.mode === opt.key }"
                :style="{
                  '--chip-color': opt.color,
                  '--chip-bg': opt.bg,
                  '--chip-border': opt.border,
                }"
                @click="launchForm.mode = opt.key; onModeChange()"
              >
                {{ opt.label }}
              </button>
            </div>

            <!-- Claude mode fields -->
            <template v-if="launchForm.mode === 'claude'">
              <div class="section-header">Configuration</div>
              <div class="form-group">
                <label class="form-label">Preset</label>
                <select v-model="launchForm.presetName" class="form-select">
                  <option value="">Default provider preset chain</option>
                  <option v-for="preset in claudePresets" :key="preset.key" :value="preset.key">{{ presetLabel(preset) }}</option>
                </select>
              </div>
              <div class="form-group">
                <label class="form-label">Provider Override</label>
                <select v-model="launchForm.providerName" class="form-select">
                  <option value="">Auto</option>
                  <option v-for="p in claudeProviders" :key="p.id" :value="p.id">{{ p.name }}</option>
                </select>
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
              <div class="section-header">Configuration</div>
              <div class="form-group">
                <label class="form-label">Preset</label>
                <select v-model="launchForm.presetName" class="form-select">
                  <option value="">Default OpenCode profile</option>
                  <option v-for="preset in openCodePresets" :key="preset.key" :value="preset.key">
                    {{ preset.label }}{{ preset.bindingCount ? ` - ${preset.bindingCount} bindings` : '' }}
                  </option>
                </select>
              </div>
              <div class="form-group">
                <label class="form-label">Provider Override</label>
                <select v-model="launchForm.providerName" class="form-select">
                  <option value="">Auto</option>
                  <option v-for="p in openCodeProviders" :key="p.id" :value="p.id">{{ p.name }}</option>
                </select>
              </div>
            </template>

            <!-- Codex mode fields -->
            <template v-if="launchForm.mode === 'codex'">
              <div class="section-header">Configuration</div>
              <div class="form-group">
                <label class="form-label">Preset</label>
                <select v-model="launchForm.modelName" class="form-select">
                  <option value="">Custom model</option>
                  <option v-for="preset in codexPresets" :key="preset.key" :value="preset.key">{{ presetLabel(preset) }}</option>
                </select>
              </div>
              <div class="form-group">
                <label class="form-label">Model Name</label>
                <input v-model="launchForm.modelName" class="form-input" placeholder="e.g. o4-mini" />
              </div>
              <div class="form-group">
                <label class="form-label">Provider ID</label>
                <select v-model="launchForm.providerID" class="form-select">
                  <option value="">Auto</option>
                  <option v-for="p in codexProviders" :key="p.id" :value="p.id">{{ p.name }}</option>
                </select>
              </div>
            </template>

            <!-- Environment fields -->
            <div class="section-header">Environment</div>
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
      </Transition>
    </Teleport>
  </div>
</template>

<style scoped>
/* ===========================
   Page Layout
   =========================== */
.sessions-page {
  padding: 16px;
  min-height: 100%;
}

/* ===========================
   Header
   =========================== */
.page-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 20px;
  padding-bottom: 12px;
  border-bottom: 1px solid #21262d;
}

.header-left {
  display: flex;
  align-items: center;
  gap: 8px;
}

.page-title {
  font-size: 20px;
  font-weight: 700;
  color: #f0f6fc;
  margin: 0;
  letter-spacing: -0.3px;
}

.session-count {
  font-size: 12px;
  font-weight: 600;
  color: #8b949e;
  background: #21262d;
  padding: 2px 8px;
  border-radius: 10px;
  min-width: 20px;
  text-align: center;
}

.header-actions {
  display: flex;
  gap: 6px;
  align-items: center;
}

.icon-btn {
  background: none;
  border: 1px solid #30363d;
  color: #8b949e;
  cursor: pointer;
  padding: 6px;
  border-radius: 8px;
  display: flex;
  align-items: center;
  justify-content: center;
  transition: border-color 0.15s, color 0.15s;
}

.icon-btn:active {
  background: #21262d;
  border-color: #484f58;
  color: #c9d1d9;
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
  gap: 5px;
  padding: 7px 14px;
  background: #238636;
  color: #fff;
  border: none;
  border-radius: 8px;
  font-size: 13px;
  font-weight: 600;
  cursor: pointer;
  transition: background 0.15s;
  letter-spacing: -0.1px;
}

.launch-btn:active {
  background: #2ea043;
}

.launch-btn--large {
  padding: 10px 20px;
  font-size: 14px;
  border-radius: 10px;
}

/* ===========================
   Empty State
   =========================== */
.empty-state {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 16px;
  padding: 56px 24px 40px;
}

.empty-icon {
  width: 64px;
  height: 64px;
  display: flex;
  align-items: center;
  justify-content: center;
  background: #161b22;
  border-radius: 16px;
  border: 1px solid #21262d;
}

.empty-text {
  text-align: center;
}

.empty-title {
  font-size: 15px;
  font-weight: 600;
  color: #c9d1d9;
  margin: 0 0 4px;
}

.empty-desc {
  font-size: 13px;
  color: #8b949e;
  margin: 0;
}

/* ===========================
   Loading Skeleton
   =========================== */
.loading-skeleton {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.skeleton-card {
  background: #161b22;
  border: 1px solid #21262d;
  border-radius: 10px;
  padding: 14px;
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.skeleton-line {
  height: 14px;
  border-radius: 4px;
  background: linear-gradient(90deg, #161b22 25%, #1c2128 50%, #161b22 75%);
  background-size: 200% 100%;
  animation: shimmer 1.5s ease-in-out infinite;
}

.skeleton-line--title { width: 40%; height: 18px; }
.skeleton-line--meta { width: 65%; }
.skeleton-line--short { width: 35%; }

@keyframes shimmer {
  0% { background-position: 200% 0; }
  100% { background-position: -200% 0; }
}

/* ===========================
   Section Labels
   =========================== */
.section-label {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 11px;
  font-weight: 600;
  color: #8b949e;
  text-transform: uppercase;
  letter-spacing: 0.5px;
  margin-bottom: 8px;
  padding: 0 2px;
}

.section-label--dim {
  margin-top: 20px;
  color: #484f58;
}

.section-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: #3fb950;
}

.section-dot--active {
  box-shadow: 0 0 6px rgba(63, 185, 80, 0.4);
}

.section-dot--dim {
  background: #484f58;
  box-shadow: none;
}

.section-count {
  font-size: 11px;
  font-weight: 700;
  color: #3fb950;
  background: rgba(63, 185, 80, 0.12);
  padding: 1px 6px;
  border-radius: 8px;
}

.section-count--dim {
  color: #484f58;
  background: #1c2128;
}

/* ===========================
   Session List & Groups
   =========================== */
.session-list {
  display: flex;
  flex-direction: column;
}

.session-group {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

/* ===========================
   Session Card
   =========================== */
.session-card {
  background: #161b22;
  border: 1px solid #21262d;
  border-left: 3px solid #30363d;
  border-radius: 10px;
  padding: 12px 14px;
  cursor: pointer;
  transition: border-color 0.15s, background 0.15s;
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.session-card:active {
  background: #1c2128;
  border-color: #30363d;
}

.session-card--stopped {
  opacity: 0.7;
  cursor: default;
}

.session-card--stopped:active {
  background: #161b22;
}

/* Card top row */
.card-top {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}

/* App type badge */
.app-type-badge {
  font-size: 11px;
  font-weight: 700;
  letter-spacing: 0.3px;
  padding: 3px 8px;
  border-radius: 5px;
  border: 1px solid;
  line-height: 1;
  flex-shrink: 0;
}

.app-type-badge--dim {
  font-weight: 600;
}

/* Status area */
.card-status {
  display: flex;
  align-items: center;
  gap: 6px;
  margin-left: auto;
}

.status-dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  flex-shrink: 0;
}

.status-dot--pulse {
  animation: pulse-glow 2s ease-in-out infinite;
}

@keyframes pulse-glow {
  0%, 100% { box-shadow: 0 0 0 0 rgba(63, 185, 80, 0.4); }
  50% { box-shadow: 0 0 0 4px rgba(63, 185, 80, 0); }
}

.status-text {
  font-size: 11px;
  font-weight: 600;
  letter-spacing: 0.2px;
}

.session-time {
  font-size: 11px;
  color: #484f58;
  font-variant-numeric: tabular-nums;
}

/* Card meta tags */
.card-meta {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}

.meta-tag {
  font-size: 11px;
  padding: 2px 7px;
  border-radius: 4px;
  line-height: 1.4;
}

.meta-tag--primary {
  color: #c9d1d9;
  background: #21262d;
  font-weight: 500;
}

.meta-tag--secondary {
  color: #8b949e;
  background: #1c2128;
  font-weight: 400;
}

.meta-tag--dim {
  color: #484f58;
}

.meta-tag--dim.meta-tag--primary {
  background: #1c2128;
  color: #484f58;
}

/* Workdir */
.card-dir {
  display: flex;
  align-items: center;
  gap: 5px;
  font-size: 11px;
  color: #6e7681;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.card-dir--dim {
  color: #30363d;
}

/* Card actions */
.card-actions {
  display: flex;
  gap: 8px;
  margin-top: 2px;
}

.action-btn {
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 5px 10px;
  border-radius: 6px;
  font-size: 11px;
  font-weight: 600;
  cursor: pointer;
  border: 1px solid;
  transition: opacity 0.15s;
  letter-spacing: 0.1px;
}

.action-btn--terminal {
  background: rgba(88, 166, 255, 0.08);
  color: #58a6ff;
  border-color: rgba(88, 166, 255, 0.2);
}

.action-btn--terminal:active {
  background: rgba(88, 166, 255, 0.16);
}

.action-btn--stop {
  background: rgba(248, 81, 73, 0.06);
  color: #f85149;
  border-color: rgba(248, 81, 73, 0.2);
}

.action-btn--stop:active {
  background: rgba(248, 81, 73, 0.14);
}

.action-btn--remove {
  background: transparent;
  color: #484f58;
  border-color: #21262d;
}

.action-btn--remove:active {
  background: #21262d;
  color: #f85149;
  border-color: rgba(248, 81, 73, 0.2);
}

/* Clear stopped */
.clear-stopped-btn {
  width: 100%;
  padding: 10px;
  margin-top: 12px;
  background: transparent;
  color: #484f58;
  border: 1px dashed #21262d;
  border-radius: 8px;
  font-size: 12px;
  font-weight: 500;
  cursor: pointer;
  transition: color 0.15s, border-color 0.15s;
}

.clear-stopped-btn:active {
  color: #f85149;
  border-color: rgba(248, 81, 73, 0.3);
}

/* ===========================
   Dialog
   =========================== */
.dialog-overlay {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.65);
  display: flex;
  align-items: flex-end;
  z-index: 200;
}

.dialog {
  width: 100%;
  background: #0d1117;
  border-radius: 16px 16px 0 0;
  padding: 12px 16px 20px;
  max-height: 85vh;
  overflow-y: auto;
  padding-bottom: calc(20px + env(safe-area-inset-bottom, 0));
  border-top: 1px solid #21262d;
}

.dialog-handle {
  width: 36px;
  height: 4px;
  background: #30363d;
  border-radius: 2px;
  margin: 4px auto 16px;
}

.dialog-title {
  font-size: 17px;
  font-weight: 700;
  color: #f0f6fc;
  margin: 0 0 16px;
  letter-spacing: -0.2px;
}

/* Section header for form grouping */
.section-header {
  font-size: 11px;
  font-weight: 600;
  color: #484f58;
  text-transform: uppercase;
  letter-spacing: 0.6px;
  margin-bottom: 10px;
  padding-bottom: 6px;
  border-bottom: 1px solid #161b22;
}

/* Mode selector chips */
.mode-selector {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 8px;
  margin-bottom: 16px;
}

.mode-chip {
  padding: 10px 8px;
  border-radius: 8px;
  border: 1px solid var(--chip-border);
  background: var(--chip-bg);
  color: var(--chip-color);
  font-size: 13px;
  font-weight: 600;
  cursor: pointer;
  transition: background 0.15s, border-color 0.15s, box-shadow 0.15s;
  text-align: center;
}

.mode-chip--active {
  background: var(--chip-bg);
  border-color: var(--chip-color);
  box-shadow: 0 0 0 1px var(--chip-color), inset 0 0 12px rgba(255, 255, 255, 0.03);
}

.mode-chip:active {
  opacity: 0.85;
}

/* Form elements */
.form-group {
  margin-bottom: 14px;
}

.form-label {
  display: block;
  font-size: 12px;
  color: #8b949e;
  margin-bottom: 5px;
  font-weight: 500;
}

.form-input,
.form-select {
  width: 100%;
  padding: 9px 12px;
  background: #161b22;
  border: 1px solid #21262d;
  border-radius: 8px;
  color: #c9d1d9;
  font-size: 14px;
  outline: none;
  box-sizing: border-box;
  -webkit-appearance: none;
  transition: border-color 0.15s;
}

.form-input:focus,
.form-select:focus {
  border-color: #58a6ff;
  background: #161b22;
}

.form-select {
  background-image: url("data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='12' height='12' viewBox='0 0 24 24' fill='none' stroke='%23484f58' stroke-width='2'%3E%3Cpolyline points='6 9 12 15 18 9'/%3E%3C/svg%3E");
  background-repeat: no-repeat;
  background-position: right 12px center;
  padding-right: 36px;
}

/* Dialog actions */
.dialog-actions {
  display: flex;
  gap: 8px;
  margin-top: 20px;
  padding-top: 16px;
  border-top: 1px solid #21262d;
}

.btn {
  flex: 1;
  padding: 12px;
  border: none;
  border-radius: 10px;
  font-size: 14px;
  font-weight: 600;
  cursor: pointer;
  transition: opacity 0.15s;
}

.btn--secondary {
  background: #21262d;
  color: #c9d1d9;
}

.btn--secondary:active {
  background: #30363d;
}

.btn--primary {
  background: #238636;
  color: #fff;
}

.btn--primary:active {
  background: #2ea043;
}

/* Toggle */
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
  background: #21262d;
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
  background: #8b949e;
  border-radius: 50%;
  transition: 0.2s;
}

.toggle input:checked + .toggle-slider {
  background: #238636;
}

.toggle input:checked + .toggle-slider::before {
  transform: translateX(20px);
  background: #fff;
}

/* ===========================
   Dialog Transition
   =========================== */
.dialog-enter-active {
  transition: opacity 0.2s ease;
}

.dialog-enter-active .dialog {
  transition: transform 0.25s cubic-bezier(0.32, 0.72, 0, 1);
}

.dialog-leave-active {
  transition: opacity 0.15s ease;
}

.dialog-leave-active .dialog {
  transition: transform 0.15s ease-in;
}

.dialog-enter-from {
  opacity: 0;
}

.dialog-enter-from .dialog {
  transform: translateY(100%);
}

.dialog-leave-to {
  opacity: 0;
}

.dialog-leave-to .dialog {
  transform: translateY(40%);
}

/* ===========================
   Desktop Overrides (pointer + hover capable)
   =========================== */
@media (hover: hover) and (pointer: fine) {
  .sessions-page {
    max-width: 720px;
    margin: 0 auto;
  }

  .dialog-overlay {
    align-items: center;
    justify-content: center;
  }

  .dialog {
    max-width: 520px;
    border-radius: 16px;
    border: 1px solid #21262d;
  }

  .dialog-handle {
    display: none;
  }

  .mode-selector {
    grid-template-columns: repeat(4, 1fr);
  }

  .icon-btn:hover {
    background: #21262d;
    border-color: #484f58;
    color: #c9d1d9;
  }

  .icon-btn:focus-visible {
    outline: 2px solid #58a6ff;
    outline-offset: 2px;
  }

  .launch-btn:hover {
    background: #2ea043;
  }

  .launch-btn:focus-visible {
    outline: 2px solid #58a6ff;
    outline-offset: 2px;
  }

  .session-card:hover {
    background: #1c2128;
    border-color: #30363d;
  }

  .action-btn:hover {
    opacity: 0.85;
  }

  .action-btn:focus-visible {
    outline: 2px solid #58a6ff;
    outline-offset: 2px;
  }

  .clear-stopped-btn:hover {
    color: #f85149;
    border-color: rgba(248, 81, 73, 0.3);
  }

  .clear-stopped-btn:focus-visible {
    outline: 2px solid #58a6ff;
    outline-offset: 2px;
  }

  .mode-chip:hover {
    border-color: var(--chip-color);
  }

  .mode-chip:focus-visible {
    outline: 2px solid #58a6ff;
    outline-offset: 2px;
  }

  .btn--secondary:hover {
    background: #30363d;
  }

  .btn--primary:hover {
    background: #2ea043;
  }

  .btn:focus-visible {
    outline: 2px solid #58a6ff;
    outline-offset: 2px;
  }
}
</style>
