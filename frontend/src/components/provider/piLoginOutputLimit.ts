// 输出额度与上下文窗口是不同的模型元数据；未知额度绝不猜测。
export function modelOutputMaximum(models: ReadonlyArray<{ id: string; maxTokens?: number }>, model: string): number {
  const value = models.find(item => item.id === model.trim())?.maxTokens;
  return Number.isSafeInteger(value) && value! > 0 ? value! : 0;
}

export function normalizeOutputLimitInput(input: string | number): number {
  if (typeof input === 'string' && input.trim() === '') return 0;
  const value = Number(input);
  if (!Number.isSafeInteger(value) || value < 0) {
    throw new Error('输出上限须为非负整数；留空或 0 使用 Pi 目录默认值');
  }
  return value;
}
