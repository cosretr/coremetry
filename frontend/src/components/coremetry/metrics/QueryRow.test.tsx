// @vitest-environment jsdom
//
// QueryRow + FormulaRow — v0.10.1862 (Metrics Explorer Faz 2 dilim 2.2).
// Kabul ölçütü: birim testler; inline style 0, ham element 0 (statik tarama
// aşağıda). Satır anatomisi, tür notu ("sayaç → rate"), kapsam çipi ✕,
// süzgeç kaldırma, gizle düğmesi aria-pressed, metrik seçicinin sunucu
// araması (limit 20, cluster, AbortSignal), formül hatası role=alert.
import { describe, it, expect, afterEach, beforeEach, vi } from 'vitest';
import { act, useState } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { readFileSync, readdirSync } from 'node:fs';
import { join } from 'node:path';
import { QueryRow } from './QueryRow';
import { FormulaRow } from './FormulaRow';
import { blankMxQuery, type MxQuery } from '@/lib/metricsExplorer/model';

const m = vi.hoisted(() => ({ names: [] as { q?: string; limit: number; cluster?: string; signal?: AbortSignal }[] }));
vi.mock('@/lib/api', () => ({
  api: {
    metricNamesSearch: (_svc: string, q: string | undefined, limit: number, _off: number, _src: unknown, opts?: { cluster?: string; signal?: AbortSignal }) => {
      m.names.push({ q, limit, cluster: opts?.cluster, signal: opts?.signal });
      return Promise.resolve({
        names: [{ name: 'http_server_request_duration_seconds_count', type: 'counter', unit: '1', description: '', typeSource: 'inferred' }],
        total: 137, hasMore: true,
      });
    },
    metricsDiscoverLabels: () => Promise.resolve({ supported: false, reason: 'Yalnız VictoriaMetrics' }),
    metricsDiscoverLabelValues: () => Promise.resolve({ supported: false, reason: 'Yalnız VictoriaMetrics' }),
  },
  isCanceled: () => false,
}));

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
(globalThis as unknown as { ResizeObserver: unknown }).ResizeObserver = class { observe() {} unobserve() {} disconnect() {} };
Element.prototype.scrollIntoView = function scrollIntoView() {};

let root: Root | null = null;
let host: HTMLDivElement | null = null;
beforeEach(() => { host = document.createElement('div'); document.body.appendChild(host); root = createRoot(host); m.names = []; });
afterEach(() => { act(() => root?.unmount()); host?.remove(); document.body.innerHTML = ''; });

const Q: MxQuery = {
  ...blankMxQuery('A'), metric: 'http_server_request_duration_seconds_count', metricKind: 'counter', kindEstimated: true,
  fn: 'rate', agg: 'sum', by: ['service_name'], filters: [{ k: 'http_route', op: '!~', v: '/health.*' }],
};

let last: MxQuery | null = null;
let scopeOff = 0;
function Harness({ initial }: { initial: MxQuery }) {
  const [q, setQ] = useState(initial);
  return <QueryRow query={q} onChange={n => { last = n; setQ(n); }} cluster="acme-prod-1"
    scope={{ label: 'k8s_cluster_name', value: 'acme-prod-1' }} onScopeOff={() => { scopeOff++; }} onRemove={() => {}} />;
}

const byLabel = (l: string) => document.querySelector<HTMLElement>(`[aria-label="${l}"]`);

describe('QueryRow', () => {
  it('anatomi: harf, metrik, kapsam, süzgeç, fonksiyon notu, by', () => {
    act(() => root!.render(<Harness initial={Q} />));
    const row = document.querySelector('[data-testid="query-row-A"]')!;
    expect(row.getAttribute('role')).toBe('group');
    expect(row.textContent).toContain('http_server_request_duration_seconds_count');
    expect(row.textContent).toContain('k8s_cluster_name = acme-prod-1');
    expect(row.textContent).toContain('http_route !~ /health.*');
    expect(row.textContent).toContain('sayaç → rate · tahmini');
    expect(row.textContent).toContain('service_name');
    expect(byLabel('Fonksiyon: rate')).not.toBeNull();
    expect(byLabel('Toplama: sum')).not.toBeNull();
  });

  it('kapsam ✕ tek tıkla kapatır; süzgeç ✕ kaldırır; by ✕ çıkarır', () => {
    act(() => root!.render(<Harness initial={Q} />));
    act(() => byLabel("Kapsamı kaldır: tüm cluster'lar")!.click());
    expect(scopeOff).toBe(1);
    act(() => byLabel('Süzgeci kaldır: http_route')!.click());
    expect(last!.filters).toEqual([]);
    act(() => byLabel('Gruplamadan çıkar: service_name')!.click());
    expect(last!.by).toEqual([]);
  });

  it('gizle düğmesi aria-pressed taşır', () => {
    act(() => root!.render(<Harness initial={Q} />));
    const b = byLabel('A sorgusunu grafikte gizle')!;
    expect(b.getAttribute('aria-pressed')).toBe('false');
    act(() => b.click());
    expect(last!.hidden).toBe(true);
    expect(byLabel('A sorgusunu grafikte göster')!.getAttribute('aria-pressed')).toBe('true');
  });

  it('lejant biçimi yazılır', () => {
    act(() => root!.render(<Harness initial={Q} />));
    const input = byLabel('Sorgu A lejant biçimi') as HTMLInputElement;
    const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!;
    act(() => { setter.call(input, '{{service_name}}'); input.dispatchEvent(new Event('input', { bubbles: true })); });
    expect(last!.legend).toBe('{{service_name}}');
  });

  it('yüzdelikte toplama menüsü yok, "topla sum" sabit', () => {
    act(() => root!.render(<Harness initial={{ ...Q, metric: 'x_bucket', metricKind: 'histogram', fn: 'p95' }} />));
    expect(byLabel('Toplama: sum')).toBeNull();
    expect(document.body.textContent).toContain('histogram → p95');
  });

  it('metrik seçici sunucu araması: limit 20, cluster, AbortSignal; seçim türü kurar', async () => {
    vi.useFakeTimers();
    act(() => root!.render(<Harness initial={{ ...blankMxQuery('A') }} />));
    act(() => byLabel('Metrik seç')!.click());
    await act(async () => { vi.advanceTimersByTime(200); });
    vi.useRealTimers();
    await act(async () => { await Promise.resolve(); });
    expect(m.names.length).toBeGreaterThan(0);
    expect(m.names[0]).toMatchObject({ limit: 20, cluster: 'acme-prod-1' });
    expect(m.names[0].signal).toBeInstanceOf(AbortSignal);
    expect(document.body.textContent).toContain('137 sonuçtan ilk 1');
    const opt = [...document.querySelectorAll<HTMLElement>('[cmdk-item]')].find(e => e.textContent?.includes('http_server_request_duration_seconds_count'))!;
    act(() => opt.click());
    expect(last).toMatchObject({ metric: 'http_server_request_duration_seconds_count', metricKind: 'counter', fn: 'rate', agg: 'sum' });
  });
});

describe('FormulaRow', () => {
  it('bilinmeyen harf → role=alert', () => {
    const qs = [Q];
    act(() => root!.render(<FormulaRow query={{ ...blankMxQuery('F1'), kind: 'formula', expr: 'Z / A' }} queries={qs} onChange={() => {}} />));
    expect(document.querySelector('[role="alert"]')!.textContent).toBe('Bilinmeyen sorgu: Z');
  });
  it('geçerli formül hatasız', () => {
    act(() => root!.render(<FormulaRow query={{ ...blankMxQuery('F1'), kind: 'formula', expr: 'A * 100' }} queries={[Q]} onChange={() => {}} />));
    expect(document.querySelector('[role="alert"]')).toBeNull();
  });
});

describe('statik: inline style 0, ham element 0 (audit §7 dilim 2.2)', () => {
  const dir = __dirname;
  const files = readdirSync(dir).filter(f => f.endsWith('.tsx') && !f.includes('.test.'));
  it.each(files)('%s', f => {
    const src = readFileSync(join(dir, f), 'utf8');
    expect(src).not.toMatch(/style=\{\{/);
    expect(src).not.toMatch(/<(button|input|select|textarea)[\s>]/);
    expect(src).not.toMatch(/@\/components\/ui\//);
  });
});
