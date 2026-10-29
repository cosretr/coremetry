// uiStreamFixture.test.ts — v0.10.1598 (CoSRE v2 Faz 1, R8): Go ile AYNI fixture
// (operatör kararı §0-5). testdata/cosre/*.sse dosyalarını Go'nun
// internal/cosre/uistream/golden_test.go'su üretir (-update); bu test aynı
// baytları okur. Protokol kayması (yeni parça türü, data adı, sıra) iki tarafı
// birden kırar.
import { describe, it, expect } from 'vitest';
import { existsSync, readFileSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { DATA_PART_NAMES, UI_STREAM_PART_TYPES, dataPartName, isDataPart } from './protocol';
import { parseUIStreamSSE } from './uiStreamFixture';

const ROOT = resolve(__dirname, '../../../..');
const FIXTURES = ['ui-stream-v1', 'ui-stream-v1-error', 'ui-stream-v1-closed'];
const PART_TYPES = new Set<string>(UI_STREAM_PART_TYPES);
const DATA_NAMES = new Set<string>(DATA_PART_NAMES);

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
        const target = (p.data as { target?: string }).target ?? '';
        expect(closed.has(target), `reset hedefi ${target}`).toBe(true);
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
