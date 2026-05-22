<script lang="ts" setup>
import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'

const route = useRoute()
const router = useRouter()

const tabs = [
  {
    key: 'claude',
    label: 'ClaudeCode 插件',
    description: '管理 ClaudeCode 插件市场、安装项与子项开关',
    path: '/extensions/plugins/claude'
  },
  {
    key: 'codex',
    label: 'Codex 插件',
    description: '管理 Codex 插件市场、安装状态与详情',
    path: '/extensions/plugins/codex'
  }
]

const activeTab = computed(() => {
  const matched = tabs.find(tab => route.path === tab.path || route.path.startsWith(tab.path + '/'))
  return matched?.key || tabs[0].key
})

function switchTab(path: string) {
  if (route.path !== path) {
    router.push(path)
  }
}
</script>

<template>
  <div class="plugin-management-layout">
    <div class="plugin-subtabs" role="tablist" aria-label="插件生态切换">
      <button
        v-for="tab in tabs"
        :key="tab.key"
        type="button"
        :class="['plugin-subtab', { active: activeTab === tab.key }]"
        role="tab"
        :aria-selected="activeTab === tab.key"
        @click="switchTab(tab.path)"
      >
        <span class="subtab-label">{{ tab.label }}</span>
        <span class="subtab-description">{{ tab.description }}</span>
      </button>
    </div>

    <router-view />
  </div>
</template>

<style scoped>
.plugin-management-layout {
  display: flex;
  flex-direction: column;
  gap: 20px;
  min-height: 0;
}

.plugin-subtabs {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 12px;
}

.plugin-subtab {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 6px;
  min-height: 74px;
  padding: 14px 16px;
  background: #1a1f2e;
  border: 1px solid #2a2f3e;
  border-radius: 8px;
  color: #8899aa;
  font-family: inherit;
  text-align: left;
  cursor: pointer;
  transition: border-color 0.15s ease, background 0.15s ease, transform 0.15s ease;
}

.plugin-subtab:hover {
  border-color: #3a4f5e;
  background: rgba(42, 47, 62, 0.45);
}

.plugin-subtab.active {
  border-color: #4fc3f7;
  background: linear-gradient(135deg, rgba(79, 195, 247, 0.14), rgba(26, 31, 46, 0.92));
  box-shadow: inset 0 1px 0 rgba(255, 255, 255, 0.03);
}

.subtab-label {
  color: #e0e6ed;
  font-size: 15px;
  font-weight: 700;
}

.plugin-subtab.active .subtab-label {
  color: #7bd4f9;
}

.subtab-description {
  color: #6f8090;
  font-size: 12px;
  line-height: 1.45;
}

@media (max-width: 760px) {
  .plugin-subtabs {
    grid-template-columns: 1fr;
  }
}
</style>
