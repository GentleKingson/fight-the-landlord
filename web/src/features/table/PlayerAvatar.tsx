import { useState } from 'react';

export function PlayerAvatar({ name, src }: { name: string; src?: string }) {
  const [failedSrc, setFailedSrc] = useState<string>();
  return (
    <span className="player-avatar" aria-hidden="true">
      {Array.from(name)[0] || '?'}
      {src && src !== failedSrc ? (
        <img src={src} alt="" width={192} height={192} decoding="async" draggable={false} onError={() => setFailedSrc(src)} />
      ) : null}
    </span>
  );
}
