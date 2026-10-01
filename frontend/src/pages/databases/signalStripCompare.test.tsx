// @vitest-environment jsdom
//
// signalStripCompare — v0.10.1025 (Databases dilim 3).
//
// NE ÇİVİLİYOR: /database şeridi önceki eşit pencereye göre farkı YALNIZ
// `hasPrior` iken çizer; hasPrior yokken karolar bugünküyle aynıdır.
// Sıfır prior'lı hata karosu "listede yeni" DEĞİL "önce 0" der (detay
// sayfasında kıyaslanan bir liste yok). İyileşme renk ALMAZ, yalnız
// kötüleşme alır (K5/T9). Err rate farkı yüzde PUAN.
import { describe, it, expect, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { DatabaseSignalStrip } from './detailSections';
import type { DBDetail } from '@/lib/types';

const BASE: DBDetail = {
  system: 'oracle', instance: 'db-a', dbName: 'LEDGER',
  spanCount: 120, errorCount: 4, errorRate: 3.33,
  avgDurationMs: 10, p50DurationMs: 6, p95DurationMs: 30, p99DurationMs: 90,
  callers: [], topOps: [],
};

const WITH_PRIOR: DBDetail = {
  ...BASE,
  hasPrior: true,
  priorSpanCount: 100,
  priorErrorCount: 0,          // → "önce 0", --err (0'dan artış = kötüleşme)
  priorErrorRate: 0,           // → +3.33 pp, kötüleşme
  priorAvgDurationMs: 20,      // → −50 %, İYİLEŞME: renksiz
  priorP50DurationMs: 6,       // → |Δ| < %5: "·"
  priorP95DurationMs: 25,      // → +20 %, kötüleşme
  priorP99DurationMs: 60,      // → +50 %, kötüleşme
};

let host: HTMLDivElement | null = null;
let root: Root | null = null;
function render(d: DBDetail): HTMLElement {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  act(() => { root!.render(<DatabaseSignalStrip d={d} />); });
  return host;
}
afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  host = null; root = null;
});

/** Etiketi verilen karo (StatTile etiketi büyük harfe CSS'le çevriliyor; metin olduğu gibi). */
function tile(el: HTMLElement, label: string): HTMLElement {
  const t = Array.from(el.querySelectorAll('div'))
    .find(d => d.firstElementChild?.textContent === label && d.children.length === 2);
  if (!t) throw new Error(`${label} karosu bulunamadı`);
  return t as HTMLElement;
}
const pctDeltas = (el: HTMLElement) => el.querySelectorAll('[title^="Prior window"]');
// v0.10.1025 (R1) — "eşit uzunluktaki" iddiası kalktı: prior N tam kova,
// canlı current'ın son kovası dolmamış.
const CAPTION = 'Karşılaştırma: bir önceki pencere';
const SCALED_NOTE = 'sayılar, süren pencerenin dolu kısmına oranlandı';

describe('DatabaseSignalStrip — önceki pencere farkları (v0.10.1025)', () => {
  it('hasPrior yok → hiçbir fark öğesi ve açıklama satırı yok', () => {
    const el = render(BASE);
    expect(pctDeltas(el).length).toBe(0);
    expect(el.querySelector('[data-trend-delta]')).toBeNull();
    expect(el.querySelector('[data-pp-delta]')).toBeNull();
    expect(el.textContent).not.toContain(CAPTION);
    expect(el.textContent).not.toContain('önce 0');
  });

  it('hasPrior=false + doldurulmuş prior alanları → yine hiçbir şey (prior* hasPrior\'sız okunmaz)', () => {
    const el = render({ ...WITH_PRIOR, hasPrior: false });
    expect(pctDeltas(el).length).toBe(0);
    expect(el.querySelector('[data-pp-delta]')).toBeNull();
    expect(el.textContent).not.toContain(CAPTION);
  });

  it('hasPrior → karolarda fark + tek satır açıklama', () => {
    const el = render(WITH_PRIOR);
    // Calls, Avg, P95, P99, Total time yüzde farkı; P50 "·" (|Δ| < %5).
    expect(pctDeltas(el).length).toBe(5);
    expect(el.textContent).toContain(CAPTION);
  });

  it('sıfır prior\'lı Errors "önce 0" der, "listede yeni" DEMEZ — ve kötüleşme rengini alır', () => {
    const el = render(WITH_PRIOR);
    const errs = tile(el, 'Errors');
    const badge = errs.querySelector('[data-trend-delta="was-zero"]') as HTMLElement | null;
    expect(badge?.textContent).toContain('önce 0');
    expect(badge?.getAttribute('title')).toBe("Önceki pencerede 0'dı.");
    expect(badge?.style.color).toBe('var(--err)');
    expect(el.textContent).not.toContain('listede yeni');
  });

  it('iyileşme renksiz: Avg 20 → 10 ms nötr, P99 60 → 90 ms kırmızı', () => {
    const el = render(WITH_PRIOR);
    const avg = tile(el, 'Avg').querySelector('[title^="Prior window"]') as HTMLElement;
    expect(avg.textContent).toBe('50%');
    expect(avg.style.color).not.toBe('var(--err)');
    expect(avg.style.color).toBe('var(--text2)');
    const p99 = tile(el, 'P99').querySelector('[title^="Prior window"]') as HTMLElement;
    expect(p99.style.color).toBe('var(--err)');
  });

  it('Err rate YÜZDE PUAN: 0 → 3.33 % "+3.33 pp" kötüleşme', () => {
    const el = render(WITH_PRIOR);
    const pp = tile(el, 'Err rate').querySelector('[data-pp-delta]') as HTMLElement;
    expect(pp.textContent).toBe('+3.33 pp');
    expect(pp.getAttribute('data-pp-delta')).toBe('worse');
    expect(pp.style.color).toBe('var(--err)');
  });

  it('Err rate iyileşmesi nötr, çok küçük fark hiç çizilmez', () => {
    const better = render({ ...WITH_PRIOR, errorRate: 1, priorErrorRate: 2 });
    const pp = tile(better, 'Err rate').querySelector('[data-pp-delta]') as HTMLElement;
    expect(pp.textContent).toBe('-1.00 pp');
    expect(pp.style.color).toBe('var(--text2)');
    act(() => { root?.unmount(); });
    host?.remove();
    const same = render({ ...WITH_PRIOR, errorRate: 2.003, priorErrorRate: 2 });
    expect(tile(same, 'Err rate').querySelector('[data-pp-delta]')).toBeNull();
  });

  it('Total time farkı çağrı × avg çarpımından: 120×10 vs 100×20 → −40 %', () => {
    const el = render(WITH_PRIOR);
    const tt = tile(el, 'Total time').querySelector('[title^="Prior window"]') as HTMLElement;
    expect(tt.textContent).toBe('40%');
    // neutral tür: düşüş --text3, kırmızı değil.
    expect(tt.style.color).not.toBe('var(--err)');
  });

  // R1 — sunucu prior sayaçlarını canlı pencerenin dolu kısmına oranladıysa
  // açıklama bunu söyler; ölçek yoksa (1 / yok) söylemez.
  it('priorScale < 1 → açıklamaya oranlama notu eklenir; 1 iken eklenmez', () => {
    const live = render({ ...WITH_PRIOR, priorScale: 0.875 });
    expect(live.textContent).toContain(`${CAPTION} · ${SCALED_NOTE}`);
    act(() => { root?.unmount(); });
    host?.remove();
    const past = render({ ...WITH_PRIOR, priorScale: 1 });
    expect(past.textContent).toContain(CAPTION);
    expect(past.textContent).not.toContain(SCALED_NOTE);
    act(() => { root?.unmount(); });
    host?.remove();
    const old = render(WITH_PRIOR); // rolling deploy: alan yok
    expect(old.textContent).not.toContain(SCALED_NOTE);
  });

  // R5 — current pencerede 0 çağrı: gecikme ölçülmemiş ("0.0 ms" yokluk),
  // ona karşı "↓100%" çizilmez. Calls / Errors farkı kalır.
  it('0 çağrılı current → gecikme farkları YOK, çağrı/hata farkı VAR', () => {
    const el = render({
      ...WITH_PRIOR, spanCount: 0, errorCount: 0, errorRate: 0,
      avgDurationMs: 0, p50DurationMs: 0, p95DurationMs: 0, p99DurationMs: 0,
      priorErrorCount: 4,
    });
    for (const label of ['Avg', 'P50', 'P95', 'P99']) {
      const t = tile(el, label);
      expect(t.querySelector('[title^="Prior window"]'), `${label} farkı çizilmiş`).toBeNull();
      expect(t.textContent).not.toContain('100%');
    }
    expect(tile(el, 'Calls').querySelector('[title^="Prior window"]')?.textContent).toBe('100%');
    expect(tile(el, 'Errors').querySelector('[title^="Prior window"]')?.textContent).toBe('100%');
  });

  // R6b — "pp" 104 px'lik karoda tek başına alt satıra düşmesin.
  it('pp farkı satır kırmaz (nowrap)', () => {
    const el = render(WITH_PRIOR);
    const pp = tile(el, 'Err rate').querySelector('[data-pp-delta]') as HTMLElement;
    expect(pp.style.whiteSpace).toBe('nowrap');
  });
});
