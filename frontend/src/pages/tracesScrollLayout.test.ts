// tracesScrollLayout.test.ts — v0.10.722 (/traces tek kaydırıcı; denetim
// docs/audit/traces-scroll-audit-2026-09-13.md, operatör onayı "A"
// 2026-09-13). Pinler: liste görünümü .tr-fill (height:100%) kolonu, gövde
// .tr-fill__body flex:1/min-height:0, VirtualTable fill + scrollResetKey,
// pager kutunun ALTINDA (kaydırıcının dışında), kabuk zincirinde min-height:0,
// aggregate/shapes sınıf almaz, hâlâ hiçbir sayfa-düzeyi position:sticky yok.
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

const traces = readFileSync(resolve(__dirname, 'Traces.tsx'), 'utf8');
const css = readFileSync(resolve(__dirname, '../styles/globals.css'), 'utf8');
const strip = (s: string) => s.replace(/\{\/\*[\s\S]*?\*\/\}/g, '').replace(/^\s*\/\/.*$/gm, '');

describe('/traces tek kaydırıcı (v0.10.722)', () => {
  it('liste görünümü .tr-fill kolonu; diğer görünümler sınıfsız', () => {
    expect(traces).toContain("<div className={view === 'list' ? 'tr-fill' : undefined}>");
    expect(css).toContain('.tr-fill { height: 100%; display: flex; flex-direction: column; min-height: 0; }');
    expect(css).toContain('.tr-fill > * { flex: none; }');
  });
  it('gövde kalan yüksekliği alır; VirtualTable fill + sayfa/sıralama değişiminde tepeye dön', () => {
    expect(traces).toContain('className="tr-fill__body"');
    expect(css).toContain('.tr-fill__body { flex: 1 1 0; min-height: 0; display: flex; flex-direction: column; }');
    expect(traces).toContain('height="fill"');
    expect(traces).toContain('scrollResetKey={`${page}|${sort}|${order}`}');
  });
  it('pager kaydırıcının dışında, tablonun altında', () => {
    const t = strip(traces);
    const vt = t.indexOf('<VirtualTable<TraceRow>');
    const pager = t.indexOf('<Pager mode="offset"');
    const bodyEnd = t.indexOf('</div>\n        )}\n\n        {/* Aggregate view. */}') ;
    expect(vt).toBeGreaterThan(-1);
    expect(pager).toBeGreaterThan(vt);
    // pager gövde kapanışından önce (aynı .tr-fill__body içinde)
    if (bodyEnd > -1) expect(pager).toBeLessThan(bodyEnd);
  });
  it('kabuk zinciri min-height:0; sayfa-düzeyi sticky hâlâ yok', () => {
    expect(css).toMatch(/#main \{[^}]*min-height: 0;/);
    expect(css).toMatch(/#content, \.page-body \{[^}]*min-height: 0;/);
    // .controls.is-sticky'nin sticky olmadığını pageControls.test.ts çiviliyor (yorum sıyırarak).
    expect(strip(traces)).not.toMatch(/position:\s*['"]?sticky/);
  });
});
