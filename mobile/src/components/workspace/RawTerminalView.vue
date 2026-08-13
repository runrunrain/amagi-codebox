<script setup lang="ts">
/**
 * RawTerminalView — PG-04 终端诊断视图网格（M2-D）
 * ---------------------------------------------------------------------------
 * 权威依据：P5 v1.2 §PG-04 + CHG-20260801-05（原始终端降级为按需诊断视图，
 * 非默认、非并列 tab）+ §9（xterm/addon 仅在进入诊断视图时动态导入）。
 * 语义定论（如实声明）：
 *   · 网格只读（disableStdin）；输入与主阅读面完全一致——走同一个
 *     ComposerBar + store 控制权过滤路径，本组件不产生任何输入帧；
 *   · 网格渲染同一会话的原始输出流（store 回放缓冲 + subscribe 直播续写）；
 *   · xterm 几何经 fit 后由 emit('resize') 上报（与主面同一 sendResize 路径，
 *     PR-04 几何写入规则不变；诊断视图提供精确网格，替代主面近似换算）；
 *   · E-10：引擎加载失败时明示不可用原因 + 回落指引，不假装可用；
 *   · 软键盘：visualViewport 变化即 refit，网格让位、Composer 保持可达。
 * 性能锚点：>300ms 加载出文字提示；回放/突发输出经 createBatchedWriter
 * 分批写入（单批 64KB，批间让出事件循环）；xterm scrollback 有界。
 * ---------------------------------------------------------------------------
 */
import { onMounted, onUnmounted, ref, watch } from 'vue';
import {
  buildXtermTheme,
  createBatchedWriter,
  readVtThemeTokens,
  type BatchedWriter,
} from '../../lib/rawTerminal';

const props = defineProps<{
  /** 打开时的回放快照（store.getRawTranscript()）。 */
  initialTranscript: string;
  /** 直播续写订阅（store.subscribeRawOutput）；返回退订函数。 */
  subscribe: (cb: (text: string) => void) => () => void;
  /** WS 已附着：附着完成后重 fit 并补报真实网格（挂载早于 attach 时首次
   * 上报会丢在未连接窗口，attach 后以此补齐；重连恢复同理）。 */
  wsAttached: boolean;
}>();

const emit = defineEmits<{
  /** xterm fit 后的真实网格尺寸（cols/rows），由父级走 store.sendResize。 */
  resize: [cols: number, rows: number];
}>();

type LoadState = 'loading' | 'ready' | 'unavailable';

const state = ref<LoadState>('loading');
/** >300ms 未完成加载时出文字提示（性能锚点；快速加载时不闪烁）。 */
const showLoadingHint = ref(false);
const unavailableReason = ref<string | null>(null);
const hostEl = ref<HTMLElement | null>(null);

interface TerminalLike {
  open(el: HTMLElement): void;
  write(data: string, callback?: () => void): void;
  dispose(): void;
  cols: number;
  rows: number;
}

interface FitAddonLike {
  fit(): void;
}

let terminal: TerminalLike | null = null;
let fitAddon: FitAddonLike | null = null;
let writer: BatchedWriter | null = null;
let unsubscribe: (() => void) | null = null;
let resizeObserver: ResizeObserver | null = null;
let loadingHintTimer: ReturnType<typeof setTimeout> | null = null;
let fitDebounceTimer: ReturnType<typeof setTimeout> | null = null;
let disposed = false;

function scheduleFit(): void {
  if (fitDebounceTimer) clearTimeout(fitDebounceTimer);
  fitDebounceTimer = setTimeout(() => {
    fitDebounceTimer = null;
    if (!terminal || !fitAddon || disposed) return;
    try {
      fitAddon.fit();
      emit('resize', terminal.cols, terminal.rows);
    } catch {
      // 宿主不可见（display:none）时 fit 会抛错；忽略，下次可见时重试。
    }
  }, 120);
}

onMounted(async () => {
  loadingHintTimer = setTimeout(() => {
    showLoadingHint.value = true;
  }, 300);
  // attach 完成后补一次 fit + 真实网格上报（见 props.wsAttached 说明）。
  watch(
    () => props.wsAttached,
    (attached) => {
      if (attached && terminal) scheduleFit();
    },
  );
  try {
    // 动态导入：xterm 引擎与样式仅在进入诊断视图时加载（§9 性能预算；
    // 字面 import() 保证 Vite 代码分割，主 bundle 不含 xterm）。
    const [xtermModule, fitModule] = await Promise.all([
      import('@xterm/xterm'),
      import('@xterm/addon-fit'),
      import('@xterm/xterm/css/xterm.css'),
    ]);
    if (disposed) return;
    const host = hostEl.value;
    if (!host) {
      state.value = 'unavailable';
      unavailableReason.value = '诊断视图宿主未就绪';
      return;
    }
    const term = new xtermModule.Terminal({
      theme: buildXtermTheme(readVtThemeTokens()),
      fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Consolas, monospace',
      fontSize: 12,
      lineHeight: 1.35,
      // 只读诊断语义：网格不接受键盘输入（输入走 Composer 同一路径）。
      disableStdin: true,
      cursorBlink: false,
      cursorStyle: 'bar',
      // 读屏可达（R05 §9）：xterm 自带 screenReaderMode。
      screenReaderMode: true,
      // 原始流保真：PT 输出自带 \r\n，不做 EOL 改写。
      convertEol: false,
      // 服务端 replay window 最多 1 MiB。最坏情况下每个字节都是换行，
      // 因而用 1 Mi 行作为上限，避免内容已到浏览器却又被 xterm 二次裁掉。
      // 诊断视图按需加载，常规移动端首屏不会承担这部分缓冲成本。
      scrollback: 1024 * 1024,
    }) as TerminalLike;
    const fit = new fitModule.FitAddon() as FitAddonLike;
    // loadAddon 为 xterm 实例方法；结构类型上未声明，运行时调用。
    (term as unknown as { loadAddon(addon: FitAddonLike): void }).loadAddon(fit);
    terminal = term;
    fitAddon = fit;
    term.open(host);
    try {
      fit.fit();
      emit('resize', term.cols, term.rows);
    } catch {
      scheduleFit();
    }
    writer = createBatchedWriter((chunk) => term.write(chunk), { maxBatchChars: 65_536 });
    // 回放 + 直播续写。
    if (props.initialTranscript) writer.push(props.initialTranscript);
    unsubscribe = props.subscribe((text) => writer?.push(text));
    state.value = 'ready';
    // 视口/容器变化（含软键盘弹收）即 refit。观察能力缺失属老 WebView 降级——
    // 退回 window resize 监听，不升级为 E-10（引擎本身可用）。
    try {
      if (typeof ResizeObserver !== 'undefined') {
        resizeObserver = new ResizeObserver(scheduleFit);
        resizeObserver.observe(host);
      } else {
        window.addEventListener('resize', scheduleFit);
      }
      window.visualViewport?.addEventListener('resize', scheduleFit);
    } catch {
      window.addEventListener('resize', scheduleFit);
    }
  } catch (err) {
    // E-10：能力缺失/加载失败——明示原因，不假装可用。
    if (disposed) return;
    state.value = 'unavailable';
    unavailableReason.value = err instanceof Error ? err.message : String(err);
  } finally {
    if (loadingHintTimer) {
      clearTimeout(loadingHintTimer);
      loadingHintTimer = null;
    }
  }
});

onUnmounted(() => {
  disposed = true;
  if (loadingHintTimer) clearTimeout(loadingHintTimer);
  if (fitDebounceTimer) clearTimeout(fitDebounceTimer);
  resizeObserver?.disconnect();
  resizeObserver = null;
  window.removeEventListener('resize', scheduleFit);
  window.visualViewport?.removeEventListener('resize', scheduleFit);
  unsubscribe?.();
  unsubscribe = null;
  // 卸载前排空缓冲，防丢尾部输出。
  writer?.flushAll();
  writer?.dispose();
  writer = null;
  terminal?.dispose();
  terminal = null;
  fitAddon = null;
});
</script>

<template>
  <div class="raw-terminal-view">
    <!-- 加载态：>300ms 出文字提示（非 spinner 动画，reduced-motion 天然合规） -->
    <div v-if="state === 'loading'" class="raw-status" role="status">
      <span v-if="showLoadingHint">正在加载终端诊断引擎…</span>
    </div>

    <!-- E-10：能力缺失诚实回落，不假装可用 -->
    <div v-else-if="state === 'unavailable'" class="raw-status raw-status--error" role="alert">
      <strong>诊断视图不可用</strong>
      <span>终端引擎未能加载<template v-if="unavailableReason">：{{ unavailableReason }}</template></span>
      <span>请返回主阅读面，原始内容可由等宽块（MonoBlock）兜底查看。</span>
    </div>

    <!-- 终端网格（二维例外区；暖深墨面 + VT ANSI 映射） -->
    <div
      v-show="state === 'ready'"
      ref="hostEl"
      class="raw-terminal-host"
      aria-label="终端原始输出（只读诊断网格）"
    ></div>
  </div>
</template>

<style scoped>
.raw-terminal-view {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  background: var(--VT-surface-dark);
}

.raw-status {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 8px;
  padding: 24px;
  color: var(--VT-on-dark);
  font-size: 13px;
  line-height: 1.6;
  text-align: center;
}

.raw-status--error strong {
  color: var(--VT-ansi-red);
  font-size: 14px;
}

.raw-terminal-host {
  flex: 1;
  min-height: 0;
  padding: 4px 0 4px 8px;
  overflow: hidden;
}

/* xterm 容器填满宿主（xterm.js 自带类名，非 scoped 选择器可命中子节点） */
.raw-terminal-host :deep(.xterm) {
  height: 100%;
}

/* 视口底色对齐 VT 暖深墨面：xterm.css 默认 .xterm-viewport 为 #000，
   theme.background 不覆盖该层（v6 实测），此处以令牌显式覆盖（P4「灯下暗格」）。 */
.raw-terminal-host :deep(.xterm-viewport) {
  background-color: var(--VT-surface-dark);
}
</style>
