// @vitest-environment jsdom
//
// LogPatternCountSection.render — v0.10.1060.
//
// Operatör (prod, "Log deseni" detayı): "Bunu doğru yakalamış ama artışın ne
// zaman başladığını göstermiyor. Elastic'e gidip bakınca barlardan net
// görüyorum."
//
// NE ÇİVİLİYOR (grafik motoru sahte — CorePanelMulti prop'larını DOM'a yazar):
//   • veri: TEK bar serisi, kova genişliği başlıkta, x ekseni sunucunun
//     hizalı penceresi, olay başlangıcında "başladı" bölgesi;
//   • istek: desen adı + pencere; aktif olayda `to` YOK (sunucu "şimdi"),
//     bitmiş olayda var;
//   • boş (hepsi 0) / hata / 404 (tanım yok) ayrı durumlar, sessiz boş tuval yok;
//   • ES kısmi cevabı tek satırlık notla söylenir.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { QueryClient, QueryClientProvider, keepPreviousData } from '@tanstack/react-query';
import type { AnomalyEvent, LogPatternSeries } from '@/lib/types';

const MIN = 60e9;
const T0 = 1_759_399_980 * 1e9;

const m = vi.hoisted(() => ({
  calls: [] as { pattern: string; fromNs: number; toNs?: number }[],
  mode: 'ok' as 'ok' | 'empty' | 'fail' | 'gone' | 'partial',
}));

function series(vals: number[], extra: Partial<LogPatternSeries> = {}): LogPatternSeries {
  return {
    pattern: 'Oracle errors (ORA-)', bucketSec: 60, from: T0 - 60 * MIN, to: T0 - 60 * MIN + vals.length * MIN,
    points: vals.map((v, i) => ({ t: T0 - 60 * MIN + i * MIN, v })), ...extra,
  };
}

vi.mock('@/lib/api', () => ({
  api: {
    anomalyLogPatternSeries: async (p: { pattern: string; fromNs: number; toNs?: number }) => {
      m.calls.push(p);
      switch (m.mode) {
        case 'fail': throw new Error('HTTP 502: log backend slow');
        case 'gone': throw new Error('HTTP 404: bilinmeyen log deseni');
        case 'empty': return series([0, 0, 0, 0]);
        case 'partial': return series([10, 12, 9000], { partial: true });
        default: return series([10, 12, 9000, 8800]);
      }
    },
  },
  isCanceled: () => false,
}));

vi.mock('@/components/chart/corePanelEntry', () => ({
  CorePanelMulti: (p: {
    viz?: string; loading?: boolean; error?: string; emptyReason?: string; note?: string | null;
    xRange?: { from: number; to: number } | null;
    regions?: { fromSec: number; toSec: number; label?: string }[];
    items: { name: string; series: { points: { time: number; value: number }[] }[] }[];
  }) => (
    <div data-panel
      data-viz={p.viz ?? ''}
      data-loading={p.loading ? '1' : ''}
      data-error={p.error ?? ''}
      data-empty={p.emptyReason ?? ''}
      data-note={p.note ?? ''}
      data-xrange={p.xRange ? `${p.xRange.from}-${p.xRange.to}` : ''}
      data-regions={JSON.stringify(p.regions ?? [])}
      data-items={p.items.length}
      data-points={p.items[0]?.series[0]?.points.map(x => x.value).join(',') ?? ''} />
  ),
}));

import { LogPatternCountSection } from './LogPatternCountSection';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const ev = (over: Partial<AnomalyEvent> = {}): AnomalyEvent => ({
  id: 'e1', kind: 'log_pattern', pattern: 'Oracle errors (ORA-)', service: 'orders-svc',
  startedAt: T0, lastSeen: T0 + 10 * MIN, peakRatio: 781, currentRatio: 640, currentCount: 15620,
  sample: 'ORA-00060', status: 'active', ...over,
});

let host: HTMLDivElement | null = null;
let root: Root | null = null;

async function mount(e: AnomalyEvent): Promise<HTMLElement> {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false, retryDelay: 0, placeholderData: keepPreviousData } } });
  await act(async () => {
    root!.render(<QueryClientProvider client={qc}><LogPatternCountSection event={e} /></QueryClientProvider>);
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

describe('LogPatternCountSection', () => {
  it('veri: tek bar serisi, kova genişliği başlıkta, x ekseni sunucu penceresi, başlangıç işareti', async () => {
    const el = await mount(ev());
    expect(el.querySelector('.pb-sect .h')?.textContent).toBe('Desen sayısı1 dk · tüm servisler');
    const p = panel(el);
    expect(p.dataset.viz).toBe('bars');
    expect(p.dataset.items).toBe('1');
    expect(p.dataset.points).toBe('10,12,9000,8800');
    expect(p.dataset.loading).toBe('');
    expect(p.dataset.error).toBe('');
    expect(p.dataset.empty).toBe('');
    expect(p.dataset.note).toBe('');
    expect(p.dataset.xrange).toBe(`${(T0 - 60 * MIN) / 1e9}-${(T0 - 56 * MIN) / 1e9}`);
    const regions = JSON.parse(p.dataset.regions!) as { fromSec: number; label: string }[];
    expect(regions).toHaveLength(1);
    expect(regions[0].fromSec).toBe(T0 / 1e9);
    expect(regions[0].label).toBe('başladı');
    // Açıklama paragrafı yok: bölüm başlık + grafikten ibaret.
    expect(el.querySelectorAll('p').length).toBe(0);
  });

  it('istek: aktif olayda to YOK (sunucu "şimdi"), bitmiş olayda var', async () => {
    await mount(ev({ status: 'active' }));
    expect(m.calls).toEqual([{ pattern: 'Oracle errors (ORA-)', fromNs: T0 - 60 * MIN, toNs: undefined }]);
    act(() => { root!.unmount(); });
    host?.remove();
    m.calls = [];
    await mount(ev({ status: 'cleared' }));
    expect(m.calls).toEqual([{ pattern: 'Oracle errors (ORA-)', fromNs: T0 - 60 * MIN, toNs: T0 + 20 * MIN }]);
  });

  it('boş (hepsi 0): sebep yazar, sessiz boş tuval yok', async () => {
    m.mode = 'empty';
    const p = panel(await mount(ev()));
    expect(p.dataset.empty).toBe('Bu pencerede desene uyan log yok.');
    expect(p.dataset.error).toBe('');
  });

  it('hata: panelin İÇİNDE hata, fırlatmaz', async () => {
    m.mode = 'fail';
    const el = await mount(ev());
    expect(panel(el).dataset.error).toBe('Desen sayısı okunamadı.');
    expect(el.querySelector('.pb-sect .h')?.textContent).toContain('Desen sayısı');
  });

  it('404 (desen tanımı artık yok): "sıfır eşleşme" diye okunmaz', async () => {
    m.mode = 'gone';
    const p = panel(await mount(ev()));
    expect(p.dataset.empty).toBe('Bu desenin tanımı artık yok — sayım çizilemiyor.');
    expect(p.dataset.error).toBe('');
  });

  it('ES kısmi cevap: tek satırlık not', async () => {
    m.mode = 'partial';
    const p = panel(await mount(ev()));
    expect(p.dataset.note).toBe('Log arka ucu zaman aşımına uğradı — sayımlar eksik olabilir.');
  });
});
