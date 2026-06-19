<template>
  <section class="terminal-page">
    <!-- 无选中会话：空态 -->
    <div v-if="!activeSession" class="term-empty-wrap">
      <PageHead title="终端" description="" />
      <EmptyState
        icon="▢"
        title="尚未选择会话"
        description="请从左侧选择一个运行中的会话，或点击「新建会话」开始"
      />
    </div>

    <!-- 有选中会话：挂载真实 xterm 终端 -->
    <!-- key 强制在切换会话时重建组件，保证每个会话的 xterm 生命周期干净 -->
    <TerminalView
      v-else
      :key="activeSession.id"
      :session-id="activeSession.id"
    />
  </section>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import PageHead from '../components/ui/PageHead.vue'
import EmptyState from '../components/ui/EmptyState.vue'
import TerminalView from '../components/terminal/TerminalView.vue'
import { useSessionStore } from '../stores/session'

const sessionStore = useSessionStore()

const activeSession = computed(() => sessionStore.activeSession)
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
