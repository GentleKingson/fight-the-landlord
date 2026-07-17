import { initialServerClock, type ServerClockState } from './clock';

export type ConnectionStatus = 'idle' | 'connecting' | 'fresh-connected' | 'reconnecting' | 'reconnected' | 'connected' | 'closing' | 'offline';

export interface StoredIdentity {
  id: string;
  name: string;
}

export type ProvisionalIdentity = StoredIdentity;

export interface ConnectionSlice extends ServerClockState {
  connected: boolean;
  connectionStatus: ConnectionStatus;
  playerId: string;
  playerName: string;
  provisionalIdentity: ProvisionalIdentity | null;
  reconnectNotice: string;
  latency: number;
  error: string;
  maintenance: boolean;
}

export const initialConnectionSlice: ConnectionSlice = {
  connected: false,
  connectionStatus: 'idle',
  playerId: '',
  playerName: '',
  provisionalIdentity: null,
  reconnectNotice: '',
  latency: 0,
  error: '',
  maintenance: false,
  ...initialServerClock
};
