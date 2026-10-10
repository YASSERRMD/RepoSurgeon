import { defineConfig, devices } from '@playwright/test';

// The Playwright suite runs against the fully composed stack: the web app is
// served by the nginx container, which proxies /api and /health to the Go API.
export default defineConfig({
  testDir: '.',
  testMatch: /.*\.spec\.ts/,
  timeout: 30_000,
  expect: { timeout: 10_000 },
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  reporter: process.env.CI ? [['list'], ['html', { open: 'never' }]] : [['list']],
  use: {
    baseURL: process.env.E2E_BASE_URL ?? 'http://127.0.0.1:8080',
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
    video: 'off',
  },
  projects: [
    {
      name: 'chromium',
      use: { ...devices['Desktop Chrome'] },
    },
    {
      name: 'mobile',
      use: { ...devices['Pixel 7'] },
      testMatch: /responsive\.spec\.ts/,
    },
  ],
  webServer: process.env.E2E_WEB_SERVER
    ? {
        command: process.env.E2E_WEB_SERVER,
        url: process.env.E2E_BASE_URL ?? 'http://127.0.0.1:8080',
        reuseExistingServer: !process.env.CI,
        timeout: 120_000,
      }
    : undefined,
});
