import { describe, it, expect, beforeEach, vi } from 'vitest';
import { requestBake, cancelBake, currentSkinImage } from '../../utils/skinBake';

/**
 * 回归：requestBake 曾在同步 no-op 路径（blur<=0 / 烘焙不可用 / 缓存命中）同步回调
 * onDone，而 App.vue 传入的 onDone 是 syncSkinDom 本身 → syncSkinDom → requestBake →
 * onDone(...) 同步无限递归（blur=0 或烘焙失败后必现 RangeError: Maximum call stack）。
 * 修复后同步 no-op 路径不回调（调用方已在同一同步渲染中经 currentSkinImage 取到终态）。
 */
describe('skinBake 同步 no-op 路径不回调 onDone', () => {
  beforeEach(() => {
    cancelBake();
  });

  it('blur<=0：不回调且回落原图（曾在 jsdom/禁用烘焙环境触发同步递归栈溢出）', () => {
    const onDone = vi.fn();
    expect(() => requestBake({ url: 'x://skin', blur: 0, dim: 0.3 }, onDone)).not.toThrow();
    expect(onDone).not.toHaveBeenCalled();
    expect(currentSkinImage({ url: 'x://skin', blur: 0, dim: 0.3 })).toEqual({ url: 'x://skin', blurred: false });
  });

  it('烘焙不可用（测试环境无 2d filter）：同样不回调', () => {
    const onDone = vi.fn();
    expect(() => requestBake({ url: 'x://skin', blur: 8, dim: 0.3 }, onDone)).not.toThrow();
    expect(onDone).not.toHaveBeenCalled();
    // 烘焙不可用时 currentSkinImage 永远回落原图
    expect(currentSkinImage({ url: 'x://skin', blur: 8, dim: 0.3 }).blurred).toBe(false);
  });

  it('模拟 App.vue 回调重入模式：onDone 内再调 requestBake 不会形成同步循环', () => {
    let calls = 0;
    const reentrant = () => {
      calls++;
      // 模拟 syncSkinDom 尾部行为：回调中再次发起烘焙请求
      requestBake({ url: 'x://skin', blur: 0, dim: 0.3 }, reentrant);
    };
    requestBake({ url: 'x://skin', blur: 0, dim: 0.3 }, reentrant);
    expect(calls).toBe(0); // 修复后根本不会进入回调，循环无从发生
  });
});
