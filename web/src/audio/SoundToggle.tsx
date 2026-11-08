import { useSyncExternalStore } from 'react';
import { Icon } from '../shared/ui/Icon';
import { gameAudio } from './gameAudio';

export function SoundToggle() {
  const enabled = useSyncExternalStore(gameAudio.subscribe, gameAudio.getEnabled);
  const label = enabled ? '关闭音效' : '开启音效';
  return (
    <button className="tool-button" aria-label={label} aria-pressed={enabled} title={label}
      onClick={() => gameAudio.setEnabled(!enabled)}>
      <Icon name={enabled ? 'sound' : 'muted'} />
      <span>{enabled ? '音效开' : '音效关'}</span>
    </button>
  );
}
