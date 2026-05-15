<script lang="ts" setup>
import { useRouter, useRoute } from 'vue-router'
import { computed, ref, onMounted, onUnmounted } from 'vue'
import { OpenRemoteWebUI, GetRemoteWebUIStatus, GetAppInfo, CheckForUpdate } from '../../../wailsjs/go/main/App'
import { BrowserOpenURL } from '../../../wailsjs/runtime/runtime'
import { useToast } from '../../composables/useToast'

const router = useRouter()
const route = useRoute()
const { showError } = useToast()

const navItems = [
  { path: '/dashboard', label: '仪表盘', icon: '▶' },
  { path: '/terminals', label: '内嵌终端', icon: '⬛' },
  { path: '/provider-center', label: 'Provider Center', icon: '☁' },
  { path: '/extensions', label: '扩展管理', icon: '◈' },
  { path: '/rules', label: '注入规则', icon: '⚙' },
  { path: '/logs', label: '系统日志', icon: '▣' },
]

function isActive(path: string): boolean {
  return route.path === path || route.path.startsWith(path + '/')
}

// --- Special tools section ---
const specialToolsExpanded = ref(false)
const webUILoading = ref(false)
const webUIAvailable = ref(false)
const webUIUnavailableReason = ref('Web UI 不可用')

const releasesURL = 'https://github.com/runrunrain/amagi-codebox/releases'
const currentVersion = ref('')
const latestVersion = ref('')
const updateAvailable = ref(false)
const updateChecking = ref(false)
const updateCheckError = ref('')
const updatePopoverOpen = ref(false)
const versionEntryRef = ref<HTMLElement | null>(null)

const wailsBindingUnavailableMessage = '更新服务暂不可用，请确认应用已在桌面客户端中正常启动后重试。'

const displayVersion = computed(() => currentVersion.value || '检测中')
const versionTooltip = computed(() => {
  if (updateAvailable.value) return '有新版本可用！'
  if (updateChecking.value) return '正在检查当前版本'
  if (updateCheckError.value) return `当前版本 v${displayVersion.value}，更新状态暂不可用`
  return `当前版本 v${displayVersion.value}`
})
const popoverVersionHint = computed(() => {
  if (updateAvailable.value) return `发现新版本 v${latestVersion.value || '最新版本'}，可前往软件更新页安装。`
  if (updateChecking.value) return '正在刷新版本状态，请稍候。'
  if (updateCheckError.value) return `更新状态检查失败：${updateCheckError.value}`
  return '当前已是最新版本。'
})

function getErrorMessage(err: unknown): string {
  if (err instanceof Error) return err.message
  return String(err || '未知错误')
}

function normalizeUpdateError(err: unknown): string {
  const message = getErrorMessage(err)
  const bindingUnavailablePatterns = [
    /Cannot read properties of undefined/i,
    /Cannot read property .* of undefined/i,
    /undefined \(reading ['"].*['"]\)/i,
    /window\.go/i,
    /wails/i,
  ]
  if (bindingUnavailablePatterns.some(pattern => pattern.test(message))) {
    return wailsBindingUnavailableMessage
  }
  return message
}

async function checkWebUIStatus() {
  try {
    const status = await GetRemoteWebUIStatus() as any
    webUIAvailable.value = !!status?.openable
    webUIUnavailableReason.value = status?.reason || 'Web UI 不可用'
  } catch {
    webUIAvailable.value = false
    webUIUnavailableReason.value = '获取 Web UI 状态失败'
  }
}

async function handleOpenWebUI() {
  if (webUILoading.value || !webUIAvailable.value) return
  webUILoading.value = true
  try {
    await OpenRemoteWebUI()
  } catch (e) {
    const detail = e instanceof Error ? e.message : String(e)
    showError(`打开 Web 界面失败: ${detail || '未知错误'}`, 5000)
    await checkWebUIStatus()
  } finally {
    webUILoading.value = false
  }
}

async function loadVersionInfo() {
  try {
    const info = await GetAppInfo()
    currentVersion.value = (info as any)?.version || currentVersion.value || ''
  } catch {
    currentVersion.value = currentVersion.value || ''
  }
}

async function refreshUpdateStatus() {
  if (updateChecking.value) return
  updateChecking.value = true
  updateCheckError.value = ''
  try {
    if (!currentVersion.value) {
      await loadVersionInfo()
    }
    const info = await CheckForUpdate()
    currentVersion.value = (info as any)?.currentVersion || currentVersion.value
    latestVersion.value = (info as any)?.latestVersion || ''
    updateAvailable.value = !!(info as any)?.hasUpdate
  } catch (err) {
    updateAvailable.value = false
    console.warn('更新状态检查失败:', err)
    updateCheckError.value = normalizeUpdateError(err)
  } finally {
    updateChecking.value = false
  }
}

function toggleUpdatePopover() {
  updatePopoverOpen.value = !updatePopoverOpen.value
}

function closeUpdatePopover() {
  updatePopoverOpen.value = false
}

function handleDocumentClick(event: MouseEvent) {
  if (!updatePopoverOpen.value) return
  const target = event.target
  if (!(target instanceof Node)) return
  if (!versionEntryRef.value || !versionEntryRef.value.contains(target)) {
    closeUpdatePopover()
  }
}

function handleDocumentKeydown(event: KeyboardEvent) {
  if (event.key === 'Escape' && updatePopoverOpen.value) {
    closeUpdatePopover()
  }
}

function goToUpdatePage() {
  updatePopoverOpen.value = false
  router.push({
    path: '/settings',
    query: {
      tab: 'updates',
      action: 'update',
      source: 'sidebar-version',
      t: String(Date.now()),
    },
  })
}

function openReleaseNotes() {
  BrowserOpenURL(releasesURL)
}

onMounted(() => {
  checkWebUIStatus()
  loadVersionInfo()
  refreshUpdateStatus()
  document.addEventListener('click', handleDocumentClick)
  document.addEventListener('keydown', handleDocumentKeydown)
})

onUnmounted(() => {
  document.removeEventListener('click', handleDocumentClick)
  document.removeEventListener('keydown', handleDocumentKeydown)
})
</script>

<template>
  <nav class="sidebar">
    <div class="sidebar-header">
      <h1 class="app-title">Amagi CodeBox</h1>
      <div ref="versionEntryRef" class="version-entry" @click.stop>
        <button
          class="version-pill"
          :class="{ 'has-update': updateAvailable, checking: updateChecking }"
          type="button"
          :aria-expanded="updatePopoverOpen"
          aria-haspopup="dialog"
          :title="versionTooltip"
          @click="toggleUpdatePopover"
        >
          <span class="version-text">v{{ displayVersion }}</span>
          <span v-if="updateAvailable" class="version-dot" aria-label="有新版本可用"></span>
          <span class="version-tooltip" role="tooltip">{{ versionTooltip }}</span>
        </button>

        <transition name="update-popover">
          <section
            v-if="updatePopoverOpen"
            class="update-popover-card"
            role="dialog"
            aria-label="版本更新"
          >
            <button
              class="popover-refresh"
              type="button"
              :disabled="updateChecking"
              aria-label="刷新版本状态"
              title="刷新版本状态"
              @click="refreshUpdateStatus"
            >
              <span :class="['refresh-symbol', { spinning: updateChecking }]">↻</span>
            </button>

            <div class="popover-kicker">版本更新</div>
            <h2 class="popover-title">Amagi CodeBox</h2>
            <div class="current-version-display">v{{ displayVersion }}</div>

            <div class="update-alert" :class="{ muted: !updateAvailable }">
              <span class="alert-dot"></span>
              <span>{{ popoverVersionHint }}</span>
            </div>

            <button class="update-primary" type="button" @click="goToUpdatePage">
              立即更新
            </button>
            <button class="release-link" type="button" @click="openReleaseNotes">
              查看更新日志
            </button>
          </section>
        </transition>
      </div>
    </div>
    <ul class="nav-list">
      <li
        v-for="item in navItems"
        :key="item.path"
        :class="['nav-item', { active: isActive(item.path) }]"
        @click="router.push(item.path)"
      >
        <span class="nav-icon">{{ item.icon }}</span>
        <span class="nav-label">{{ item.label }}</span>
      </li>
    </ul>

    <!-- Special tools: collapsible section -->
    <div class="special-tools">
      <div
        class="special-tools-toggle"
        @click="specialToolsExpanded = !specialToolsExpanded"
        :title="specialToolsExpanded ? '收起特殊功能' : '展开特殊功能'"
      >
        <span class="toggle-arrow" :class="{ expanded: specialToolsExpanded }">&#9654;</span>
        <span class="toggle-label">特殊功能</span>
      </div>
      <div class="special-tools-content" v-if="specialToolsExpanded">
        <div
          class="nav-item special-tool-item"
          :class="{ disabled: !webUIAvailable, loading: webUILoading }"
          @click="handleOpenWebUI"
          :title="webUIAvailable ? '在浏览器中打开 Web 界面' : webUIUnavailableReason"
        >
          <span class="nav-icon">&#127760;</span>
          <span class="nav-label">打开 Web 界面</span>
        </div>
      </div>
    </div>

    <div class="sidebar-footer">
      <div
        :class="['nav-item', { active: isActive('/settings') }]"
        @click="router.push('/settings')"
      >
        <span class="nav-icon">⚙</span>
        <span class="nav-label">设置</span>
      </div>
    </div>
  </nav>
</template>

<style scoped>
.sidebar {
  width: 200px;
  min-width: 200px;
  background: #1a1f2e;
  border-right: 1px solid #2a2f3e;
  display: flex;
  flex-direction: column;
  height: 100vh;
}

.sidebar-header {
  padding: 20px 16px;
  border-bottom: 1px solid #2a2f3e;
}

.app-title {
  margin: 0;
  font-size: 18px;
  font-weight: 700;
  color: #4fc3f7;
  letter-spacing: 0.5px;
}

.version-entry {
  position: relative;
  display: inline-flex;
  margin-top: 10px;
  align-items: flex-start;
}

.version-pill {
  position: relative;
  display: inline-flex;
  align-items: center;
  gap: 6px;
  min-height: 24px;
  padding: 4px 10px;
  border: 1px solid rgba(195, 142, 41, 0.38);
  border-radius: 999px;
  background: linear-gradient(135deg, #fff4c8 0%, #f6df93 100%);
  color: #5f3d10;
  font-size: 11px;
  font-weight: 700;
  line-height: 1;
  letter-spacing: 0.2px;
  cursor: pointer;
  box-shadow: 0 4px 14px rgba(0, 0, 0, 0.18), inset 0 1px 0 rgba(255, 255, 255, 0.65);
  transition: transform 0.15s ease, box-shadow 0.15s ease, border-color 0.15s ease;
}

.version-pill:hover,
.version-pill:focus-visible {
  transform: translateY(-1px);
  border-color: rgba(222, 166, 47, 0.72);
  box-shadow: 0 7px 20px rgba(0, 0, 0, 0.26), 0 0 0 2px rgba(245, 194, 77, 0.18);
  outline: none;
}

.version-pill.checking .version-text {
  opacity: 0.72;
}

.version-text {
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
}

.version-dot {
  width: 7px;
  height: 7px;
  border-radius: 999px;
  background: #f59e0b;
  box-shadow: 0 0 0 3px rgba(245, 158, 11, 0.23), 0 0 12px rgba(245, 158, 11, 0.88);
}

.version-tooltip {
  position: absolute;
  left: 0;
  top: calc(100% + 9px);
  z-index: 25;
  min-width: max-content;
  max-width: 180px;
  padding: 7px 9px;
  border: 1px solid rgba(255, 224, 132, 0.16);
  border-radius: 8px;
  background: rgba(18, 20, 28, 0.96);
  color: #ffe7a6;
  font-size: 11px;
  font-weight: 600;
  line-height: 1.35;
  box-shadow: 0 10px 24px rgba(0, 0, 0, 0.34);
  opacity: 0;
  pointer-events: none;
  transform: translateY(-4px);
  transition: opacity 0.15s ease, transform 0.15s ease;
}

.version-tooltip::before {
  content: '';
  position: absolute;
  left: 18px;
  top: -5px;
  width: 8px;
  height: 8px;
  background: rgba(18, 20, 28, 0.96);
  border-left: 1px solid rgba(255, 224, 132, 0.16);
  border-top: 1px solid rgba(255, 224, 132, 0.16);
  transform: rotate(45deg);
}

.version-pill:hover .version-tooltip,
.version-pill:focus-visible .version-tooltip {
  opacity: 1;
  transform: translateY(0);
}

.update-popover-card {
  position: absolute;
  left: 0;
  top: calc(100% + 12px);
  z-index: 30;
  width: 256px;
  box-sizing: border-box;
  padding: 18px;
  border: 1px solid rgba(255, 218, 139, 0.22);
  border-radius: 18px;
  background: linear-gradient(180deg, #20202a 0%, #151923 100%);
  color: #f7ead0;
  box-shadow: 0 24px 55px rgba(0, 0, 0, 0.42), 0 8px 22px rgba(0, 0, 0, 0.28);
}

.popover-refresh {
  position: absolute;
  top: 12px;
  right: 12px;
  width: 28px;
  height: 28px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border: 1px solid rgba(245, 223, 174, 0.14);
  border-radius: 999px;
  background: rgba(255, 255, 255, 0.04);
  color: #d4b981;
  cursor: pointer;
  transition: background 0.15s ease, color 0.15s ease, transform 0.15s ease;
}

.popover-refresh:hover:not(:disabled) {
  background: rgba(255, 235, 186, 0.1);
  color: #ffe6a3;
  transform: translateY(-1px);
}

.popover-refresh:disabled {
  cursor: wait;
  opacity: 0.6;
}

.refresh-symbol {
  font-size: 15px;
  line-height: 1;
}

.refresh-symbol.spinning {
  animation: version-spin 0.8s linear infinite;
}

.popover-kicker {
  margin-bottom: 5px;
  color: #b9a77c;
  font-size: 11px;
  font-weight: 700;
  letter-spacing: 0.8px;
}

.popover-title {
  margin: 0;
  padding-right: 32px;
  color: #fff3d5;
  font-size: 15px;
  font-weight: 700;
}

.current-version-display {
  margin: 14px 0 16px;
  text-align: center;
  color: #ffe7a6;
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 28px;
  font-weight: 800;
  letter-spacing: -0.5px;
}

.update-alert {
  display: flex;
  align-items: flex-start;
  gap: 9px;
  margin-bottom: 16px;
  padding: 11px 12px;
  border: 1px solid rgba(246, 188, 78, 0.25);
  border-radius: 12px;
  background: rgba(255, 200, 87, 0.14);
  color: #f7d27f;
  font-size: 12px;
  line-height: 1.45;
}

.update-alert.muted {
  border-color: rgba(196, 180, 139, 0.15);
  background: rgba(255, 255, 255, 0.045);
  color: #c9bea5;
}

.alert-dot {
  width: 8px;
  height: 8px;
  margin-top: 4px;
  border-radius: 999px;
  flex: 0 0 auto;
  background: #f59e0b;
  box-shadow: 0 0 0 3px rgba(245, 158, 11, 0.16);
}

.update-primary {
  width: 100%;
  min-height: 38px;
  border: none;
  border-radius: 11px;
  background: linear-gradient(135deg, #24c8b3 0%, #14b8a6 100%);
  color: #06211e;
  font-size: 14px;
  font-weight: 800;
  cursor: pointer;
  box-shadow: 0 10px 20px rgba(20, 184, 166, 0.22);
  transition: transform 0.15s ease, box-shadow 0.15s ease, filter 0.15s ease;
}

.update-primary:hover {
  transform: translateY(-1px);
  filter: brightness(1.05);
  box-shadow: 0 14px 26px rgba(20, 184, 166, 0.28);
}

.release-link {
  display: block;
  margin: 12px auto 0;
  padding: 0;
  border: none;
  background: transparent;
  color: #9ddfd6;
  font-size: 12px;
  font-weight: 700;
  cursor: pointer;
  text-decoration: none;
}

.release-link:hover {
  color: #c3fff7;
  text-decoration: underline;
}

.update-popover-enter-active,
.update-popover-leave-active {
  transition: opacity 0.16s ease, transform 0.16s ease;
}

.update-popover-enter-from,
.update-popover-leave-to {
  opacity: 0;
  transform: translateY(-6px) scale(0.98);
}

@keyframes version-spin {
  to { transform: rotate(360deg); }
}

.nav-list {
  list-style: none;
  margin: 0;
  padding: 8px 0;
  flex: 1;
}

.nav-item {
  display: flex;
  align-items: center;
  padding: 10px 16px;
  cursor: pointer;
  color: #8899aa;
  transition: all 0.15s ease;
  font-size: 14px;
}

.nav-item:hover {
  background: #232838;
  color: #ccd6e0;
}

.nav-item.active {
  background: #1e3a5f;
  color: #4fc3f7;
  border-right: 3px solid #4fc3f7;
}

.nav-icon {
  margin-right: 10px;
  font-size: 16px;
  width: 20px;
  text-align: center;
}

.nav-label {
  font-weight: 500;
}

.sidebar-footer {
  border-top: 1px solid #2a2f3e;
  padding: 8px 0;
}

/* --- Special tools section --- */
.special-tools {
  border-top: 1px solid #2a2f3e;
  padding: 4px 0;
}

.special-tools-toggle {
  display: flex;
  align-items: center;
  padding: 10px 16px;
  cursor: pointer;
  color: #667788;
  font-size: 12px;
  transition: all 0.15s ease;
  user-select: none;
}

.special-tools-toggle:hover {
  background: #232838;
  color: #99aabb;
}

.toggle-arrow {
  display: inline-block;
  margin-right: 8px;
  font-size: 10px;
  width: 14px;
  text-align: center;
  transition: transform 0.2s ease;
}

.toggle-arrow.expanded {
  transform: rotate(90deg);
}

.toggle-label {
  font-weight: 600;
  letter-spacing: 0.3px;
  text-transform: uppercase;
  font-size: 11px;
}

.special-tools-content {
  padding: 2px 0 4px 0;
}

.special-tool-item {
  opacity: 1;
  transition: all 0.15s ease;
}

.special-tool-item.disabled {
  opacity: 0.4;
  cursor: not-allowed;
}

.special-tool-item.disabled:hover {
  background: transparent;
  color: #8899aa;
}

.special-tool-item.loading {
  opacity: 0.6;
  cursor: wait;
}
</style>
