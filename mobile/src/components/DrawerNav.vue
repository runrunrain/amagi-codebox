<script setup lang="ts">
import { useConnection } from '../stores/connection'

const props = defineProps<{ open: boolean }>()
const emit = defineEmits<{ 'update:open': [value: boolean] }>()

const { isConnected, serverUrl, disconnect } = useConnection()

function close() {
  emit('update:open', false)
}
</script>

<template>
  <Teleport to="body">
    <Transition name="drawer">
      <div v-if="props.open" class="drawer-overlay" @click.self="close">
        <div class="drawer">
          <div class="drawer-header">
            <h2>Amagi CodeBox</h2>
            <button class="close-btn" @click="close">
              <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <line x1="18" y1="6" x2="6" y2="18" />
                <line x1="6" y1="6" x2="18" y2="18" />
              </svg>
            </button>
          </div>

          <div class="drawer-status">
            <div class="status-dot" :class="{ 'status-dot--connected': isConnected }"></div>
            <span v-if="isConnected" class="status-text">{{ serverUrl }}</span>
            <span v-else class="status-text">Not connected</span>
          </div>

          <nav class="drawer-nav">
            <router-link to="/" class="drawer-link" @click="close">
              <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <path d="M15 3h4a2 2 0 0 1 2 2v14a2 2 0 0 1-2 2h-4" />
                <polyline points="10 17 15 12 10 7" />
                <line x1="15" y1="12" x2="3" y2="12" />
              </svg>
              Connect
            </router-link>
            <router-link to="/dashboard" class="drawer-link" @click="close">
              <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <rect x="3" y="3" width="7" height="7" />
                <rect x="14" y="3" width="7" height="7" />
                <rect x="3" y="14" width="7" height="7" />
                <rect x="14" y="14" width="7" height="7" />
              </svg>
              Dashboard
            </router-link>
            <router-link to="/sessions" class="drawer-link" @click="close">
              <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <rect x="2" y="3" width="20" height="14" rx="2" />
                <line x1="8" y1="21" x2="16" y2="21" />
                <line x1="12" y1="17" x2="12" y2="21" />
              </svg>
              Sessions
            </router-link>
            <router-link to="/providers" class="drawer-link" @click="close">
              <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <path d="M12 2L2 7l10 5 10-5-10-5z" />
                <path d="M2 17l10 5 10-5" />
                <path d="M2 12l10 5 10-5" />
              </svg>
              Providers
            </router-link>
            <router-link to="/settings" class="drawer-link" @click="close">
              <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <circle cx="12" cy="12" r="3" />
                <path d="M12 1v2M12 21v2M4.22 4.22l1.42 1.42M18.36 18.36l1.42 1.42M1 12h2M21 12h2M4.22 19.78l1.42-1.42M18.36 5.64l1.42-1.42" />
              </svg>
              Settings
            </router-link>
          </nav>

          <div v-if="isConnected" class="drawer-footer">
            <button class="disconnect-btn" @click="disconnect(); close()">
              Disconnect
            </button>
          </div>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<style scoped>
.drawer-overlay {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.5);
  z-index: 100;
}

.drawer {
  position: absolute;
  top: 0;
  left: 0;
  bottom: 0;
  width: 280px;
  background: #161b22;
  display: flex;
  flex-direction: column;
  box-shadow: 4px 0 16px rgba(0, 0, 0, 0.4);
}

.drawer-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 16px;
  border-bottom: 1px solid #30363d;
}

.drawer-header h2 {
  margin: 0;
  font-size: 18px;
  color: #f0f6fc;
}

.close-btn {
  background: none;
  border: none;
  color: #8b949e;
  cursor: pointer;
  padding: 4px;
  border-radius: 6px;
}

.close-btn:active {
  background: #30363d;
}

.drawer-status {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 12px 16px;
  border-bottom: 1px solid #30363d;
}

.status-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: #f85149;
  flex-shrink: 0;
}

.status-dot--connected {
  background: #3fb950;
}

.status-text {
  font-size: 13px;
  color: #8b949e;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.drawer-nav {
  flex: 1;
  padding: 8px 0;
  overflow-y: auto;
}

.drawer-link {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 12px 16px;
  color: #c9d1d9;
  text-decoration: none;
  font-size: 14px;
}

.drawer-link:active {
  background: #30363d;
}

.drawer-link.router-link-active {
  color: #58a6ff;
  background: rgba(88, 166, 255, 0.1);
}

.drawer-footer {
  padding: 16px;
  border-top: 1px solid #30363d;
}

.disconnect-btn {
  width: 100%;
  padding: 10px;
  background: rgba(248, 81, 73, 0.15);
  color: #f85149;
  border: 1px solid rgba(248, 81, 73, 0.3);
  border-radius: 6px;
  font-size: 14px;
  cursor: pointer;
}

.disconnect-btn:active {
  background: rgba(248, 81, 73, 0.25);
}

.drawer-enter-active,
.drawer-leave-active {
  transition: opacity 0.2s ease;
}

.drawer-enter-active .drawer,
.drawer-leave-active .drawer {
  transition: transform 0.2s ease;
}

.drawer-enter-from,
.drawer-leave-to {
  opacity: 0;
}

.drawer-enter-from .drawer,
.drawer-leave-to .drawer {
  transform: translateX(-100%);
}
</style>
