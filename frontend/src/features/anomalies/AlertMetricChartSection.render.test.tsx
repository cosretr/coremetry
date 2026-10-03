// @vitest-environment jsdom
//
// AlertMetricChartSection.render — v0.10.1064.
//
// Operatör (prod, "HTTP P99 latency >3s (sustained 10 min)" problemi):
// "grafik olmadığı için de anlamak çok zor artışları".
//
// NE ÇİVİLİYOR (grafik motoru sahte — CorePanelMulti prop'larını DOM'a yazar):
//   • veri: TEK seri, başlık "metrik · servis", pencere uzunluğu, birim,
//     eşik kesik çizgi + etiket, başlangıçta "başladı" bölgesi, x ekseni
//     sunucu penceresi → son nokta; boşluk null → NaN (sıfır değil);
//   • istek: kural + servis + metrik + pencere; açık problemde `to` YOK
//     (sunucu "şimdi"), bitmiş problemde var;
//   • boş (hiç değer yok) / hata panelin İÇİNDE; 404 (dizisi olmayan kural)
//     bölümü HİÇ çizmez; açıklama paragrafı yok.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { QueryClient, QueryClientProvider, keepPreviousData } from '@tanstack/react-query';
import type { AlertRuleSeries, Problem } from '@/lib/types';

const MIN = 60e9;
const T0 = 1_791_015_960 * 1e9; // 10:26 civarı, dakikaya hizalı

const m = vi.hoisted(() => ({
  calls: [] as { ruleId: string; service: string; metric: string; fromNs: number; toNs?: number }[],
  mode: 'ok' as 'ok' | 'empty' | 'fail' | 'gone',
}));

function series(vals: (number | null)[]): AlertRuleSeries {
  const from = T0 - 60 * MIN;
  return {
    metric: 'http_p99_ms', service: 'checkout-svc', windowSec: 600, stepSec: 300,
    from, to: from + vals.length * 5 * MIN,
    points: vals.map((v, i) => ({ t: from + (i + 1) * 5 * MIN, v })),
  };
}

vi.mock('@/lib/api', () => ({
  api: {
    alertRuleSeries: async (p: { ruleId: string; service: string; metric: string; fromNs: number; toNs?: number }) => {
      m.calls.push(p);
      switch (m.mode) {
        case 'fail': throw new Error('HTTP 500: code: 159, timeout');
        case 'gone': throw new Error('HTTP 404: log sorgusu kuralı — metrik dizisi yok');
        case 'empty': return series([null, null, null]);
        default: return series([820, 910, null, 3400, 3872.62]);
      }
    },
  },
  isCanceled: () => false,
}));

vi.mock('@/components/chart/corePanelEntry', () => ({
  CorePanelMulti: (p: {
    ariaLabel?: string; unit?: string; loading?: boolean; error?: string; emptyReason?: string;
    xRange?: { from: number; to: number } | null;
    regions?: { fromSec: number; toSec: number; label?: string }[];
    thresholds?: { value: number; label?: string; color?: string }[];
    items: { name: string; series: { points: { time: number; value: number }[] }[] }[];
  }) => (
    <div data-panel
      data-aria={p.ariaLabel ?? ''}
      data-unit={p.unit ?? ''}
      data-loading={p.loading ? '1' : ''}
      data-error={p.error ?? ''}
      data-empty={p.emptyReason ?? ''}
      data-xrange={p.xRange ? `${p.xRange.from}-${p.xRange.to}` : ''}
      data-regions={JSON.stringify(p.regions ?? [])}
      data-thresholds={JSON.stringify(p.thresholds ?? [])}
      data-items={p.items.length}
      data-points={p.items[0]?.series[0]?.points.map(x => String(x.value)).join(',') ?? ''} />
  ),
}));

import { AlertMetricChartSection } from './AlertMetricChartSection';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const prob = (over: Partial<Problem> = {}): Problem => ({
  id: 'p1', ruleId: 'builtin-warn-http-p99-3s', ruleName: 'HTTP P99 latency >3s (sustained 10 min)',
  severity: 'warning', service: 'checkout-svc', metric: 'http_p99_ms', value: 3872.62, threshold: 3000,
  comparator: '>', status: 'open', description: '', startedAt: T0, ...over,
});

let host: HTMLDivElement | null = null;
let root: Root | null = null;

async function mount(p: Problem): Promise<HTMLElement> {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  // Küresel keepPreviousData açıkken de bir problemin dizisi ötekinin altında görünmemeli.
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false, retryDelay: 0, placeholderData: keepPreviousData } } });
  await act(async () => {
    root!.render(<QueryClientProvider client={qc}><AlertMetricChartSection problem={p} /></QueryClientProvider>);
  });
  await act(async () => { await new Promise(r => setTimeout(r, 30)); });
  return host;
}

const panel = (el: HTMLElement) => el.querySelector<HTMLElement>('[data-panel]')!;

beforeEach(() => { m.calls = []; m.mode = 'ok'; });
afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  host = null; root = null;
});

describe('AlertMetricChartSection', () => {
  it('veri: tek seri, başlık metrik · servis, eşik çizgisi, başlangıç işareti, boşluk sıfır değil', async () => {
    const el = await mount(prob());
    expect(el.querySelector('.pb-sect .h')?.textContent).toBe('http_p99_ms · checkout-svc10 dk pencere');
    const p = panel(el);
    expect(p.dataset.aria).toBe('http_p99_ms · checkout-svc');
    expect(p.dataset.unit).toBe('ms');
    expect(p.dataset.items).toBe('1');
    expect(p.dataset.points).toBe('820,910,NaN,3400,3872.62');
    expect(p.dataset.loading).toBe('');
    expect(p.dataset.error).toBe('');
    expect(p.dataset.empty).toBe('');
    // x ekseni: sunucunun hizalı başı → son nokta.
    expect(p.dataset.xrange).toBe(`${(T0 - 60 * MIN) / 1e9}-${(T0 - 35 * MIN) / 1e9}`);
    const thr = JSON.parse(p.dataset.thresholds!) as { value: number; label: string; color: string }[];
    expect(thr).toEqual([{ value: 3000, label: '> 3000 ms', color: 'var(--warn)' }]);
    const regions = JSON.parse(p.dataset.regions!) as { fromSec: number; label: string; endSec?: number }[];
    expect(regions).toHaveLength(1);
    expect(regions[0].fromSec).toBe(T0 / 1e9);
    expect(regions[0].label).toBe('başladı');
    expect(regions[0].endSec).toBeUndefined(); // açık problem: sürüyor
    // Açıklama paragrafı yok: bölüm başlık + grafikten ibaret.
    expect(el.querySelectorAll('p').length).toBe(0);
  });

  it('istek: açık problemde to YOK (sunucu "şimdi"), bitmiş problemde kapanış + 10 dk', async () => {
    await mount(prob());
    expect(m.calls).toEqual([{ ruleId: 'builtin-warn-http-p99-3s', service: 'checkout-svc', metric: 'http_p99_ms', fromNs: T0 - 60 * MIN, toNs: undefined }]);
    act(() => { root!.unmount(); });
    host?.remove();
    m.calls = [];
    await mount(prob({ status: 'resolved', resolvedAt: T0 + 20 * MIN }));
    expect(m.calls).toEqual([{ ruleId: 'builtin-warn-http-p99-3s', service: 'checkout-svc', metric: 'http_p99_ms', fromNs: T0 - 60 * MIN, toNs: T0 + 30 * MIN }]);
  });

  it('boş (hiç değer yok): sebep yazar, sessiz boş tuval yok', async () => {
    m.mode = 'empty';
    const p = panel(await mount(prob()));
    expect(p.dataset.empty).toBe('Bu pencerede bu servis için veri yok.');
    expect(p.dataset.error).toBe('');
  });

  it('hata: panelin İÇİNDE hata, fırlatmaz, başlık kalır', async () => {
    m.mode = 'fail';
    const el = await mount(prob());
    expect(panel(el).dataset.error).toBe('Metrik dizisi okunamadı.');
    expect(el.querySelector('.pb-sect .h')?.textContent).toContain('http_p99_ms · checkout-svc');
  });

  it('404 (dizisi olmayan kural / silinmiş kural): bölüm HİÇ çizilmez', async () => {
    m.mode = 'gone';
    const el = await mount(prob());
    expect(el.querySelector('.pb-sect')).toBeNull();
    expect(el.querySelector('[data-panel]')).toBeNull();
  });

  it('critical kural: eşik kırmızı, yüzde birimi', async () => {
    const p = panel(await mount(prob({ metric: 'error_rate', threshold: 15, severity: 'critical', ruleId: 'builtin-error-rate-15pct' })));
    expect(p.dataset.unit).toBe('percent');
    expect(JSON.parse(p.dataset.thresholds!)).toEqual([{ value: 15, label: '> 15%', color: 'var(--err)' }]);
  });
});
