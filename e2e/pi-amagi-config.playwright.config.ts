import { defineConfig } from '@playwright/test';

export default defineConfig({
  testDir: '.',
  testMatch: 'pi-amagi-config.spec.ts',
  workers: 1,
  reporter: 'list',
  outputDir: '../test-results/pi-amagi-config',
  use: { browserName: 'chromium', channel: process.env.PI_CONFIG_BROWSER_CHANNEL, viewport: { width: 1080, height: 950 }, video: 'off', screenshot: 'only-on-failure' },
  webServer: {
    command: 'node frontend/node_modules/vite/bin/vite.js --config e2e/pi-login.vite.config.mjs --port 5188',
    cwd: '..',
    url: 'http://127.0.0.1:5188/e2e/pi-amagi-config.html',
    reuseExistingServer: false,
    timeout: 30_000,
  },
});
