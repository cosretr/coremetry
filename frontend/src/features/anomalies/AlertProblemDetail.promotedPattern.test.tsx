// @vitest-environment jsdom
//
// AlertProblemDetail.promotedPattern — v0.10.1106 (operatör kuyruğu, onaylı).
//
// v0.10.1060 "Desen sayısı" grafiğini yalnız anomali olayı detayına koymuş,
// terfi Problem'ini (`anomaly-auto:`) "kaynak olayı ayrıca okumak gerekir" diye
// dışarıda bırakmıştı. Operatör artışın ne zaman başladığını Problem
// detayında görmek istiyor.
//
// NE ÇİVİLİYOR:
//   • log deseni kaynaklı terfi Problem'i → sol kolonun İLK bölümü "Desen
//     sayısı" (açık, kapanır değil); kaynak olay Problem kimliğiyle TEK istek;
//     grafik olayın deseniyle sorar; "Correlated signals"ta desen log pivotu;
//   • desen DIŞI kaynaklı terfi Problem'i (trace_op) ve olay bulunamayan terfi
//     Problem'i → bölüm yok, desen sayısı isteği yok;
//   • düz alarm Problem'i → kaynak olay isteği bile yok.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { LogPatternSeries, Problem, ProblemSourceEventResponse } from '@/lib/types';

const MIN = 60e9;
const T0 = 1_759_399_980 * 1e9;
const FP = '0123456789abcdef';

const m = vi.hoisted(() => {
  window.matchMedia = ((q: string) => ({
    matches: false, media: q, onchange: null,
    addListener: () => {}, removeListener: () => {},
    addEventListener: () => {}, removeEventListener: () => {}, dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
  return {
    source: {} as ProblemSourceEventResponse,
    sourceCalls: [] as string[],
    seriesCalls: [] as { pattern: string; fromNs: number; toNs?: number }[],
  };
});

vi.mock('@/lib/api', () => {
  const stub: Record<string, (...a: unknown[]) => Promise<unknown>> = {
    serviceOperations: async () => [],
    problemVerdicts: async () => ({ verdicts: [] }),
    servicesMetadata: async () => ({}),
    problemSourceEvent: async (id: unknown) => { m.sourceCalls.push(id as string); return m.source; },
    anomalyLogPatternSeries: async (p: unknown) => {
      m.seriesCalls.push(p as { pattern: string; fromNs: number; toNs?: number });
      const s: LogPatternSeries = {
        pattern: 'Oracle errors (ORA-)', bucketSec: 60, from: T0 - 60 * MIN, to: T0 + 30 * MIN,
        points: [{ t: T0 - MIN, v: 3 }, { t: T0, v: 900 }],
        topServices: [{ service: 'svc-orders', count: 880 }, { service: 'payments-api', count: 20 }],
      };
      return s;
    },
  };
  return {
    api: new Proxy({}, { get: (_t, k: string) => stub[k] ?? (() => Promise.resolve(null)) }),
    isCanceled: () => false,
  };
});
vi.mock('@/components/AuthProvider', () => ({
  useAuth: () => ({ user: { username: 'op', email: 'op@x', role: 'editor' }, loading: false }),
}));
vi.mock('./ProblemInsightStrip', () => ({ ProblemInsightStrip: () => null }));
vi.mock('@/components/RootCausePanel', () => ({ RootCausePanel: () => <div data-rcpanel /> }));
vi.mock('./ExternalEvidencePanel', () => ({ ExternalEvidencePanel: () => null }));
vi.mock('@/components/AffectedEntitiesList', () => ({ AffectedEntitiesList: () => null }));
vi.mock('./ProblemLogEvidence', () => ({ ProblemLogEvidence: () => null }));
vi.mock('./ProblemNotifyPanel', () => ({ ProblemNotifyPanel: () => null }));
vi.mock('@/components/ProblemRunbookPanel', () => ({ ProblemRunbookPanel: () => null }));
vi.mock('@/components/ai/AIExplainButton', () => ({ AIExplainButton: () => null }));
vi.mock('@/components/ShareButton', () => ({ ShareButton: () => null }));
vi.mock('./AlertMetricChartSection', () => ({ AlertMetricChartSection: () => <div data-alert-chart /> }));
vi.mock('@/components/chart/corePanelEntry', () => ({
  CorePanelMulti: (p: { viz?: string; regions?: { label?: string }[] }) => (
    <div data-panel data-viz={p.viz ?? ''} data-regions={JSON.stringify(p.regions ?? [])} />
  ),
}));

import { AlertProblemDetail } from './ProblemDetail';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const promoted = (over: Partial<Problem> = {}): Problem => ({
  id: `anomaly-auto:${FP}:svc-orders`, ruleId: `anomaly-auto:${FP}`, ruleName: 'Auto-promoted anomaly',
  severity: 'critical', service: 'svc-orders', metric: 'peak_ratio', value: 781, threshold: 10,
  status: 'open', description: 'Auto-promoted from anomaly: log_pattern / Oracle errors (ORA-)',
  startedAt: T0, ...over,
});

let host: HTMLDivElement | null = null;
let root: Root | null = null;
const settle = () => act(async () => { await new Promise(r => setTimeout(r, 30)); });

async function mount(p: Problem): Promise<HTMLElement> {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => {
    root!.render(
      <QueryClientProvider client={qc}>
        <MemoryRouter>
          <AlertProblemDetail problem={p} isAdmin onBack={() => {}} onChanged={() => {}} />
        </MemoryRouter>
      </QueryClientProvider>,
    );
  });
  await settle();
  await settle();
  return host;
}

const title = (s: Element) => (s.querySelector(':scope > .h')?.textContent ?? '').replace(/^[▸▾]\s*/, '');
const sect = (el: HTMLElement, t: string) =>
  [...el.querySelectorAll<HTMLElement>('.pb-sect')].find(s => title(s).startsWith(t));

beforeEach(() => { m.source = {}; m.sourceCalls = []; m.seriesCalls = []; });
afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  host = null; root = null;
});

describe('AlertProblemDetail — terfi Problem\'inde desen sayısı grafiği (v0.10.1106)', () => {
  it('log deseni kaynaklı terfi Problem\'i: sol kolonun ilk bölümü "Desen sayısı", açık; desen log pivotu', async () => {
    m.source = { sourceEvent: {
      id: FP, kind: 'log_pattern', pattern: 'Oracle errors (ORA-)', service: 'svc-orders',
      startedAt: T0, lastSeen: T0 + 20 * MIN, status: 'active',
    } };
    const el = await mount(promoted());
    expect(m.sourceCalls).toEqual([`anomaly-auto:${FP}:svc-orders`]);
    const s = sect(el, 'Desen sayısı');
    expect(s, 'Desen sayısı bölümü').toBeTruthy();
    expect(s!.querySelector(':scope > .h button[aria-expanded]'), 'kapanır olmamalı — açık gelir').toBeNull();
    expect(s!.querySelector('[data-panel]')?.getAttribute('data-viz')).toBe('bars');
    expect(s!.querySelector('[data-panel]')?.getAttribute('data-regions')).toContain('başladı');
    const leftCol = el.querySelector('.pd-cols')!.children[0];
    expect(leftCol.querySelector('.pb-sect')).toBe(s);
    // Grafik ve pivot AYNI anahtarla okur: desen adı olaydan, aktif olayda to yok.
    expect(m.seriesCalls.length).toBeGreaterThan(0);
    expect(m.seriesCalls.every(c => c.pattern === 'Oracle errors (ORA-)' && c.toNs === undefined)).toBe(true);
    const signals = sect(el, 'Correlated signals')!;
    const link = [...signals.querySelectorAll('a')].find(a => a.textContent?.includes('Logları aç (desen)'));
    expect(link, 'desen log pivotu').toBeTruthy();
    const href = new URL(link!.getAttribute('href') ?? '', 'http://x');
    expect(href.pathname).toBe('/logs');
    expect(href.searchParams.get('pattern')).toBe('Oracle errors (ORA-)');
    expect(signals.textContent).toContain('En çok: svc-orders, payments-api');
  });

  it('desen DIŞI kaynaklı terfi Problem\'i (trace_op) → bölüm yok, desen sayısı isteği yok', async () => {
    m.source = { sourceEvent: {
      id: FP, kind: 'trace_op', pattern: 'POST /checkout', service: 'svc-orders',
      startedAt: T0, lastSeen: T0 + 20 * MIN, status: 'active',
    } };
    const el = await mount(promoted({ description: 'Auto-promoted from anomaly: trace_op / POST /checkout' }));
    expect(m.sourceCalls).toHaveLength(1);
    expect(sect(el, 'Desen sayısı')).toBeUndefined();
    expect(m.seriesCalls).toHaveLength(0);
    expect(el.textContent).not.toContain('Logları aç (desen)');
  });

  it('kaynak olay bulunamadı (alan yok) → bölüm yok', async () => {
    m.source = {};
    const el = await mount(promoted());
    expect(m.sourceCalls).toHaveLength(1);
    expect(sect(el, 'Desen sayısı')).toBeUndefined();
    expect(m.seriesCalls).toHaveLength(0);
  });

  it('düz alarm Problem\'i → kaynak olay isteği YOK, bölüm yok', async () => {
    const el = await mount(promoted({
      id: 'builtin-error-rate-15pct:payments-api', ruleId: 'builtin-error-rate-15pct', ruleName: 'High error rate',
      service: 'payments-api', metric: 'error_rate', value: 20, threshold: 15, description: '',
    }));
    expect(m.sourceCalls).toHaveLength(0);
    expect(m.seriesCalls).toHaveLength(0);
    expect(sect(el, 'Desen sayısı')).toBeUndefined();
    expect(sect(el, 'Root cause analysis')).toBeTruthy();
  });
});
