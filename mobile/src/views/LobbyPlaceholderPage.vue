<script setup lang="ts">
/**
 * LobbyPlaceholderPage — 会话大厅诚实占位（#/lobby，M1-D1）
 * 大厅本体是 M2 交付（PG-02）。本页只做：
 *   · 证明配对与授权态真实生效（展示 auth store 的非密 device/host 投影）；
 *   · 明示占位身份，不伪装功能；
 *   · 授权失效（E-03/E-04）入口示例：诊断失败可返回 PG-01。
 */
import { onMounted, ref } from 'vue';
import { useRouter } from 'vue-router';
import { getHostSummary, toApiRequestError } from '../lib/api';
import {
  ERROR_CODE_AUTH_REVOKED,
  ERROR_CODE_AUTH_UNPAIRED,
} from '../lib/contract';
import { useAuthStore } from '../stores/auth';

const router = useRouter();
const auth = useAuthStore();
const checking = ref(true);
const checkError = ref('');

onMounted(async () => {
  // 未配对直达占位页：诚实拦回 PG-01。
  if (auth.status !== 'paired') {
    await router.replace({ name: 'connect' });
    return;
  }
  try {
    const host = await getHostSummary();
    auth.applyAuthorized(host);
  } catch (rawErr) {
    const err = toApiRequestError(rawErr);
    if (err.code === ERROR_CODE_AUTH_UNPAIRED || err.code === ERROR_CODE_AUTH_REVOKED) {
      // E-03/E-04：授权失效 → 清态踢回 PG-01。
      auth.invalidateAuthorization(err.code === ERROR_CODE_AUTH_REVOKED ? 'revoked' : 'expired');
      await router.replace({ name: 'connect' });
      return;
    }
    checkError.value = err.message;
  } finally {
    checking.value = false;
  }
});
</script>

<template>
  <div class="lobby-page">
    <header class="lobby-header">
      <h1 class="lobby-title">会话大厅</h1>
      <p class="lobby-subtitle">占位页 · 大厅功能随 M2 交付</p>
    </header>

    <main class="lobby-main">
      <div v-if="checking" class="lobby-status" role="status">正在确认授权状态…</div>

      <template v-else>
        <section class="placeholder-card" aria-label="占位说明">
          <p class="placeholder-text">
            配对已完成，这台设备已获得授权。会话列表、启动与管理工作区将在下一里程碑（M2）交付，本页为诚实占位，不伪装功能。
          </p>
        </section>

        <section v-if="auth.device || auth.host" class="projection-card" aria-label="设备与宿主信息">
          <h2 class="projection-title">当前连接</h2>
          <dl class="projection-list">
            <template v-if="auth.device">
              <dt>设备</dt>
              <dd>{{ auth.device.name }}</dd>
              <dt>配对于</dt>
              <dd>{{ auth.device.pairedAt }}</dd>
            </template>
            <template v-if="auth.host">
              <dt>宿主服务</dt>
              <dd>{{ auth.host.serverVersion }} · API {{ auth.host.apiVersion }}</dd>
            </template>
          </dl>
        </section>

        <div v-if="checkError" class="lobby-error" role="alert">
          授权状态确认失败：{{ checkError }}
        </div>

        <button type="button" class="btn-secondary" @click="router.push({ name: 'connect' })">
          返回连接与配对
        </button>
      </template>
    </main>
  </div>
</template>

<style scoped>
.lobby-page {
  min-height: 100%;
  background: var(--VT-canvas);
  color: var(--VT-text);
  padding: 24px 20px 40px;
}

.lobby-header {
  margin-bottom: 20px;
}

.lobby-title {
  margin: 0;
  font-size: 20px;
  font-weight: 700;
}

.lobby-subtitle {
  margin: 4px 0 0;
  font-size: 13px;
  color: var(--VT-text-secondary);
}

.lobby-main {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.lobby-status {
  font-size: 14px;
  color: var(--VT-text-secondary);
}

.placeholder-card {
  padding: 16px;
  background: var(--VT-surface);
  border: 1px solid var(--VT-border);
  border-left: 4px solid var(--VT-accent);
  border-radius: 10px;
}

.placeholder-text {
  margin: 0;
  font-size: 14px;
  line-height: 1.6;
}

.projection-card {
  padding: 16px;
  background: var(--VT-surface);
  border: 1px solid var(--VT-border);
  border-radius: 10px;
}

.projection-title {
  margin: 0 0 10px;
  font-size: 15px;
  font-weight: 700;
}

.projection-list {
  margin: 0;
  display: grid;
  grid-template-columns: auto 1fr;
  gap: 6px 12px;
  font-size: 14px;
}

.projection-list dt {
  color: var(--VT-text-secondary);
  font-weight: 600;
}

.projection-list dd {
  margin: 0;
  word-break: break-word;
}

.lobby-error {
  padding: 12px 14px;
  background: var(--VT-surface);
  border: 1px solid var(--VT-border);
  border-left: 4px solid var(--VT-danger);
  border-radius: 10px;
  font-size: 14px;
}

.btn-secondary {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-height: 44px;
  padding: 0 20px;
  border: 1px solid var(--VT-border-strong);
  border-radius: 8px;
  background: transparent;
  color: var(--VT-text);
  font-size: 16px;
  font-weight: 600;
  cursor: pointer;
}

.btn-secondary:focus-visible {
  outline: 2px solid var(--VT-accent);
  outline-offset: 2px;
}

@media (hover: hover) {
  .btn-secondary:hover {
    background: var(--VT-surface-raised);
  }
}
</style>
