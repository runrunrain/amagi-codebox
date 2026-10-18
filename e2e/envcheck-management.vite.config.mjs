import vue from '../frontend/node_modules/@vitejs/plugin-vue/dist/index.mjs';
export default {
  root: new URL('..', import.meta.url).pathname,
  plugins: [vue()],
  optimizeDeps: { entries: ['e2e/envcheck-management.html'], include: ['vue'] },
  resolve: { alias: {
    vue: new URL('../frontend/node_modules/vue/dist/vue.runtime.esm-bundler.js', import.meta.url).pathname,
  } },
  server: { host: '127.0.0.1', port: 5194, strictPort: true, fs: { allow: [new URL('..', import.meta.url).pathname] } },
};
