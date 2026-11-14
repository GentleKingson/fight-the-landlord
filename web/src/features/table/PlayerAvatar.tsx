import { useState } from 'react';

/** Props for rendering a player's seat avatar with initial-character fallback. */
export interface PlayerAvatarProps {
  name: string;
  src?: string;
}

/** Renders a player's avatar illustration with automatic text-initial fallback on error. */
export function PlayerAvatar({ name, src }: PlayerAvatarProps) {
  const [failedSrc, setFailedSrc] = useState<string>();
  return (
    <span className="player-avatar" aria-hidden="true">
      {Array.from(name)[0] || '?'}
      {src && src !== failedSrc ? (
        <img
          src={src}
          alt=""
          width={192}
          height={192}
          decoding="async"
          draggable={false}
          onError={() => {
            setFailedSrc(src);
          }}
        />
      ) : null}
    </span>
  );
}
