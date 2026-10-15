import vue from '../frontend/node_modules/@vitejs/plugin-vue/dist/index.mjs';
export default {
  root: new URL('..', import.meta.url).pathname,
  plugins: [vue()],
  optimizeDeps: { entries: ['e2e/ai-proxy.html'], include: ['vue', 'vue-router'] },
  resolve: { alias: { vue: new URL('../frontend/node_modules/vue/dist/vue.runtime.esm-bundler.js', import.meta.url).pathname, pinia: new URL('../frontend/node_modules/pinia/dist/pinia.mjs', import.meta.url).pathname, 'vue-router': new URL('../frontend/node_modules/vue-router/dist/vue-router.mjs', import.meta.url).pathname } },
  server: { host: '127.0.0.1', port: 5188, strictPort: true, fs: { allow: [new URL('..', import.meta.url).pathname] } },
};
