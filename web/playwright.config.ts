import { defineConfig, devices } from '@playwright/test';

export default defineConfig({
  testDir: './tests/e2e',
  timeout: 30_000,
  workers: process.env.CI ? 4 : undefined,
  use: {
    baseURL: 'http://127.0.0.1:5174',
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
    launchOptions: { executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH }
  },
  webServer: {
    command: 'npm run dev -- --host 127.0.0.1',
    url: 'http://127.0.0.1:5174',
    reuseExistingServer: true,
    timeout: 60_000
  },
  projects: [
    { name: 'desktop', use: { ...devices['Desktop Chrome'], viewport: { width: 1440, height: 900 } } },
    { name: 'mobile-portrait', use: { ...devices['Pixel 7'] } },
    { name: 'mobile-landscape', use: { ...devices['Pixel 7 landscape'] } },
    ...(process.env.PLAYWRIGHT_WEBKIT === '1'
      ? [
          {
            name: 'webkit-portrait',
            use: { ...devices['iPhone 13'], browserName: 'webkit' as const, launchOptions: {} }
          },
          {
            name: 'webkit-landscape',
            use: { ...devices['iPhone 13 landscape'], browserName: 'webkit' as const, launchOptions: {} }
          }
        ]
      : [])
  ]
});
