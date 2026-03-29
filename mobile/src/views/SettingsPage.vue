<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { useConnection } from '../stores/connection'
import { apiClient, type SettingsData, type LogEntry } from '../api/client'

const router = useRouter()
const { isConnected, appInfo, serverUrl } = useConnection()

const settings = ref<SettingsData | null>(null)
const logs = ref<LogEntry[]>([])
const activeTab = ref<'settings' | 'logs' | 'about'>('settings')
const loading = ref(false)
const saving = ref(false)
const logFilter = ref({ level: '', source: '', keyword: '' })

async function loadSettings() {
  loading.value = true
  try {
    settings.value = await apiClient.getSettings()
  } catch {
    settings.value = null
  } finally {
    loading.value = false
  }
}

async function saveSettings() {
  if (!settings.value) return
  saving.value = true
  try {
    await apiClient.updateSettings(settings.value)
  } catch (err) {
    alert(err instanceof Error ? err.message : 'Save failed')
  } finally {
    saving.value = false
  }
}

async function loadLogs() {
  try {
    logs.value = await apiClient.getLogs({
      level: logFilter.value.level || undefined,
      source: logFilter.value.source || undefined,
      keyword: logFilter.value.keyword || undefined,
      limit: 200,
    })
  } catch {
    logs.value = []
  }
}

function logLevelColor(level: string): string {
  switch (level.toLowerCase()) {
    case 'error': return '#f85149'
    case 'warn':
    case 'warning': return '#d29922'
    case 'info': return '#58a6ff'
    case 'debug': return '#8b949e'
    default: return '#c9d1d9'
  }
}

onMounted(() => {
  if (!isConnected.value) {
    router.replace('/')
    return
  }
  loadSettings()
})
</script>

<template>
  <div class="settings-page">
    <div class="tabs">
      <button
        class="tab"
        :class="{ 'tab--active': activeTab === 'settings' }"
        @click="activeTab = 'settings'; loadSettings()"
      >Settings</button>
      <button
        class="tab"
        :class="{ 'tab--active': activeTab === 'logs' }"
        @click="activeTab = 'logs'; loadLogs()"
      >Logs</button>
      <button
        class="tab"
        :class="{ 'tab--active': activeTab === 'about' }"
        @click="activeTab = 'about'"
      >About</button>
    </div>

    <!-- Settings Tab -->
    <div v-if="activeTab === 'settings'" class="tab-content">
      <div v-if="settings" class="settings-form">
        <div class="form-group">
          <label class="form-label">Remote Port</label>
          <input v-model.number="settings.remotePort" type="number" class="form-input" />
        </div>
        <div class="form-group">
          <label class="form-label">Remote Token</label>
          <input v-model="settings.remoteToken" type="password" class="form-input" />
        </div>
        <div class="form-group">
          <label class="form-label">Log Level</label>
          <select v-model="settings.logLevel" class="form-select">
            <option value="debug">Debug</option>
            <option value="info">Info</option>
            <option value="warn">Warn</option>
            <option value="error">Error</option>
          </select>
        </div>
        <div class="form-group toggle-group">
          <label class="form-label">Auto Start</label>
          <label class="toggle">
            <input type="checkbox" v-model="settings.autoStart" />
            <span class="toggle-slider"></span>
          </label>
        </div>
        <button class="save-btn" @click="saveSettings" :disabled="saving">
          {{ saving ? 'Saving...' : 'Save Settings' }}
        </button>
      </div>
      <div v-else-if="loading" class="loading-state">Loading settings...</div>
    </div>

    <!-- Logs Tab -->
    <div v-if="activeTab === 'logs'" class="tab-content">
      <div class="log-filters">
        <select v-model="logFilter.level" class="filter-select" @change="loadLogs()">
          <option value="">All Levels</option>
          <option value="debug">Debug</option>
          <option value="info">Info</option>
          <option value="warn">Warn</option>
          <option value="error">Error</option>
        </select>
        <input
          v-model="logFilter.keyword"
          class="filter-input"
          placeholder="Search..."
          @keyup.enter="loadLogs()"
        />
        <button class="filter-btn" @click="loadLogs()">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <polyline points="23 4 23 10 17 10" />
            <path d="M20.49 15a9 9 0 1 1-2.12-9.36L23 10" />
          </svg>
        </button>
      </div>
      <div class="log-list">
        <div v-if="logs.length === 0" class="empty-state">No logs available</div>
        <div v-for="(log, i) in logs" :key="i" class="log-entry">
          <span class="log-time">{{ log.timestamp }}</span>
          <span class="log-level" :style="{ color: logLevelColor(log.level) }">{{ log.level }}</span>
          <span class="log-msg">{{ log.message }}</span>
        </div>
      </div>
    </div>

    <!-- About Tab -->
    <div v-if="activeTab === 'about'" class="tab-content">
      <div class="about-card">
        <div class="about-logo">
          <svg width="40" height="40" viewBox="0 0 24 24" fill="none" stroke="#58a6ff" stroke-width="1.5">
            <rect x="2" y="3" width="20" height="14" rx="2" />
            <line x1="8" y1="21" x2="16" y2="21" />
            <line x1="12" y1="17" x2="12" y2="21" />
            <path d="M7 8l3 3-3 3" stroke-width="2" />
            <line x1="13" y1="14" x2="17" y2="14" stroke-width="2" />
          </svg>
        </div>
        <h3 class="about-title">Amagi CodeBox Mobile</h3>
        <p class="about-desc">Remote terminal controller for Amagi CodeBox</p>

        <div class="about-info">
          <div class="info-row">
            <span>App Version</span>
            <span>1.0.0</span>
          </div>
          <div class="info-row" v-if="appInfo">
            <span>Server Version</span>
            <span>{{ appInfo.version }}</span>
          </div>
          <div class="info-row">
            <span>Server URL</span>
            <span class="truncate">{{ serverUrl }}</span>
          </div>
          <div class="info-row" v-if="appInfo">
            <span>Uptime</span>
            <span>{{ appInfo.uptime }}</span>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.settings-page {
  display: flex;
  flex-direction: column;
  height: 100%;
}

.tabs {
  display: flex;
  background: #161b22;
  border-bottom: 1px solid #30363d;
  padding: 0 8px;
  flex-shrink: 0;
}

.tab {
  flex: 1;
  padding: 12px;
  background: none;
  border: none;
  border-bottom: 2px solid transparent;
  color: #8b949e;
  font-size: 14px;
  cursor: pointer;
}

.tab--active {
  color: #f0f6fc;
  border-bottom-color: #58a6ff;
}

.tab-content {
  flex: 1;
  padding: 16px;
  overflow-y: auto;
}

.settings-form {
  max-width: 400px;
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

.toggle-group {
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.toggle-group .form-label {
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

.save-btn {
  width: 100%;
  padding: 12px;
  background: #238636;
  color: #fff;
  border: none;
  border-radius: 6px;
  font-size: 15px;
  font-weight: 600;
  cursor: pointer;
}

.save-btn:active {
  background: #2ea043;
}

.save-btn:disabled {
  opacity: 0.5;
}

.loading-state {
  text-align: center;
  color: #8b949e;
  padding: 40px;
}

/* Logs */
.log-filters {
  display: flex;
  gap: 6px;
  margin-bottom: 12px;
  align-items: center;
}

.filter-select {
  padding: 6px 8px;
  background: #0d1117;
  border: 1px solid #30363d;
  border-radius: 4px;
  color: #c9d1d9;
  font-size: 12px;
  outline: none;
  -webkit-appearance: none;
}

.filter-input {
  flex: 1;
  padding: 6px 8px;
  background: #0d1117;
  border: 1px solid #30363d;
  border-radius: 4px;
  color: #c9d1d9;
  font-size: 12px;
  outline: none;
}

.filter-input:focus,
.filter-select:focus {
  border-color: #58a6ff;
}

.filter-btn {
  background: #21262d;
  border: 1px solid #30363d;
  border-radius: 4px;
  color: #8b949e;
  padding: 5px 8px;
  cursor: pointer;
  display: flex;
  align-items: center;
}

.filter-btn:active {
  background: #30363d;
}

.log-list {
  font-family: "Cascadia Code", "Fira Code", monospace;
  font-size: 12px;
  line-height: 1.6;
}

.log-entry {
  display: flex;
  gap: 8px;
  padding: 2px 0;
  border-bottom: 1px solid #21262d;
}

.log-time {
  color: #484f58;
  white-space: nowrap;
  flex-shrink: 0;
}

.log-level {
  font-weight: 600;
  text-transform: uppercase;
  min-width: 44px;
  flex-shrink: 0;
}

.log-msg {
  color: #c9d1d9;
  word-break: break-all;
}

.empty-state {
  text-align: center;
  color: #8b949e;
  padding: 40px;
}

/* About */
.about-card {
  text-align: center;
  padding: 24px;
}

.about-logo {
  width: 72px;
  height: 72px;
  background: rgba(88, 166, 255, 0.1);
  border-radius: 16px;
  display: flex;
  align-items: center;
  justify-content: center;
  margin: 0 auto 16px;
}

.about-title {
  font-size: 20px;
  font-weight: 700;
  color: #f0f6fc;
  margin: 0 0 4px;
}

.about-desc {
  font-size: 14px;
  color: #8b949e;
  margin: 0 0 24px;
}

.about-info {
  background: #0d1117;
  border: 1px solid #30363d;
  border-radius: 8px;
  overflow: hidden;
  text-align: left;
}

.info-row {
  display: flex;
  justify-content: space-between;
  padding: 12px 16px;
  border-bottom: 1px solid #21262d;
  font-size: 14px;
}

.info-row:last-child {
  border-bottom: none;
}

.info-row span:first-child {
  color: #8b949e;
}

.info-row span:last-child {
  color: #c9d1d9;
}

.truncate {
  max-width: 180px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
</style>
