import {
  MessageType as ProtoMessageType,
  decodeMessage as decodeProtoEnvelope,
  encodeMessage as encodeProtoEnvelope,
  staticMessageCodecs,
  type StaticPayloadCodec
} from './proto.generated';
import type { IncomingMessage, MessageType, OutgoingPayload } from './types';

const enumToType = new Map<number, string>();
const typeToEnum = new Map<string, number>();

for (const [name, value] of Object.entries(ProtoMessageType)) {
  const wireName = name.replace(/^MSG_/, '').toLowerCase();
  enumToType.set(value, wireName);
  typeToEnum.set(wireName, value);
}

typeToEnum.set('maintenance_pull', ProtoMessageType.MSG_MAINTENANCE_STATUS);
typeToEnum.set('maintenance_push', ProtoMessageType.MSG_MAINTENANCE);
enumToType.set(ProtoMessageType.MSG_MAINTENANCE_STATUS, 'maintenance_pull');
enumToType.set(ProtoMessageType.MSG_MAINTENANCE, 'maintenance_push');

const payloadNames: Record<string, string> = {
  reconnect: 'ReconnectPayload',
  ping: 'PingPayload',
  join_room: 'JoinRoomPayload',
  bid: 'BidPayload',
  play_cards: 'PlayCardsPayload',
  pass: 'PlayCardsPayload',
  get_leaderboard: 'GetLeaderboardPayload',
  connected: 'ConnectedPayload',
  reconnected: 'ReconnectedPayload',
  pong: 'PongPayload',
  player_offline: 'PlayerOfflinePayload',
  player_online: 'PlayerOnlinePayload',
  online_count: 'OnlineCountPayload',
  room_created: 'RoomCreatedPayload',
  room_joined: 'RoomJoinedPayload',
  player_joined: 'PlayerJoinedPayload',
  player_left: 'PlayerLeftPayload',
  player_ready: 'PlayerReadyPayload',
  game_start: 'GameStartPayload',
  deal_cards: 'DealCardsPayload',
  bid_turn: 'BidTurnPayload',
  bid_result: 'BidResultPayload',
  landlord: 'LandlordPayload',
  play_turn: 'PlayTurnPayload',
  card_played: 'CardPlayedPayload',
  player_pass: 'PlayerPassPayload',
  game_over: 'GameOverPayload',
  stats_result: 'StatsResultPayload',
  leaderboard_result: 'LeaderboardResultPayload',
  room_list_result: 'RoomListResultPayload',
  maintenance: 'MaintenancePayload',
  maintenance_push: 'MaintenancePayload',
  maintenance_status: 'MaintenanceStatusPayload',
  maintenance_pull: 'MaintenanceStatusPayload',
  error: 'ErrorPayload'
};

const payloadTypes = new Map<string, StaticPayloadCodec>();
for (const [type, name] of Object.entries(payloadNames)) {
  const codec = staticMessageCodecs[name];
  if (codec) {
    payloadTypes.set(type, codec);
  }
}

export function encodeMessage(type: MessageType | string, payload?: OutgoingPayload): Uint8Array<ArrayBufferLike> {
  const enumValue = typeToEnum.get(type) ?? ProtoMessageType.MSG_UNKNOWN;
  const payloadCodec = payloadTypes.get(type);
  let payloadBytes: Uint8Array<ArrayBufferLike> = new Uint8Array();

  if (payload && payloadCodec) {
    payloadBytes = payloadCodec.encode(payload as Record<string, unknown>);
  } else if (payload) {
    payloadBytes = new TextEncoder().encode(JSON.stringify(payload));
  }

  return encodeProtoEnvelope({ type: enumValue, payload: payloadBytes }).finish();
}

export function decodeMessage(data: ArrayBuffer | Uint8Array): IncomingMessage {
  const buffer = ArrayBuffer.isView(data)
    ? new Uint8Array(data.buffer, data.byteOffset, data.byteLength)
    : new Uint8Array(data);
  const decoded = decodeProtoEnvelope(buffer) as { type: number; payload?: Uint8Array };
  const type = enumToType.get(decoded.type) ?? 'unknown';
  const payload = decodePayload(type, decoded.payload);
  return { type, payload } as IncomingMessage;
}

function decodePayload(type: string, bytes?: Uint8Array): unknown {
  const raw = bytes ?? new Uint8Array(0);
  const payloadCodec = payloadTypes.get(type);
  if (payloadCodec) {
    return payloadCodec.decode(raw);
  }
  if (raw.length === 0) return {};

  try {
    return JSON.parse(new TextDecoder().decode(raw));
  } catch {
    return { _raw: Array.from(raw) };
  }
}

