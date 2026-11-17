/**
 * basePath.ts — 运行时 base 推导（design §7.2 双部署形态硬约束，t7）。
 *
 * 同一份构建产物必须双形态运行：
 *   - 桌面：http://127.0.0.1:<port>/            → dir = '/'
 *   - 远程：https://<remote>/webui/{sid}/        → dir = '/webui/{sid}/'
 * （两种 URL 均以 / 结尾；资源相对化由 vite base:'./' 保证，本模块负责
 * API/WS 请求地址的同一纪律。）
 *
 * 规则（design §7.2-2）：
 *   - dir = pathname 截至最后一个 '/'（含）；
 *   - api(path) = dir + path（dir='/webui/s1/' → '/webui/s1/api/info'；dir='/' → '/api/info'）；
 *   - wsUrl() = (protocol==='https:' ? 'wss' : 'ws') + '://' + host + dir + 'ws/events'；
 *   - query（?skin=light）与 fragment（#/t=）不参与推导。
 *
 * 红线：所有 fetch 与 WS 一律经此推导——禁止根绝对路径 '/api/...'（远程子
 * 路径形态下打到反代根 404）与硬编码 127.0.0.1（反代场景下那是移动设备
 * 自身；pi 同款教训：基址推导以路径前缀判定而非 hostname）。
 */

/** 与 window.location 同形的最小位置面（可注入，纯函数可测）。 */
export interface PlaneLocation {
  protocol: string
  host: string
  pathname: string
}

/** 推导出的请求基址面。 */
export interface PlaneBase {
  /** 目录前缀（恒以 '/' 结尾）。 */
  apiBase: string
  /** 拼 API 路径：容忍调用侧带多余的前导 '/'（防御纵深——即使误写 '/api/info' 也不会丢子路径前缀）。 */
  api(path: string): string
  /** WS 事件流地址（协议按 https→wss / 其他→ws 换算；不含 query，续传 ?since= 由调用方追加）。 */
  wsUrl(): string
}

/** 截取 pathname 的目录前缀（含末尾 '/'）；防御性剥掉混入的 query/fragment 段。 */
function dirOf(pathname: string): string {
  let p = pathname
  const cut = p.search(/[?#]/)
  if (cut !== -1) p = p.slice(0, cut)
  const i = p.lastIndexOf('/')
  if (i === -1) return '/'
  return p.slice(0, i + 1)
}

/**
 * resolvePlaneBase 从位置信息推导请求基址（纯函数）。
 * 输入取 window.location 的 protocol/host/pathname 即可——query 与 fragment
 * 天然不在 pathname 内，即使调用方误传整段 URL 也被防御性截断。
 */
export function resolvePlaneBase(loc: PlaneLocation): PlaneBase {
  const dir = dirOf(loc.pathname)
  return {
    apiBase: dir,
    api(path: string): string {
      return dir + path.replace(/^\/+/, '')
    },
    wsUrl(): string {
      const scheme = loc.protocol === 'https:' ? 'wss' : 'ws'
      return `${scheme}://${loc.host}${dir}ws/events`
    },
  }
}
