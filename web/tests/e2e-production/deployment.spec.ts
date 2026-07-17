import { spawnSync } from 'node:child_process';
import { randomBytes } from 'node:crypto';
import http from 'node:http';
import https from 'node:https';
import net from 'node:net';
import { expect, test } from '@playwright/test';

const baseURL = process.env.E2E_BASE_URL ?? 'http://127.0.0.1:1782';
const expectedVersion = process.env.E2E_EXPECTED_VERSION;

test('final image serves versioned assets with browser security headers', async ({ request }) => {
  const indexResponse = await request.get('/');
  expect(indexResponse.status()).toBe(200);
  expect(indexResponse.headers()['content-type']).toContain('text/html');
  expect(indexResponse.headers()['cache-control']).toBe('no-cache');

  const headers = indexResponse.headers();
  expect(headers['content-security-policy']).toContain("default-src 'self'");
  expect(headers['content-security-policy']).toContain("connect-src 'self'");
  expect(headers['content-security-policy']).toContain("object-src 'none'");
  expect(headers['content-security-policy']).toContain("frame-ancestors 'none'");
  expect(headers['x-content-type-options']).toBe('nosniff');
  expect(headers['x-frame-options']).toBe('DENY');
  expect(headers['referrer-policy']).toBe('no-referrer');
  expect(headers['permissions-policy']).toContain('camera=()');

  const html = await indexResponse.text();
  expect(html).toContain('<div id="root"></div>');
  const version = metaContent(html, 'application-version');
  expect(version).toBeTruthy();
  if (expectedVersion) expect(version).toBe(expectedVersion);
  expect(headers['x-web-client-version']).toBe(version);

  const assets = [...html.matchAll(/(?:src|href)="(\/assets\/[^"]+)"/g)]
    .map((match) => match[1]);
  expect(assets.length).toBeGreaterThan(0);
  expect(assets.some((asset) => asset.endsWith('.js'))).toBe(true);
  expect(assets.some((asset) => asset.endsWith('.css'))).toBe(true);

  for (const asset of assets) {
    expect(asset).toMatch(/^\/assets\/.+-[A-Za-z0-9_-]{8,}\.(?:css|js)$/);
    const assetResponse = await request.get(asset);
    expect(assetResponse.status(), asset).toBe(200);
    expect(assetResponse.headers()['cache-control'], asset).toBe('public, max-age=31536000, immutable');
    expect(assetResponse.headers()['x-web-client-version'], asset).toBe(version);
    expect((await assetResponse.body()).byteLength, asset).toBeGreaterThan(0);
  }
});

test('final image exposes healthy version, liveness, and readiness endpoints', async ({ request }) => {
  for (const [path, body] of [['/health', 'OK'], ['/livez', 'OK'], ['/readyz', 'READY']] as const) {
    const response = await request.get(path);
    expect(response.status(), path).toBe(200);
    expect((await response.text()).trim(), path).toBe(body);
    expect(response.headers()['cache-control'], path).toBe('no-store');
  }

  const indexResponse = await request.get('/');
  const clientVersion = metaContent(await indexResponse.text(), 'application-version');
  const versionResponse = await request.get('/version');
  expect(versionResponse.status()).toBe(200);
  expect(versionResponse.headers()['content-type']).toContain('application/json');
  expect(versionResponse.headers()['cache-control']).toBe('no-store');
  expect(await versionResponse.json()).toEqual({
    server_version: clientVersion,
    min_client_version: expect.any(String),
    web_client_version: clientVersion
  });
});

test('WebSocket upgrade accepts the deployed origin and rejects a foreign origin', async () => {
  const allowedOrigin = process.env.E2E_ALLOWED_ORIGIN ?? new URL(baseURL).origin;
  expect(await rawWebSocketStatus(allowedOrigin)).toBe(101);
  expect(await rawWebSocketStatus('https://foreign-origin.invalid')).toBe(403);
});

test('production Redis service has no host port binding', async () => {
  if (process.env.E2E_ASSERT_REDIS_HIDDEN === 'false') {
    test.skip(true, 'Redis exposure assertion explicitly disabled');
  }

  const composeProject = process.env.E2E_COMPOSE_PROJECT_NAME;
  const redisService = process.env.E2E_REDIS_SERVICE ?? 'redis';
  if (composeProject) {
    const container = runDocker([
      'ps',
      '--filter', `label=com.docker.compose.project=${composeProject}`,
      '--filter', `label=com.docker.compose.service=${redisService}`,
      '--format', '{{.ID}}'
    ]).trim().split(/\s+/).filter(Boolean);
    expect(container, `running ${composeProject}/${redisService} container`).toHaveLength(1);

    const bindings = JSON.parse(runDocker([
      'inspect',
      '--format', '{{json .HostConfig.PortBindings}}',
      container[0]
    ])) as Record<string, unknown> | null;
    expect(bindings?.['6379/tcp']).toBeUndefined();
    return;
  }

  const target = new URL(baseURL);
  const redisHost = process.env.E2E_REDIS_HOST ?? target.hostname;
  const redisPort = Number(process.env.E2E_REDIS_PORT ?? '6379');
  expect(await canConnect(redisHost, redisPort), `${redisHost}:${redisPort} must not accept host traffic`).toBe(false);
});

function metaContent(html: string, name: string): string {
  const escapedName = name.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  const match = new RegExp(`<meta\\s+name=["']${escapedName}["']\\s+content=["']([^"']+)["']`).exec(html);
  expect(match, `meta[name="${name}"]`).not.toBeNull();
  return match![1].trim();
}

function rawWebSocketStatus(origin: string): Promise<number> {
  const target = new URL('/ws', baseURL);
  const headers = {
    Connection: 'Upgrade',
    Upgrade: 'websocket',
    Origin: origin,
    'Sec-WebSocket-Key': randomBytes(16).toString('base64'),
    'Sec-WebSocket-Version': '13'
  };

  return new Promise((resolve, reject) => {
    const request = target.protocol === 'https:'
      ? https.request(target, { headers, rejectUnauthorized: false })
      : http.request(target, { headers });
    const timer = setTimeout(() => {
      request.destroy(new Error('timed out waiting for WebSocket upgrade response'));
    }, 5_000);
    const finish = (status: number) => {
      clearTimeout(timer);
      resolve(status);
    };

    request.once('upgrade', (response, socket) => {
      socket.destroy();
      finish(response.statusCode ?? 0);
    });
    request.once('response', (response) => {
      response.resume();
      finish(response.statusCode ?? 0);
    });
    request.once('error', (error) => {
      clearTimeout(timer);
      reject(error);
    });
    request.end();
  });
}

function runDocker(args: string[]): string {
  const result = spawnSync('docker', args, { encoding: 'utf8' });
  if (result.error) throw result.error;
  expect(result.status, `docker ${args.join(' ')}\n${result.stderr}`).toBe(0);
  return result.stdout.trim();
}

function canConnect(host: string, port: number): Promise<boolean> {
  return new Promise((resolve) => {
    const socket = net.createConnection({ host, port });
    const finish = (connected: boolean) => {
      socket.destroy();
      resolve(connected);
    };
    socket.setTimeout(750, () => finish(false));
    socket.once('connect', () => finish(true));
    socket.once('error', () => finish(false));
  });
}
