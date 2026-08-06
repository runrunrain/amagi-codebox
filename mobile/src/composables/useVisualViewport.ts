/**
 * useVisualViewport — M4-A 软键盘 VisualViewport 跟随（全局 CSS 变量）
 * ---------------------------------------------------------------------------
 * 职责：监听 window.visualViewport 的 resize/scroll，把可视高度写为
 * :root CSS 变量，供布局层消费：
 *   --vvh        可视高度（px；软键盘弹出时收缩后的真实可视高度）
 *   --vv-offset  可视区相对布局视口的纵向偏移（iOS 键盘弹出时 >0）
 *
 * 平台差异（如实声明，模拟断言 + 真机验证待 M4-C）：
 *   · Android Chrome：100dvh 随软键盘收缩（resize 行为近似 interactive-widget
 *     resizes-content）；--vvh 与 100dvh 大体一致，变量冗余但无害。
 *   · iOS Safari / WKWebView(Capacitor)：100dvh/100vh **不随**软键盘收缩，
 *     布局视口不变、仅 visualViewport 收缩并可能整体上移（offsetTop>0）——
 *     此平台是 --vvh/--vv-offset 的必需场景；无 visualViewport API 的老
 *     WebView 回落 100dvh（fallback 即现状，不劣化）。
 * 消费方式：高度容器用 `height: var(--vvh, 100dvh)`（fallback 链保持无 JS/
 * 旧 WebView 可用）；底部停靠元素（Composer）随容器收缩自然跟随。
 * 降级：无 window.visualViewport（老 Android WebView）→ 不写变量，纯 100dvh。
 * ---------------------------------------------------------------------------
 */
import { onBeforeUnmount, onMounted } from 'vue';

let listenerCount = 0;

function writeVars(): void {
  const vv = window.visualViewport;
  const root = document.documentElement;
  if (!vv) {
    root.style.removeProperty('--vvh');
    root.style.removeProperty('--vv-offset');
    return;
  }
  root.style.setProperty('--vvh', `${Math.round(vv.height)}px`);
  root.style.setProperty('--vv-offset', `${Math.round(vv.offsetTop)}px`);
}

export function useVisualViewport(): void {
  onMounted(() => {
    listenerCount += 1;
    if (typeof window === 'undefined' || !window.visualViewport) return;
    writeVars();
    window.visualViewport.addEventListener('resize', writeVars);
    window.visualViewport.addEventListener('scroll', writeVars);
  });

  onBeforeUnmount(() => {
    listenerCount = Math.max(0, listenerCount - 1);
    if (typeof window === 'undefined' || !window.visualViewport) return;
    if (listenerCount === 0) {
      window.visualViewport.removeEventListener('resize', writeVars);
      window.visualViewport.removeEventListener('scroll', writeVars);
      document.documentElement.style.removeProperty('--vvh');
      document.documentElement.style.removeProperty('--vv-offset');
    }
  });
}
