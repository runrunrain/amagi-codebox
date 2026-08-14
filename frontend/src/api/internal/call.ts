/**
 * API 调用共享包装器（仅 frontend/src/api 内部使用，勿向 views/stores 暴露）
 *
 * 以 ompPlugin.ts 的 callOmp 为范本推广到全部 api 模块，统一
 * "log + rethrow" 错误处理语义：失败时打印带 `[api.<module>.<fn>]`
 * 操作上下文的日志，然后原样 rethrow——调用方拿到的 rejection 与直接调用
 * wails 绑定完全一致（不包装、不替换错误对象）。
 *
 * 注意：ompPlugin.ts 的 callOmp 是另一种契约（用中文操作上下文包装
 * error.message 供视图直接展示，且不打印日志），属该模块私有实现，
 * 有意不在此合并。
 */
export async function callApi<T>(context: string, fn: () => Promise<T>): Promise<T> {
  try {
    return await fn();
  } catch (error) {
    console.error(context, error);
    throw error;
  }
}
