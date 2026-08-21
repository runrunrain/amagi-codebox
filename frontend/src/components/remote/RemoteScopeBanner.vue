<!--
  RemoteScopeBanner（RC1-6 桌面端互联 · 远程模式提示条）
  交互稿 §1/§3：不适用远程的功能页（环境检测/更新等）在远程模式下显示
  远程不可用提示条；数据面尚未接入远程的页面显示"仍为本机数据"提示。
-->
<template>
  <StatusBanner v-if="store.isRemoteMode" type="warning" :message="message" />
</template>

<script setup lang="ts">
import { computed } from 'vue';
import StatusBanner from '../ui/StatusBanner.vue';
import { useRemoteClientStore } from '../../stores/remoteClient';

interface Props {
  /** 页面名，如「环境检测」 */
  subject: string;
  /**
   * unavailable：本机功能，远程模式下不适用；
   * local：页面仍展示本机数据，远程数据面待后续里程碑接入。
   */
  mode?: 'unavailable' | 'local';
}

const props = withDefaults(defineProps<Props>(), { mode: 'unavailable' });

const store = useRemoteClientStore();

const message = computed(() => {
  const host = store.currentHostName;
  if (props.mode === 'local') {
    return `当前处于远程模式（主机：${host}）。${props.subject}暂不支持远程数据面，此处显示的仍是本机内容。`;
  }
  return `当前处于远程模式（主机：${host}）。${props.subject}为本机功能，不适用于远程主机。`;
});
</script>
