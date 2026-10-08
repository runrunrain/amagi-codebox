import vue from '../frontend/node_modules/@vitejs/plugin-vue/dist/index.mjs';

// Test-only server: expose the root e2e harness, without changing app/Vite policy.
export default {
  root: new URL('..', import.meta.url).pathname,
  plugins: [vue()],
  optimizeDeps: { include: ['vue', 'pinia'] },
  resolve: { alias: { vue: new URL('../frontend/node_modules/vue/dist/vue.runtime.esm-bundler.js', import.meta.url).pathname, pinia: new URL('../frontend/node_modules/pinia/dist/pinia.mjs', import.meta.url).pathname } },
  server: { host: '127.0.0.1', port: 5187, strictPort: true, fs: { allow: [new URL('..', import.meta.url).pathname] } },
};
