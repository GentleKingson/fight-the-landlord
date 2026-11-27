import { cleanup, fireEvent, render } from '@testing-library/react';
import { afterEach, expect, it } from 'vitest';
import { BOT_AVATARS } from '../src/features/table/botAvatars';
import { PlayerAvatar } from '../src/features/table/PlayerAvatar';

afterEach(cleanup);

it('falls back to the full first character and retries a different avatar source', () => {
  const { container, rerender } = render(<PlayerAvatar name="🤖机器人" src={BOT_AVATARS[0]} />);
  const avatar = container.querySelector('.player-avatar');
  if (!avatar) {
    throw new Error('Expected .player-avatar element');
  }
  expect(avatar.getAttribute('aria-hidden')).toBe('true');
  expect(avatar.textContent).toBe('🤖');
  const firstImg = container.querySelector('img');
  if (!firstImg) {
    throw new Error('Expected avatar img element');
  }
  expect(firstImg.alt).toBe('');
  fireEvent.error(firstImg);
  expect(container.querySelector('img')).toBeNull();
  rerender(<PlayerAvatar name="小王" src={BOT_AVATARS[1]} />);
  expect(container.querySelector('img')?.getAttribute('src')).toBe(BOT_AVATARS[1]);
  rerender(<PlayerAvatar name="" />);
  expect(avatar.textContent).toBe('?');
});
