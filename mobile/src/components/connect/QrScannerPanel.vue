<script setup lang="ts">
/**
 * QrScannerPanel — 扫码面板（html5-qrcode，E-11 分类降级）
 * 失败分类（不笼统失败）：
 *   permission-denied  相机权限被拒 → 引导改用手动输入
 *   no-camera          无摄像头/无可用相机 API → 引导改用手动输入
 *   unavailable        其他启动失败 → 提示并降级
 * 扫描成功 emit('decoded', 原始文本)，解析归父组件（QR 载荷格式权威在桌面端）。
 */
import { onBeforeUnmount, onMounted, ref } from 'vue';
// html5-qrcode 动态导入：仅在用户进入扫码路径时加载（P5 §9 首屏预算）。
import type { Html5Qrcode as Html5QrcodeInstance } from 'html5-qrcode';

export type ScannerFailure = 'permission-denied' | 'no-camera' | 'unavailable';

const emit = defineEmits<{
  (e: 'decoded', text: string): void;
  (e: 'failed', kind: ScannerFailure): void;
  (e: 'cancel'): void;
}>();

const REGION_ID = 'pg01-qr-region';
const starting = ref(true);
const failure = ref<ScannerFailure | null>(null);

let scanner: Html5QrcodeInstance | null = null;
let stopped = false;

function classifyError(err: unknown): ScannerFailure {
  const message = err instanceof Error ? err.message : String(err);
  const name = err instanceof DOMException ? err.name : '';
  if (name === 'NotAllowedError' || /permission|denied|NotAllowedError/i.test(message)) {
    return 'permission-denied';
  }
  if (
    name === 'NotFoundError' || name === 'OverconstrainedError' ||
    /NotFoundError|no camera|No camera|Requested device not found|getUserMedia is not supported/i.test(message)
  ) {
    return 'no-camera';
  }
  return 'unavailable';
}

async function start(): Promise<void> {
  stopped = false;
  starting.value = true;
  failure.value = null;
  if (typeof navigator === 'undefined' || !navigator.mediaDevices?.getUserMedia) {
    failure.value = 'no-camera';
    starting.value = false;
    emit('failed', failure.value);
    return;
  }
  try {
    const { Html5Qrcode } = await import('html5-qrcode');
    if (stopped) return;
    scanner = new Html5Qrcode(REGION_ID);
    await scanner.start(
      { facingMode: 'environment' },
      { fps: 10, qrbox: { width: 220, height: 220 } },
      (decodedText) => {
        emit('decoded', decodedText);
      },
      () => {
        // 帧级"未识别到二维码"是常态，不视为错误。
      },
    );
    if (stopped) {
      if (scanner.isScanning) {
        await scanner.stop();
      }
      scanner.clear();
      scanner = null;
      return;
    }
    starting.value = false;
  } catch (err) {
    if (stopped) return;
    const kind = classifyError(err);
    failure.value = kind;
    starting.value = false;
    emit('failed', kind);
  }
}

async function stop(): Promise<void> {
  if (stopped) return;
  stopped = true;
  if (scanner) {
    try {
      if (scanner.isScanning) {
        await scanner.stop();
      }
      scanner.clear();
    } catch {
      // 停止失败不阻断退出扫码流程。
    }
    scanner = null;
  }
}

async function handleCancel(): Promise<void> {
  await stop();
  emit('cancel');
}

onMounted(start);
onBeforeUnmount(stop);

defineExpose({ stop });
</script>

<template>
  <div class="qr-panel">
    <div v-if="starting" class="qr-status" role="status">正在启动相机…</div>

    <template v-if="failure">
      <div class="qr-failure" role="alert">
        <p v-if="failure === 'permission-denied'" class="qr-failure-text">
          相机权限被拒绝。扫码不可用，请改用下方「手动输入」完成配对；
          或在浏览器/系统设置中允许相机后重试。
        </p>
        <p v-else-if="failure === 'no-camera'" class="qr-failure-text">
          这台设备没有可用的摄像头。请改用下方「手动输入」完成配对。
        </p>
        <p v-else class="qr-failure-text">
          相机启动失败。请改用下方「手动输入」完成配对，或重试扫码。
        </p>
      </div>
    </template>

    <div :id="REGION_ID" class="qr-region" :class="{ 'qr-region--hidden': failure !== null }"></div>

    <div class="qr-actions">
      <button v-if="failure && failure === 'unavailable'" type="button" class="qr-retry" @click="start">
        重试扫码
      </button>
      <button type="button" class="qr-cancel" @click="handleCancel">取消扫码</button>
    </div>
  </div>
</template>

<style scoped>
.qr-panel {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.qr-status {
  padding: 12px;
  text-align: center;
  font-size: 14px;
  color: var(--VT-text-secondary);
}

.qr-region {
  width: 100%;
  overflow: hidden;
  border-radius: 10px;
  border: 1px solid var(--VT-border-strong);
  background: var(--VT-surface-dark);
  min-height: 220px;
}

.qr-region--hidden {
  display: none;
}

.qr-failure {
  padding: 14px;
  background: var(--VT-surface);
  border: 1px solid var(--VT-border);
  border-left: 4px solid var(--VT-warning);
  border-radius: 10px;
}

.qr-failure-text {
  margin: 0;
  font-size: 14px;
  line-height: 1.55;
  color: var(--VT-text);
}

.qr-actions {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.qr-cancel,
.qr-retry {
  min-height: 44px;
  min-width: 44px;
  padding: 0 20px;
  border-radius: 8px;
  font-size: 15px;
  font-weight: 600;
  cursor: pointer;
}

.qr-cancel {
  border: 1px solid var(--VT-border-strong);
  background: transparent;
  color: var(--VT-text);
}

.qr-retry {
  border: none;
  background: var(--VT-accent-strong);
  color: #fff;
}

.qr-cancel:focus-visible,
.qr-retry:focus-visible {
  outline: 2px solid var(--VT-accent);
  outline-offset: 2px;
}

@media (hover: hover) {
  .qr-cancel:hover {
    background: var(--VT-surface-raised);
  }
}
</style>
