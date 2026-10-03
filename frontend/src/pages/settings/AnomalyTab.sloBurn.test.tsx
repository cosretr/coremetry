// @vitest-environment jsdom
//
// AnomalyTab.sloBurn — v0.10.1081.
//
// Operatör (prod): "SLO burn rate problem olmasın, çıkar. SLO ile ilgili
// beklentim yok." SLO burn-rate Problem üretimi varsayılan KAPALI
// (problem_priority.sloBurnProblems, backend *bool, yok = kapalı).
//
// NE ÇİVİLİYOR ("SLO burn-rate problemleri" kutusu, Alert problemi önceliği):
//   • alanı taşımayan blob → kutu KAPALI, tek satırlık Türkçe ipucu görünür;
//   • kayıtlı true → kutu açık;
//   • dokunmadan kayıt AÇIK `sloBurnProblems: false` gönderir;
//   • kutuyu açıp kayıt `true` gönderir; diğer vidalar korunur.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';

const m = vi.hoisted(() => {
  window.matchMedia = ((q: string) => ({
    matches: false, media: q, onchange: null,
    addListener: () => {}, removeListener: () => {},
    addEventListener: () => {}, removeEventListener: () => {}, dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
  return {
    prio: {} as Record<string, unknown>,
    puts: [] as Record<string, unknown>[],
  };
});
vi.mock('@/lib/api', () => {
  const stub: Record<string, (...a: unknown[]) => Promise<unknown>> = {
    // Sekmenin üst bölümü yüklenmeden aşağısı çizilmez.
    getAnomalyPromotion: async () => ({ enabled: true, minPeakRatio: 15, criticalPeakRatio: 20, minSustainedSec: 1800, minCount: 1000 }),
    getProblemPriority: async () => m.prio,
    putProblemPriority: async (body: unknown) => {
      m.puts.push(body as Record<string, unknown>);
      return body;
    },
  };
  return {
    // Diğer bölümlerin okumaları asılı kalır (yalnız bu bölüm çizilir).
    api: new Proxy({}, { get: (_t, k: string) => stub[k] ?? (() => new Promise(() => {})) }),
    isCanceled: () => false,
  };
});

import { AnomalyPromotionTab } from './AnomalyTab';

// Bilerek sloBurnProblems TAŞIMAYAN blob (v0.10.1081 öncesi kayıt).
const basePrio = (extra: Record<string, unknown> = {}) => ({
  bigBreachRatio: 2, staleCriticalHours: 4,
  inboxKeepSourcePriority: ['anomaly:*:error_rate', 'builtin-*', 'db-health:*', 'incident:critical'],
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

const box = (el: HTMLElement) => {
  const b = el.querySelector<HTMLInputElement>('input[type="checkbox"][aria-label="SLO burn-rate problemleri"]');
  if (!b) throw new Error('SLO burn-rate kutusu yok');
  return b;
};

function prioritySave(el: HTMLElement): HTMLButtonElement {
  const h2 = [...el.querySelectorAll('h2')].find(h => h.textContent === 'Alert problemi önceliği');
  const btn = [...(h2?.parentElement?.querySelectorAll('button') ?? [])].find(b => b.textContent?.includes('Kaydet'));
  if (!btn) throw new Error('Alert problemi önceliği Kaydet düğmesi yok');
  return btn as HTMLButtonElement;
}

beforeEach(() => { m.puts = []; m.prio = basePrio(); });
afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  host = null; root = null;
});

describe('SLO burn-rate problemleri kutusu', () => {
  it('alanı taşımayan blob → KAPALI, ipucu görünür', async () => {
    expect('sloBurnProblems' in m.prio).toBe(false);
    const el = await mount();
    expect(box(el).checked).toBe(false);
    expect(el.textContent).toContain('Kapalı (varsayılan): SLO bütçe yanması Problem, incident ve bildirim açmaz');
  });

  it('kayıtlı true → açık', async () => {
    m.prio = basePrio({ sloBurnProblems: true });
    const el = await mount();
    expect(box(el).checked).toBe(true);
  });

  it('dokunmadan kayıt AÇIK false gönderir', async () => {
    const el = await mount();
    await act(async () => { prioritySave(el).click(); });
    await settle();
    expect(m.puts).toHaveLength(1);
    expect(m.puts[0].sloBurnProblems).toBe(false);
  });

  it('kutuyu açıp kayıt true gönderir, diğer vidalar korunur', async () => {
    const el = await mount();
    await act(async () => { box(el).click(); });
    expect(box(el).checked).toBe(true);
    await act(async () => { prioritySave(el).click(); });
    await settle();
    expect(m.puts[0].sloBurnProblems).toBe(true);
    expect(m.puts[0].bigBreachRatio).toBe(2);
    expect(m.puts[0].staleCriticalHours).toBe(4);
  });
});
