<template>
  <section class="terminal-page">
    <!-- RC1-6：远程模式下终端页仍为本机终端（远程终端属后续里程碑） -->
    <div v-if="remoteClientStore.isRemoteMode" class="scope-banner-wrap">
      <RemoteScopeBanner subject="终端页" mode="local" />
    </div>

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
  </section>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import PageHead from '../components/ui/PageHead.vue'
import EmptyState from '../components/ui/EmptyState.vue'
import TerminalView from '../components/terminal/TerminalView.vue'
import RemoteScopeBanner from '../components/remote/RemoteScopeBanner.vue'
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

.scope-banner-wrap {
  padding: 12px 16px 0;
  flex-shrink: 0;
}
</style>
