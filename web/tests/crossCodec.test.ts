import { readFileSync, writeFileSync } from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';
import { decodeMessage, encodeMessage } from '../src/protocol/codec';
import { MsgType, type MessageName } from '../src/protocol/generated';

interface ManifestEntry {
  type: MessageName;
  payload: Record<string, unknown> | null;
}

interface WireFixture {
  type: MessageName;
  frame: string;
}

const fixtureDir = path.resolve(process.cwd(), '../internal/protocol/testdata');
const manifest = readJSON<ManifestEntry[]>(path.join(fixtureDir, 'messages.json'));
const encodeDynamic = encodeMessage as (
  type: MessageName,
  payload?: Record<string, unknown>
) => Uint8Array<ArrayBufferLike>;

describe('Go/TypeScript protobuf golden fixtures', () => {
  it('covers every canonical non-unknown message type', () => {
    expect(manifest.map((entry) => entry.type).sort()).toEqual([...Object.values(MsgType)].sort());
  });

  it('encodes the committed TypeScript-to-Go fixture corpus', () => {
    const fixtures = manifest.map((entry) => ({
      type: entry.type,
      frame: toHex(encodeDynamic(entry.type, entry.payload ?? undefined))
    }));

    if (process.env.UPDATE_PROTOCOL_FIXTURES === '1') {
      writeFileSync(path.join(fixtureDir, 'web-to-go.json'), `${JSON.stringify(fixtures, null, 2)}\n`);
    }

    expect(fixtures).toEqual(readJSON<WireFixture[]>(path.join(fixtureDir, 'web-to-go.json')));
  });

  it('decodes every Go-produced fixture with the same values as TypeScript', () => {
    const goFixtures = readJSON<WireFixture[]>(path.join(fixtureDir, 'go-to-web.json'));
    expect(goFixtures).toHaveLength(manifest.length);

    goFixtures.forEach((fixture, index) => {
      const entry = manifest[index];
      expect(fixture.type).toBe(entry.type);
      const decodedFromGo = decodeMessage(fromHex(fixture.frame));
      const decodedFromTypeScript = decodeMessage(encodeDynamic(entry.type, entry.payload ?? undefined));
      expect(decodedFromGo).toEqual(decodedFromTypeScript);
    });
  });
});

function readJSON<T>(file: string): T {
  return JSON.parse(readFileSync(file, 'utf8')) as T;
}

function toHex(bytes: Uint8Array): string {
  return Array.from(bytes, (byte) => byte.toString(16).padStart(2, '0')).join('');
}

function fromHex(value: string): Uint8Array {
  if (value.length % 2 !== 0) throw new Error('invalid hexadecimal fixture');
  const bytes = new Uint8Array(value.length / 2);
  for (let index = 0; index < bytes.length; index += 1) {
    bytes[index] = Number.parseInt(value.slice(index * 2, index * 2 + 2), 16);
  }
  return bytes;
}
