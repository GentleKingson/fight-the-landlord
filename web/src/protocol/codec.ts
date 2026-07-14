import type protobuf from 'protobufjs/light';
import {
  MESSAGE_NAME_BY_TYPE,
  MESSAGE_TYPE_BY_NAME,
  PAYLOAD_TYPE_BY_NAME,
  protocolRoot,
  type EmptyPayload,
  type MessageName,
  type PayloadByName,
  type ProtocolMessage
} from './generated';

type UnknownRecord = Record<string, unknown>;
type DeepPartial<T> = T extends readonly (infer Item)[]
  ? DeepPartial<Item>[]
  : T extends Uint8Array
    ? T
    : T extends object
      ? { [Key in keyof T]?: DeepPartial<T[Key]> }
      : T;

export type EncodablePayload<Name extends MessageName> = PayloadByName[Name] extends EmptyPayload
  ? EmptyPayload | undefined
  : DeepPartial<PayloadByName[Name]>;

export class ProtocolEncodeError extends Error {
  constructor(message: string, readonly messageType?: string, options?: ErrorOptions) {
    super(message, options);
    this.name = 'ProtocolEncodeError';
  }
}

export class ProtocolDecodeError extends Error {
  constructor(message: string, readonly numericType?: number, options?: ErrorOptions) {
    super(message, options);
    this.name = 'ProtocolDecodeError';
  }
}

const Envelope = protocolRoot.lookupType('protocol.Message');

const requiredFields: Partial<Record<MessageName, readonly string[]>> = {
  reconnect: ['token', 'player_id'],
  ping: ['timestamp'],
  join_room: ['room_code'],
  bid: ['bid'],
  play_cards: ['cards'],
  get_leaderboard: ['type', 'offset', 'limit'],
  chat: ['content', 'scope'],
  connected: ['player_id', 'player_name', 'reconnect_token'],
  reconnected: ['player_id', 'player_name'],
  pong: ['client_timestamp', 'server_timestamp'],
  player_offline: ['player_id', 'player_name', 'timeout'],
  player_online: ['player_id', 'player_name'],
  online_count: ['count'],
  room_created: ['room_code', 'player'],
  room_joined: ['room_code', 'player', 'players'],
  player_joined: ['player'],
  player_left: ['player_id', 'player_name'],
  player_ready: ['player_id', 'ready'],
  game_start: ['players'],
  deal_cards: ['cards', 'bottom_cards'],
  bid_turn: ['player_id', 'timeout', 'is_grab', 'multiplier'],
  bid_result: ['player_id', 'player_name', 'bid', 'is_grab', 'multiplier'],
  landlord: ['player_id', 'player_name', 'bottom_cards', 'multiplier'],
  play_turn: ['player_id', 'timeout', 'must_play', 'can_beat'],
  card_played: ['player_id', 'player_name', 'cards', 'cards_left', 'hand_type'],
  player_pass: ['player_id', 'player_name'],
  game_over: ['winner_id', 'winner_name', 'is_landlord', 'player_hands', 'multiplier', 'scores'],
  stats_result: ['player_id', 'player_name', 'total_games', 'wins', 'losses', 'win_rate'],
  leaderboard_result: ['type', 'entries'],
  room_list_result: ['rooms'],
  maintenance_status: ['maintenance'],
  maintenance: ['maintenance'],
  error: ['code', 'message']
};

export function encodeMessage<Name extends MessageName>(
  type: Name,
  payload?: EncodablePayload<Name>
): Uint8Array<ArrayBufferLike> {
  const numericType = MESSAGE_TYPE_BY_NAME[type];
  if (numericType === undefined) {
    throw new ProtocolEncodeError(`Unknown message type: ${String(type)}`, String(type));
  }

  try {
    const payloadBytes = encodePayload(type, payload);
    const envelope = { type: numericType, payload: payloadBytes };
    const envelopeError = Envelope.verify(envelope);
    if (envelopeError) throw new Error(envelopeError);
    return Envelope.encode(Envelope.create(envelope)).finish();
  } catch (error) {
    if (error instanceof ProtocolEncodeError) throw error;
    throw new ProtocolEncodeError(`Failed to encode ${type}: ${errorMessage(error)}`, type, { cause: error });
  }
}

export function decodeMessage(data: ArrayBuffer | Uint8Array): ProtocolMessage {
  const bytes = data instanceof Uint8Array ? data : new Uint8Array(data);
  let envelope: protobuf.Message<UnknownRecord>;

  try {
    envelope = Envelope.decode(bytes) as protobuf.Message<UnknownRecord>;
  } catch (error) {
    throw new ProtocolDecodeError(`Malformed message envelope: ${errorMessage(error)}`, undefined, { cause: error });
  }

  const envelopeRecord = envelope as unknown as UnknownRecord;
  const numericType = numberValue(envelopeRecord.type);
  const type = MESSAGE_NAME_BY_TYPE[numericType];
  if (!type) throw new ProtocolDecodeError(`Unknown message type: ${numericType}`, numericType);

  const payloadBytes = envelopeRecord.payload instanceof Uint8Array ? envelopeRecord.payload : new Uint8Array();
  try {
    const payload = decodePayload(type, payloadBytes);
    return { type, payload } as ProtocolMessage;
  } catch (error) {
    if (error instanceof ProtocolDecodeError) throw error;
    throw new ProtocolDecodeError(`Failed to decode ${type}: ${errorMessage(error)}`, numericType, { cause: error });
  }
}

function encodePayload<Name extends MessageName>(type: Name, payload?: EncodablePayload<Name>): Uint8Array {
  const payloadTypeName = PAYLOAD_TYPE_BY_NAME[type];
  if (!payloadTypeName) {
    if (payload !== undefined && Object.keys(payload).length > 0) {
      throw new ProtocolEncodeError(`${type} does not accept a payload`, type);
    }
    return new Uint8Array();
  }

  if (!isRecord(payload)) throw new ProtocolEncodeError(`${type} requires an object payload`, type);
  assertRequiredFields(type, payload, 'encode');

  const PayloadType = protocolRoot.lookupType(payloadTypeName);
  assertSafeInt64Fields(PayloadType, payload, type);
  const validationError = PayloadType.verify(payload);
  if (validationError) throw new ProtocolEncodeError(`Invalid ${type} payload: ${validationError}`, type);
  return PayloadType.encode(PayloadType.fromObject(payload)).finish();
}

function decodePayload(type: MessageName, bytes: Uint8Array): PayloadByName[MessageName] {
  const payloadTypeName = PAYLOAD_TYPE_BY_NAME[type];
  if (!payloadTypeName) {
    if (bytes.length > 0) throw new ProtocolDecodeError(`${type} must have an empty payload`, MESSAGE_TYPE_BY_NAME[type]);
    return {};
  }
  const PayloadType = protocolRoot.lookupType(payloadTypeName);
  const decoded = PayloadType.decode(bytes);
  const decodedRecord = decoded as unknown as UnknownRecord;
  assertSafeInt64Fields(PayloadType, decodedRecord, type);

  const payload = PayloadType.toObject(decoded, {
    longs: Number,
    enums: Number,
    defaults: true,
    arrays: true,
    objects: true,
    bytes: Uint8Array
  }) as UnknownRecord;
  assertDecodedRequiredFields(type, payload);
  const validationError = PayloadType.verify(payload);
  if (validationError) throw new ProtocolDecodeError(`Invalid ${type} payload: ${validationError}`, MESSAGE_TYPE_BY_NAME[type]);
  return payload as PayloadByName[MessageName];
}

function assertDecodedRequiredFields(type: MessageName, payload: UnknownRecord): void {
  for (const field of requiredFields[type] ?? []) {
    const value = payload[field];
    // Proto3 does not carry presence for a false/zero scalar. Required textual
    // identifiers can still be distinguished from their invalid empty default.
    if (value === undefined || value === null || (typeof value === 'string' && value.length === 0)) {
      throw new ProtocolDecodeError(
        `${type} payload is missing required field ${field}`,
        MESSAGE_TYPE_BY_NAME[type]
      );
    }
  }
}

function assertRequiredFields(type: MessageName, payload: UnknownRecord, operation: 'encode' | 'decode'): void {
  for (const field of requiredFields[type] ?? []) {
    if (!Object.hasOwn(payload, field) || payload[field] === undefined || payload[field] === null) {
      const message = `${type} payload is missing required field ${field}`;
      if (operation === 'encode') throw new ProtocolEncodeError(message, type);
      throw new ProtocolDecodeError(message, MESSAGE_TYPE_BY_NAME[type]);
    }
  }
}

function assertSafeInt64Fields(type: protobuf.Type, value: UnknownRecord, messageType: MessageName, path = ''): void {
  for (const field of type.fieldsArray) {
    const fieldValue = value[field.name];
    if (fieldValue === undefined || fieldValue === null) continue;
    const values = field.repeated && Array.isArray(fieldValue) ? fieldValue : [fieldValue];
    for (const item of values) {
      const fieldPath = path ? `${path}.${field.name}` : field.name;
      if (isInt64(field.type) && !isSafeIntegerValue(item)) {
        throw new ProtocolEncodeError(`${messageType} field ${fieldPath} exceeds JavaScript's safe integer range`, messageType);
      }
      if (field.resolvedType && 'fieldsArray' in field.resolvedType && isRecord(item)) {
        assertSafeInt64Fields(field.resolvedType as protobuf.Type, item, messageType, fieldPath);
      }
    }
  }
}

function isInt64(type: string): boolean {
  return ['int64', 'uint64', 'sint64', 'fixed64', 'sfixed64'].includes(type);
}

function isSafeIntegerValue(value: unknown): boolean {
  if (typeof value === 'number') return Number.isSafeInteger(value);
  if (!isRecord(value) || typeof value.toString !== 'function') return false;
  try {
    const integer = BigInt(value.toString());
    return integer >= BigInt(Number.MIN_SAFE_INTEGER) && integer <= BigInt(Number.MAX_SAFE_INTEGER);
  } catch {
    return false;
  }
}

function numberValue(value: unknown): number {
  return typeof value === 'number' && Number.isInteger(value) ? value : Number.NaN;
}

function isRecord(value: unknown): value is UnknownRecord {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}
