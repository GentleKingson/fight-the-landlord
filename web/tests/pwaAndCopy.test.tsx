import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { Lobby } from '../src/features/lobby/Lobby';
import { useAppStore } from '../src/stores/appStore';
import type { GameSocket } from '../src/transport/wsClient';

const originalClipboard = Object.getOwnPropertyDescriptor(navigator, 'clipboard');
const originalShare = Object.getOwnPropertyDescriptor(navigator, 'share');
const originalCanShare = Object.getOwnPropertyDescriptor(navigator, 'canShare');

function restoreNavigatorProps() {
  if (originalClipboard) {
    Object.defineProperty(navigator, 'clipboard', originalClipboard);
  } else {
    Reflect.deleteProperty(navigator, 'clipboard');
  }
  if (originalShare) {
    Object.defineProperty(navigator, 'share', originalShare);
  } else {
    Reflect.deleteProperty(navigator, 'share');
  }
  if (originalCanShare) {
    Object.defineProperty(navigator, 'canShare', originalCanShare);
  } else {
    Reflect.deleteProperty(navigator, 'canShare');
  }
}

afterEach(() => {
  cleanup();
  vi.useRealTimers();
  restoreNavigatorProps();
});

describe('RoomWaiting copy and share controls', () => {
  it('copies room code via Clipboard API, announces via sibling aria-live region, deduplicates repeats, and resets', async () => {
    vi.useFakeTimers();
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: { writeText }
    });
    Reflect.deleteProperty(navigator, 'share');

    useAppStore.setState({
      phase: 'waiting',
      lobbyPanel: 'home',
      roomCode: '654321',
      playerId: 'p1',
      players: [{ id: 'p1', name: '青竹', seat: 0, ready: false, is_landlord: false, online: true, cards_count: 0 }]
    });

    render(<Lobby socket={{ subscribe: () => () => {}, send: vi.fn() } as unknown as GameSocket} />);

    expect(screen.queryByRole('button', { name: '分享房间号' })).toBeNull();

    const copyBtn = screen.getByRole('button', { name: '复制房间号' });
    const liveStatus = screen.getByRole('status');
    // Live region must live outside the button so assistive tech announces updates reliably.
    expect(copyBtn.contains(liveStatus)).toBe(false);
    expect(copyBtn.getAttribute('command')).toBe('--copy');
    expect(copyBtn.getAttribute('commandfor')).toBe('waiting-room-code');

    await act(async () => {
      fireEvent.click(copyBtn);
    });

    expect(writeText).toHaveBeenCalledWith('654321');
    expect(copyBtn.hasAttribute('disabled')).toBe(false);
    expect(copyBtn.hasAttribute('data-copied')).toBe(true);
    expect(liveStatus.getAttribute('aria-live')).toBe('polite');
    expect(liveStatus.textContent).toBe('已复制房间号 654321');

    // Consecutive identical copy appends NBSP so VoiceOver re-announces.
    await act(async () => {
      fireEvent.click(copyBtn);
    });
    expect(liveStatus.textContent).toBe('已复制房间号 654321\u00A0');

    act(() => {
      vi.advanceTimersByTime(2000);
    });
    expect(copyBtn.hasAttribute('data-copied')).toBe(false);
    expect(liveStatus.textContent).toBe('');
  });

  it('surfaces failure feedback when clipboard write rejects', async () => {
    const writeText = vi.fn().mockRejectedValue(new DOMException('Denied', 'NotAllowedError'));
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: { writeText }
    });

    useAppStore.setState({
      phase: 'waiting',
      lobbyPanel: 'home',
      roomCode: '888999',
      playerId: 'p1',
      players: []
    });

    render(<Lobby socket={{ subscribe: () => () => {}, send: vi.fn() } as unknown as GameSocket} />);
    const copyBtn = screen.getByRole('button', { name: '复制房间号' });

    await act(async () => {
      fireEvent.click(copyBtn);
    });

    expect(copyBtn.hasAttribute('data-failed')).toBe(true);
    expect(screen.getByRole('status').textContent).toBe('复制失败，请手动复制房间号');
  });

  it('renders Web Share button when navigator.share is available and ignores user AbortError', async () => {
    const share = vi
      .fn()
      .mockRejectedValueOnce(Object.assign(new Error('Cancelled'), { name: 'AbortError' }))
      .mockResolvedValueOnce(undefined);
    Object.defineProperty(navigator, 'share', {
      configurable: true,
      value: share
    });

    useAppStore.setState({
      phase: 'waiting',
      lobbyPanel: 'home',
      roomCode: '135790',
      playerId: 'p1',
      players: []
    });

    render(<Lobby socket={{ subscribe: () => () => {}, send: vi.fn() } as unknown as GameSocket} />);
    const shareBtn = screen.getByRole('button', { name: '分享房间号' });
    const liveStatus = screen.getByRole('status');

    // First click: user dismisses native share sheet -> silent no-op.
    await act(async () => {
      fireEvent.click(shareBtn);
    });
    expect(share).toHaveBeenCalledTimes(1);
    expect(liveStatus.textContent).toBe('');

    // Second click: share succeeds -> polite status announcement.
    await act(async () => {
      fireEvent.click(shareBtn);
    });
    expect(share).toHaveBeenCalledWith({
      title: '斗地主房间邀请',
      text: '快来加入我的斗地主房间，房间号：135790'
    });
    expect(liveStatus.textContent).toBe('已分享房间号 135790');
  });
});

describe('PWA manifest and lobby background preload links', () => {
  it('configures fullscreen landscape manifest with #143d2a theme color and valid PNG icons', () => {
    const raw = readFileSync(resolve(process.cwd(), 'public/manifest.webmanifest'), 'utf8');
    const manifest = JSON.parse(raw) as {
      id: string;
      display: string;
      orientation: string;
      theme_color: string;
      icons: Array<{ src: string; sizes: string; type: string; purpose?: string }>;
    };

    expect(manifest.id).toBe('/');
    expect(manifest.display).toBe('fullscreen');
    expect(manifest.orientation).toBe('landscape');
    expect(manifest.theme_color).toBe('#143d2a');
    expect(manifest.icons.length).toBeGreaterThanOrEqual(2);

    for (const icon of manifest.icons) {
      expect(icon.type).toBe('image/png');
      const bytes = readFileSync(resolve(process.cwd(), `public${icon.src}`));
      // PNG signature + IHDR width/height match declared sizes
      expect(bytes.subarray(0, 8)).toEqual(Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]));
      const [expectedWidth, expectedHeight] = icon.sizes.split('x').map(Number);
      expect(bytes.readUInt32BE(16)).toBe(expectedWidth);
      expect(bytes.readUInt32BE(20)).toBe(expectedHeight);
    }
  });

  it('declares manifest link and mutually-exclusive portrait/landscape high-priority lobby preloads in index.html', () => {
    const html = readFileSync(resolve(process.cwd(), 'index.html'), 'utf8');
    const doc = new DOMParser().parseFromString(html, 'text/html');

    const manifestLink = doc.querySelector('link[rel="manifest"]');
    expect(manifestLink?.getAttribute('href')).toBe('/manifest.webmanifest');

    const preloadLinks = Array.from(doc.querySelectorAll('link[rel="preload"][as="image"]'));
    expect(preloadLinks).toHaveLength(3);

    const byMedia = new Map(preloadLinks.map((el) => [el.getAttribute('media'), el]));
    expect(byMedia.get('(orientation: portrait)')?.getAttribute('href')).toContain('scene-lobby-portrait-900.webp');
    expect(byMedia.get('(orientation: landscape) and (max-width: 960px)')?.getAttribute('href')).toContain('scene-lobby-960.webp');
    expect(byMedia.get('(orientation: landscape) and (min-width: 961px)')?.getAttribute('href')).toContain('scene-lobby-1600.webp');

    for (const link of preloadLinks) {
      expect(link.getAttribute('fetchpriority')).toBe('high');
      expect(link.getAttribute('type')).toBe('image/webp');
      // Must omit crossorigin so no-cors CSS background-image reuses the preloaded response.
      expect(link.hasAttribute('crossorigin')).toBe(false);
    }
  });
});
