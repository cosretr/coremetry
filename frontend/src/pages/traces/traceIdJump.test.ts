// traceIdJump.test.ts — v0.10.1423 (operatör, prod Traces ekranı: "Sağ üst
// köşedeki trace ID / function ID kısmına gerek yok artık. Search'ten zaten
// aranabiliyor."). Kutu kalktı; operasyon kutusu 32-hex trace id'ye atlar,
// function_id gibi kimlikleri arar. Eski `?traceId=` derin bağlantıları bir
// kez yönlenir ve param düşer. Saf çekirdek + kablolama pinleri; gerçek sayfa
// montajı: pages/Traces.traceIdBox.test.tsx.

import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { legacyTraceIdRedirect, traceIdJump } from './traceIdJump';

const HEX = '0af7651916cd43dd8448eb211c80319c';

describe('traceIdJump', () => {
  it('tam 32-hex → küçük harfli id (kırpılır)', () => {
    expect(traceIdJump(HEX)).toBe(HEX);
    expect(traceIdJump(`  ${HEX.toUpperCase()} `)).toBe(HEX);
  });
  it('32-hex olmayan her şey → null (kimlik / operasyon / önek / boş)', () => {
    expect(traceIdJump('KYC-ID-42')).toBeNull();
    expect(traceIdJump('GET /cart')).toBeNull();
    expect(traceIdJump(HEX.slice(0, 31))).toBeNull();
    expect(traceIdJump(HEX + '0')).toBeNull();
    expect(traceIdJump('g'.repeat(32))).toBeNull();
    expect(traceIdJump('')).toBeNull();
    expect(traceIdJump(undefined)).toBeNull();
  });
});

describe('legacyTraceIdRedirect — eski ?traceId= bir kez yönlenir', () => {
  it('param yoksa null (yönlendirme yok)', () => {
    expect(legacyTraceIdRedirect('')).toBeNull();
    expect(legacyTraceIdRedirect('?service=checkout&search=x')).toBeNull();
  });
  it('32-hex → /trace?id=… (sayfa range taşınır)', () => {
    expect(legacyTraceIdRedirect(`?traceId=${HEX.toUpperCase()}&range=1h&service=checkout`))
      .toBe(`/trace?id=${HEX}&range=1h`);
    expect(legacyTraceIdRedirect(`?traceId=${HEX}`)).toBe(`/trace?id=${HEX}`);
  });
  it('kimlik → ?search= (ham hâli, harf korunur); diğer paramlar sırasıyla kalır', () => {
    expect(legacyTraceIdRedirect('?service=checkout&traceId=Kyc-ID-42&range=3h'))
      .toBe('/traces?service=checkout&range=3h&search=Kyc-ID-42');
  });
  it('arama zaten doluysa o kazanır; kimlik yalnız düşer', () => {
    expect(legacyTraceIdRedirect('?search=timeout&traceId=KYC-ID-42')).toBe('/traces?search=timeout');
  });
  it('boş ?traceId= yalnız düşer', () => {
    expect(legacyTraceIdRedirect('?traceId=&service=a')).toBe('/traces?service=a');
    expect(legacyTraceIdRedirect('?traceId=')).toBe('/traces');
  });
  it('sonuç bir daha yönlenmez (tek seferlik)', () => {
    const next = legacyTraceIdRedirect('?traceId=KYC-ID-42')!;
    expect(legacyTraceIdRedirect(next.slice(next.indexOf('?')))).toBeNull();
  });
});

describe('kablolama (Traces.tsx)', () => {
  const src = readFileSync(resolve(__dirname, '../Traces.tsx'), 'utf8');
  it('Trace ID kutusu ve durumu yok', () => {
    expect(src).not.toMatch(/className="trace-lookup"/);
    expect(src).not.toContain('Trace ID veya function_id…');
    expect(src).not.toMatch(/draft\.traceId|filter\.traceId/);
    expect(src).not.toMatch(/\['traceId',/);
  });
  it('apply operasyon kutusunun terimini atlayış için okur', () => {
    expect(src).toContain('const tid = traceIdJump(draft.search);');
    expect(src).toMatch(/if \(tid\) \{ navigate\(traceHref\(tid, \{ pageRange: range \}\)\); return; \}/);
  });
  it('operasyon kutusu yazılanı search’e, seçileni op’a yazar ve id yer tutucusunu taşır', () => {
    expect(src).toMatch(/<OperationPicker service=\{draft\.service\} value=\{draft\.op \|\| draft\.search\}\s*onChange=\{v => setDraft\(\{ \.\.\.draft, search: v, op: '' \}\)\}/);
    expect(src).toContain("placeholder={tl('tracesP.f.op', lang)}"); // v0.10.2486 — katalogda (EN değeri aynen)
  });
  it('sarmalayıcı eski paramı iç sayfa monte olmadan, replace ile yönlendirir', () => {
    expect(src).toContain('const legacy = legacyTraceIdRedirect(search);');
    expect(src).toContain('if (legacy) return <Navigate to={legacy} replace />;');
  });
});
