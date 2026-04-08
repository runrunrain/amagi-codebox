<script lang="ts" setup>
import { ref, computed } from 'vue'
import { useRouter, useRoute } from 'vue-router'

const router = useRouter()
const route = useRoute()

interface TabItem {
  key: string
  label: string
  path: string
}

const tabs: TabItem[] = [
  { key: 'plugins', label: '插件管理', path: '/extensions/plugins' },
  { key: 'envvars', label: '环境变量', path: '/extensions/envvars' },
  { key: 'amagi', label: 'AmagiCode 特有功能', path: '/extensions/amagi' },
]

const activeTab = computed(() => {
  const matched = tabs.find(t => route.path === t.path || route.path.startsWith(t.path + '/'))
  return matched?.key || tabs[0].key
})

function switchTab(tab: TabItem) {
  if (route.path !== tab.path) {
    router.push(tab.path)
  }
}
</script>

<template>
  <div class="extensions-page">
    <div class="page-header">
      <div class="header-left">
        <h1 class="page-title">扩展管理</h1>
        <p class="page-description">管理环境变量、自定义脚本等扩展功能</p>
      </div>
    </div>

    <div class="tab-bar">
      <button
        v-for="tab in tabs"
        :key="tab.key"
        :class="['tab-item', { active: activeTab === tab.key }]"
        @click="switchTab(tab)"
      >
        {{ tab.label }}
      </button>
      <div class="tab-bar-filler"></div>
    </div>

    <div class="tab-content">
      <router-view />
    </div>
  </div>
</template>

<style scoped>
.extensions-page {
  display: flex;
  flex-direction: column;
  gap: 0;
  height: 100%;
}

.page-header {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  margin-bottom: 24px;
}

.header-left {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.page-title {
  margin: 0;
  font-size: 24px;
  font-weight: 600;
  color: #e0e6ed;
}

.page-description {
  margin: 0;
  font-size: 14px;
  color: #8899aa;
}

.tab-bar {
  display: flex;
  align-items: stretch;
  border-bottom: 1px solid #2a2f3e;
  margin-bottom: 24px;
  gap: 0;
}

.tab-item {
  position: relative;
  padding: 10px 20px;
  background: transparent;
  border: none;
  border-bottom: 2px solid transparent;
  color: #8899aa;
  font-size: 14px;
  font-weight: 600;
  cursor: pointer;
  transition: all 0.15s ease;
  font-family: inherit;
  white-space: nowrap;
}

.tab-item:hover {
  color: #ccd6e0;
  background: rgba(255, 255, 255, 0.03);
}

.tab-item.active {
  color: #4fc3f7;
  border-bottom-color: #4fc3f7;
}

.tab-bar-filler {
  flex: 1;
}

.tab-content {
  flex: 1;
  min-height: 0;
}
</style>
