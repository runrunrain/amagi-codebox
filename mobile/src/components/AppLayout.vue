<script setup lang="ts">
import { nextTick, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import DrawerNav from './DrawerNav.vue'
import ConnectionStatus from './ConnectionStatus.vue'

const route = useRoute()
const drawerOpen = ref(false)

const isTerminalView = () => route.name === 'workspace' && route.query.view === 'terminal'
// M1-D1：PG-01 连接配对页 / PG-02 大厅使用独立壳（VT 浅色、无 legacy 导航）
const isBareView = () => route.meta.bare === true

// M2-D（PG-04）：workspace 的 ?view=terminal 查询变化是同一会话的呈现切换
// （结构化主面 ⇄ 诊断视图），必须复用组件实例与 WS attach——按 path 作为 key，
// query 变化不重挂载；其余页面维持 fullPath 语义（ConnectPage 等依赖重挂载）。
const routerViewKey = () => (route.name === 'workspace' ? route.path : route.fullPath)

// M4-A：路由切换焦点管理——读屏路径从页首逻辑起点开始（h1 聚焦宣告页面身份），
// 不落在浏览器默认 body 起点。workspace 的 ?view=terminal query 变化是同页呈现
// 切换（不重挂载），不重复抢焦点。
const contentRef = ref<HTMLElement | null>(null)
watch(
  () => route.fullPath,
  async (_path, prev) => {
    if (route.name === 'workspace' && prev && route.path === prev.split('?')[0]) return
    await nextTick()
    const heading = contentRef.value?.querySelector('h1')
    if (!heading) return
    if (!heading.hasAttribute('tabindex')) heading.setAttribute('tabindex', '-1')
    ;(heading as HTMLElement).focus({ preventScroll: true })
  },
)
</script>

<template>
  <div class="app-layout" :class="{ 'app-layout--bare': isBareView() }">
    <header v-if="!isTerminalView() && !isBareView()" class="top-bar">
      <button class="menu-btn" aria-label="打开导航菜单" :aria-expanded="drawerOpen" @click="drawerOpen = true">
        <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <line x1="3" y1="6" x2="21" y2="6" />
          <line x1="3" y1="12" x2="21" y2="12" />
          <line x1="3" y1="18" x2="21" y2="18" />
        </svg>
      </button>
      <h1 class="title">Amagi CodeBox Mobile</h1>
      <ConnectionStatus />
    </header>

    <DrawerNav v-if="!isBareView()" v-model:open="drawerOpen" />

    <main ref="contentRef" class="content" :class="{ 'content--terminal': isTerminalView(), 'content--bare': isBareView() }">
      <router-view :key="routerViewKey()" />
    </main>

    <nav v-if="!isTerminalView() && !isBareView()" class="bottom-nav" aria-label="主导航">
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
  /* M4-A：软键盘收缩时跟随 visualViewport（iOS 必需；Android 与 dvh 一致；
     无 API/无 JS 时回落 dvh → vh，不劣化）。 */
  height: 100dvh;
  height: var(--vvh, 100dvh);
  background: #0d1117;
  color: #c9d1d9;
}

.app-layout--bare {
  background: var(--VT-canvas, #FAF9F5);
  color: var(--VT-text, #252523);
}

.content--bare {
  overflow-y: auto;
}

.top-bar {
  display: flex;
  align-items: center;
  min-height: 48px;
  padding: 0 12px;
  /* M4-A safe-area：横屏刘海/圆角下顶栏不贴边 */
  padding-left: calc(12px + env(safe-area-inset-left, 0px));
  padding-right: calc(12px + env(safe-area-inset-right, 0px));
  padding-top: env(safe-area-inset-top, 0px);
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
  justify-content: center;
  border-radius: 6px;
  /* M4-A：44px 触控目标 */
  min-width: 44px;
  min-height: 44px;
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
  min-height: 56px;
  background: #161b22;
  border-top: 1px solid #30363d;
  flex-shrink: 0;
  padding-bottom: env(safe-area-inset-bottom, 0);
  /* M4-A safe-area：横屏下左右内边距 */
  padding-left: env(safe-area-inset-left, 0px);
  padding-right: env(safe-area-inset-right, 0px);
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
