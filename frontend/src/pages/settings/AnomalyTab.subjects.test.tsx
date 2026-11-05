// @vitest-environment jsdom
//
// AnomalyTab.subjects — v0.10.1790 Davis paritesi FAZ 1.2: Settings › Anomaly ›
// "Özneler" (anomaly_sensitivity.subjects + seasonalTimezone, aynı Kaydet).
//
// NE ÇİVİLİYOR: bölümü taşımayan blob → üç tür kapalı (eval kapısı) ve dilim
// Europe/Istanbul (karar 3); kayıtlı değerlerin gidiş-dönüşü; tek tür açma ve
// dilim değişikliği gövdeye tam bölümle gider.
import '@/test/radixJsdom'; // Faz 2 dilim 1 R17 — Radix Checkbox
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import type { AnomalySensitivityConfig } from '@/lib/types';

const m = vi.hoisted(() => {
  window.matchMedia = ((q: string) => ({
    matches: false, media: q, onchange: null,
    addListener: () => {}, removeListener: () => {},
    addEventListener: () => {}, removeEventListener: () => {}, dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
  return {
    sens: {} as Record<string, unknown>,
    puts: [] as Record<string, unknown>[],
  };
});
vi.mock('@/lib/api', () => {
  const stub: Record<string, (...a: unknown[]) => Promise<unknown>> = {
    getAnomalyPromotion: async () => ({ enabled: true, minPeakRatio: 15, criticalPeakRatio: 20, minSustainedSec: 1800, minCount: 1000 }),
    getAnomalyTracked: async () => ({ error_rate: true, p99_ms: true, request_rate: false }),
    getAnomalySensitivity: async () => m.sens,
    putAnomalySensitivity: async (body: unknown) => {
      m.puts.push(body as Record<string, unknown>);
      return body;
    },
  };
  return {
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
  batchServicePatterns: ['-batch'],
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

const toggle = (el: HTMLElement, label: string) => {
  const b = el.querySelector<HTMLButtonElement>(`button[role="checkbox"][aria-label="Özne: ${label}"]`);
  if (!b) throw new Error(`özne kutusu yok: ${label}`);
  return b;
};
const numInput = (el: HTMLElement, label: string) => {
  const i = el.querySelector<HTMLInputElement>(`input[type="number"][aria-label="${label}"]`);
  if (!i) throw new Error(`alan yok: ${label}`);
  return i;
};
const tzSelect = (el: HTMLElement) => {
  const s = el.querySelector<HTMLSelectElement>('select[aria-label="Mevsimsel taban saat dilimi"]');
  if (!s) throw new Error('dilim seçicisi yok');
  return s;
};
function sensitivitySave(el: HTMLElement): HTMLButtonElement {
  const h2 = [...el.querySelectorAll('h2')].find(h => h.textContent === 'Dedektör hassasiyeti');
  const btn = [...(h2?.parentElement?.querySelectorAll('button') ?? [])].find(b => b.textContent?.includes('Kaydet'));
  if (!btn) throw new Error('Dedektör hassasiyeti Kaydet düğmesi yok');
  return btn as HTMLButtonElement;
}
async function type(input: HTMLInputElement, v: string) {
  await act(async () => {
    const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!;
    setter.call(input, v);
    input.dispatchEvent(new Event('input', { bubbles: true }));
  });
}

beforeEach(() => { m.puts = []; m.sens = baseSens(); });
afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  host = null; root = null;
});

const EP = 'Endpoint (HTTP rota)';
const DB = 'Veritabanı instance';
const Q = 'Kuyruk';

describe('Özneler bölümü (v0.10.1790 Davis F1.2)', () => {
  it('bölümü taşımayan blob → üç tür KAPALI, varsayılan tavanlar, dilim İstanbul', async () => {
    const el = await mount();
    for (const k of [EP, DB, Q]) {
      expect(toggle(el, k).getAttribute('aria-checked')).toBe('false');
      expect(numInput(el, `${k}: servis başına en çok`).value).toBe('20');
      expect(numInput(el, `${k}: servis başına en çok`).disabled).toBe(true);
      expect(numInput(el, `${k}: tik başına seri tavanı`).value).toBe('2000');
    }
    expect(tzSelect(el).value).toBe('Europe/Istanbul');
    expect(el.textContent).toContain('Özneler');
  });

  it('kayıtlı değerler gidiş-dönüş: açılan tür, tavanlar ve UTC dilimi aynen gider', async () => {
    m.sens = baseSens({
      subjects: {
        endpoint: { enabled: true, topN: 7, maxSeriesPerTick: 900 },
        db: { enabled: false, topN: 20, maxSeriesPerTick: 2000 },
        queue: { enabled: false, topN: 20, maxSeriesPerTick: 2000 },
      },
      seasonalTimezone: 'UTC',
    });
    const el = await mount();
    expect(toggle(el, EP).getAttribute('aria-checked')).toBe('true');
    expect(numInput(el, `${EP}: servis başına en çok`).value).toBe('7');
    expect(tzSelect(el).value).toBe('UTC');
    await act(async () => { sensitivitySave(el).click(); });
    await settle();
    expect(m.puts).toHaveLength(1);
    expect(m.puts[0].subjects).toEqual({
      endpoint: { enabled: true, topN: 7, maxSeriesPerTick: 900 },
      db: { enabled: false, topN: 20, maxSeriesPerTick: 2000 },
      queue: { enabled: false, topN: 20, maxSeriesPerTick: 2000 },
    });
    expect(m.puts[0].seasonalTimezone).toBe('UTC');
  });

  it('kuyruğu aç + dilimi değiştir → bölüm tam gider, gövdenin geri kalanı korunur', async () => {
    const el = await mount();
    await act(async () => { toggle(el, Q).click(); });
    await act(async () => {
      const s = tzSelect(el);
      const setter = Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, 'value')!.set!;
      setter.call(s, 'UTC');
      s.dispatchEvent(new Event('change', { bubbles: true }));
    });
    await act(async () => { sensitivitySave(el).click(); });
    await settle();
    expect(m.puts).toHaveLength(1);
    const sj = m.puts[0].subjects as Record<string, { enabled: boolean }>;
    expect(sj.queue.enabled).toBe(true);
    expect(sj.endpoint.enabled).toBe(false);
    expect(sj.db.enabled).toBe(false);
    expect(m.puts[0].seasonalTimezone).toBe('UTC');
    expect(m.puts[0].dwellBuckets).toBe(3);
  });

  // v0.10.1804 (Davis dilim 3) — Kafka consumer lag tabanı yalnız kuyrukta;
  // kuyruk kapalıyken kilitli, kayıtlı değer gidiş-dönüş, değişiklik gider.
  it('kuyruk: en az consumer lag alanı yalnız kuyrukta, varsayılan 1000, değişiklik gider', async () => {
    const el = await mount();
    expect(el.querySelector(`input[aria-label="${EP}: en az consumer lag"]`)).toBeNull();
    expect(el.querySelector(`input[aria-label="${DB}: en az consumer lag"]`)).toBeNull();
    const lag = numInput(el, `${Q}: en az consumer lag`);
    expect(lag.value).toBe('1000');
    expect(lag.disabled).toBe(true);
    await act(async () => { toggle(el, Q).click(); });
    expect(numInput(el, `${Q}: en az consumer lag`).disabled).toBe(false);
    await type(numInput(el, `${Q}: en az consumer lag`), '5000');
    await act(async () => { sensitivitySave(el).click(); });
    await settle();
    const sj = m.puts[0].subjects as Record<string, { enabled: boolean; lagMinRecords?: number }>;
    expect(sj.queue).toMatchObject({ enabled: true, lagMinRecords: 5000 });
    expect(sj.db.lagMinRecords).toBeUndefined();
  });

  // v0.10.1797 (Davis FAZ 3 dilim 1) — terfi kipi: yok → off; seçilen kip gövdeye gider.
  it('terfi kipi: alan yok → Kapalı; Gölge seçilince bölümle birlikte gider', async () => {
    const el = await mount();
    const sel = el.querySelector<HTMLSelectElement>('select[aria-label="Özne terfi kipi"]');
    if (!sel) throw new Error('terfi kipi seçicisi yok');
    expect(sel.value).toBe('off');
    expect([...sel.options].map(o => o.value)).toEqual(['off', 'shadow', 'live']);
    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, 'value')!.set!;
      setter.call(sel, 'shadow');
      sel.dispatchEvent(new Event('change', { bubbles: true }));
    });
    await act(async () => { sensitivitySave(el).click(); });
    await settle();
    expect(m.puts).toHaveLength(1);
    const sj = m.puts[0].subjects as { promotionMode: string; endpoint: { enabled: boolean } };
    expect(sj.promotionMode).toBe('shadow');
    expect(sj.endpoint.enabled).toBe(false);
  });

  // v0.10.1798 (dilim 2) — eşikler: yok → 600 sn / 2; kip kapalıyken kilitli.
  it('terfi eşikleri: varsayılan 600 sn / 2×, kip kapalıyken kilitli, değişiklik gider', async () => {
    m.sens = baseSens({ subjects: {
      endpoint: { enabled: true, topN: 20, maxSeriesPerTick: 2000 },
      db: { enabled: false, topN: 20, maxSeriesPerTick: 2000 },
      queue: { enabled: false, topN: 20, maxSeriesPerTick: 2000 },
      promotionMode: 'shadow',
    } });
    const el = await mount();
    const sec = numInput(el, 'Özne terfi: en az süre');
    const ratio = numInput(el, 'Özne terfi: tabanın en az katı');
    expect(sec.value).toBe('600');
    expect(ratio.value).toBe('2');
    expect(sec.disabled).toBe(false);
    await type(ratio, '3');
    await act(async () => { sensitivitySave(el).click(); });
    await settle();
    const sj = m.puts[0].subjects as { promotionMinRatio: number; promotionMinSustainedSec: number };
    expect(sj.promotionMinRatio).toBe(3);
    expect(sj.promotionMinSustainedSec).toBe(600);
  });

  it('terfi eşikleri: kip kapalıyken alanlar kilitli', async () => {
    const el = await mount();
    expect(numInput(el, 'Özne terfi: en az süre').disabled).toBe(true);
    expect(numInput(el, 'Özne terfi: tabanın en az katı').disabled).toBe(true);
  });
});
