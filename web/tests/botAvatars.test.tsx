import { act, cleanup, fireEvent, render } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { assignBotAvatars, BOT_AVATARS } from '../src/features/table/botAvatars';
import { PlayerAvatar } from '../src/features/table/PlayerAvatar';
import { GameTable } from '../src/features/table/GameTable';
import { useAppStore } from '../src/stores/appStore';
import { MsgType, type PlayerInfo } from '../src/protocol/types';
import type { GameSocket } from '../src/transport/wsClient';

vi.mock('../src/shared/cards/Hand', () => ({ Hand: () => null }));
afterEach(cleanup);
const bot = (id: string, seat = 1): PlayerInfo => ({ id, seat, name: '机器人', is_bot: true, ready: true, online: true, is_landlord: false, cards_count: 17 });

it('draws distinct avatars even when IDs hash to the same initial choice', () => {
  for (let i = 0; i < 100; i++) {
    const avatars = assignBotAvatars([bot(`bot-${i}`, 1), bot(`bot-${i + 1}`, 2)]);
    expect(avatars.size).toBe(2);
    expect(new Set(avatars.values()).size).toBe(2);
    for (const src of avatars.values()) expect(BOT_AVATARS).toContain(src);
  }
});

it('is stable across repeated renders, reordered players and role/name/online changes', () => {
  const players = [bot('a'), bot('b', 2)];
  const expected = assignBotAvatars(players);
  expect(assignBotAvatars(players)).toEqual(expected);
  expect(assignBotAvatars([...players].reverse().map(player => ({ ...player, name: '新名字', is_landlord: true, cards_count: 1, online: false })))).toEqual(expected);
  expect(assignBotAvatars([...players, { ...bot('human', 0), is_bot: false }])).toEqual(expected);
});

it('uses all six portraits over new bot identities, without guessing from names', () => {
  const sources = new Set(Array.from({ length: 100 }, (_, i) => assignBotAvatars([bot(`new-${i}`)]).values().next().value));
  expect(sources.size).toBe(6);
  expect(assignBotAvatars([{ ...bot('human'), is_bot: undefined }, bot('')]).size).toBe(0);
});

it('falls back to the full first character and retries a different avatar source', () => {
  const { container, rerender } = render(<PlayerAvatar name="🤖机器人" src={BOT_AVATARS[0]} />);
  const avatar = container.querySelector('.player-avatar')!;
  expect(avatar.getAttribute('aria-hidden')).toBe('true');
  expect(avatar.textContent).toBe('🤖');
  expect(container.querySelector('img')!.alt).toBe('');
  fireEvent.error(container.querySelector('img')!);
  expect(container.querySelector('img')).toBeNull();
  rerender(<PlayerAvatar name="小王" src={BOT_AVATARS[1]} />);
  expect(container.querySelector('img')!.getAttribute('src')).toBe(BOT_AVATARS[1]);
  rerender(<PlayerAvatar name="" />);
  expect(avatar.textContent).toBe('?');
});

it('keeps real bot avatars across game start, redeal and reconnect snapshots', () => {
  const players = [{ ...bot('human', 0), name: '我', is_bot: false }, bot('a', 1), bot('b', 2)];
  useAppStore.setState({ ...useAppStore.getInitialState(), playerId: 'human', connected: true });
  const receive = useAppStore.getState().handleMessage;
  receive({ type: MsgType.GameStart, payload: { players } });
  const { container } = render(<GameTable socket={{ send: vi.fn() } as unknown as GameSocket} />);
  const sources = () => [...container.querySelectorAll<HTMLImageElement>('.player-avatar img')].map(image => image.getAttribute('src'));
  const initial = sources();
  expect(new Set(initial).size).toBe(2);
  act(() => receive({ type: MsgType.GameStart, payload: { players: [...players].reverse() } }));
  expect(sources()).toEqual(initial);
  act(() => receive({ type: MsgType.Reconnected, payload: {
    player_id: 'human', player_name: '我', room_code: '123456', game_state: {
      phase: 'playing', players: players.map(player => ({ ...player, is_landlord: player.id === 'a' })),
      hand: [], bottom_cards: [], current_turn: 'a', last_played: [], last_player_id: '',
      must_play: false, can_beat: true
    }
  } }));
  expect(sources()).toEqual(initial);
  act(() => useAppStore.setState({ players: players.map(player => ({ ...player, is_bot: false })) }));
  expect(sources()).toEqual([]);
});
