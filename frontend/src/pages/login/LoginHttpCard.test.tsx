// @vitest-environment jsdom
//
// LoginHttpCard.test.tsx — v0.10.1908 (Login HTTP grafik kartı, audit §6.2).
// CorePanelMulti (grafik chunk'ı) stub'lanır: burada kartın VERİ → PROP
// eşlemesi çivilenir (HTTP/gRPC/Kafka throughput, ortalama yanıt, ortak syncKey, xRange
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
  // v0.10.1923 — HTTP / gRPC / Kafka hızı, her biri tek seri (grup anahtarı boş).
  throughput: { groupKey: [], points: pts(104) },
  grpc: { groupKey: [], points: pts(12) },
  kafka: { groupKey: [], points: pts(400) },
  avg: { groupKey: ['avg'], points: pts(0.2) },
  summary: { now_rps: 1500, grpc_now_rps: 12, kafka_now_mps: 40_000, avg_now_s: 0.186 },
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
      expect(p.height).toBe(76);
      expect(p.xRange).toEqual({ from: T0 / 1000, to: (T0 + 600_000) / 1000 });
    }
    expect(tp.storageKey).toBe('login-tput');
    expect(rt.storageKey).toBe('login-rt');
    expect(tp.unit).toBe('short'); // v0.10.1923: istek ve mesaj aynı eksende
    expect(rt.unit).toBe('s');
    expect(tp.syncKey).toBeTruthy();
    expect(tp.syncKey).toBe(rt.syncKey);
    expect(tp.syncKey).toMatch(/-ms$/);
  });

  it('Throughput: HTTP / gRPC / Kafka tek panelde, ayrı renk; olmayan protokol çizilmez', () => {
    render(<LoginHttpCard http={HTTP} orgName="acme" />);
    const tp = panels()[0];
    expect(tp.items.map(i => i.name)).toEqual(['HTTP', 'gRPC', 'Kafka']);
    expect(tp.items.map(i => i.color)).toEqual(['var(--ok)', 'var(--purple)', 'var(--orange)']);
    expect(tp.items[0].series[0].points).toHaveLength(3);
    act(() => root?.unmount()); host?.remove(); seen.panels = [];
    render(<LoginHttpCard http={{ ...HTTP, grpc: undefined, kafka: undefined }} orgName="acme" />);
    expect(panels()[0].items.map(i => i.name)).toEqual(['HTTP']);
  });

  // v0.10.1922 — p95 kaldırıldı: Response time yalnız ortalama.
  it('Response time: yalnız ort (--info); ortalama yoksa panel çizilmez', () => {
    render(<LoginHttpCard http={HTTP} orgName="acme" />);
    const rt = panels()[1];
    expect(rt.items.map(i => i.name)).toEqual(['ort']);
    expect(rt.items.map(i => i.color)).toEqual(['var(--info)']);
    act(() => root?.unmount()); host?.remove(); seen.panels = [];
    const el = render(<LoginHttpCard http={{ ...HTTP, avg: undefined }} orgName="acme" />);
    expect(panels()).toHaveLength(1);
    expect(el.textContent).not.toContain('Response time');
  });

  it('başlık + satır başlıkları + tek statik lejant (TR)', () => {
    const el = render(<LoginHttpCard http={HTTP} orgName="acme" />);
    const txt = el.textContent ?? '';
    expect(txt).toContain('acme OpenTelemetry metrikleri · son 24 saat');
    expect(txt).toContain('tüm servisler');
    expect(txt).toContain('Throughput · metrik');
    expect(txt).toContain('şimdi · saniyede: HTTP 1.5K · gRPC 12 · Kafka 40.0K');
    expect(txt).toContain('Response time · metrik');
    expect(txt).toContain('ort 186 ms');
    expect(txt).not.toContain('p95');
    const legend = [...el.querySelectorAll('li')].map(l => l.textContent);
    expect(legend).toEqual(['—HTTP', '—gRPC', '—Kafka', '—ort']);
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
    ['yetersiz nokta (< 2)', { ...base, http_24h: { ...HTTP, throughput: { groupKey: [], points: pts(1).slice(0, 1) }, grpc: undefined, kafka: undefined } }],
    ['hız serisi yok', { ...base, http_24h: { ...HTTP, throughput: undefined, grpc: undefined, kafka: undefined } }],
  ] as [string, LoginSummary][])('%s → kart yok, grafik chunk\'ı çağrılmaz', async (_n, data) => {
    const el = render(<LoginSummaryView data={data} />);
    await wait(); await wait();
    expect(el.textContent).not.toContain('OpenTelemetry metrikleri');
    expect(panels()).toHaveLength(0);
    expect(el.textContent).toContain('5.0M');
  });
});
