// @vitest-environment jsdom
//
// AnomalyTab.serviceSlowdown — v0.10.1091 (operatör: "Dün söylediğim CRM
// sorunu yine oldu, bir sürü anomali geldi ama P1 problem gelmedi" → "Onay").
//
// NE ÇİVİLİYOR ("Yaygın yavaşlama problemi" kutusu, Dedektör hassasiyeti, op-
// latency kutusunun altında):
//   • bölümü taşımayan blob → kutu AÇIK, alanlar varsayılanlarla, her alanın
//     tek satır Türkçe yardımı var;
//   • kayıtlı enabled:false → kutu kapalı, alanlar devre dışı;
//   • alan değişikliği + kutu kayıtla gider (enabled AÇIK boolean), gövdenin
//     geri kalanı korunur.
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

const toggle = (el: HTMLElement) => {
  const b = el.querySelector<HTMLInputElement>('input[type="checkbox"][aria-label="Yaygın yavaşlama problemi"]');
  if (!b) throw new Error('Yaygın yavaşlama kutusu yok');
  return b;
};
const numInput = (el: HTMLElement, label: string) => {
  const i = el.querySelector<HTMLInputElement>(`input[type="number"][aria-label="Yaygın yavaşlama: ${label}"]`);
  if (!i) throw new Error(`alan yok: ${label}`);
  return i;
};
function sensitivitySave(el: HTMLElement): HTMLButtonElement {
  const h2 = [...el.querySelectorAll('h2')].find(h => h.textContent === 'Dedektör hassasiyeti');
  const btn = [...(h2?.parentElement?.querySelectorAll('button') ?? [])].find(b => b.textContent?.includes('Kaydet'));
  if (!btn) throw new Error('Dedektör hassasiyeti Kaydet düğmesi yok');
  return btn as HTMLButtonElement;
}

beforeEach(() => { m.puts = []; m.sens = baseSens(); });
afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  host = null; root = null;
});

describe('Yaygın yavaşlama problemi bölümü', () => {
  it('bölümü taşımayan blob → AÇIK, varsayılanlar, alan başına yardım', async () => {
    const el = await mount();
    expect(toggle(el).checked).toBe(true);
    expect(numInput(el, 'En az operasyon').value).toBe('3');
    expect(numInput(el, 'Operasyon p99 tabanı (ms)').value).toBe('5000');
    expect(numInput(el, 'Artış katı (× kendi tabanı)').value).toBe('20');
    expect(numInput(el, 'Operasyon başına en az çağrı').value).toBe('30');
    expect(numInput(el, 'Servis toplam çağrı tabanı').value).toBe('100');
    expect(numInput(el, 'Trafik çöküşü (% düşüş)').value).toBe('40');
    expect(numInput(el, 'Kapanış için temiz kova').value).toBe('2');
    expect(numInput(el, 'Tik başına en çok yeni problem').value).toBe('10');
    expect(el.textContent).toContain('sürdürme beklenmez');
    expect(el.textContent).toContain('Operasyon anomalileri ayrıca açılmaya devam eder.');
    expect(el.textContent).toContain('Varsayılan 5000 ms.');
  });

  it('kayıtlı enabled:false → kapalı, alanlar devre dışı', async () => {
    m.sens = baseSens({ serviceSlowdown: { enabled: false, minOps: 4, minCallsPerOp: 30, minP99Ms: 8000, riseFactor: 20, minCallsTotal: 100, dropPct: 40, clearBuckets: 2, maxNewPerTick: 10 } });
    const el = await mount();
    expect(toggle(el).checked).toBe(false);
    expect(numInput(el, 'En az operasyon').disabled).toBe(true);
    expect(numInput(el, 'En az operasyon').value).toBe('4');
    expect(numInput(el, 'Operasyon p99 tabanı (ms)').value).toBe('8000');
  });

  it('alan değişikliği kayıtla gider, enabled açık boolean, gövdenin geri kalanı korunur', async () => {
    const el = await mount();
    const input = numInput(el, 'En az operasyon');
    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!;
      setter.call(input, '5');
      input.dispatchEvent(new Event('input', { bubbles: true }));
    });
    await act(async () => { sensitivitySave(el).click(); });
    await settle();
    expect(m.puts).toHaveLength(1);
    const ss = m.puts[0].serviceSlowdown as Record<string, unknown>;
    expect(ss.minOps).toBe(5);
    expect(ss.enabled).toBe(true);
    expect(ss.minP99Ms).toBe(5000);
    expect(m.puts[0].criticalZ).toBe(6);
  });

  it('kutuyu kapatınca enabled:false gider', async () => {
    const el = await mount();
    await act(async () => { toggle(el).click(); });
    await act(async () => { sensitivitySave(el).click(); });
    await settle();
    expect((m.puts[0].serviceSlowdown as Record<string, unknown>).enabled).toBe(false);
  });
});
