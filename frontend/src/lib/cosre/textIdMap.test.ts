// textIdMap.test — v0.10.1618 (CoSRE v2 Faz 2a): sarmalayıcı paylaşılan reset
// fixture'larını (Go golden_test.go üretir) okur. İddialar: (1) parçalar
// AYNEN geçer (aynı nesne, aynı sıra); (2) data-reset anında textId →
// o mesajdaki text parçası SIRASI çözülür; (3) harita messageId başına —
// ikinci mesajın t1'i birincinin t1'ini ezmez.
import { describe, it, expect } from 'vitest';
import { existsSync, readFileSync } from 'node:fs';
import { join, resolve } from 'node:path';
import type { UIMessageChunk } from 'ai';
import { parseUIStreamSSE } from './uiStreamFixture';
import { createTextIdMap } from './textIdMap';

const ROOT = resolve(__dirname, '../../../..');
const fixture = (name: string) =>
  parseUIStreamSSE(readFileSync(join(ROOT, 'testdata', 'cosre', `${name}.sse`), 'utf8')).parts as unknown as UIMessageChunk[];

function streamOf(chunks: UIMessageChunk[]): ReadableStream<UIMessageChunk> {
  return new ReadableStream({ start(c) { for (const ch of chunks) c.enqueue(ch); c.close(); } });
}

/** Sarılı akışı okur; her data-reset görüldüğü AN çözümü kaydeder. */
async function drain(map: ReturnType<typeof createTextIdMap>, chunks: UIMessageChunk[]) {
  const out: UIMessageChunk[] = [];
  const resolved: Array<{ textId: string; at: ReturnType<typeof map.resolve> }> = [];
  const reader = map.wrap(streamOf(chunks)).getReader();
  for (;;) {
    const { value, done } = await reader.read();
    if (done) break;
    out.push(value);
    if (value.type === 'data-reset') {
      const textId = (value.data as { textId: string }).textId;
      resolved.push({ textId, at: map.resolve(textId) });
    }
  }
  return { out, resolved };
}

it('repo kökü bulunur', () => {
  expect(existsSync(join(ROOT, 'go.mod'))).toBe(true);
});

describe('createTextIdMap', () => {
  it('ui-stream-v1-reset: parçalar aynen geçer, t1 → sıra 0', async () => {
    const chunks = fixture('ui-stream-v1-reset');
    const map = createTextIdMap();
    const { out, resolved } = await drain(map, chunks);
    expect(out).toHaveLength(chunks.length);
    out.forEach((c, i) => expect(c).toBe(chunks[i]));
    expect(resolved).toEqual([{ textId: 't1', at: { messageId: '01M62F9N000000000000000000', ordinal: 0 } }]);
    expect(map.ordinalOf('01M62F9N000000000000000000', 't2')).toBe(1);
  });

  it('ui-stream-v1-reset2: çift reset t1 → 0, t2 → 1 (son parça kestirmesi değil)', async () => {
    const map = createTextIdMap();
    const { resolved } = await drain(map, fixture('ui-stream-v1-reset2'));
    expect(resolved.map(r => [r.textId, r.at?.ordinal])).toEqual([['t1', 0], ['t2', 1]]);
    expect(map.ordinalOf('01M62F9N000000000000000000', 't3')).toBe(2);
  });

  it('harita messageId başına: ikinci mesajın t1\'i ayrı sayılır', async () => {
    const map = createTextIdMap();
    await drain(map, fixture('ui-stream-v1-reset2'));
    const second = fixture('ui-stream-v1-reset').map(c =>
      c.type === 'start' ? { ...c, messageId: '01M62F9N000000000000000001' } : c);
    const { resolved } = await drain(map, second);
    expect(resolved[0].at).toEqual({ messageId: '01M62F9N000000000000000001', ordinal: 0 });
    expect(map.ordinalOf('01M62F9N000000000000000000', 't3')).toBe(2);
    expect(map.resolve('t9')).toBeUndefined();
  });
});
