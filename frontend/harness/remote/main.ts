/**
 * TEST-ONLY harness 入口（M1-C PG-05 浏览器证据用）。
 * 仅在 vite dev 下经 /harness/remote/ 访问；不进入生产构建。
 * 关键：必须在挂载应用前注入 window.go.main.App stub（wailsjs 绑定运行时调用它）。
 */
import { createApp } from 'vue';
import { createPinia } from 'pinia';
import '../../src/styles/index.css';
import { installRemoteStub } from './stub';
import HarnessApp from './HarnessApp.vue';

installRemoteStub();

createApp(HarnessApp).use(createPinia()).mount('#app');
