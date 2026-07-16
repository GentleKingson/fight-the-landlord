import { useAppStore, type BusinessErrorCategory } from '../../stores/appStore';
import { retryBusinessCommand } from '../../transport/commandDispatcher';
import type { GameSocket } from '../../transport/wsClient';
import { Icon } from './Icon';

export function BusinessErrorBanner({ socket }: { socket: GameSocket }) {
  const error = useAppStore((state) => state.businessError);
  const clearBusinessError = useAppStore((state) => state.clearBusinessError);
  const commandPending = useAppStore((state) => Object.keys(state.pendingCommands).length > 0);

  if (!error) return null;

  return (
    <div className={`business-error business-error--${error.category}`} role="alert">
      <div>
        <strong>{categoryLabel(error.category)}</strong>
        <span>{error.message}</span>
      </div>
      <div className="business-error__actions">
        {error.retry ? (
          <button type="button" disabled={commandPending} onClick={() => retryBusinessCommand(socket)}>重试</button>
        ) : null}
        <button type="button" className="business-error__close" onClick={clearBusinessError} aria-label="清除错误">
          <Icon name="close" />
        </button>
      </div>
    </div>
  );
}

function categoryLabel(category: BusinessErrorCategory): string {
  switch (category) {
    case 'validation': return '操作未完成';
    case 'rate-limit': return '操作太频繁';
    case 'maintenance': return '服务器维护';
    case 'not-in-room': return '房间状态已变化';
    case 'timeout': return '响应超时';
    case 'network': return '网络异常';
  }
}
