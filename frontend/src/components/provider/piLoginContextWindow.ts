// Pi 登录预设「上下文窗口」输入的归一化（组件 PiLoginProviderDetail 消费）。
//
// number input 的 v-model 会被 Vue 隐式转换为 number（type="number" 自带
// castToNumber，无需 .number 修饰符），表单状态可能是 string 或 number——
// 对 number 调 .trim() 会抛 TypeError（diting F01）。归一化统一处理：
// 留空/非法/非正 → 0 = 不覆写（Pi 内置目录窗口）；正整数 → 原值。
export function normalizeContextWindowInput(value: string | number | null | undefined): number {
  const cw = Number.parseInt(String(value ?? '').trim(), 10);
  return Number.isFinite(cw) && cw > 0 ? cw : 0;
}
