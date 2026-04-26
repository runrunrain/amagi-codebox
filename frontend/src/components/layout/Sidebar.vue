<script lang="ts" setup>
import { useRouter, useRoute } from 'vue-router'
import { ref, onMounted } from 'vue'
import { OpenRemoteWebUI, GetRemoteWebUIStatus } from '../../../wailsjs/go/main/App'
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

onMounted(() => {
  checkWebUIStatus()
})
</script>

<template>
  <nav class="sidebar">
    <div class="sidebar-header">
      <h1 class="app-title">Amagi CodeBox</h1>
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
