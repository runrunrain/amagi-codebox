<script lang="ts" setup>
import { useRouter, useRoute } from 'vue-router'

const router = useRouter()
const route = useRoute()

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
</style>
