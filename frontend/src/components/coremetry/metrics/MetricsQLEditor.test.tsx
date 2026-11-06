// @vitest-environment jsdom
//
// MetricsQLEditor — v0.10.1864 (Metrics Explorer Faz 2 dilim 2.4).
// Renklendirme katmanı metnin kendisini taşır; hata konumu altı çizili +
// aria-invalid; Ctrl+Space tamamlama (combobox ↔ listbox bağı, fonksiyonlar
// + sunucu metrik araması); MetricsQL'e özgü fonksiyon CH'de nedeniyle
// kapalı; ⌘↵ çalıştırır; Tab seçimi metne yazar.
import { describe, it, expect, afterEach, beforeEach, vi } from 'vitest';
import { act, useState } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MetricsQLEditor } from './MetricsQLEditor';

const m = vi.hoisted(() => ({ names: [] as string[] }));
vi.mock('@/lib/api', () => ({
  api: {
    metricNamesSearch: (_s: string, q: string) => {
      m.names.push(q);
      return Promise.resolve({ names: [{ name: 'http_server_request_duration_seconds_bucket', type: 'histogram', unit: 's', description: 'HTTP istek süresi' }], total: 1, hasMore: false });
    },
    metricsDiscoverLabels: () => Promise.resolve({ supported: true, labels: [{ name: 'service_name' }], hasMore: false, partial: false, limit: 30, valueCounts: false, windowSec: 86400 }),
    metricsDiscoverLabelValues: () => Promise.resolve({ supported: true, label: 'service_name', values: ['orders-api'], hasMore: false, partial: false, limit: 30, windowSec: 86400 }),
  },
  isCanceled: () => false,
}));

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
let root: Root | null = null;
let host: HTMLDivElement | null = null;
beforeEach(() => { host = document.createElement('div'); document.body.appendChild(host); root = createRoot(host); m.names = []; });
afterEach(() => { act(() => root?.unmount()); host?.remove(); });

let runs = 0;
let lastValue = '';
function H({ initial, errorPos, metricsql = true }: { initial: string; errorPos?: number; metricsql?: boolean }) {
  const [v, setV] = useState(initial);
  return <MetricsQLEditor value={v} onChange={x => { lastValue = x; setV(x); }} onRun={() => { runs++; }} errorPos={errorPos} metricsql={metricsql} metricsqlReason="Yalnız VictoriaMetrics" />;
}
const ta = () => document.querySelector<HTMLTextAreaElement>('textarea')!;
const key = (k: string, o: KeyboardEventInit = {}) => act(() => { ta().dispatchEvent(new KeyboardEvent('keydown', { key: k, bubbles: true, ...o })); });

describe('MetricsQLEditor', () => {
  it('renklendirme katmanı metni aynen taşır; satır numaraları', () => {
    act(() => root!.render(<H initial={'sum by (le) (\n  rate(x_bucket{a="b"}[5m]))'} />));
    const pre = document.querySelector('pre')!;
    expect(pre.textContent).toBe('sum by (le) (\n  rate(x_bucket{a="b"}[5m]))\n');
    expect(pre.querySelectorAll('span').length).toBeGreaterThan(5);
    expect(document.body.textContent).toContain('12');
  });

  it('hata konumu: aria-invalid + dalgalı alt çizgi', () => {
    act(() => root!.render(<H initial="sum(up))" errorPos={7} />));
    expect(ta().getAttribute('aria-invalid')).toBe('true');
    expect(document.querySelector('pre .decoration-wavy')!.textContent).toBe(')');
  });

  it('⌘↵ çalıştırır', () => {
    runs = 0;
    act(() => root!.render(<H initial="up" />));
    key('Enter', { metaKey: true });
    expect(runs).toBe(1);
  });

  it('Ctrl+Space: fonksiyon + sunucu metrik önerisi; MetricsQL uzantısı kapalı; Tab yazar', async () => {
    act(() => root!.render(<H initial="rollup_r" metricsql={false} />));
    ta().setSelectionRange(8, 8);
    key(' ', { ctrlKey: true });
    await act(async () => { await new Promise(r => setTimeout(r, 220)); });
    const lb = document.querySelector('[role="listbox"]')!;
    expect(ta().getAttribute('aria-expanded')).toBe('true');
    expect(ta().getAttribute('aria-controls')).toBe(lb.id);
    const opt = [...lb.querySelectorAll('[role="option"]')].find(o => o.textContent?.includes('rollup_rate()'))!;
    expect(opt.getAttribute('aria-disabled')).toBe('true');
    expect(opt.textContent).toContain('Yalnız VictoriaMetrics');
    expect(m.names).toContain('rollup_r');
    // seçilebilir ilk öğe metrik (devre dışı fonksiyon atlanır)
    key('Tab');
    expect(lastValue).toBe('http_server_request_duration_seconds_bucket');
  });

  it('süslü parantez içinde etiket önerisi', async () => {
    act(() => root!.render(<H initial="up{se" />));
    ta().setSelectionRange(5, 5);
    key(' ', { ctrlKey: true });
    await act(async () => { await new Promise(r => setTimeout(r, 220)); });
    expect(document.querySelector('[role="listbox"]')!.textContent).toContain('service_name');
    key('Enter');
    expect(lastValue).toBe('up{service_name');
  });
});
