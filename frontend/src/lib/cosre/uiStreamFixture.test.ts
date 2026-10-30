// uiStreamFixture.test.ts — v0.10.1598 (CoSRE v2 Faz 1, R8): Go ile AYNI fixture
// (operatör kararı §0-5). testdata/cosre/*.sse dosyalarını Go'nun
// internal/cosre/uistream/golden_test.go'su üretir (-update); bu test aynı
// baytları okur. Protokol kayması (yeni parça türü, data adı, sıra) iki tarafı
// birden kırar.
import { describe, it, expect } from 'vitest';
import { existsSync, readFileSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { DATA_PART_NAMES, MESSAGE_ID_RE, UI_STREAM_PART_TYPES, dataPartName, isDataPart, parseDataReset } from './protocol';
import { parseUIStreamSSE } from './uiStreamFixture';

const ROOT = resolve(__dirname, '../../../..');
const FIXTURES = ['ui-stream-v1', 'ui-stream-v1-error', 'ui-stream-v1-closed', 'ui-stream-v1-reset', 'ui-stream-v1-reset2', 'ui-stream-v1-approval'];
const PART_TYPES = new Set<string>(UI_STREAM_PART_TYPES);
const DATA_NAMES = new Set<string>(DATA_PART_NAMES);

// v0.10.1607 — transient:true ZORUNLU: AI SDK onData'ya iletir ama
// message.parts'a eklemez; bayraksız/false reset kalıcılığa sızar → null.
describe('parseDataReset (v0.10.1606, transient v0.10.1607)', () => {
  it.each([
    [{ type: 'data-reset', data: { textId: 't1' }, transient: true }, 't1'],
    [{ type: 'data-reset', data: { textId: 't1' } }, null],
    [{ type: 'data-reset', data: { textId: 't1' }, transient: false }, null],
    [{ type: 'data-reset', data: { textId: 't1' }, transient: 'true' }, null],
    [{ type: 'data-reset', data: { target: 't1' }, id: 'r1', transient: true }, null],
    [{ type: 'data-reset', id: 'r1', data: { textId: 't1' }, transient: true }, null],
    [{ type: 'data-reset', data: { textId: 't1', providerMetadata: {} }, transient: true }, null],
    [{ type: 'data-reset', data: { textId: '' }, transient: true }, null],
    [{ type: 'text-end', id: 't1' }, null],
  ])('%j → %s', (p, want) => {
    expect(parseDataReset(p)?.data.textId ?? null).toBe(want);
  });
});

describe('parseUIStreamSSE', () => {
  it('yorum (ping) satırlarını atlar, [DONE] sonrası çerçeveyi sayar', () => {
    const r = parseUIStreamSSE(': ping\n\ndata: {"type":"start"}\n\n: ping\n\ndata: [DONE]\n\ndata: {"type":"x"}\n\n');
    expect(r.parts).toEqual([{ type: 'start' }]);
    expect(r.done).toBe(true);
    expect(r.afterDone).toBe(1);
  });
});

it('repo kökü bulunur (taşınan test yüksek sesle kırılsın)', () => {
  expect(existsSync(join(ROOT, 'go.mod'))).toBe(true);
});

describe.each(FIXTURES)('%s.sse', name => {
  const raw = readFileSync(join(ROOT, 'testdata', 'cosre', `${name}.sse`), 'utf8');
  const { parts, done, afterDone } = parseUIStreamSSE(raw);

  it('her data satırı JSON, son satır [DONE]', () => {
    for (const line of raw.split('\n').filter(l => l.startsWith('data: '))) {
      const body = line.slice(6);
      if (body !== '[DONE]') expect(() => JSON.parse(body)).not.toThrow();
    }
    expect(done).toBe(true);
    expect(afterDone).toBe(0);
    expect(raw.endsWith('data: [DONE]\n\n')).toBe(true);
  });

  it('parça türleri ve data adları kapalı kümede', () => {
    for (const p of parts) {
      if (isDataPart(p)) expect(DATA_NAMES.has(dataPartName(p)), p.type).toBe(true);
      else expect(PART_TYPES.has(p.type), p.type).toBe(true);
    }
    expect(parts.some(p => p.type === 'tool-output-error')).toBe(false);
  });

  it('start, start-step ile açılır; finish ile kapanır', () => {
    expect(parts.slice(0, 2).map(p => p.type)).toEqual(['start', 'start-step']);
    expect(parts[parts.length - 1].type).toBe('finish');
  });

  // v0.10.1603 — start sunucu üretimi ULID messageId taşır; fixture'lar sabit
  // üreticiyle (GoldenMessageID) deterministik.
  it('start, ULID messageId taşır; başka parça taşımaz', () => {
    expect(String(parts[0].messageId ?? '')).toMatch(MESSAGE_ID_RE);
    expect(parts.slice(1).some(p => 'messageId' in p)).toBe(false);
  });

  it('metin parçaları açılmadan kullanılmaz; reset kapanmış metni hedefler', () => {
    const opened = new Set<string>();
    const closed = new Set<string>();
    for (const p of parts) {
      if (p.type === 'text-start') opened.add(String(p.id));
      if (p.type === 'text-delta') expect(opened.has(String(p.id))).toBe(true);
      if (p.type === 'text-end') {
        expect(opened.has(String(p.id))).toBe(true);
        closed.add(String(p.id));
      }
      if (p.type === 'data-reset') {
        const reset = parseDataReset(p);
        expect(reset, JSON.stringify(p)).not.toBeNull();
        const textId = reset?.data.textId ?? '';
        expect(closed.has(textId), `reset hedefi ${textId}`).toBe(true);
      }
    }
  });

  it('data-error, error parçasından önce gelir', () => {
    const de = parts.findIndex(p => p.type === 'data-error');
    const e = parts.findIndex(p => p.type === 'error');
    expect(de >= 0).toBe(e >= 0);
    if (e >= 0) expect(de).toBeLessThan(e);
  });

  it('araç çıktısı bilinen bir araç girdisine bağlanır', () => {
    const inputs = new Set(parts.filter(p => p.type === 'tool-input-available').map(p => String(p.toolCallId)));
    for (const p of parts.filter(p => p.type === 'tool-output-available')) {
      expect(inputs.has(String(p.toolCallId))).toBe(true);
    }
  });
});

it('ana fixture beklenen kanalları taşır (data-reset, data-budget, kaynaklar)', () => {
  const { parts } = parseUIStreamSSE(readFileSync(join(ROOT, 'testdata', 'cosre', 'ui-stream-v1.sse'), 'utf8'));
  const types = new Set(parts.map(p => p.type));
  for (const t of ['data-reset', 'data-budget', 'data-step', 'source-url', 'source-document', 'message-metadata', 'data-block-chart', 'data-code-lock']) {
    expect(types.has(t), t).toBe(true);
  }
});

// v0.10.1606 — geri alma senaryosu: dizi tam olarak pinli (Go golden ile aynı).
it('reset fixture: t1 akar, data-reset{textId:t1}, t2 yeni tam metinle kapanır', () => {
  const { parts } = parseUIStreamSSE(readFileSync(join(ROOT, 'testdata', 'cosre', 'ui-stream-v1-reset.sse'), 'utf8'));
  expect(parts.slice(2)).toEqual([
    { type: 'text-start', id: 't1' },
    { type: 'text-delta', id: 't1', delta: 'İlk ' },
    { type: 'text-delta', id: 't1', delta: 'taslak ' },
    { type: 'text-delta', id: 't1', delta: 'metin' },
    { type: 'text-end', id: 't1' },
    { type: 'data-reset', data: { textId: 't1' }, transient: true },
    { type: 'text-start', id: 't2' },
    { type: 'text-delta', id: 't2', delta: 'Düzeltilmiş tam metin.' },
    { type: 'text-end', id: 't2' },
    { type: 'finish-step' },
    { type: 'finish', finishReason: 'stop' },
  ]);
});

// v0.10.1607 — çift geri alma: iki transient reset, üç metin parçası; her
// reset yalnız kendi (kapanmış) segmentini hedefler. Dizi tam pinli.
it('reset2 fixture: t1 → reset t1 → t2 → reset t2 → t3 tam metin', () => {
  const { parts } = parseUIStreamSSE(readFileSync(join(ROOT, 'testdata', 'cosre', 'ui-stream-v1-reset2.sse'), 'utf8'));
  expect(parts.slice(2)).toEqual([
    { type: 'text-start', id: 't1' },
    { type: 'text-delta', id: 't1', delta: 'Birinci ' },
    { type: 'text-delta', id: 't1', delta: 'taslak' },
    { type: 'text-end', id: 't1' },
    { type: 'data-reset', data: { textId: 't1' }, transient: true },
    { type: 'text-start', id: 't2' },
    { type: 'text-delta', id: 't2', delta: 'İkinci ' },
    { type: 'text-delta', id: 't2', delta: 'taslak' },
    { type: 'text-end', id: 't2' },
    { type: 'data-reset', data: { textId: 't2' }, transient: true },
    { type: 'text-start', id: 't3' },
    { type: 'text-delta', id: 't3', delta: 'Son tam metin.' },
    { type: 'text-end', id: 't3' },
    { type: 'finish-step' },
    { type: 'finish', finishReason: 'stop' },
  ]);
  expect(parts.filter(p => p.type === 'data-reset').every(p => parseDataReset(p) !== null)).toBe(true);
});

// agent-core S4 — onay bekleyen araç: girdisi var, çıktısı YOK (input-available'da
// kalır); eşli data-approval aynı toolCallId'yi taşır; akış finish 'tool-calls'.
it('approval fixture: onay bekleyen araç çıktısız, eşli data-approval, finish tool-calls', () => {
  const { parts } = parseUIStreamSSE(readFileSync(join(ROOT, 'testdata', 'cosre', 'ui-stream-v1-approval.sse'), 'utf8'));
  const approvals = parts.filter(p => p.type === 'data-approval');
  expect(approvals).toHaveLength(1);
  const data = approvals[0].data as Record<string, unknown>;
  expect(approvals[0].id).toBe('a3');
  expect(Object.keys(data).sort()).toEqual(['argsDigest', 'expiresAt', 'token', 'tool', 'toolCallId']);
  expect(data.toolCallId).toBe('s3');
  expect(parts.some(p => p.type === 'tool-input-available' && p.toolCallId === 's3')).toBe(true);
  expect(parts.some(p => p.type === 'tool-output-available' && p.toolCallId === 's3')).toBe(false);
  expect(parts.some(p => p.type === 'tool-output-available' && p.toolCallId === 's2')).toBe(true);
  expect(parts.at(-1)).toEqual({ type: 'finish', finishReason: 'tool-calls' });
});
