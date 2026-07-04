<template>
  <div class="sb-normal">
    <!-- Logo + Brand -->
    <div class="logo-row">
      <div class="logo-mark">A</div>
      <div class="brand">CodeBox</div>
    </div>

    <!-- New Session Button -->
    <button class="new-btn" @click="handleNewSession" title="新建会话">
      <svg class="ic" viewBox="0 0 24 24" fill="none" stroke="#fff" stroke-width="2.2" stroke-linecap="round">
        <line x1="12" y1="5" x2="12" y2="19"/>
        <line x1="5" y1="12" x2="19" y2="12"/>
      </svg>
      新建会话
    </button>

    <!-- Navigation -->
    <nav class="nav">
      <router-link
        v-for="item in navItems"
        :key="item.path"
        :to="item.path"
        class="nav-item"
        :class="{ active: isActive(item.path) }"
      >
        <svg class="ic" viewBox="0 0 24 24" fill="none" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" v-html="item.icon"/>
        {{ item.label }}
      </router-link>
    </nav>

    <!-- Web UI Entry (conditional) -->
    <button
      v-if="webUIAvailable"
      class="webui-btn"
      @click="handleOpenWebUI"
      title="打开 Web 界面"
    >
      <svg class="ic" viewBox="0 0 24 24" fill="none" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round">
        <circle cx="12" cy="12" r="10"/>
        <line x1="2" y1="12" x2="22" y2="12"/>
        <path d="M12 2a15.3 15.3 0 0 1 4 10 15.3 15.3 0 0 1-4 10 15.3 15.3 0 0 1-4-10 15.3 15.3 0 0 1 4-10z"/>
      </svg>
      打开 Web 界面
    </button>

    <!-- Running Sessions Section -->
    <div class="section-label">
      <span>运行中</span>
      <span class="count-pill">{{ sessionCount }}</span>
    </div>
    <div class="sess-list">
      <template v-if="runningSessions.length > 0">
        <div
          v-for="group in groupedSessionsByAppType"
          :key="group.key"
          class="sess-group"
        >
          <div class="sess-group-header" :title="group.label">
            <span class="sg-label">
              <span class="sg-dot" :style="{ background: group.color }"/>
              {{ group.label }}
            </span>
            <span class="sg-count">{{ group.sessions.length }}</span>
          </div>
          <SessionListItem
            v-for="session in group.sessions"
            :key="session.id"
            :session="session"
            :active="activeSessionId === session.id"
            @click="handleSessionClick(session)"
            @close="handleSessionClose(session)"
          />
        </div>
      </template>
      <div v-else class="sess-empty">
        无运行中会话
        <span class="sess-empty-hint">点击上方「新建会话」开始</span>
      </div>
    </div>

    <!-- Sidebar Footer: Gear + Version -->
    <div class="sidebar-footer">
      <button class="icon-btn" @click="handleEnterSettings" title="设置">
        <svg class="ic" viewBox="0 0 24 24" fill="none" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round">
          <circle cx="12" cy="12" r="3"/>
          <path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 0 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 0 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 0 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z"/>
        </svg>
      </button>
      <button class="version" @click="showUpdateDialog = true" title="检查更新">
        <span class="sess-dot"></span>{{ appVersion }}
      </button>
    </div>

    <!-- Update Dialog -->
    <UpdateDialog v-model:open="showUpdateDialog" />
  </div>
</template>

<script setup lang="ts">
import { computed, ref, onMounted, onUnmounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useUIStore } from '../../stores/ui'
import { useSessionStore } from '../../stores/session'
import { useSessionList } from '../../composables/useSessionList'
import { useDashboardState } from '../../composables/useDashboardState'
import { usePlatformCapabilities } from '../../composables/usePlatformCapabilities'
import { useToast } from '../../composables/useToast'
import { useSessionLaunch } from '../../composables/useSessionLaunch'
import SessionListItem from './SessionListItem.vue'
import UpdateDialog from '../common/UpdateDialog.vue'
import { GetAppInfo, GetRemoteWebUIStatus, OpenRemoteWebUI } from '../../../wailsjs/go/main/App'
import * as sessionApi from '../../api/session'
import { appTypeLabel, tagColor } from '../../utils/format'

const route = useRoute()
const router = useRouter()
const uiStore = useUIStore()
const sessionStore = useSessionStore()
const { refresh, startPolling, stopPolling } = useSessionList()
const { state: dashState, persistDefaults } = useDashboardState()
const platformCaps = usePlatformCapabilities()
const { showSuccess, showError } = useToast()
// 共用会话启动逻辑（与 SessionSettingsView 共享，消除重复）
const { canLaunchFromSettings, launchFromSettings: launchSession } = useSessionLaunch()

const launching = ref(false)

const navItems = [
  {
    path: '/',
    label: '会话设置',
    icon: '<rect x="3" y="3" width="7" height="7" rx="1.5"/><rect x="14" y="3" width="7" height="7" rx="1.5"/><rect x="3" y="14" width="7" height="7" rx="1.5"/><rect x="14" y="14" width="7" height="7" rx="1.5"/>'
  },
  {
    path: '/provider',
    label: 'Provider Center',
    icon: '<rect x="3" y="4" width="18" height="7" rx="1.5"/><rect x="3" y="13" width="18" height="7" rx="1.5"/><line x1="7" y1="7.5" x2="7.01" y2="7.5"/><line x1="7" y1="16.5" x2="7.01" y2="16.5"/>'
  },
  {
    path: '/extensions',
    label: '扩展管理',
    icon: '<path d="M14 3v4h4"/><rect x="3" y="3" width="11" height="11" rx="1.5"/><rect x="13" y="13" width="8" height="8" rx="1.5"/>'
  },
  {
    path: '/envcheck',
    label: '环境检测',
    icon: '<path d="M9 11l3 3L22 4"/><path d="M21 12v7a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h11"/>'
  },
  {
    path: '/logs',
    label: '系统日志',
    icon: '<path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><polyline points="14 2 14 8 20 8"/><line x1="9" y1="13" x2="15" y2="13"/><line x1="9" y1="17" x2="15" y2="17"/>'
  },
]

const runningSessions = computed(() => sessionStore.runningSessions)
const activeSessionId = computed(() => sessionStore.activeSessionId)
const sessionCount = computed(() => runningSessions.value.length)

// Group running sessions by appType (Apple HIG: restrained group headers).
// Truth source of appType value: internal/session/types.go AppType const.
// Fixed group order: claudecode -> opencode -> codex -> amagicode (legacy last).
// Empty groups are not rendered. Within-group order follows backend List (倒序).
const APP_TYPE_ORDER: readonly string[] = ['claudecode', 'opencode', 'codex', 'amagicode']

const groupedSessionsByAppType = computed(() => {
  const sessions = runningSessions.value
  const groups = new Map<string, { key: string; label: string; color: string; sessions: typeof sessions }>()

  sessions.forEach((session: any) => {
    const appType: string = (session?.appType as string) || ''
    if (!appType) return
    if (!groups.has(appType)) {
      groups.set(appType, {
        key: appType,
        label: appTypeLabel(appType),
        color: tagColor(appType),
        sessions: [],
      })
    }
    groups.get(appType)!.sessions.push(session)
  })

  // 固定顺序：已知类型按 APP_TYPE_ORDER 排；未知类型按字母序追加到末尾
  return Array.from(groups.values()).sort((a, b) => {
    const ia = APP_TYPE_ORDER.indexOf(a.key)
    const ib = APP_TYPE_ORDER.indexOf(b.key)
    if (ia !== -1 && ib !== -1) return ia - ib
    if (ia !== -1) return -1
    if (ib !== -1) return 1
    return a.label.localeCompare(b.label)
  })
})
const showUpdateDialog = ref(false)
const appVersion = ref('v1.0.0') // Fallback until loaded
const webUIAvailable = ref(false)
const webUIUrl = ref('')

onMounted(async () => {
  await platformCaps.ensure()
  refresh()
  startPolling(2000)
  // Fetch real version from backend
  try {
    const info = await GetAppInfo()
    if (info?.version) {
      appVersion.value = `v${info.version}`
    }
  } catch (error) {
    console.error('[SidebarNormal] Failed to get app info:', error)
  }
  // Check Web UI availability
  try {
    const status = await GetRemoteWebUIStatus()
    webUIAvailable.value = status?.openable || false
    webUIUrl.value = status?.url || ''
  } catch (error) {
    console.error('[SidebarNormal] Failed to get Web UI status:', error)
  }
})

function isActive(path: string): boolean {
  return route.path === path
}

function handleNewSession() {
  // 配置足够直接启动 + 跳 /terminal；配置不全才跳会话设置页补全
  if (canLaunchFromSettings(dashState)) {
    launchSession(dashState, {
      launchingRef: launching,
      platformCaps,
      sessionStore,
      refresh,
      router,
      persistDefaults,
      showSuccess,
      showError,
    })
  } else {
    router.push('/')
  }
}

function handleSessionClick(session: any) {
  sessionStore.setActiveSession(session.id)
  router.push('/terminal')
}

async function handleSessionClose(session: any) {
  const sessionId = session.id

  try {
    await sessionApi.stopSession(sessionId)
    // refresh 后基于真实剩余会话数决策，不依赖"被关闭会话是否当前 active"
    // 否则最后一个会话关闭（或关闭非 active 会话）时 if(isCurrentSession) 守卫会跳过返回会话设置逻辑
    await refresh()

    const remaining = runningSessions.value
    if (remaining.length > 0) {
      // 仅在 active 失效或为 null 时才自动切换，保留"关闭非 active 不切 active"UX
      if (!activeSessionId.value || !remaining.find(s => s.id === activeSessionId.value)) {
        sessionStore.setActiveSession(remaining[0].id)
      }
      router.push('/terminal')
    } else {
      // 最后一个会话关闭：必返回会话设置
      sessionStore.setActiveSession(null)
      router.push('/')
    }
  } catch (err) {
    console.error('Failed to close session:', err)
    showError('关闭会话失败: ' + err)
  }
}

function handleEnterSettings() {
  uiStore.enterSettingsMode()
}

async function handleOpenWebUI() {
  try {
    const result = await OpenRemoteWebUI()
    if (result?.url) {
      webUIUrl.value = result.url
    }
  } catch (error) {
    console.error('[SidebarNormal] Failed to open Web UI:', error)
  }
}

onUnmounted(() => {
  stopPolling()
})
</script>

<style scoped>
.sb-normal {
  display: flex;
  flex-direction: column;
  flex: 1;
  gap: 16px;
  min-height: 0;
  overflow: hidden;
}

.logo-row {
  display: flex;
  align-items: center;
  gap: 9px;
  padding-left: 5px;
}

.logo-mark {
  width: 24px;
  height: 24px;
  border-radius: 6px;
  background: var(--accent);
  display: flex;
  align-items: center;
  justify-content: center;
  color: #fff;
  font-weight: 600;
  font-size: 14px;
}

.brand {
  font-size: 16px;
  font-weight: 600;
  color: var(--label);
}

.new-btn {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
  width: 100%;
  padding: 10px 14px;
  border: none;
  border-radius: 10px;
  cursor: pointer;
  background: var(--accent);
  color: #fff;
  font-size: 13px;
  font-weight: 500;
  transition: background .15s;
}

.new-btn:hover {
  background: var(--accentHover);
}

.new-btn .ic {
  width: 16px;
  height: 16px;
}

.nav {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.nav-item {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 7px 9px;
  border-radius: 8px;
  cursor: pointer;
  color: var(--secondary);
  font-size: 14px;
  transition: background .12s;
  text-decoration: none;
}

.nav-item:hover {
  background: var(--control);
}

.nav-item.active {
  background: var(--control);
  color: var(--label);
}

.nav-item.active .ic {
  stroke: var(--accent);
}

.nav-item .ic {
  width: 17px;
  height: 17px;
  stroke: var(--tertiary);
  flex-shrink: 0;
}

.webui-btn {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 9px;
  border: none;
  border-radius: 10px;
  cursor: pointer;
  background: var(--control);
  color: var(--label);
  font-size: 13px;
  font-weight: 500;
  transition: background .15s;
}

.webui-btn:hover {
  background: var(--controlHover);
}

.webui-btn .ic {
  width: 16px;
  height: 16px;
  stroke: var(--accent);
  flex-shrink: 0;
}

.section-label {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0 5px;
  color: var(--tertiary);
  font-size: 11px;
  font-weight: 500;
}

.count-pill {
  background: var(--control);
  border-radius: 999px;
  padding: 1px 7px;
  font-size: 11px;
  color: var(--secondary);
}

.sess-list {
  display: flex;
  flex-direction: column;
  gap: 3px;
  min-height: 0;
  overflow-y: auto;
}

.sess-group {
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding-bottom: 6px;
}

.sess-group:not(:last-child) {
  border-bottom: 1px solid var(--separator);
  margin-bottom: 6px;
}

.sess-group-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 4px 5px;
  color: var(--tertiary);
  font-size: 11px;
  font-weight: 500;
  letter-spacing: 0.02em;
  white-space: nowrap;
  overflow: hidden;
}

.sess-group-header .sg-label {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  overflow: hidden;
  text-overflow: ellipsis;
}

.sess-group-header .sg-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  flex-shrink: 0;
}

.sess-group-header .sg-count {
  flex-shrink: 0;
  background: var(--control);
  border-radius: 999px;
  padding: 1px 7px;
  font-size: 10px;
  color: var(--secondary);
}

.sess-empty {
  padding: 14px 9px;
  font-size: 12px;
  color: var(--tertiary);
  text-align: center;
  line-height: 1.7;
}

.sess-empty-hint {
  display: block;
  font-size: 11px;
  opacity: 0.7;
  margin-top: 2px;
}

.sidebar-footer {
  margin-top: auto;
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 4px 5px 0;
}

.icon-btn {
  width: 26px;
  height: 26px;
  border: none;
  background: transparent;
  border-radius: 7px;
  cursor: pointer;
  display: flex;
  align-items: center;
  justify-content: center;
  transition: background .12s;
}

.icon-btn:hover {
  background: var(--control);
}

.icon-btn .ic {
  width: 17px;
  height: 17px;
  stroke: var(--tertiary);
}

.version {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 11px;
  color: var(--tertiary);
  padding: 4px 8px;
  border: none;
  background: transparent;
  border-radius: 7px;
  cursor: pointer;
  transition: background .12s;
}

.version:hover {
  background: var(--control);
}

.version .sess-dot {
  width: 6px;
  height: 6px;
  background: var(--success);
}
</style>
