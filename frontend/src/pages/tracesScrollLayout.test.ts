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

// v0.10.723 — Start time sola sabit: kolon tanımı stickyLeft, kümülatif
// ofset (Endpoints deseni), gövde hücresi sınıf + left, hata tonu opak.
describe('/traces Start time sticky-left (v0.10.723)', () => {
  it('yalnız time kolonu sabit; ofsetler saf çekirdekten; hücre sınıf + left', () => {
    expect(traces).toContain("stickyLeft: id === 'time',");
    expect(traces).toContain('const leftOffs = stickyLeftOffsets(dt.visibleColumns, dt.colWidths, ATTR_W);');
    expect(traces).toContain("stickyL !== undefined ? 'sticky-left' : ''");
    expect(traces).toContain("${stickyL !== undefined ? 'var(--bg1)' : 'transparent'}");
    expect(css).toMatch(/th\.sticky-left, td\.sticky-left \{[^}]*position: sticky/);
  });
});

// v0.10.724 — üst şerit katlanabilir; durum localStorage (URL değil);
// katlıyken çizim yok, başlık şeridi (anahtar + istatistikler) kalır.
describe('/traces şerit katlama (v0.10.724)', () => {
  it('localStorage anahtarı, toggle düğmesi, VolumeChart collapsed, latency dalı da katlanır', () => {
    expect(traces).toContain("const STRIP_COLLAPSED_KEY = 'traces-strip-collapsed';");
    // (depo adı yazılmıyor: testEnvContract storage global'i geçen test dosyasından jsdom ister)
    expect(traces).toContain("getItem(STRIP_COLLAPSED_KEY) === '1'");
    expect(traces).toContain('collapsed={stripCollapsed}');
    expect(traces).toContain('{!stripCollapsed && <LatencyScatter');
    expect(traces).toContain('aria-expanded={!stripCollapsed}');
    // URL'e yazılmaz
    expect(traces).not.toMatch(/\[['"]strip['"],/);
  });
  it('VolumeChart katlıyken TimeChart çizmez, başlık şeridi kalır', () => {
    const vc = readFileSync(resolve(__dirname, '../components/traces/VolumeChart.tsx'), 'utf8');
    expect(vc).toContain('collapsed?: boolean;');
    expect(vc).toContain('{collapsed ? null : times.length === 0 ? (');
    expect(vc).toContain('data-collapsed={collapsed || undefined}');
  });
});
