<template>
  <section class="terminal-page">
    <!-- RC2-5：远程模式下终端页指向当前主机（交互稿 §1：全应用远程模式） -->
    <template v-if="remoteClientStore.isRemoteMode">
      <div v-if="!activeRemoteTerminalId" class="term-empty-wrap">
        <PageHead title="终端" :description="`主机：${remoteClientStore.currentHostName}`" />
        <EmptyState
          icon="▢"
          title="尚未打开远程终端"
          description="请回到会话页，在远端会话行点击「打开终端」"
        />
      </div>

      <!-- 已打开的远程终端保持挂载（同本机终端缓存策略：只隐藏不销毁） -->
      <RemoteTerminalView
        v-for="sid in mountedRemoteIds"
        v-show="activeRemoteTerminalId === sid"
        :key="sid"
        :session-id="sid"
        :active="activeRemoteTerminalId === sid"
        @close="closeRemoteTerminal(sid)"
      />
    </template>

    <template v-else>
      <!-- 无选中会话：空态 -->
      <div v-if="!activeSession" class="term-empty-wrap">
        <PageHead title="终端" description="" />
        <EmptyState
          icon="▢"
          title="尚未选择会话"
          description="请从左侧选择一个运行中的会话，或点击「新建会话」开始"
        />
      </div>

      <!-- 已访问会话保持挂载。切换路由/会话时只隐藏表面，不销毁 xterm
           buffer，避免用截断的 ANSI 历史流反复重建正在运行的 TUI。 -->
      <TerminalView
        v-for="sessionId in mountedSessionIds"
        v-show="activeSession?.id === sessionId"
        :key="sessionId"
        :session-id="sessionId"
        :active="activeSession?.id === sessionId"
      />
    </template>
  </section>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import PageHead from '../components/ui/PageHead.vue'
import EmptyState from '../components/ui/EmptyState.vue'
import TerminalView from '../components/terminal/TerminalView.vue'
import RemoteTerminalView from '../components/terminal/RemoteTerminalView.vue'
import { useSessionStore } from '../stores/session'
import { useRemoteClientStore } from '../stores/remoteClient'

const sessionStore = useSessionStore()
const remoteClientStore = useRemoteClientStore()

const activeSession = computed(() => sessionStore.activeSession)
const mountedSessionIds = ref<string[]>([])

watch(
  activeSession,
  (session) => {
    if (session && !mountedSessionIds.value.includes(session.id)) {
      mountedSessionIds.value.push(session.id)
    }
  },
  { immediate: true },
)

// A removed session can never be selected again; release its cached terminal.
watch(
  () => new Set(sessionStore.sessions.map((session) => session.id)),
  (existingIds) => {
    mountedSessionIds.value = mountedSessionIds.value.filter((id) => existingIds.has(id))
  },
)

// ---- RC2-5 远程终端缓存（远程模式）----
const activeRemoteTerminalId = computed(() => remoteClientStore.activeRemoteTerminalId)
const mountedRemoteIds = ref<string[]>([])

watch(
  activeRemoteTerminalId,
  (id) => {
    if (id && !mountedRemoteIds.value.includes(id)) {
      mountedRemoteIds.value.push(id)
    }
  },
  { immediate: true },
)

function closeRemoteTerminal(id: string) {
  // 从缓存移除即触发卸载；RemoteTerminalView onBeforeUnmount 负责
  // disposeTerm + RemoteClientTerminalDetach。
  mountedRemoteIds.value = mountedRemoteIds.value.filter((x) => x !== id)
  if (remoteClientStore.activeRemoteTerminalId === id) {
    remoteClientStore.activeRemoteTerminalId = null
  }
}

// 切换主机/切回本机：远程终端全部卸载（store 侧已同步清空状态并 Disconnect）。
watch(
  () => remoteClientStore.scope,
  () => {
    mountedRemoteIds.value = []
  },
)
</script>

<style scoped>
.terminal-page {
  display: flex;
  flex-direction: column;
  flex: 1;
  min-height: 0;
}

.term-empty-wrap {
  padding: 32px 36px;
  display: flex;
  flex-direction: column;
  gap: 22px;
  overflow: auto;
}
</style>
