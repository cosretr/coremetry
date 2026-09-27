// @vitest-environment jsdom
//
// ServiceChartsExplainBody.signals — v0.10.973 (tablo standardı T1, dilim 6).
//
// NE ÇİVİLİYOR: AI çekmecesinin "İlişkili sinyaller" bölümü etiket → değer
// satırlarından oluşan bir öznitelik paneli; ham `<table>` yerine KeyValue
// (`<dl class="keyval">`). Göç yalnız YERLEŞİMİ değiştirir:
//   • aynı dört satır türü (deploy/rollout · problem · anomali · operasyon),
//     aynı sırada, aynı koşullarla;
//   • problem linki /inbox?problem=<id> olarak kalır, "Sonraki adım"
//     pivotları (hatalı izler, loglar, problem) yerinde;
//   • sinyal yoksa bölüm hiç çizilmez;
//   • KeyValue değeri `pre-wrap` — operasyon satırında çift boşluk görünmez.
import { describe, it, expect, vi, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { ServiceChartsExplain } from '@/lib/types';

const m = vi.hoisted(() => ({ data: null as unknown }));
vi.mock('@/lib/api', () => ({
  api: { copilotExplainCharts: async () => m.data },
  isCanceled: () => false,
}));
vi.mock('@/components/AuthProvider', () => ({
  useAuth: () => ({ user: { username: 'op', role: 'admin' }, loading: false }),
}));

import { ServiceChartsExplainBody } from './ServiceChartsExplainBody';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const FROM = 1_700_000_000e9;
const TO = 1_700_003_600e9;

const FULL: ServiceChartsExplain = {
  explanation: 'p95 yükseldi.',
  scope: 'err',
  signals: {
    deploy: { timeUnixNs: FROM + 60e9, kind: 'deploy', versionBefore: 'v1', versionAfter: 'v2', podsReplaced: 3 },
    problems: [{
      id: 'pr/1', title: 'Error rate high', severity: 'critical', priority: 'P1',
      startedAt: FROM, metric: 'error_rate', value: 7.5, threshold: 5,
    }],
    anomalies: [{ id: 'an1', kind: 'trace_op', pattern: 'POST /pay', startedAt: FROM, peakRatio: 4.25, status: 'active' }],
    opDeltas: [
      { name: 'POST /pay', calls: 120, p95Ratio: 2, errDeltaPp: 1.5 },
      { name: 'GET /cart', calls: 40, p95Ratio: 0, errDeltaPp: 0, isNew: true },
    ],
    otherOps: 4,
  },
};

let host: HTMLDivElement | null = null;
let root: Root | null = null;

async function mount(data: ServiceChartsExplain): Promise<HTMLElement> {
  m.data = data;
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => {
    root!.render(
      <QueryClientProvider client={qc}>
        <MemoryRouter>
          <ServiceChartsExplainBody service="payments" fromNs={FROM} toNs={TO} scope="err" />
        </MemoryRouter>
      </QueryClientProvider>,
    );
  });
  await act(async () => { await new Promise(r => setTimeout(r, 20)); });
  return host;
}

afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  host = null; root = null;
});

const rows = (el: HTMLElement) => [...el.querySelectorAll('dl.keyval > .keyval__row')] as HTMLElement[];
const label = (r: HTMLElement) => r.querySelector('dt')?.textContent;
const value = (r: HTMLElement) => r.querySelector('dd')?.textContent ?? '';

describe('ServiceChartsExplainBody — İlişkili sinyaller KeyValue (v0.10.973)', () => {
  it('ham tablo yok; sinyaller tek <dl class="keyval"> içinde, sıra ve etiketler aynı', async () => {
    const el = await mount(FULL);
    expect(el.querySelector('table')).toBeNull();
    expect(el.querySelectorAll('dl.keyval')).toHaveLength(1);
    expect(rows(el).map(label)).toEqual(['deploy', 'problem', 'anomali', 'operasyon']);
  });

  it('değerler: sürüm geçişi + pod, problem linki, anomali oranı, operasyon farkları', async () => {
    const el = await mount(FULL);
    const [dep, prob, anom, ops] = rows(el);
    expect(value(dep)).toMatch(/^\d{2}:\d{2}:\d{2} · v1 → v2 · 3 pod$/);

    const a = prob.querySelector('a');
    expect(a?.getAttribute('href')).toBe('/inbox?problem=pr%2F1');
    expect(a?.textContent).toBe('P1 · Error rate high');
    expect(value(prob)).toContain('— error_rate 7.50 / eşik 5.00');

    expect(value(anom)).toBe('POST /pay 4.3× · trace_op · active');

    expect(value(ops)).toContain('p95 2.00× err +1.50pp');
    expect(value(ops)).not.toContain('  ');
    expect(value(ops)).toContain('yeni');
    expect(value(ops)).toContain('diğer 4 operasyon: kayda değer değişim yok');
  });

  it('restart → "rollout" etiketi; sürümsüz deploy değeri yalnız saat + pod', async () => {
    const el = await mount({
      ...FULL,
      signals: { deploy: { timeUnixNs: FROM, kind: 'restart', podsReplaced: 2 }, otherOps: 0 },
    });
    const r = rows(el);
    expect(r.map(label)).toEqual(['rollout']);
    expect(value(r[0])).toMatch(/^\d{2}:\d{2}:\d{2} · 2 pod$/);
  });

  it('sinyal yoksa bölüm çizilmez; Sonraki adım pivotları yerinde', async () => {
    const el = await mount({ ...FULL, signals: { otherOps: 0 } });
    expect(el.textContent).not.toContain('İlişkili sinyaller');
    expect(el.querySelector('dl.keyval')).toBeNull();
    const hrefs = [...el.querySelectorAll('a')].map(a => a.getAttribute('href') ?? '');
    expect(hrefs.some(h => h.startsWith('/traces'))).toBe(true);
    expect(hrefs.some(h => h.startsWith('/logs'))).toBe(true);
  });

  it('opDeltas boş, otherOps > 0 → yalnız "diğer N operasyon" satırı', async () => {
    const el = await mount({ ...FULL, signals: { opDeltas: [], otherOps: 7 } });
    const r = rows(el);
    expect(r.map(label)).toEqual(['operasyon']);
    expect(value(r[0])).toBe('diğer 7 operasyon: kayda değer değişim yok');
  });
});
