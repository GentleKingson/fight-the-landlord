import { defineConfig, devices } from '@playwright/test';

const serverPort = 1781;
const webPort = 5175;

export default defineConfig({
  testDir: './tests/e2e-real',
  timeout: 180_000,
  expect: { timeout: 10_000 },
  workers: 1,
  fullyParallel: false,
  use: {
    ...devices['Desktop Chrome'],
    baseURL: `http://127.0.0.1:${webPort}`,
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
    video: 'retain-on-failure'
  },
  webServer: [
    {
      command: 'go run ../cmd/server -config ../config.yaml',
      url: `http://127.0.0.1:${serverPort}/health`,
      reuseExistingServer: false,
      timeout: 120_000,
      env: {
        ...process.env,
        SERVER_HOST: '127.0.0.1',
        SERVER_PORT: String(serverPort),
        SERVER_MIN_CLIENT_VERSION: '',
        REDIS_ADDR: process.env.E2E_REDIS_ADDR ?? '127.0.0.1:6379',
        BOT_ENABLED: 'false',
        DOUZERO_ENABLED: 'false',
        GAME_BID_TIMEOUT: '5',
        GAME_TURN_TIMEOUT: '5',
        GAME_OFFLINE_WAIT_TIMEOUT: '5',
        SECURITY_ALLOWED_ORIGINS: `http://127.0.0.1:${webPort}`
      }
    },
    {
      command: `npm run dev -- --host 127.0.0.1 --port ${webPort}`,
      url: `http://127.0.0.1:${webPort}`,
      reuseExistingServer: false,
      timeout: 60_000,
      env: {
        ...process.env,
        VITE_WS_TARGET: `ws://127.0.0.1:${serverPort}`
      }
    }
  ]
});
