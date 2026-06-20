<template>
  <div class="sb-settings">
    <!-- Back Button -->
    <button class="sb-back" @click="handleExitSettings">
      <svg class="ic" viewBox="0 0 24 24" fill="none" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
        <polyline points="15 18 9 12 15 6"/>
      </svg>
      返回
    </button>

    <!-- Title -->
    <div class="sb-settings-title">设置</div>

    <!-- Settings Navigation -->
    <nav class="sb-settings-nav">
      <div
        v-for="item in settingItems"
        :key="item.key"
        class="sb-set-item"
        :class="{ active: uiStore.activeSettingKey === item.key }"
        @click="handleSettingClick(item.key)"
      >
        <svg class="ic" viewBox="0 0 24 24" fill="none" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" v-html="item.icon"/>
        {{ item.label }}
      </div>
    </nav>

    <!-- Version at Bottom -->
    <div class="version" style="margin-top: auto; padding: 0 5px">
      <span class="sess-dot"></span>{{ appVersion }}
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useUIStore } from '../../stores/ui'
import { GetAppInfo } from '../../../wailsjs/go/main/App'

const uiStore = useUIStore()
const appVersion = ref('v1.0.0') // Fallback until loaded

onMounted(async () => {
  // Fetch real version from backend
  try {
    const info = await GetAppInfo()
    if (info?.version) {
      appVersion.value = `v${info.version}`
    }
  } catch (error) {
    console.error('[SidebarSettings] Failed to get app info:', error)
  }
})

const settingItems = [
  {
    key: 'general',
    label: '常规设置',
    icon: '<circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 0 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 0 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 0 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z"/>'
  },
  {
    key: 'shell',
    label: 'Shell',
    icon: '<polyline points="4 17 10 11 4 5"/><line x1="12" y1="19" x2="20" y2="19"/>'
  },
  {
    key: 'terminal',
    label: '终端设置',
    icon: '<rect x="3" y="4" width="18" height="16" rx="2"/><polyline points="7 9 11 12 7 15"/>'
  },
  {
    key: 'remote',
    label: '远程控制',
    icon: '<path d="M5 12.55a11 11 0 0 1 14.08 0"/><path d="M1.42 9a16 16 0 0 1 21.16 0"/><path d="M8.53 16.11a6 6 0 0 1 6.95 0"/><line x1="12" y1="20" x2="12.01" y2="20"/>'
  },
  {
    key: 'update',
    label: '软件更新',
    icon: '<path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><polyline points="7 10 12 15 17 10"/><line x1="12" y1="15" x2="12" y2="3"/>'
  },
  {
    key: 'rules',
    label: '注入规则',
    icon: '<line x1="9" y1="6" x2="20" y2="6"/><line x1="9" y1="12" x2="20" y2="12"/><line x1="9" y1="18" x2="20" y2="18"/><circle cx="4.5" cy="6" r="1"/><circle cx="4.5" cy="12" r="1"/><circle cx="4.5" cy="18" r="1"/>'
  },
  {
    key: 'about',
    label: '关于',
    icon: '<circle cx="12" cy="12" r="10"/><line x1="12" y1="16" x2="12" y2="12"/><line x1="12" y1="8" x2="12.01" y2="8"/>'
  },
]

function handleExitSettings() {
  uiStore.exitSettingsMode()
}

function handleSettingClick(key: string) {
  uiStore.setActiveSettingKey(key)
}
</script>

<style scoped>
.sb-settings {
  display: flex;
  flex-direction: column;
  flex: 1;
  gap: 14px;
  min-height: 0;
}

.sb-back {
  display: flex;
  align-items: center;
  gap: 6px;
  background: none;
  border: none;
  cursor: pointer;
  color: var(--secondary);
  font-size: 13px;
  font-family: inherit;
  padding: 2px 5px;
}

.sb-back:hover {
  color: var(--accent);
}

.sb-back .ic {
  width: 16px;
  height: 16px;
  stroke: currentColor;
}

.sb-settings-title {
  font-size: 20px;
  font-weight: 600;
  color: var(--label);
  padding: 0 5px;
  letter-spacing: -0.3px;
}

.sb-settings-nav {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.sb-set-item {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 7px 9px;
  border-radius: 8px;
  cursor: pointer;
  color: var(--secondary);
  font-size: 14px;
  transition: background .12s;
}

.sb-set-item:hover {
  background: var(--control);
}

.sb-set-item.active {
  background: var(--control);
  color: var(--label);
}

.sb-set-item.active .ic {
  stroke: var(--accent);
}

.sb-set-item .ic {
  width: 17px;
  height: 17px;
  stroke: var(--tertiary);
  flex-shrink: 0;
}

.version {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 11px;
  color: var(--tertiary);
}

.version .sess-dot {
  width: 6px;
  height: 6px;
  background: var(--success);
}
</style>
