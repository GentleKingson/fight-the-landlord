import { defineConfig, devices } from '@playwright/test';

const baseURL = process.env.E2E_BASE_URL ?? 'http://127.0.0.1:1782';

export default defineConfig({
  testDir: './tests/e2e-production',
  timeout: 180_000,
  expect: { timeout: 15_000 },
  use: {
    baseURL,
    actionTimeout: 10_000,
    trace: 'on',
    screenshot: 'on',
    video: 'off'
  },
  workers: 1,
  fullyParallel: false,
  outputDir: 'test-results/production',
  reporter: [
    ['line'],
    ['json', { outputFile: 'test-results/production/results.json' }]
  ],
  projects: [
    {
      name: 'chromium-production',
      testIgnore: /browser-smoke\.spec\.ts/,
      use: { ...devices['Desktop Chrome'], viewport: { width: 1440, height: 900 } }
    },
    {
      name: 'firefox-smoke',
      testMatch: /browser-smoke\.spec\.ts/,
      use: { ...devices['Desktop Firefox'], viewport: { width: 1440, height: 900 }, trace: 'off' }
    },
    {
      name: 'webkit-smoke',
      testMatch: /browser-smoke\.spec\.ts/,
      use: { ...devices['Desktop Safari'], viewport: { width: 1440, height: 900 }, trace: 'off' }
    }
  ]
});
