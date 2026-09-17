// traceIdAnchor.test.ts — v0.10.753 ?traceId= çıpası: boş-durum metni saf
// + kablolama pinleri (TracesEmpty traceId alır, metin yardımcıdan; başlık
// çipi çıpalı pencereyi söyler).
import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { traceIdIdentityText } from './emptyReason';

const fmt = (ns: number) => `t${ns}`;

describe('traceIdIdentityText', () => {
  it('kimlik trace_id değilse null', () => {
    expect(traceIdIdentityText(undefined, 'abc', fmt)).toBeNull();
    expect(traceIdIdentityText({ hits: 1 }, 'abc', fmt)).toBeNull();
  });
  it('hits=0 → 90 gün özetinde yok + detay sayfası ipucu', () => {
    const s = traceIdIdentityText({ traceId: true, hits: 0 }, ' abc ', fmt)!;
    expect(s).toContain('Trace abc');
    expect(s).toContain('90 gün');
    expect(s).toContain('ham taramayı da dener'); // yol hecelenmez (traceLogsLinkGate)
  });
  it('hits>0 → bulundu ama süzgeç eledi + çıpalı pencere', () => {
    const s = traceIdIdentityText({ traceId: true, hits: 1, windowFromNs: 5, windowToNs: 9 }, 'abc', fmt)!;
    expect(s).toContain('bulundu ama listeye girmedi');
    expect(s).toContain('t5 → t9');
    expect(traceIdIdentityText({ traceId: true, hits: 1 }, 'abc', fmt)).not.toContain('çıpalandı');
  });
});

describe('kablolama', () => {
  const src = readFileSync(resolve(__dirname, '../Traces.tsx'), 'utf8');
  it('TracesEmpty traceId alır ve metni yardımcıdan çizer; başlık çipi pencereyi söyler', () => {
    expect(src).toContain('traceId={filter.traceId}');
    expect(src).toContain('traceIdIdentityText(identity, traceId, ');
    expect(src).toContain('pencere trace zamanına çıpalandı');
  });
});
