// v0.10.1463 (CoSRE D1) — /ai çağrı çekmecesinin "Trace" linki: yalnız
// traceId doluyken (sunucu yalnız ÖRNEKLENMİŞ trace'i yazar) ve tek
// /trace?id= kurucusundan (traceHref) geçer.
import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { traceHref } from '@/lib/traceHref';

describe('v0.10.1463 — ai_calls satırından trace linki', () => {
  it('link traceId kapısının arkasında ve traceHref ile', () => {
    const src = readFileSync(resolve(__dirname, '../AIObservability.tsx'), 'utf8');
    expect(src).toMatch(/\{call\.traceId && \(/);
    expect(src).toContain('<Link to={traceHref(call.traceId)}');
    expect(src).not.toContain('/trace?id=${call.traceId}');
  });
  // v0.10.1470 — link ayrıca traceQueryable kapısının arkasında: dış
  // collector'a giden trace bu Coremetry'de yok, link boş sayfaya giderdi.
  it('link traceQueryable kapısının arkasında; aksi halde ipuçlu düz metin', () => {
    const src = readFileSync(resolve(__dirname, '../AIObservability.tsx'), 'utf8');
    const gate = src.indexOf('{call.traceQueryable');
    const link = src.indexOf('<Link to={traceHref(call.traceId)}');
    expect(gate).toBeGreaterThan(-1);
    expect(link).toBeGreaterThan(gate);
    expect(src.slice(gate, link)).toMatch(/^\{call\.traceQueryable\s*\?\s*$/);
    expect(src).toContain("title={tn('aiObs.drawer.traceExternalTitle')}"); // v0.10.2486 — katalogda
  });
  it('tablo satırı da ayrıntı yanıtını (traceQueryable) çeker', () => {
    const src = readFileSync(resolve(__dirname, '../AIObservability.tsx'), 'utf8');
    expect(src).toContain('rowActivation(() => openCallRow(c))');
    expect(src).not.toContain('rowActivation(() => setOpen(c))');
    expect(src).toMatch(/const openCallRow = [\s\S]{0,400}api\.aiCall\(c\.id\)/);
  });
  it('hedef /trace sayfası', () => {
    expect(traceHref('0af7651916cd43dd8448eb211c80319c')).toBe('/trace?id=0af7651916cd43dd8448eb211c80319c');
  });
});
