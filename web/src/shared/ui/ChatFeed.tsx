import { useEffect, useRef, useState } from 'react';
import { useAppStore, useChatStore } from '../../stores/appStore';

export function ChatFeed({ scope }: { scope: 'room' | 'lobby' }) {
  const feed = useRef<HTMLDivElement>(null);
  const playerId = useAppStore(state => state.playerId);
  const latestMessages = useChatStore(state => state.messages)
    .filter(message => (message.scope === 'room') === (scope === 'room'))
    .slice(scope === 'room' ? -24 : -20);
  const latest = latestMessages.at(-1);
  const [reading, setReading] = useState<typeof latestMessages | null>(null);
  const unread = reading && latest && latest !== reading.at(-1);

  function showLatest() {
    const el = feed.current!;
    el.scrollTop = el.scrollHeight;
    setReading(null);
  }

  // The room log is inside a popover opened by its parent's layout effect.
  useEffect(() => {
    if (!reading || (unread && latest.sender_id === playerId)) showLatest();
  }, [latest, playerId, reading, unread]);

  return (
    <div className="chat-history">
      <div className="chat-feed" ref={feed} role="log" aria-label={scope === 'room' ? '房间聊天记录' : '大厅聊天记录'}
        aria-relevant="additions" tabIndex={0} onScroll={event => {
          const el = event.currentTarget;
          if (el.scrollHeight - el.clientHeight - el.scrollTop <= 24) {
            if (reading) showLatest();
          } else if (!reading) setReading(latestMessages);
        }}>
        {(reading ?? latestMessages).map(message => <p key={message.id}><strong>{message.sender_name || '玩家'}:</strong> {message.content}</p>)}
      </div>
      {unread && <button type="button" className="chat-latest" onClick={() => {
        showLatest();
        feed.current!.focus({ preventScroll: true });
      }}>有新消息，查看最新</button>}
    </div>
  );
}
