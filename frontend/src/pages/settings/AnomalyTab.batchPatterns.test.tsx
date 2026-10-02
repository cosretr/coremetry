// @vitest-environment jsdom
//
// AnomalyTab.batchPatterns — v0.10.1039.
//
// Operatör (prod): "Bazı batch işlerde ani yük artışı olabilir, onları
// anomali gibi düşünme — özellikle `-batch` geçen servis isimlerinde."
//
// NE ÇİVİLİYOR ("Batch servis ad kalıpları" alanı, Dedektör hassasiyeti):
//   • alan ETKİN listeyi gösterir — sunucu alanı döndürmese de (eski sunucu)
//     varsayılan `-batch`;
//   • boş metinle kayıt `batchServicePatterns: []` GÖNDERİR (kural kapalı);
//     alanı atlamak sunucuda varsayılanı geri getirirdi;
//   • metinle kayıt ayrıştırılmış listeyi gönderir ve sunucunun döndürdüğü
//     normalize listeyi geri gösterir;
//   • gövdenin geri kalanı (whole blob PUT) korunur;
//   • (inceleme) virgül VE boşlukla bölünür; kayıtlı liste boşsa "Kural
//     kapalı" notu; normalizasyon giriş düşürdüyse sayısı söylenir.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import type { AnomalySensitivityConfig } from '@/lib/types';
import { droppedPatternCount, formatBatchPatterns, parseBatchPatterns } from './batchPatterns';

const m = vi.hoisted(() => {
  window.matchMedia = ((q: string) => ({
    matches: false, media: q, onchange: null,
    addListener: () => {}, removeListener: () => {},
    addEventListener: () => {}, removeEventListener: () => {}, dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
  return {
    sens: {} as Record<string, unknown>,
    puts: [] as Record<string, unknown>[],
    // Sunucunun Normalize'ının taklidi (kırp, küçült, <3 at, tekrar at, tavan
    // 10) — yanıtın ekrana geri yazıldığını ve düşenlerin sayıldığını görmek için.
    normalize: (b: Record<string, unknown>) => {
      const out: string[] = [];
      for (const raw of (b.batchServicePatterns as string[] | undefined) ?? ['-batch']) {
        const p = raw.trim().toLowerCase();
        if (p.length < 3 || out.includes(p) || out.length === 10) continue;
        out.push(p);
      }
      return { ...b, batchServicePatterns: out };
    },
  };
});
vi.mock('@/lib/api', () => {
  const stub: Record<string, (...a: unknown[]) => Promise<unknown>> = {
    getAnomalyPromotion: async () => ({ enabled: true, minPeakRatio: 15, criticalPeakRatio: 20, minSustainedSec: 1800, minCount: 1000 }),
    getAnomalyTracked: async () => ({ error_rate: true, p99_ms: true, request_rate: false }),
    getAnomalySensitivity: async () => m.sens,
    putAnomalySensitivity: async (body: unknown) => {
      m.puts.push(body as Record<string, unknown>);
      return m.normalize(body as Record<string, unknown>);
    },
  };
  return {
    // Diğer bölümlerin okumaları asılı kalır (yükleniyor) — bu test onları ölçmüyor.
    api: new Proxy({}, { get: (_t, k: string) => stub[k] ?? (() => new Promise(() => {})) }),
    isCanceled: () => false,
  };
});

import { AnomalyPromotionTab } from './AnomalyTab';

const baseSens = (extra: Partial<AnomalySensitivityConfig> = {}): Record<string, unknown> => ({
  metrics: {
    error_rate: { floorPct: 0.1, absFloor: 1, minAbsDelta: 0, minMAD: 0, minBaselineRate: 0 },
    p99_ms: { floorPct: 0.1, absFloor: 10, minAbsDelta: 0, minMAD: 1, minBaselineRate: 0 },
    request_rate: { floorPct: 0.15, absFloor: 0, minAbsDelta: 0, minMAD: 0, minBaselineRate: 0 },
  },
  dwellBuckets: 3, criticalZ: 6, attachToIncident: true, serviceSilent: false,
  ...extra,
});

let host: HTMLDivElement | null = null;
let root: Root | null = null;
const settle = () => act(async () => { await new Promise(r => setTimeout(r, 30)); });

async function mount(): Promise<HTMLElement> {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  await act(async () => { root!.render(<AnomalyPromotionTab />); });
  await settle();
  return host;
}

const field = (el: HTMLElement) =>
  el.querySelector<HTMLInputElement>('input[aria-label="Batch servis ad kalıpları"]');

function sensitivitySave(el: HTMLElement): HTMLButtonElement {
  const h2 = [...el.querySelectorAll('h2')].find(h => h.textContent === 'Dedektör hassasiyeti');
  const btn = [...(h2?.parentElement?.querySelectorAll('button') ?? [])].find(b => b.textContent?.includes('Kaydet'));
  if (!btn) throw new Error('Dedektör hassasiyeti Kaydet düğmesi yok');
  return btn as HTMLButtonElement;
}

async function typeInto(input: HTMLInputElement, v: string) {
  const set = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!;
  await act(async () => {
    set.call(input, v);
    input.dispatchEvent(new Event('input', { bubbles: true }));
  });
}

beforeEach(() => { m.puts = []; m.sens = baseSens({ batchServicePatterns: ['-batch'] }); });
afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  host = null; root = null;
});

describe('Batch servis ad kalıpları alanı', () => {
  it('etkin listeyi gösterir', async () => {
    m.sens = baseSens({ batchServicePatterns: ['-batch', 'etl-'] });
    const el = await mount();
    expect(field(el)?.value).toBe('-batch, etl-');
  });

  it('sunucu alanı döndürmezse varsayılan -batch görünür', async () => {
    m.sens = baseSens();
    const el = await mount();
    expect(field(el)?.value).toBe('-batch');
  });

  it('kural kapalıyken (boş liste) alan boş görünür', async () => {
    m.sens = baseSens({ batchServicePatterns: [] });
    const el = await mount();
    expect(field(el)?.value).toBe('');
  });

  it('boş metinle kayıt AÇIK boş liste gönderir', async () => {
    const el = await mount();
    await typeInto(field(el)!, '');
    await act(async () => { sensitivitySave(el).click(); });
    await settle();
    expect(m.puts).toHaveLength(1);
    expect('batchServicePatterns' in m.puts[0]).toBe(true);
    expect(m.puts[0].batchServicePatterns).toEqual([]);
    // Bütün blob gider (whole-blob PUT): diğer alanlar korunur.
    expect(m.puts[0].criticalZ).toBe(6);
    expect(field(el)?.value).toBe('');
  });

  it('metinle kayıt ayrıştırılmış listeyi gönderir, normalize yanıtı gösterir', async () => {
    const el = await mount();
    await typeInto(field(el)!, ' ETL- , -batch,, -Cron ');
    expect(field(el)?.value).toBe(' ETL- , -batch,, -Cron '); // yazarken yeniden biçimlenmez
    await act(async () => { sensitivitySave(el).click(); });
    await settle();
    expect(m.puts[0].batchServicePatterns).toEqual(['ETL-', '-batch', '-Cron']);
    expect(field(el)?.value).toBe('etl-, -batch, -cron');
    expect(el.textContent).not.toContain('kalıp alınmadı');
    expect(el.textContent).not.toContain('Kural kapalı');
  });

  it('boşlukla ayrılmış kalıplar AYRI kalıp olarak gider', async () => {
    const el = await mount();
    await typeInto(field(el)!, '-batch -cron\tetl-');
    await act(async () => { sensitivitySave(el).click(); });
    await settle();
    expect(m.puts[0].batchServicePatterns).toEqual(['-batch', '-cron', 'etl-']);
  });

  it('kayıtlı liste boşsa "Kural kapalı" notu görünür', async () => {
    m.sens = baseSens({ batchServicePatterns: [] });
    const el = await mount();
    expect(el.textContent).toContain('Kural kapalı — batch servisler de diğerleri gibi değerlendirilir.');
    // Boş kaydın ARDINDAN da (yanıt normalize boş liste).
    m.sens = baseSens({ batchServicePatterns: ['-batch'] });
    act(() => { root?.unmount(); });
    host?.remove();
    const el2 = await mount();
    expect(el2.textContent).not.toContain('Kural kapalı');
    await typeInto(field(el2)!, '');
    await act(async () => { sensitivitySave(el2).click(); });
    await settle();
    expect(el2.textContent).toContain('Kural kapalı — batch servisler de diğerleri gibi değerlendirilir.');
  });

  it('normalizasyon giriş düşürünce sayısı söylenir', async () => {
    const el = await mount();
    await typeInto(field(el)!, 'ab, -batch, -BATCH');
    await act(async () => { sensitivitySave(el).click(); });
    await settle();
    expect(m.puts[0].batchServicePatterns).toEqual(['ab', '-batch', '-BATCH']);
    expect(field(el)?.value).toBe('-batch');
    expect(el.textContent).toContain("2 kalıp alınmadı: 3 karakterden kısa / 10'dan fazla / tekrar.");
    expect(el.textContent).not.toContain('Kural kapalı');
  });

  it('yalnız kısa kalıplar → hem düşürme notu hem "Kural kapalı"', async () => {
    const el = await mount();
    await typeInto(field(el)!, 'ab x');
    await act(async () => { sensitivitySave(el).click(); });
    await settle();
    expect(el.textContent).toContain('2 kalıp alınmadı');
    expect(el.textContent).toContain('Kural kapalı');
  });
});

describe('batchPatterns saf dönüşümler', () => {
  it('format: undefined → varsayılan, [] → boş', () => {
    expect(formatBatchPatterns(undefined)).toBe('-batch');
    expect(formatBatchPatterns([])).toBe('');
    expect(formatBatchPatterns(['-batch', 'etl-'])).toBe('-batch, etl-');
  });
  it('parse: boş → [], virgül VE boşlukla böl, boşları at', () => {
    expect(parseBatchPatterns('')).toEqual([]);
    expect(parseBatchPatterns('  ,  , ')).toEqual([]);
    expect(parseBatchPatterns(' -batch ,etl-,')).toEqual(['-batch', 'etl-']);
    expect(parseBatchPatterns('-batch -cron')).toEqual(['-batch', '-cron']);
    expect(parseBatchPatterns('-batch\n-cron,\t etl-')).toEqual(['-batch', '-cron', 'etl-']);
  });
  it('droppedPatternCount: gönderilen − dönen, negatif değil', () => {
    expect(droppedPatternCount(['ab', '-batch', '-BATCH'], ['-batch'])).toBe(2);
    expect(droppedPatternCount(['-batch'], ['-batch'])).toBe(0);
    expect(droppedPatternCount([], [])).toBe(0);
    expect(droppedPatternCount([], undefined)).toBe(0);
  });
});
