/**
 * 皮肤预烘焙引擎（性能修复 v1.3.38）
 *
 * 背景：皮肤层曾用逐帧 CSS `filter: blur()`（全窗）+ 透明根 + 全窗蒙版合成，
 * 在 GPU 加速不可用/回退软件光栅的 WebView（Windows GPU 黑名单/RDP/虚拟机
 * 常见）上每帧成本数百毫秒，直接拖垮终端打字回显与整体操作响应。
 *
 * 方案：把模糊与调光在**调参时一次性烘焙**进位图——
 *   1. `createImageBitmap`（异步解码，不阻塞主线程）→ drawImage 到
 *      `filter: blur()` 的离屏 canvas（一次性）；
 *   2. dim 压暗与 scale(1.08) 边缘补偿同步烘焙；
 *   3. `canvas.toDataURL()` 产出烘焙图，皮肤层运行期零 filter、零逐帧混合，
 *      仅剩一张普通背景图 + 极低成本的纯色层；
 *   4. 烘焙在低分辨率（最长边 ≤1280）离屏 canvas 上完成，成本与分辨率解耦。
 *
 * 滑块拖动 = 防抖重烘焙（500ms）+ 过程中先回落原图直显（不卡 UI）；
 * 烘焙完成后原子换图。烘焙失败（极老 WebView 无 filter/低内存）→ 永久回退
 * CSS 直显模式（行为同 v1.3.32 前，仍正确只是无优化）。
 */

const BAKE_MAX_EDGE = 1280
const BAKE_DEBOUNCE_MS = 500
const EDGE_SCALE = 1.08

export interface SkinBakeParams {
  url: string
  blur: number
  dim: number
}

export interface BakedSkin {
  url: string
  blurred: boolean
}

interface BakeState {
  token: number
  bitmap: ImageBitmap | null
  bitmapUrl: string
}

const state: BakeState = { token: 0, bitmap: null, bitmapUrl: '' }

let debounceTimer: ReturnType<typeof setTimeout> | null = null
let bakeFailed = false

/** 烘焙能力探测：canvas 2d filter 与 createImageBitmap 均可用才启用。 */
export function bakeSupported(): boolean {
  if (bakeFailed) return false
  try {
    if (typeof createImageBitmap !== 'function') return false
    const c = document.createElement('canvas')
    const ctx = c.getContext('2d')
    if (!ctx) return false
    ctx.filter = 'blur(1px)'
    return ctx.filter === 'blur(1px)'
  } catch {
    return false
  }
}

/**
 * 同步取当前应显示的皮肤 URL：烘焙产物优先；烘焙中/不可用时回落原图。
 * blur=0 时无需烘焙，直接原图（skin.css 会输出 filter:none）。
 */
export function currentSkinImage(p: SkinBakeParams): BakedSkin {
  const needBake = p.blur > 0
  if (!needBake || !bakeSupported()) {
    return { url: p.url, blurred: false }
  }
  const baked = bakedResult
  if (baked && baked.srcUrl === p.url && baked.blur === p.blur && baked.dim === p.dim) {
    return { url: baked.dataUrl, blurred: true }
  }
  return { url: p.url, blurred: false } // 烘焙中先直显原图
}

interface BakedResult {
  srcUrl: string
  blur: number
  dim: number
  dataUrl: string
}

let bakedResult: BakedResult | null = null

/** 取消未落盘的烘焙（皮肤关闭/切换图片时调用）。 */
export function cancelBake(): void {
  state.token++
  if (debounceTimer !== null) {
    clearTimeout(debounceTimer)
    debounceTimer = null
  }
  bakedResult = null
}

/**
 * 请求烘焙（防抖）。onDone 在烘焙完成后回调（无论结果是否变化），调用方
 * 据此原子换图。同一时刻仅最后一次请求有效（token 代际）。
 */
export function requestBake(p: SkinBakeParams, onDone: () => void): void {
  if (p.blur <= 0 || !bakeSupported()) {
    bakedResult = null
    onDone()
    return
  }
  if (bakedResult && bakedResult.srcUrl === p.url && bakedResult.blur === p.blur && bakedResult.dim === p.dim) {
    onDone() // 已是最新
    return
  }
  const token = ++state.token
  if (debounceTimer !== null) clearTimeout(debounceTimer)
  debounceTimer = setTimeout(() => {
    void bake(p, token, onDone)
  }, BAKE_DEBOUNCE_MS)
}

async function bake(p: SkinBakeParams, token: number, onDone: () => void): Promise<void> {
  if (token !== state.token) return
  try {
    let bmp = state.bitmap
    if (state.bitmapUrl !== p.url) {
      bmp?.close()
      const resp = await fetch(p.url)
      if (token !== state.token) return
      const blob = await resp.blob()
      if (token !== state.token) return
      bmp = await createImageBitmap(blob)
      state.bitmap = bmp
      state.bitmapUrl = p.url
    }
    if (token !== state.token || !bmp) return

    // 低分辨率离屏烘焙：成本与屏幕分辨率/图片原始分辨率解耦。
    const scale = Math.min(1, BAKE_MAX_EDGE / Math.max(bmp.width, bmp.height))
    const w = Math.max(1, Math.round(bmp.width * scale))
    const h = Math.max(1, Math.round(bmp.height * scale))
    const canvas = document.createElement('canvas')
    canvas.width = w
    canvas.height = h
    const ctx = canvas.getContext('2d')
    if (!ctx) throw new Error('no 2d context')
    ctx.filter = `blur(${p.blur * scale}px)`
    // scale(1.08) 边缘补偿烘焙进位图：cover 放大裁切语义由 CSS 承担，
    // 这里等比放大再绘制，保证模糊后的边缘不在烘焙图内露底。
    const over = EDGE_SCALE
    const dw = w * over
    const dh = h * over
    ctx.drawImage(bmp, (w - dw) / 2, (h - dh) / 2, dw, dh)
    ctx.filter = 'none'
    // dim 压暗烘焙（black alpha 覆盖；0.35 下限由调用方 clamp 后传入）。
    if (p.dim > 0) {
      ctx.fillStyle = `rgba(0, 0, 0, ${p.dim})`
      ctx.fillRect(0, 0, w, h)
    }
    if (token !== state.token) return
    bakedResult = { srcUrl: p.url, blur: p.blur, dim: p.dim, dataUrl: canvas.toDataURL('image/jpeg', 0.85) }
    onDone()
  } catch {
    // 烘焙失败：本次会话永久回退 CSS 直显（不再反复尝试）。
    bakeFailed = true
    bakedResult = null
    onDone()
  }
}
