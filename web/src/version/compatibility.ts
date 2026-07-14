export interface VersionResponse {
  server_version: string;
  min_client_version: string;
  web_client_version: string;
}

export type VersionGateResult =
  | { status: 'compatible'; serverVersion: string }
  | { status: 'upgrade-required'; serverVersion: string; minimumVersion: string; clientVersion: string }
  | { status: 'refresh-required'; serverVersion: string; deployedVersion: string; clientVersion: string };

export async function checkVersionCompatibility(
  clientVersion = readClientVersion(),
  fetcher: typeof fetch = fetch
): Promise<VersionGateResult> {
  const response = await fetcher('/version', {
    cache: 'no-store',
    headers: { Accept: 'application/json' }
  });
  if (!response.ok) throw new Error(`版本检查失败 (${response.status})`);

  const versions = parseVersionResponse(await response.json());
  if (versions.min_client_version && compareVersions(clientVersion, versions.min_client_version) < 0) {
    return {
      status: 'upgrade-required',
      serverVersion: versions.server_version,
      minimumVersion: versions.min_client_version,
      clientVersion
    };
  }

  if (
    isReleaseVersion(clientVersion)
    && isReleaseVersion(versions.web_client_version)
    && compareVersions(clientVersion, versions.web_client_version) !== 0
  ) {
    return {
      status: 'refresh-required',
      serverVersion: versions.server_version,
      deployedVersion: versions.web_client_version,
      clientVersion
    };
  }

  return { status: 'compatible', serverVersion: versions.server_version };
}

export function readClientVersion(): string {
  return document.querySelector<HTMLMetaElement>('meta[name="application-version"]')?.content.trim() || 'dev';
}

export function compareVersions(left: string, right: string): number {
  const leftParts = parseReleaseVersion(left);
  const rightParts = parseReleaseVersion(right);
  if (!leftParts || !rightParts) {
    if (left === right) return 0;
    return left === 'dev' ? 1 : right === 'dev' ? -1 : left.localeCompare(right);
  }

  for (const index of [0, 1, 2] as const) {
    const difference = leftParts[index] - rightParts[index];
    if (difference !== 0) return Math.sign(difference);
  }
  return comparePrerelease(leftParts[3], rightParts[3]);
}

function parseVersionResponse(value: unknown): VersionResponse {
  if (!isRecord(value)) throw new Error('版本响应格式无效');
  const serverVersion = stringField(value, 'server_version');
  const minimumVersion = optionalStringField(value, 'min_client_version');
  const webClientVersion = stringField(value, 'web_client_version');
  return {
    server_version: serverVersion,
    min_client_version: minimumVersion,
    web_client_version: webClientVersion
  };
}

function parseReleaseVersion(value: string): readonly [number, number, number, string] | null {
  const match = /^v?(\d+)\.(\d+)\.(\d+)(?:-([0-9A-Za-z.-]+))?(?:\+[0-9A-Za-z.-]+)?$/.exec(value.trim());
  if (!match) return null;
  return [Number(match[1]), Number(match[2]), Number(match[3]), match[4] ?? ''];
}

function isReleaseVersion(value: string): boolean {
  return parseReleaseVersion(value) !== null;
}

function comparePrerelease(left: string, right: string): number {
  if (left === right) return 0;
  if (!left) return 1;
  if (!right) return -1;
  const leftParts = left.split('.');
  const rightParts = right.split('.');
  for (let index = 0; index < Math.max(leftParts.length, rightParts.length); index += 1) {
    const leftPart = leftParts[index];
    const rightPart = rightParts[index];
    if (leftPart === undefined) return -1;
    if (rightPart === undefined) return 1;
    if (leftPart === rightPart) continue;
    const leftNumeric = /^\d+$/.test(leftPart);
    const rightNumeric = /^\d+$/.test(rightPart);
    if (leftNumeric && rightNumeric) return Math.sign(Number(leftPart) - Number(rightPart));
    if (leftNumeric !== rightNumeric) return leftNumeric ? -1 : 1;
    return leftPart.localeCompare(rightPart);
  }
  return 0;
}

function stringField(value: Record<string, unknown>, field: string): string {
  const result = value[field];
  if (typeof result !== 'string' || result.trim() === '') throw new Error(`版本响应缺少 ${field}`);
  return result.trim();
}

function optionalStringField(value: Record<string, unknown>, field: string): string {
  const result = value[field];
  if (result === undefined || result === null) return '';
  if (typeof result !== 'string') throw new Error(`版本响应字段 ${field} 无效`);
  return result.trim();
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}
