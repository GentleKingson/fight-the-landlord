import type { VersionGateResult } from './compatibility';

interface VersionGateScreenProps {
  result?: Exclude<VersionGateResult, { status: 'compatible' }>;
  error?: string;
  checking?: boolean;
  onRetry: () => void;
}

export function VersionGateScreen({ result, error, checking, onRetry }: VersionGateScreenProps) {
  const title = result?.status === 'upgrade-required'
    ? '客户端需要升级'
    : result?.status === 'refresh-required'
      ? '发现新版本'
      : error
        ? '无法确认版本兼容性'
        : '正在检查版本';
  const detail = result?.status === 'upgrade-required'
    ? `服务器要求 ${result.minimumVersion} 或更高版本，当前版本为 ${result.clientVersion}。`
    : result?.status === 'refresh-required'
      ? `当前资源版本为 ${result.clientVersion}，服务器已发布 ${result.deployedVersion}。`
      : error || '正在与服务器确认客户端版本。';

  return (
    <main className="version-gate" aria-busy={checking || undefined}>
      <section aria-labelledby="version-gate-title">
        <span className="brand-mark" aria-hidden="true">斗</span>
        <h1 id="version-gate-title">{title}</h1>
        <p>{detail}</p>
        {!checking ? (
          <button className="primary-action" onClick={onRetry}>
            {result ? '刷新升级' : '重新检查'}
          </button>
        ) : <span className="spinner spinner--large" aria-label="正在检查" />}
      </section>
    </main>
  );
}
