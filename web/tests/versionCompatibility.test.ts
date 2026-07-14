import { beforeEach, describe, expect, it } from 'vitest';
import { checkVersionCompatibility, compareVersions, readClientVersion } from '../src/version/compatibility';

describe('version compatibility', () => {
  beforeEach(() => {
    document.head.innerHTML = '<meta name="application-version" content="v1.2.3">';
  });

  it('reads the version embedded into the HTML', () => {
    expect(readClientVersion()).toBe('v1.2.3');
  });

  it('compares release and prerelease versions', () => {
    expect(compareVersions('v1.2.3', '1.2.2')).toBeGreaterThan(0);
    expect(compareVersions('1.2.3-beta.2', '1.2.3-beta.10')).toBeLessThan(0);
    expect(compareVersions('1.2.3', '1.2.3-rc.1')).toBeGreaterThan(0);
  });

  it('blocks a client below the server minimum', async () => {
    const result = await checkVersionCompatibility('v1.2.3', response({
      server_version: 'v2.0.0',
      min_client_version: 'v1.3.0',
      web_client_version: 'v1.3.0'
    }));

    expect(result).toEqual({
      status: 'upgrade-required',
      serverVersion: 'v2.0.0',
      minimumVersion: 'v1.3.0',
      clientVersion: 'v1.2.3'
    });
  });

  it('forces a refresh when embedded assets are stale', async () => {
    const result = await checkVersionCompatibility('v1.2.3', response({
      server_version: 'v1.2.4',
      min_client_version: 'v1.0.0',
      web_client_version: 'v1.2.4'
    }));
    expect(result.status).toBe('refresh-required');
  });

  it('accepts compatible and development builds', async () => {
    await expect(checkVersionCompatibility('v1.2.3', response({
      server_version: 'v1.9.0',
      min_client_version: 'v1.0.0',
      web_client_version: 'v1.2.3'
    }))).resolves.toEqual({ status: 'compatible', serverVersion: 'v1.9.0' });

    await expect(checkVersionCompatibility('dev', response({
      server_version: 'dev',
      min_client_version: '',
      web_client_version: 'dev'
    }))).resolves.toEqual({ status: 'compatible', serverVersion: 'dev' });
  });

  it('rejects malformed version responses', async () => {
    await expect(checkVersionCompatibility('v1.2.3', response({ server_version: '' })))
      .rejects.toThrow('版本响应缺少 server_version');
  });
});

function response(body: unknown): typeof fetch {
  return (async () => new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'Content-Type': 'application/json' }
  })) as typeof fetch;
}
