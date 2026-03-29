<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { useConnection } from '../stores/connection'
import { apiClient, type SessionInfo } from '../api/client'

const router = useRouter()
const { appInfo, isConnected, testAndConnect } = useConnection()

const sessions = ref<SessionInfo[]>([])
const loading = ref(false)

const activeSessions = () => sessions.value.filter(s => s.status === 'running')

async function refresh() {
  loading.value = true
  try {
    await testAndConnect()
    sessions.value = await apiClient.getSessions()
  } catch {
    sessions.value = []
  } finally {
    loading.value = false
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
  <div class="dashboard">
    <div class="section-header">
      <h2 class="section-title">Server Status</h2>
      <button class="refresh-btn" @click="refresh" :disabled="loading">
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"
          :class="{ spinning: loading }">
          <polyline points="23 4 23 10 17 10" />
          <path d="M20.49 15a9 9 0 1 1-2.12-9.36L23 10" />
        </svg>
      </button>
    </div>

    <div class="info-card" v-if="appInfo">
      <div class="info-row">
        <span class="info-label">Version</span>
        <span class="info-value">{{ appInfo.version }}</span>
      </div>
      <div class="info-row">
        <span class="info-label">Active Sessions</span>
        <span class="info-value highlight">{{ appInfo.activeSessionCount }}</span>
      </div>
      <div class="info-row">
        <span class="info-label">Uptime</span>
        <span class="info-value">{{ appInfo.uptime }}</span>
      </div>
    </div>

    <div class="section-header" style="margin-top: 24px;">
      <h2 class="section-title">Quick Actions</h2>
    </div>

    <div class="actions-grid">
      <button class="action-card" @click="router.push('/sessions')">
        <svg width="28" height="28" viewBox="0 0 24 24" fill="none" stroke="#58a6ff" stroke-width="1.5">
          <rect x="2" y="3" width="20" height="14" rx="2" />
          <line x1="8" y1="21" x2="16" y2="21" />
          <line x1="12" y1="17" x2="12" y2="21" />
        </svg>
        <span class="action-label">Sessions</span>
        <span class="action-count">{{ activeSessions().length }} active</span>
      </button>

      <button class="action-card" @click="router.push('/providers')">
        <svg width="28" height="28" viewBox="0 0 24 24" fill="none" stroke="#d2a8ff" stroke-width="1.5">
          <path d="M12 2L2 7l10 5 10-5-10-5z" />
          <path d="M2 17l10 5 10-5" />
          <path d="M2 12l10 5 10-5" />
        </svg>
        <span class="action-label">Providers</span>
        <span class="action-count">Manage AI providers</span>
      </button>

      <button class="action-card" @click="router.push('/settings')">
        <svg width="28" height="28" viewBox="0 0 24 24" fill="none" stroke="#3fb950" stroke-width="1.5">
          <circle cx="12" cy="12" r="3" />
          <path d="M12 1v2M12 21v2M4.22 4.22l1.42 1.42M18.36 18.36l1.42 1.42M1 12h2M21 12h2M4.22 19.78l1.42-1.42M18.36 5.64l1.42-1.42" />
        </svg>
        <span class="action-label">Settings</span>
        <span class="action-count">Configuration</span>
      </button>

      <button v-if="activeSessions().length > 0" class="action-card" @click="router.push(`/terminal/${activeSessions()[0].id}`)">
        <svg width="28" height="28" viewBox="0 0 24 24" fill="none" stroke="#f0883e" stroke-width="1.5">
          <polyline points="4 17 10 11 4 5" />
          <line x1="12" y1="19" x2="20" y2="19" />
        </svg>
        <span class="action-label">Terminal</span>
        <span class="action-count">Open latest session</span>
      </button>
    </div>
  </div>
</template>

<style scoped>
.dashboard {
  padding: 16px;
}

.section-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 12px;
}

.section-title {
  font-size: 16px;
  font-weight: 600;
  color: #f0f6fc;
  margin: 0;
}

.refresh-btn {
  background: none;
  border: none;
  color: #8b949e;
  cursor: pointer;
  padding: 6px;
  border-radius: 6px;
}

.refresh-btn:active {
  background: #30363d;
}

.spinning {
  animation: spin 1s linear infinite;
}

@keyframes spin {
  to { transform: rotate(360deg); }
}

.info-card {
  background: #161b22;
  border: 1px solid #30363d;
  border-radius: 8px;
  overflow: hidden;
}

.info-row {
  display: flex;
  justify-content: space-between;
  padding: 12px 16px;
  border-bottom: 1px solid #21262d;
}

.info-row:last-child {
  border-bottom: none;
}

.info-label {
  color: #8b949e;
  font-size: 14px;
}

.info-value {
  color: #c9d1d9;
  font-size: 14px;
  font-weight: 500;
}

.info-value.highlight {
  color: #58a6ff;
}

.actions-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 12px;
}

.action-card {
  background: #161b22;
  border: 1px solid #30363d;
  border-radius: 8px;
  padding: 16px;
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 8px;
  cursor: pointer;
  text-align: left;
  color: inherit;
}

.action-card:active {
  background: #1c2129;
  border-color: #58a6ff;
}

.action-label {
  font-size: 14px;
  font-weight: 600;
  color: #f0f6fc;
}

.action-count {
  font-size: 12px;
  color: #8b949e;
}
</style>
