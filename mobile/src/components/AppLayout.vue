<script setup lang="ts">
import { ref } from 'vue'
import { useRoute } from 'vue-router'
import DrawerNav from './DrawerNav.vue'
import ConnectionStatus from './ConnectionStatus.vue'

const route = useRoute()
const drawerOpen = ref(false)

const isTerminalView = () => route.name === 'terminal'
</script>

<template>
  <div class="app-layout">
    <header v-if="!isTerminalView()" class="top-bar">
      <button class="menu-btn" @click="drawerOpen = true">
        <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <line x1="3" y1="6" x2="21" y2="6" />
          <line x1="3" y1="12" x2="21" y2="12" />
          <line x1="3" y1="18" x2="21" y2="18" />
        </svg>
      </button>
      <h1 class="title">Amagi CodeBox Mobile</h1>
      <ConnectionStatus />
    </header>

    <DrawerNav v-model:open="drawerOpen" />

    <main class="content" :class="{ 'content--terminal': isTerminalView() }">
      <router-view :key="route.fullPath" />
    </main>

    <nav v-if="!isTerminalView()" class="bottom-nav">
      <router-link to="/sessions" class="nav-item" active-class="nav-item--active">
        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <rect x="2" y="3" width="20" height="14" rx="2" />
          <line x1="8" y1="21" x2="16" y2="21" />
          <line x1="12" y1="17" x2="12" y2="21" />
        </svg>
        <span>Sessions</span>
      </router-link>
      <router-link to="/providers" class="nav-item" active-class="nav-item--active">
        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <path d="M12 2L2 7l10 5 10-5-10-5z" />
          <path d="M2 17l10 5 10-5" />
          <path d="M2 12l10 5 10-5" />
        </svg>
        <span>Providers</span>
      </router-link>
      <router-link to="/dashboard" class="nav-item" active-class="nav-item--active">
        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <rect x="3" y="3" width="7" height="7" />
          <rect x="14" y="3" width="7" height="7" />
          <rect x="3" y="14" width="7" height="7" />
          <rect x="14" y="14" width="7" height="7" />
        </svg>
        <span>Dashboard</span>
      </router-link>
      <router-link to="/settings" class="nav-item" active-class="nav-item--active">
        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <circle cx="12" cy="12" r="3" />
          <path d="M12 1v2M12 21v2M4.22 4.22l1.42 1.42M18.36 18.36l1.42 1.42M1 12h2M21 12h2M4.22 19.78l1.42-1.42M18.36 5.64l1.42-1.42" />
        </svg>
        <span>Settings</span>
      </router-link>
    </nav>
  </div>
</template>

<style scoped>
.app-layout {
  display: flex;
  flex-direction: column;
  height: 100vh;
  height: 100dvh;
  background: #0d1117;
  color: #c9d1d9;
}

.top-bar {
  display: flex;
  align-items: center;
  height: 48px;
  padding: 0 12px;
  background: #161b22;
  border-bottom: 1px solid #30363d;
  flex-shrink: 0;
  z-index: 10;
}

.menu-btn {
  background: none;
  border: none;
  color: #c9d1d9;
  padding: 8px;
  cursor: pointer;
  display: flex;
  align-items: center;
  border-radius: 6px;
}

.menu-btn:active {
  background: #30363d;
}

.title {
  flex: 1;
  font-size: 16px;
  font-weight: 600;
  margin: 0 8px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.content {
  flex: 1;
  overflow-y: auto;
  -webkit-overflow-scrolling: touch;
}

.content--terminal {
  overflow: hidden;
}

.bottom-nav {
  display: flex;
  justify-content: space-around;
  height: 56px;
  background: #161b22;
  border-top: 1px solid #30363d;
  flex-shrink: 0;
  padding-bottom: env(safe-area-inset-bottom, 0);
}

.nav-item {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  flex: 1;
  color: #8b949e;
  text-decoration: none;
  font-size: 11px;
  gap: 2px;
  padding: 4px;
  min-width: 0;
}

.nav-item--active {
  color: #58a6ff;
}

.nav-item span {
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
</style>
