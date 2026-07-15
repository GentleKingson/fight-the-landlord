import { cleanup, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';

import { GameResult } from '../src/features/table/GameResult';
import { useAppStore } from '../src/stores/appStore';
import { initialGameSlice } from '../src/stores/slices/gameSlice';
import { GameSocket } from '../src/transport/wsClient';

describe('GameResult settlement compatibility', () => {
  beforeEach(() => {
    useAppStore.setState({
      ...initialGameSlice,
      phase: 'game_over',
      pendingCommands: {}
    });
  });

  afterEach(cleanup);

  it('shows an explicit synchronization error instead of inventing a winner', () => {
    useAppStore.setState({
      settlementSyncError: '结算数据缺失，请重新连接同步本局结果'
    });

    render(<GameResult socket={new GameSocket('ws://example.invalid/ws')} />);

    expect(screen.getByRole('alert').textContent).toContain('结算数据缺失，请重新连接同步本局结果');
    expect(screen.queryByText(/获胜/)).toBeNull();
  });
});
