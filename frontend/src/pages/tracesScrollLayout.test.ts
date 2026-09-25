// tracesScrollLayout.test.ts — v0.10.722 → v0.10.726. 722 kutuyu tek
// kaydırıcı yapmıştı ("A"); operatör test ortamında Dynatrace'in SAYFAYI
// kaydırdığını gösterdi → 726 sayfa-kaydırma modeline döndü: VirtualTable
// height='auto' (iç dikey kaydırma yok), #content tek kaydırıcı, pager
// tablonun altında akışta, sticky yok; 723 (Start time sabit) ve 724 (şerit
// katlama) kaldı.
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

const traces = readFileSync(resolve(__dirname, 'Traces.tsx'), 'utf8');
const css = readFileSync(resolve(__dirname, '../styles/globals.css'), 'utf8');
const strip = (s: string) => s.replace(/\{\/\*[\s\S]*?\*\/\}/g, '').replace(/^\s*\/\/.*$/gm, '');

describe('/traces sayfa kaydırır, kutu değil (v0.10.726; 722 geri alındı)', () => {
  it('tr-fill sarmalayıcı yok; VirtualTable height auto + scrollResetKey; sabit formül yok', () => {
    expect(traces).not.toContain('tr-fill');
    expect(css).not.toContain('.tr-fill');
    expect(css).not.toContain('is-fill');
    expect(traces).toContain('height="auto"');
    expect(traces).not.toContain('44 + displayRows.length * 36');
    expect(traces).toContain('scrollResetKey={`${page}|${sort}|${order}`}');
  });
  it('pager tablonun altında akışta; kabuk zinciri min-height:0; Traces.tsx\'te sticky yok', () => {
    const t = strip(traces);
    expect(t.indexOf('<Pager mode="offset"')).toBeGreaterThan(t.indexOf('<VirtualTable<TraceRow>'));
    expect(css).toMatch(/#main \{[^}]*min-height: 0;/);
    expect(css).toMatch(/#content, \.page-body \{[^}]*min-height: 0;/);
    expect(t).not.toMatch(/position:\s*['"]?sticky/);
  });
});

// v0.10.723 — Start time sola sabit: kolon tanımı stickyLeft, kümülatif
// ofset (Endpoints deseni), gövde hücresi sınıf + left.
// v0.10.922 (sade palet adım 1) — hatalı satırın kırmızı zemin tonu (ve
// sabit hücre için --bg1'e karışan opak varyantı) söküldü; sabit hücrenin
// opak zemini artık yalnız CSS kuralından (td.sticky-left → --bg1).
describe('/traces Start time sticky-left (v0.10.723)', () => {
  it('yalnız time kolonu sabit; ofsetler saf çekirdekten; hücre sınıf + left', () => {
    expect(traces).toContain("stickyLeft: id === 'time',");
    expect(traces).toContain('const leftOffs = stickyLeftOffsets(dt.visibleColumns, dt.colWidths, ATTR_W);');
    expect(traces).toContain("stickyL !== undefined ? 'sticky-left' : ''");
    expect(traces).toContain('style={{ left: stickyL }}>');
    expect(css).toMatch(/th\.sticky-left, td\.sticky-left \{[^}]*position: sticky/);
    expect(css).toMatch(/th\.sticky-left, td\.sticky-left \{[^}]*background: var\(--bg1\)/);
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

// v0.10.727 → v0.10.831 — "Last ⇥" sıralamayı ters çevirip sayfa 1'e döner
// (kesin son sayfa numarası tavanlı sayıda türetilemez). 727 o kipte girdinin
// ETİKETİNİ değiştirmişti; operatör aynı şikâyeti tekrarlayınca 831'de kutu
// tümden kalktı ve konum SONDAN yazılıyor (Pager `reverse`).
//
// Bu yalnız BAĞLANMA pini; DAVRANIŞ ölçümü pages/tracesReversePager.test.tsx
// (gerçek sayfa, gerçek tık) — kaynak grep'i repro sayılmaz (827 dersi).
describe('/traces ters sıra konum göstergesi (v0.10.831)', () => {
  it('reverse YALNIZ doğal eksende (zaman) artan sırada; eski pageLabel yolu KALKTI', () => {
    // v0.10.831 inceleme: yalnız `order`a bakmak süreye/ada göre artan
    // sıralamayı da "ters kip" sayıyor ve numara kutusunu kaldırıyordu.
    expect(traces).toContain("reverse={order === 'asc' && sort === 'time'}");
    expect(traces).toContain('reverseTitle="Liste ters sıralı');
    expect(traces).not.toContain('pageLabel=');
    // Konum ile bitiş düğmesi AYNI koşulu okur (ikisi de ters sırayı tarif eder).
    expect(traces).toContain("endLabel={order === 'desc' ? 'Last ⇥' : '⇤ First'}");
  });
});
