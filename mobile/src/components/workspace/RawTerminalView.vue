<script setup lang="ts">
/**
 * RawTerminalView — 终端仿真网格（PG-04 诊断面 + P1-A 可交互仿真面升级）
 * ---------------------------------------------------------------------------
 * 权威依据：P5 v1.2 §PG-04 + 素材F §6（pi 达标方案）+ P1-A 执行契约。
 * 语义定论：
 *   · 可交互终端仿真：支持 stdin（xterm onData → emit('data') → store.sendRaw）；
 *   · 保留只读模式（props.readonly）：控制权被夺/观察态时自动降级只读并展示横幅；
 *   · 网格渲染同一会话的原始输出流（subscribe 登记时原子回放当前缓冲 + 直播续写）；
 *   · xterm 几何经 fit 后由 emit('resize') 上报（与主面同一 sendResize 路径）；
 *   · E-10：引擎加载失败时明示不可用原因 + 回落指引，不假装可用；
 *   · 软键盘：visualViewport 变化即 refit，网格让位、Composer 保持可达；
 *   · 触屏滚动：支持单指 touchmove 滑动视口内容；
 *   · 订阅即回放（P1-D，P1-C §7.1 缺陷①收口）：初始快照与直播订阅在 store 内
 *     原子衔接——引擎动态加载窗口内到达的 attach 历史不会丢。
 * ---------------------------------------------------------------------------
 */
import { onMounted, onUnmounted, ref, watch } from 'vue';
import {
  buildXtermTheme,
  createBatchedWriter,
  readVtThemeTokens,
  type BatchedWriter,
} from '../../lib/rawTerminal';

const props = withDefaults(
  defineProps<{
    /** 原始输出订阅（store.subscribeRawOutput）：登记时同步回放当前缓冲，
     * 其后直播续写（P1-D：回放与登记原子衔接）；返回退订函数。 */
    subscribe: (cb: (text: string) => void) => () => void;
    /** WS 已附着：附着完成后重 fit 并补报真实网格。 */
    wsAttached: boolean;
    /** 是否处于只读模式（控制权被夺/观察态时为 true，禁用 stdin）。 */
    readonly?: boolean;
    /** 处于只读模式的原因（由 store.writeBlockReason 提供）。 */
    readonlyReason?: string | null;
    /** 终端字号（默认 12）。 */
    fontSize?: number;
  }>(),
  {
    readonly: false,
    readonlyReason: null,
    fontSize: 12,
  },
);

const emit = defineEmits<{
  /** xterm fit 后的真实网格尺寸（cols/rows），由父级走 store.sendResize。 */
  resize: [cols: number, rows: number];
  /** xterm 终端输入事件（键盘输入、虚拟键盘、粘贴等），由父级走 store.sendRaw。 */
  data: [input: string];
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
  options?: unknown;
  onData?(cb: (data: string) => void): { dispose(): void };
  scrollLines?(n: number): void;
}

interface FitAddonLike {
  fit(): void;
}

let terminal: TerminalLike | null = null;
let fitAddon: FitAddonLike | null = null;
let writer: BatchedWriter | null = null;
let unsubscribe: (() => void) | null = null;
let dataDisposable: { dispose(): void } | null = null;
let resizeObserver: ResizeObserver | null = null;
let loadingHintTimer: ReturnType<typeof setTimeout> | null = null;
let fitDebounceTimer: ReturnType<typeof setTimeout> | null = null;
let disposed = false;

let touchStartY = 0;
function onTouchStart(e: TouchEvent): void {
  if (e.touches.length === 1) {
    touchStartY = e.touches[0].clientY;
  }
}
function onTouchMove(e: TouchEvent): void {
  if (e.touches.length === 1 && terminal?.scrollLines) {
    const deltaY = touchStartY - e.touches[0].clientY;
    const lines = Math.trunc(deltaY / 22);
    if (lines !== 0) {
      terminal.scrollLines(lines);
      touchStartY = e.touches[0].clientY;
    }
  }
}

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
  // 只读态变化动态切换 stdin 与光标
  watch(
    () => props.readonly,
    (ro) => {
      const termOpts = (terminal as unknown as { options?: Record<string, unknown> })?.options;
      if (termOpts) {
        termOpts.disableStdin = ro;
        termOpts.cursorBlink = !ro;
        termOpts.cursorStyle = ro ? 'bar' : 'block';
      }
    },
  );
  try {
    // 动态导入：xterm 引擎与样式仅在进入诊断/终端视图时加载（§9 性能预算；
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
      fontSize: props.fontSize,
      lineHeight: 1.35,
      // 输入控制：只读模式下禁用 stdin；可写模式放开 stdin
      disableStdin: props.readonly,
      cursorBlink: !props.readonly,
      cursorStyle: props.readonly ? 'bar' : 'block',
      // 读屏可达（R05 §9）：只读诊断下可用；重绘场景适度配置
      screenReaderMode: props.readonly,
      // 原始流保真：PT 输出自带 \r\n，不做 EOL 改写。
      convertEol: false,
      // 适度上限避免内存膨胀
      scrollback: props.readonly ? 1024 * 1024 : 10_000,
    }) as unknown as TerminalLike;
    const fit = new fitModule.FitAddon() as FitAddonLike;
    // loadAddon 为 xterm 实例方法；结构类型上未声明，运行时调用。
    (term as unknown as { loadAddon(addon: FitAddonLike): void }).loadAddon(fit);
    terminal = term;
    fitAddon = fit;
    term.open(host);

    // 挂载触屏滑动手势
    host.addEventListener('touchstart', onTouchStart, { passive: true });
    host.addEventListener('touchmove', onTouchMove, { passive: true });

    // 接通 stdin 输入通路（P1-A）：xterm onData 向上派发
    if (typeof term.onData === 'function') {
      dataDisposable = term.onData((data: string) => {
        if (props.readonly) return;
        emit('data', data);
      });
    }

    try {
      fit.fit();
      emit('resize', term.cols, term.rows);
    } catch {
      scheduleFit();
    }
    writer = createBatchedWriter((chunk) => term.write(chunk), { maxBatchChars: 65_536 });
    // 回放 + 直播续写：store.subscribeRawOutput 在登记订阅的同一同步调用内
    // 先回放当前缓冲（P1-D 竞态修复）——引擎加载间隙到达的 attach 历史不丢。
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
  dataDisposable?.dispose();
  dataDisposable = null;
  const host = hostEl.value;
  if (host) {
    host.removeEventListener('touchstart', onTouchStart);
    host.removeEventListener('touchmove', onTouchMove);
  }
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
    <!-- 只读模式横幅提示（控制权被夺 / 观察态） -->
    <div
      v-if="readonly && state === 'ready'"
      class="raw-readonly-banner"
      role="status"
      data-testid="terminal-readonly-indicator"
    >
      <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
        <rect x="3" y="11" width="18" height="11" rx="2" ry="2" />
        <path d="M7 11V7a5 5 0 0 1 10 0v4" />
      </svg>
      <span>{{ readonlyReason || '只读模式：当前无控制权，终端仅供观察' }}</span>
    </div>

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

    <!-- 终端网格（二维仿真区；暖深墨面 + VT ANSI 映射） -->
    <div
      v-show="state === 'ready'"
      ref="hostEl"
      class="raw-terminal-host"
      :aria-label="readonly ? '终端原始输出（只读诊断网格）' : '终端仿真网格（可交互）'"
      data-testid="terminal-host"
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

.raw-readonly-banner {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 6px 12px;
  background: var(--VT-surface);
  border-bottom: 1px solid var(--VT-border);
  color: var(--VT-warning);
  font-size: 12px;
  line-height: 1.4;
  flex-shrink: 0;
}

.raw-readonly-banner > svg {
  flex-shrink: 0;
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
