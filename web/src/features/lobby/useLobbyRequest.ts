import { useCallback, useEffect, useState } from 'react';
import type { GameSocket } from '../../transport/wsClient';
import { MsgType, type MessageType } from '../../protocol/types';

// These three lobby reads have distinct response types and no request IDs.
export function useLobbyRequest(socket: GameSocket, request: MessageType, response: MessageType, immediate = true) {
  const [status, setStatus] = useState(immediate ? '正在加载…' : '');
  const [attempt, setAttempt] = useState(0);
  useEffect(() => {
    if (!immediate && attempt === 0) return;
    let done = false;
    const finish = (message: string) => { done = true; setStatus(message); };
    setStatus('正在加载…');
    const unsubscribe = socket.subscribe(message => {
      if (done) return;
      if (message.type === response) finish('');
      if (message.type === MsgType.Error) finish((message.payload as { message?: string }).message || '请求失败，请重试');
    });
    const sent = socket.send(request, request === MsgType.GetLeaderboard ? { type: 'total', offset: 0, limit: 30 } : undefined);
    if (sent === false) finish('连接不可用，请连接恢复后重试');
    const timer = window.setTimeout(() => { if (!done) finish('请求超时，请重试'); }, 10000);
    return () => { unsubscribe(); window.clearTimeout(timer); };
  }, [socket, request, response, attempt, immediate]);
  const retry = useCallback(() => setAttempt(value => value + 1), []);
  return { status, retry, requested: immediate || attempt > 0 };
}
