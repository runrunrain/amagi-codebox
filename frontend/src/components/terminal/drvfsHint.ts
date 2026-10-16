/**
 * drvfsHint — 失败处关联提示的纯匹配逻辑（渲染解耦，先例 pathPickerModel.ts）。
 *
 * GitPanel / PathPickerDialog 在失败态拉取 advisory 快照后，用本助手判断
 * 「当前路径是否落在已知 DrvFS 挂载上」：命中则返回提示文案，未命中返回
 * null（组件不渲染提示行）。不依赖 Vue/DOM，node 环境可直测。
 */
import type { DrvFSAdvisoryEntry } from '../../api/drvfsAdvisory'

/** mountPath 前缀匹配时忽略挂载点尾部的盘符根斜杠差异。 */
function normalizeMount(p: string): string {
  const t = p.trim()
  return t.endsWith('/') ? t.slice(0, -1) : t
}

/**
 * 对路径 path 找最新命中的 DrvFS advisory（按 at 降序取第一条）：
 * 命中条件＝path 与记录的 workDir 相同，或 path 落在 mountPath 之下
 *（含 mountPath 本身）。命中返回提示文案，否则 null。
 */
export function drvfsHintFor(entries: DrvFSAdvisoryEntry[], path: string): string | null {
  const p = path.trim()
  if (!p || entries.length === 0) return null
  const sorted = [...entries].sort((a, b) => b.at - a.at)
  for (const e of sorted) {
    const mp = normalizeMount(e.mountPath || '')
    if (!mp) continue
    if (p === e.workDir || p === mp || p.startsWith(mp + '/')) {
      return `该目录为 DrvFS 挂载（${mp}）：Windows/WSL 侧工具存在路径形态与 I/O 性能差异（stat 密集/小文件写显著慢），建议将工作区放在 WSL ext4（如 ~/）内或改用 Windows 盘符路径`
    }
  }
  return null
}
