/**
 * WSL 供给 UI 决策逻辑（SA-03/SA-17 修复配套）：
 * 从 WSLCLISettings.vue 抽出的纯逻辑——node 环境单测直接覆盖驱动模板/交互的
 * 分支（仓库测试基建无 DOM，与 quotaModel.ts 同模式）。
 *
 * cliImportOutcomeMessage 决定「导入所选」点击后的诚实提示：
 * - 零写入不报成功（修复前 UI 把 fresh 也计入成功，零导入虚报「已导入所选」）；
 * - 空选择是显式撤回入口（托管条目按来源删除语义移除）。
 */

export type SupplyOutcomeLike = { outcome: string; detail?: string }

export type ImportMessage = { kind: 'success' | 'info'; text: string }

/**
 * 根据「用户勾选数」与「后端逐单元结果」产生提示。
 *
 * 结果语义（wslsupply UnitOutcome）：
 * - applied：已写入/移除（含撤回移除）；
 * - fresh：无变化（未写入任何内容）；
 * - blocked/failed：保持现状/失败。
 */
export function cliImportOutcomeMessage(selectedCount: number, outcomes: SupplyOutcomeLike[]): ImportMessage {
  if (outcomes.length === 0) {
    return { kind: 'info', text: '导入未返回结果（详见预览）' }
  }
  const applied = outcomes.filter((o) => o.outcome === 'applied')
  const failed = outcomes.filter((o) => o.outcome === 'failed' || o.outcome === 'blocked')
  if (selectedCount === 0) {
    // 空选择 = 显式撤回：applied 表示托管条目已按来源删除语义移除。
    if (applied.length > 0) {
      return { kind: 'success', text: `已按空选择撤回导入（移除 ${applied.length} 个托管条目）` }
    }
    return { kind: 'info', text: '已提交空选择：无托管条目需要移除（目标内容未变化）' }
  }
  if (applied.length === 0) {
    return { kind: 'info', text: '未写入任何条目：所选键无变化或不可导入（详见预览，不视为导入成功）' }
  }
  if (failed.length > 0) {
    return {
      kind: 'info',
      text: `导入完成：${applied.length} 项写入，${failed.length} 项保持现状（冻结/未托管/不可转换，详见预览）`,
    }
  }
  return { kind: 'success', text: `已写入 ${applied.length} 项（冻结/未托管/不可转换条目保持 WSL 现状）` }
}
