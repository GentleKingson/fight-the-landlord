import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { VersionGateScreen } from '../src/version/VersionGateScreen';

describe('VersionGateScreen', () => {
  it('renders a mandatory upgrade without exposing the game', () => {
    const retry = vi.fn();
    render(<VersionGateScreen
      result={{
        status: 'upgrade-required',
        serverVersion: 'v2.0.0',
        minimumVersion: 'v1.4.0',
        clientVersion: 'v1.2.0'
      }}
      onRetry={retry}
    />);

    expect(screen.getByRole('heading', { name: '客户端需要升级' })).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: '刷新升级' }));
    expect(retry).toHaveBeenCalledOnce();
  });

  it('offers retry when the version endpoint is unavailable', () => {
    render(<VersionGateScreen error="版本检查失败 (503)" onRetry={() => undefined} />);
    expect(screen.getByText('版本检查失败 (503)')).toBeTruthy();
    expect(screen.getByRole('button', { name: '重新检查' })).toBeTruthy();
  });
});
