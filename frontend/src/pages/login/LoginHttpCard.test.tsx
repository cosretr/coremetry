// @vitest-environment jsdom
//
// LoginHttpCard.test.tsx — v0.10.1908 (Login HTTP grafik kartı, audit §6.2).
// CorePanelMulti (grafik chunk'ı) stub'lanır: burada kartın VERİ → PROP
// eşlemesi çivilenir (durum kodu serileri, avg/p95, ortak syncKey, xRange
// saniye, gömülü + lejantsız paneller, başlık satırları, tek statik lejant).
import { describe, it, expect, vi, afterEach, beforeEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import type { LoginSummary, LoginSummaryHttp } from '@/lib/types';

interface Captured {
  embedded?: boolean; hideLegend?: boolean; title?: string; unit?: string; height?: number;
  storageKey: string; syncKey?: string; xRange?: { from: number; to: number };
  items: { name: string; color?: string; series: { groupKey: string[]; points: unknown[] }[] }[];
}
const seen = vi.hoisted(() => ({ panels: [] as unknown[] }));
vi.mock('@/components/chart/corePanelEntry', () => ({
  CorePanelMulti: (p: unknown) => { seen.panels.push(p); return <div data-testid="panel" />; },
}));
vi.mock('@/lib/branding', async (importOriginal) => {
  const mod = await importOriginal<typeof import('@/lib/branding')>();
  return { ...mod, useBranding: () => ({ ...mod.DEFAULT_BRANDING, orgName: 'acme' }) };
});

import LoginHttpCard from './LoginHttpCard';
import LoginSummaryView from './LoginSummary';
import { setUserLang } from '@/lib/i18n';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const T0 = 1_700_000_000_000; // ms
const ns = (ms: number) => ms * 1e6;
const pts = (v: number) => [0, 1, 2].map(i => ({ time: ns(T0 + i * 300_000), value: v + i }));
const HTTP: LoginSummaryHttp = {
  from: T0, to: T0 + 600_000, step_s: 300,
  // v0.10.1914 — TEK toplam hız serisi (durum kodu kırılımı yok); grup anahtarı boş.
  throughput: { groupKey: [], points: pts(104) },
  avg: { groupKey: ['avg'], points: pts(0.2) },
  p95: { groupKey: ['p95'], points: pts(0.4) },
  summary: { now_rps: 1500, peak_rps: 2200, peak_at: new Date(2026, 10, 6, 12, 5).getTime(), avg_now_s: 0.186, p95_now_s: 0.412 },
};

function memoryStorage(): Storage {
  const mem = new Map<string, string>();
  return {
    getItem: k => mem.get(k) ?? null, setItem: (k, v) => { mem.set(k, String(v)); },
    removeItem: k => { mem.delete(k); }, clear: () => mem.clear(), key: i => Array.from(mem.keys())[i] ?? null,
    get length() { return mem.size; },
  };
}
let host: HTMLElement | null = null;
let root: Root | null = null;
function render(el: React.ReactElement): HTMLElement {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  act(() => root!.render(el));
  return host;
}
const wait = () => act(async () => { await new Promise(r => setTimeout(r, 20)); });
const panels = () => seen.panels as Captured[];

beforeEach(() => { vi.stubGlobal('localStorage', memoryStorage()); setUserLang('tr'); seen.panels = []; });
afterEach(() => { act(() => root?.unmount()); host?.remove(); host = null; root = null; setUserLang(null); vi.unstubAllGlobals(); });

describe('LoginHttpCard', () => {
  it('iki gömülü, lejantsız panel; ortak syncKey; xRange saniye', () => {
    render(<LoginHttpCard http={HTTP} orgName="acme" />);
    const [tp, rt] = panels();
    expect(panels()).toHaveLength(2);
    for (const p of [tp, rt]) {
      expect(p.embedded).toBe(true);
      expect(p.hideLegend).toBe(true);
      expect(p.title).toBe('');
      expect(p.height).toBe(96);
      expect(p.xRange).toEqual({ from: T0 / 1000, to: (T0 + 600_000) / 1000 });
    }
    expect(tp.storageKey).toBe('login-tput');
    expect(rt.storageKey).toBe('login-rt');
    expect(tp.unit).toBe('reqps');
    expect(rt.unit).toBe('s');
    expect(tp.syncKey).toBeTruthy();
    expect(tp.syncKey).toBe(rt.syncKey);
    expect(tp.syncKey).toMatch(/-ms$/);
  });

  it('Throughput: tek "toplam" serisi (durum kodu kırılımı yok)', () => {
    render(<LoginHttpCard http={HTTP} orgName="acme" />);
    const tp = panels()[0];
    expect(tp.items.map(i => i.name)).toEqual(['toplam']);
    expect(tp.items.map(i => i.color)).toEqual(['var(--ok)']);
    expect(tp.items[0].series[0].points).toHaveLength(3);
  });

  it('Response time: ort --info, p95 --purple', () => {
    render(<LoginHttpCard http={HTTP} orgName="acme" />);
    const rt = panels()[1];
    expect(rt.items.map(i => i.name)).toEqual(['ort', 'p95']);
    expect(rt.items.map(i => i.color)).toEqual(['var(--info)', 'var(--purple)']);
  });

  it('p95 yoksa yalnız ort; ikisi de yoksa Response time paneli hiç çizilmez', () => {
    render(<LoginHttpCard http={{ ...HTTP, p95: undefined, summary: { ...HTTP.summary, p95_now_s: undefined } }} orgName="acme" />);
    expect(panels()[1].items.map(i => i.name)).toEqual(['ort']);
    act(() => root?.unmount()); host?.remove(); seen.panels = [];
    const el = render(<LoginHttpCard http={{ ...HTTP, avg: undefined, p95: undefined }} orgName="acme" />);
    expect(panels()).toHaveLength(1);
    expect(el.textContent).not.toContain('Response time');
  });

  it('başlık + satır başlıkları + tek statik lejant (TR)', () => {
    const el = render(<LoginHttpCard http={HTTP} orgName="acme" />);
    const txt = el.textContent ?? '';
    expect(txt).toContain('acme OpenTelemetry metrikleri · son 24 saat');
    expect(txt).toContain('tüm HTTP servisleri');
    expect(txt).toContain('Throughput · metrik');
    expect(txt).toContain('şimdi 1.5K req/s · tepe 2.2K (12:05)');
    expect(txt).toContain('Response time · metrik');
    expect(txt).toContain('ort 186 ms · p95 412 ms');
    const legend = [...el.querySelectorAll('li')].map(l => l.textContent);
    expect(legend).toEqual(['—toplam', '—ort', '—p95']);
  });

  it('orgName boşsa ad düşer', () => {
    const el = render(<LoginHttpCard http={HTTP} />);
    expect(el.querySelector('h3')!.textContent).toBe('OpenTelemetry metrikleri · son 24 saat');
  });
});

describe('LoginSummary → grafik kartı (lazy)', () => {
  const base: LoginSummary = { enabled: true, ready: true, spans_today: 5_000_000 };
  it('http_24h kullanılabilirken kart gelir', async () => {
    const el = render(<LoginSummaryView data={{ ...base, http_24h: HTTP }} />);
    await wait(); await wait();
    expect(el.textContent).toContain('acme OpenTelemetry metrikleri');
    expect(el.querySelectorAll('[data-testid="panel"]')).toHaveLength(2);
  });
  it.each([
    ['http_24h yok', base],
    ['yetersiz nokta (< 2)', { ...base, http_24h: { ...HTTP, throughput: { groupKey: [], points: pts(1).slice(0, 1) } } }],
    ['hız serisi yok', { ...base, http_24h: { ...HTTP, throughput: undefined } }],
  ] as [string, LoginSummary][])('%s → kart yok, grafik chunk\'ı çağrılmaz', async (_n, data) => {
    const el = render(<LoginSummaryView data={data} />);
    await wait(); await wait();
    expect(el.textContent).not.toContain('OpenTelemetry metrikleri');
    expect(panels()).toHaveLength(0);
    expect(el.textContent).toContain('5.0M');
  });
});
