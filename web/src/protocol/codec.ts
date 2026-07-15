import { protocol } from './generated-runtime.js';
import {
  INT64_FIELD_METADATA_BY_TYPE,
  MESSAGE_NAME_BY_TYPE,
  MESSAGE_TYPE_BY_NAME,
  PAYLOAD_TYPE_BY_NAME,
  PROTOCOL_FIELD_METADATA_BY_TYPE,
  REQUIRED_FIELDS_BY_NAME,
  type CommandMeta,
  type EmptyPayload,
  type EventMeta,
  type MessageName,
  type PayloadByName,
  type ProtocolMessage
} from './generated';

type UnknownRecord = Record<string, unknown>;
interface StaticWriter {
  finish(): Uint8Array<ArrayBufferLike>;
}

interface StaticMessageCodec {
  encode(message: UnknownRecord): StaticWriter;
  decode(bytes: Uint8Array): UnknownRecord;
  verify(message: UnknownRecord): string | null;
}

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

const staticCodecCache = new Map<string, StaticMessageCodec>();
const Envelope = staticMessageCodec('protocol.Message');

export function encodeMessage<Name extends MessageName>(
  type: Name,
  payload?: EncodablePayload<Name>,
  command?: DeepPartial<CommandMeta>
): Uint8Array<ArrayBufferLike> {
  const numericType = MESSAGE_TYPE_BY_NAME[type];
  if (numericType === undefined) {
    throw new ProtocolEncodeError(`Unknown message type: ${String(type)}`, String(type));
  }

  try {
    const payloadBytes = encodePayload(type, payload);
    if (command && (typeof command.request_id !== 'string' || command.request_id.trim() === '')) {
      throw new ProtocolEncodeError(`${type} command metadata requires request_id`, type);
    }
    if (command) {
      assertSafeInt64Fields('protocol.CommandMeta', command, {
        operation: 'encode',
        messageType: type,
        path: 'command'
      });
    }
    const envelope = { type: numericType, payload: payloadBytes, ...(command ? { command } : {}) };
    const envelopeError = Envelope.verify(envelope);
    if (envelopeError) throw new Error(envelopeError);
    return Envelope.encode(envelope).finish();
  } catch (error) {
    if (error instanceof ProtocolEncodeError) throw error;
    throw new ProtocolEncodeError(`Failed to encode ${type}: ${errorMessage(error)}`, type, { cause: error });
  }
}

export function decodeMessage(data: ArrayBuffer | Uint8Array): ProtocolMessage {
  const bytes = data instanceof Uint8Array ? data : new Uint8Array(data);
  let envelope: UnknownRecord;

  try {
    envelope = Envelope.decode(bytes);
  } catch (error) {
    throw new ProtocolDecodeError(`Malformed message envelope: ${errorMessage(error)}`, undefined, { cause: error });
  }

  const numericType = numberValue(envelope.type);
  const type = MESSAGE_NAME_BY_TYPE[numericType];
  if (!type) throw new ProtocolDecodeError(`Unknown message type: ${numericType}`, numericType);

  const payloadBytes = envelope.payload instanceof Uint8Array ? envelope.payload : new Uint8Array();
  try {
    const payload = decodePayload(type, payloadBytes);
    const event = decodeEvent(envelope.event, type);
    const command = decodeCommand(envelope.command, type);
    return {
      type,
      payload,
      ...(event ? { event } : {}),
      ...(command ? { command } : {})
    } as ProtocolMessage;
  } catch (error) {
    if (error instanceof ProtocolDecodeError) throw error;
    throw new ProtocolDecodeError(`Failed to decode ${type}: ${errorMessage(error)}`, numericType, { cause: error });
  }
}

function decodeCommand(value: unknown, messageType: MessageName): CommandMeta | undefined {
  if (value === undefined || value === null) return undefined;
  if (!isRecord(value)) {
    throw new ProtocolDecodeError('Invalid command metadata', MESSAGE_TYPE_BY_NAME[messageType]);
  }

  assertSafeInt64Fields('protocol.CommandMeta', value, {
    operation: 'decode',
    messageType,
    path: 'command'
  });
  const validationError = staticMessageCodec('protocol.CommandMeta').verify(value);
  if (validationError) {
    throw new ProtocolDecodeError(
      `Invalid command metadata: ${validationError}`,
      MESSAGE_TYPE_BY_NAME[messageType]
    );
  }
  const command = materializeMessage('protocol.CommandMeta', value) as unknown as CommandMeta;
  if (!command.request_id) {
    throw new ProtocolDecodeError(
      'Invalid command metadata: request_id is required',
      MESSAGE_TYPE_BY_NAME[messageType]
    );
  }
  return command;
}

function decodeEvent(value: unknown, messageType: MessageName): EventMeta | undefined {
  if (value === undefined || value === null) return undefined;
  if (!isRecord(value)) {
    throw new ProtocolDecodeError('Invalid event metadata', MESSAGE_TYPE_BY_NAME[messageType]);
  }

  assertSafeInt64Fields('protocol.EventMeta', value, {
    operation: 'decode',
    messageType,
    path: 'event'
  });
  const validationError = staticMessageCodec('protocol.EventMeta').verify(value);
  if (validationError) {
    throw new ProtocolDecodeError(
      `Invalid event metadata: ${validationError}`,
      MESSAGE_TYPE_BY_NAME[messageType]
    );
  }
  return materializeMessage('protocol.EventMeta', value) as unknown as EventMeta;
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

  const PayloadType = staticMessageCodec(payloadTypeName);
  assertSafeInt64Fields(payloadTypeName, payload, { operation: 'encode', messageType: type });
  const validationError = PayloadType.verify(payload);
  if (validationError) throw new ProtocolEncodeError(`Invalid ${type} payload: ${validationError}`, type);
  return PayloadType.encode(payload).finish();
}

function decodePayload(type: MessageName, bytes: Uint8Array): PayloadByName[MessageName] {
  const payloadTypeName = PAYLOAD_TYPE_BY_NAME[type];
  if (!payloadTypeName) {
    if (bytes.length > 0) throw new ProtocolDecodeError(`${type} must have an empty payload`, MESSAGE_TYPE_BY_NAME[type]);
    return {};
  }
  const PayloadType = staticMessageCodec(payloadTypeName);
  const decoded = PayloadType.decode(bytes);
  assertSafeInt64Fields(payloadTypeName, decoded, { operation: 'decode', messageType: type });
  const validationError = PayloadType.verify(decoded);
  if (validationError) throw new ProtocolDecodeError(`Invalid ${type} payload: ${validationError}`, MESSAGE_TYPE_BY_NAME[type]);
  const payload = materializeMessage(payloadTypeName, decoded);
  assertDecodedRequiredFields(type, payload);
  return payload as PayloadByName[MessageName];
}

function assertDecodedRequiredFields(type: MessageName, payload: UnknownRecord): void {
  for (const field of REQUIRED_FIELDS_BY_NAME[type] ?? []) {
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
  for (const field of REQUIRED_FIELDS_BY_NAME[type] ?? []) {
    if (!Object.hasOwn(payload, field) || payload[field] === undefined || payload[field] === null) {
      const message = `${type} payload is missing required field ${field}`;
      if (operation === 'encode') throw new ProtocolEncodeError(message, type);
      throw new ProtocolDecodeError(message, MESSAGE_TYPE_BY_NAME[type]);
    }
  }
}

interface SafeInt64Context {
  operation: 'encode' | 'decode';
  messageType: MessageName;
  path?: string;
}

function assertSafeInt64Fields(typeName: string, value: UnknownRecord, context: SafeInt64Context): void {
  for (const field of INT64_FIELD_METADATA_BY_TYPE[typeName] ?? []) {
    const fieldValue = value[field.name];
    if (fieldValue === undefined || fieldValue === null) continue;
    const values = field.repeated && Array.isArray(fieldValue) ? fieldValue : [fieldValue];
    for (const [index, item] of values.entries()) {
      const basePath = context.path ? `${context.path}.${field.name}` : field.name;
      const fieldPath = field.repeated ? `${basePath}[${index}]` : basePath;
      if (field.kind === 'int64' && !isSafeIntegerValue(item)) {
        const message = `${context.messageType} field ${fieldPath} exceeds JavaScript's safe integer range`;
        if (context.operation === 'encode') {
          throw new ProtocolEncodeError(message, context.messageType);
        }
        throw new ProtocolDecodeError(message, MESSAGE_TYPE_BY_NAME[context.messageType]);
      }
      if (field.kind === 'message' && isRecord(item)) {
        assertSafeInt64Fields(field.message_type, item, { ...context, path: fieldPath });
      }
    }
  }
}

function staticMessageCodec(typeName: string): StaticMessageCodec {
  const cached = staticCodecCache.get(typeName);
  if (cached) return cached;

  const shortName = typeName.startsWith('protocol.') ? typeName.slice('protocol.'.length) : typeName;
  const candidate = (protocol as unknown as UnknownRecord)[shortName];
  const members = candidate as unknown as UnknownRecord;
  if ((typeof candidate !== 'function' && !isRecord(candidate))
    || typeof members.encode !== 'function'
    || typeof members.decode !== 'function'
    || typeof members.verify !== 'function') {
    throw new Error(`Missing static protobuf codec: ${typeName}`);
  }

  const codec = candidate as unknown as StaticMessageCodec;
  staticCodecCache.set(typeName, codec);
  return codec;
}

function materializeMessage(typeName: string, value: UnknownRecord): UnknownRecord {
  const result: UnknownRecord = {};
  for (const field of PROTOCOL_FIELD_METADATA_BY_TYPE[typeName] ?? []) {
    const raw = Object.hasOwn(value, field.name) ? value[field.name] : undefined;
    if (field.repeated) {
      const items = Array.isArray(raw) ? raw : [];
      result[field.name] = field.kind === 'message'
        ? items.map((item) => isRecord(item) ? materializeMessage(field.message_type, item) : {})
        : items.map((item) => materializeScalar(field.kind, item));
      continue;
    }

    if (field.kind === 'message') {
      if (isRecord(raw)) result[field.name] = materializeMessage(field.message_type, raw);
      continue;
    }
    result[field.name] = materializeScalar(field.kind, raw);
  }
  return result;
}

function materializeScalar(kind: 'number' | 'string' | 'boolean' | 'bytes', value: unknown): unknown {
  if (kind === 'number') {
    if (typeof value === 'number') return value;
    if (value !== undefined && value !== null) {
      const converted = Number(String(value));
      if (Number.isFinite(converted)) return converted;
    }
    return 0;
  }
  if (kind === 'string') return typeof value === 'string' ? value : '';
  if (kind === 'boolean') return typeof value === 'boolean' ? value : false;
  return value instanceof Uint8Array ? value : new Uint8Array();
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
