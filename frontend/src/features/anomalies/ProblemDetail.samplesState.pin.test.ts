// ProblemDetail.samplesState.pin.test.ts — v0.10.977 (tablo standardı T12, P-2).
// Exception detayının "Sample traces" tablosu (başlıksız statik tablo, ≤14
// satır) elle yazılmış yükleniyor (`<td style={{ padding: 12 }}><Spinner/>`)
// ve boş (`cell-warn|cell-faint` + padding 12) satırlarını tek
// `<DataTableState colSpan={3}>` ile değiştirdi. Yüklem eskisiyle aynı:
// isLoading → iskelet; boşken emptyNote — tarama tavanı (warn) hata türüyle
// (uyarı tonu + QueryErrorInline'ın ⚠'i), diğerleri boş türüyle. Yeniden
// deneme eskiden de yoktu. Stack kartındaki emptyNote kopyası ve satırların
// gezinme / kanıt davranışı değişmedi.
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { emptySamplesNote } from './exceptionSamples';

const src = readFileSync(resolve(__dirname, 'ProblemDetail.tsx'), 'utf8');
/** ProblemDetail.tsx'teki yüklemle birebir (öndeki glif QueryErrorInline'a kalır). */
const stripGlyph = (t: string) => t.replace(/^⚠\s*/, '');

describe('exception örnek tablosu — durum satırı tablonun içinde (v0.10.977)', () => {
  it('tek DataTableState colSpan={3}; elle yazılmış Spinner / dolgu-12 hücresi yok', () => {
    // JSX biçimi (`colSpan={`): yorumlardaki `<DataTableState colSpan>` anışı sayılmaz.
    expect(src.match(/<DataTableState colSpan=\{/g)).toHaveLength(1);
    expect(src).toContain('{(samplesQ.isLoading || samples.length === 0) && <DataTableState colSpan={3} {...samplesState} />}');
    expect(src).not.toMatch(/<td[^>]*>\s*<Spinner/);
    expect(src).not.toContain('style={{ padding: 12 }}');
    expect(src).not.toMatch(/className=\{emptyNote\.warn \? 'cell-warn' : 'cell-faint'\}/);
  });
  it('yüklem eskisiyle aynı: isLoading → loading; warn → error (glif düşer); değilse empty', () => {
    expect(src).toContain("samplesQ.isLoading\n    ? { kind: 'loading' }");
    expect(src).toContain("emptyNote.warn\n      ? { kind: 'error', message: emptyNote.text.replace(/^⚠\\s*/, '') }\n      : { kind: 'empty', message: emptyNote.text }");
    // Yeniden deneme yok (eskiden de yoktu): durum satırına onRetry bağlanmaz.
    expect(src).not.toMatch(/samplesState[^\n]*onRetry|onRetry[^\n]*samplesState/);
  });
  it('tavan uyarısının öndeki ⚠ eki düşer; diğer emptyNote metinleri olduğu gibi', () => {
    // 500: toLocaleString yerel ayara bağlı binlik ayıracı üretmesin.
    const capped = emptySamplesNote({ scanned: 500, scanCapped: true }, 'No sample traces.');
    expect(capped.warn).toBe(true);
    expect(capped.text.startsWith('⚠ ')).toBe(true);
    expect(stripGlyph(capped.text)).toBe(capped.text.slice(2));
    expect(stripGlyph(capped.text)).toMatch(/^En yeni 500 aday tarandı \(tavan\)/);
    const exhausted = emptySamplesNote({ scanned: 12, windowExhausted: true }, 'No sample traces.');
    expect(exhausted.warn).toBe(false);
    expect(stripGlyph(exhausted.text)).toBe(exhausted.text);
    const plain = emptySamplesNote(undefined, 'No sample traces.');
    expect(plain).toEqual({ warn: false, text: 'No sample traces.' });
  });
  it('stack kartındaki emptyNote kopyası ve satır davranışı (rowActivation, .wf-evidence, data-trace-id) korunur', () => {
    // stack kartı div'i + durum (error / empty) = en az 3 kullanım
    expect(src.match(/emptyNote\.text/g)!.length).toBeGreaterThanOrEqual(3);
    expect(src).toContain(": samples.length === 0 ? emptyNote.text");
    // v0.10.1104 — satır ExceptionSampleRow.tsx'e taşındı (trace id gerçek link,
    // Coremetry'de olmayan Oracle trace'i linksiz); davranış orada pinli
    // (ExceptionSampleRow.render.test.tsx), sözleşme metni burada.
    const row = readFileSync(resolve(__dirname, 'ExceptionSampleRow.tsx'), 'utf8');
    expect(src).toContain('<ExceptionSampleRow key={i} s={s} isEv={!!s.traceId && evTraces.includes(s.traceId)} />');
    expect(row).toContain("className={isEv ? 'wf-evidence' : undefined}");
    expect(row).toContain('{...(linkable ? rowActivation(() => navigate(traceHref(s.traceId))) : {})}');
    expect(row).toContain('data-trace-id={s.traceId || undefined}');
    // Spinner sayfanın başka yerinde hâlâ kullanılıyor; içe aktarma kalır.
    expect(src).toContain("import { Spinner, Empty } from '@/components/Spinner';");
    expect(src).toContain('{opsQ.isPending && <Spinner />}');
  });
});
