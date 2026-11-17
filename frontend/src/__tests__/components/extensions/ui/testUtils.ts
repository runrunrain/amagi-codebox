/**
 * extensions/ui 共享组件测试基建（契约 §8 范式：node 环境 + renderToString，
 * 禁新增 DOM 测试库；渲染断言只查字符串/class，交互断言直调 defineExpose 触发器）。
 *
 * 机制实证（对 node_modules 内安装的 vue dist 源码核对）：
 *  - server-renderer 的 renderToString 每次调用都 createVNode 新建组件实例，
 *    且不走客户端 patch（无 setRef），模板 ref 捕获不可用
 *    （server-renderer.esm-bundler.js:648-657）；
 *  - setupComponent 完整执行 setup，defineExpose 经 ctx.expose 同步写入
 *    instance.exposed；全局 mixin 的 beforeCreate 钩子由 applyOptions 在组件
 *    自身 setup 之后调用（runtime-core.esm-bundler.js:8286-8352、8432-8441），
 *    此刻 exposed 已就绪 → 渲染前用 getCurrentInstance 捕获实例；
 *  - 状态变更后的「同实例重渲」复刻 renderComponentSubTree 调用 ssrRender 的
 *    形态（server-renderer.esm-bundler.js:365-438）。
 */
import { createSSRApp, defineComponent, getCurrentInstance, h } from 'vue';
import type { Component, ComponentInternalInstance, VNode } from 'vue';
import { renderToString } from 'vue/server-renderer';

export interface CapturedRender {
  html: string;
  /** ctx.teleports：Teleport 到 body 的弹层内容（Dialog/ActionMenu 断言用） */
  teleports: Record<string, string>;
  /** 按创建顺序捕获的组件实例（含 wrapper 与目标组件及其子组件） */
  instances: ComponentInternalInstance[];
}

/** 渲染目标组件并捕获全部组件实例（全局 mixin beforeCreate，见文件头说明） */
export async function renderWithCapture(
  root: Component,
  props: Record<string, unknown> = {},
  slots: Record<string, () => VNode[]> = {},
): Promise<CapturedRender> {
  const instances: ComponentInternalInstance[] = [];
  const Wrapper = defineComponent({
    setup() {
      return () => h(root, props, slots);
    },
  });
  const app = createSSRApp(Wrapper);
  app.mixin({
    beforeCreate() {
      const instance = getCurrentInstance();
      if (instance) {
        instances.push(instance);
      }
    },
  });
  const context: { teleports?: Record<string, string> } = {};
  const html = await renderToString(app, context);
  return { html, teleports: context.teleports ?? {}, instances };
}

/** 取目标组件（instance.type === 导入的 SFC 默认导出）的被捕获实例 */
export function instanceOf(
  instances: ComponentInternalInstance[],
  component: Component,
): ComponentInternalInstance {
  const found = instances.find((instance) => instance.type === component);
  if (!found) {
    throw new Error(`目标组件未被渲染：${String(component)}`);
  }
  return found;
}

/** ssrRender 的 push 收集项：字符串 | Promise（异步子树） | 嵌套 buffer */
type PushItem = string | Promise<unknown> | PushItem[];

async function unrollItem(item: PushItem): Promise<string> {
  if (typeof item === 'string') {
    return item;
  }
  if (Array.isArray(item)) {
    let out = '';
    for (const nested of item) {
      out += await unrollItem(nested);
    }
    return out;
  }
  // await 的结果经 Awaited 递归展开会塌缩为 unknown，对结果断言回 PushItem
  return unrollItem(await item as PushItem);
}

type SsrRenderFn = (...args: unknown[]) => void;

/**
 * ssrRender 访问所需的实例内部字段。`vue` 包导出的 ComponentInternalInstance
 * 公共类型不含 ssrRender / inheritAttrs / setupState / ctx（内部实现字段），
 * 此处按 server-renderer 实际调用形态做结构化断言（不经 any）。
 */
interface SsrRenderInstance {
  ssrRender?: SsrRenderFn;
  inheritAttrs?: boolean;
  attrs: Record<string, unknown>;
  props: Record<string, unknown>;
  setupState: Record<string, unknown>;
  data: Record<string, unknown>;
  ctx: Record<string, unknown>;
}

/**
 * 对已捕获实例按当前（可能已被 expose 触发器修改过的）状态重渲子树，
 * 返回 HTML 字符串。复刻 server-renderer 的 ssrRender 调用形态。
 */
export async function renderInstanceAgain(instance: ComponentInternalInstance): Promise<string> {
  const component = instance.type as { ssrRender?: SsrRenderFn };
  const ssrInstance = instance as unknown as SsrRenderInstance;
  const ssrRender = (ssrInstance.ssrRender ?? component.ssrRender) as SsrRenderFn | undefined;
  if (!ssrRender) {
    throw new Error('组件缺少 ssrRender，无法重渲（检查 SFC 是否被 SSR 编译）');
  }
  const collected: PushItem[] = [];
  const push = (item: PushItem) => {
    collected.push(item);
  };
  ssrRender(
    instance.proxy,
    push,
    instance,
    ssrInstance.inheritAttrs !== false ? ssrInstance.attrs : undefined,
    ssrInstance.props,
    ssrInstance.setupState,
    ssrInstance.data,
    ssrInstance.ctx,
  );
  let out = '';
  for (const item of collected) {
    out += await unrollItem(item);
  }
  return out;
}
