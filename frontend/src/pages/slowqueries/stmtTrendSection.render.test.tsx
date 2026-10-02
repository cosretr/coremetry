// @vitest-environment jsdom
//
// stmtTrendSection.render.test.tsx — v0.10.1058. Operatör (prod, Statement
// detail): "Statement detail grafikleri de çok kötü, Coremetry geneline
// uymuyor. Ayrıca zaman yok vs., hiç olmamış." Trend bölümü üç Sparkline
// şeridiydi (x = kova sırası, ipucu "bucket 17/37"). Pinlenen: bölüm
// ürünün standart zaman grafiğini (CorePanelMulti) kullanıyor, x değerleri
// kova ZAMANLARI (indeks değil), birimler doğru, üç grafik tek senkron
// grubunda ve brush sayfanın zoom kancasına bağlı.
//
// Grafik STUB (jsdom'da canvas yok); ölçülen şey grafiğe NE girdiği.
import { describe, it, expect, vi, afterEach } from 'vitest';
import { act } from 'react';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { createRoot, type Root } from 'react-dom/client';
import type { DBStmtDetail, SpanMetricSeries } from '@/lib/types';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

interface Captured {
  title: string; unit?: string; height?: number; syncKey?: string;
  xRange?: { from: number; to: number } | null; emptyReason?: string;
  onZoom?: unknown; onZoomReset?: unknown;
  items: { name: string; role?: string; series: SpanMetricSeries[] }[];
}
const m = vi.hoisted(() => ({ panels: [] as Captured[] }));
vi.mock('@/components/chart/corePanelEntry', () => ({
  CorePanelMulti: (p: Captured) => {
    m.panels.push(p);
    return <div data-panel={p.title} />;
  },
}));

import { StmtTrendSection } from './stmtDetailSections';

const T0 = Date.UTC(2026, 9, 2, 8, 0, 0) / 1000;
function detail(over: Partial<DBStmtDetail> = {}): DBStmtDetail {
  return {
    stmtHash: '42',
    fromNs: (T0 + 150) * 1e9,
    toNs: (T0 + 1800) * 1e9,
    summary: null,
    trend: [
      { tsNs: T0 * 1e9, calls: 600, errors: 3, avgMs: 2, p95Ms: 11 },
      { tsNs: (T0 + 900) * 1e9, calls: 300, errors: 0, avgMs: 2, p95Ms: 14 },
    ],
    trendBucketSec: 300,
    callers: [],
    exemplars: null,
    ...over,
  };
}

let root: Root | null = null;
let host: HTMLElement | null = null;
afterEach(() => {
  act(() => root?.unmount());
  host?.remove();
  root = null; host = null;
  m.panels = [];
});

async function render(el: React.ReactElement) {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  await act(async () => { root!.render(el); });
  // lazy() çözülsün.
  await act(async () => { await new Promise(r => setTimeout(r, 0)); });
  // Son render'daki üç panel (Suspense yeniden render edebilir).
  return Object.values(Object.fromEntries(m.panels.map(p => [p.title, p])));
}

describe('StmtTrendSection — standart zaman grafiği (v0.10.1058)', () => {
  it('üç CorePanelMulti: başlık + birim + rol', async () => {
    const panels = await render(<StmtTrendSection detail={detail()} />);
    expect(panels.map(p => [p.title, p.unit, p.items[0].role])).toEqual([
      ['Calls / s', 'reqps', 'data'],
      ['Errors / 5m', 'short', 'error'],
      ['P95 latency', 'ms', 'data'],
    ]);
    for (const p of panels) expect(p.height).toBe(150);
  });

  it('x değerleri kova ZAMANI (ns), kova indeksi değil', async () => {
    const panels = await render(<StmtTrendSection detail={detail()} />);
    const calls = panels[0].items[0].series[0].points;
    expect(calls.map(p => p.time)).toEqual(
      [0, 300, 600, 900, 1200, 1500].map(o => (T0 + o) * 1e9));
    // 600 çağrı / 300 s = 2 req/s; boş kova 0.
    expect(calls.map(p => p.value)).toEqual([2, 0, 0, 1, 0, 0]);
    expect(panels[1].items[0].series[0].points[0]).toEqual({ time: T0 * 1e9, value: 3 });
    const p95 = panels[2].items[0].series[0].points;
    expect(p95[0].value).toBe(11);
    expect(Number.isNaN(p95[1].value)).toBe(true); // ölçülmemiş → boşluk
    // Eksen: ızgara başı (from'un 5 dk tabanı) → to, unix sn.
    expect(panels[0].xRange).toEqual({ from: T0, to: T0 + 1800 });
  });

  it('tek senkron grubu + zoom kancaları üç grafiğe de iner', async () => {
    const onZoom = vi.fn();
    const onZoomReset = vi.fn();
    const panels = await render(
      <StmtTrendSection detail={detail()} onZoom={onZoom} onZoomReset={onZoomReset} />);
    expect(new Set(panels.map(p => p.syncKey)).size).toBe(1);
    expect(panels[0].syncKey).toBeTruthy();
    for (const p of panels) {
      expect(p.onZoom).toBe(onZoom);
      expect(p.onZoomReset).toBe(onZoomReset);
    }
  });

  it('çağrısız pencere: grafikler boş-durum gerekçesiyle çizilir (boş şerit değil)', async () => {
    const panels = await render(<StmtTrendSection detail={detail({ trend: [] })} />);
    expect(panels).toHaveLength(3);
    for (const p of panels) expect(p.emptyReason).toMatch(/çağrı yok/);
  });

  it('trend okuması düştüyse (null) grafik yok, bölüm notu var', async () => {
    const panels = await render(<StmtTrendSection detail={detail({ trend: null })} />);
    expect(panels).toHaveLength(0);
    expect(host!.textContent).toContain('Trend');
  });

  it('vs prior açıkken başlık grafiğin yalnız bu pencereyi çizdiğini söyler', async () => {
    await render(<StmtTrendSection detail={detail()} compare />);
    expect(host!.textContent).toMatch(/vs prior yalnız özet karolarında/);
  });
});

describe('kaynak pinleri', () => {
  const src = (f: string) => readFileSync(resolve(__dirname, f), 'utf8');
  it('bölüm Sparkline şeridine geri dönmüyor', () => {
    expect(src('./stmtDetailSections.tsx')).not.toMatch(/from '@\/components\/Sparkline'/);
  });
  it('sayfa brush’ı ?range= sahibine (usePageZoomRange) bağlıyor', () => {
    const page = src('../StatementDetail.tsx');
    expect(page).toMatch(/handleZoom, handleZoomReset \} = usePageZoomRange/);
    expect(page).toMatch(/<StmtTrendSection[^>]*onZoom=\{handleZoom\}[^>]*onZoomReset=\{handleZoomReset\}/s);
  });
});
