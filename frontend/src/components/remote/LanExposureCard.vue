<!--
  卡② LAN 暴露确认卡（PG-05 + NFR-18/AC-18）
  风险文案 + 显式确认（P-02 不预勾选）+ 本机确认记录。
  说明：后端 settings 当前无 LAN 确认字段（internal/settings 仅 RemoteHost/Port/Enabled），
  确认记录落在本机 localStorage（本机可见记录），报告中如实声明。
-->
<template>
  <section class="rc-card lan-card" aria-labelledby="rc-lan-title" ref="cardRef">
    <header class="rc-card-head">
      <h2 id="rc-lan-title" class="rc-card-title">LAN 暴露确认</h2>
      <p class="rc-card-sub">开启远程服务前的显式风险确认（不预勾选）</p>
    </header>

    <RiskBanner title="LAN 暴露与明文 HTTP 风险">
      局域网内 HTTP 为明文传输；终端输出可能包含命令、路径或凭据。
      增强手段可配置，但不等于默认已解决。开启即表示你接受同网段设备可见此服务。
    </RiskBanner>

    <label class="rc-check-row lan-check">
      <input
        v-model="checked"
        type="checkbox"
        class="rc-checkbox"
        data-testid="lan-confirm-checkbox"
        @change="onCheck"
      />
      <span>我已了解上述风险，确认在本局域网内暴露远程服务</span>
    </label>

    <p v-if="record" class="lan-record">
      <span class="lan-record-icon" aria-hidden="true">✓</span>
      已于 {{ recordAt }} 确认 · 记录保存在本机可查
    </p>
    <p v-else class="lan-pending">尚未确认 · 确认前无法开启远程服务</p>
  </section>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue';
import RiskBanner from './RiskBanner.vue';
import { formatDateTime } from './remoteShared';

const STORAGE_KEY = 'amagi.remote.lanExposureConfirmedAt';

const emit = defineEmits<{
  (e: 'confirmed', at: string): void;
}>();

const cardRef = ref<HTMLElement | null>(null);
// P-02：复选框永不预勾选——每次挂载都是未勾状态，确认记录只读展示
const checked = ref(false);
const record = ref<string | null>(null);

const recordAt = computed(() => formatDateTime(record.value || undefined));

onMounted(() => {
  try {
    record.value = localStorage.getItem(STORAGE_KEY);
  } catch {
    record.value = null;
  }
});

function onCheck() {
  if (!checked.value) return;
  const at = new Date().toISOString();
  record.value = at;
  try {
    localStorage.setItem(STORAGE_KEY, at);
  } catch {
    // localStorage 不可用时仍向父级报告本次确认（会话内有效）
  }
  emit('confirmed', at);
}

/** 供父级滚动定位/高亮 */
function scrollIntoView() {
  cardRef.value?.scrollIntoView({ behavior: 'smooth', block: 'center' });
}

defineExpose({ scrollIntoView, hasRecord: () => !!record.value });
</script>

<style scoped>
.lan-card {
  border-color: var(--vt-warning);
  border-left: 4px solid var(--vt-warning);
}

.lan-check {
  margin-top: 12px;
}

.lan-record {
  margin: 10px 0 0;
  font-size: 13px;
  color: var(--vt-success);
}

.lan-record-icon {
  margin-right: 4px;
}

.lan-pending {
  margin: 10px 0 0;
  font-size: 13px;
  color: var(--vt-text-secondary);
}
</style>
