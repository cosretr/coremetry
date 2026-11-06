// @vitest-environment jsdom
//
// ServiceLogsTab.pin.test.tsx — v0.10.1817 (Log Search Faz 1 dilim 1.1,
// sözleşme çivisi). Servis sayfasının Logs sekmesi /logs ile AYNI paylaşılan
// parçaları (components/LogsHistogram + components/LogTable + api.logs)
// kendi ince kabuğunda bağlar. Faz 1 bu parçaları değiştirirken (histogram
// CorePanel'e, 400 konumlu hata, iptal sinyali) sekmenin bugünkü davranışı
// çivili kalır: histogram + tablo çizilir, liste ve histogram istekleri
// sekmenin `service` süzgecini taşır, sayfa 200 satırdır.
import '@/test/pageJsdom';
import { describe, it, expect, afterEach, beforeEach, vi } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { LogsResponse } from '@/lib/types';
import { ServiceLogsTab } from './ServiceSignalTabs';

const m = vi.hoisted(() => ({
  displayPrefCalls: 0, // v0.10.1832
  tsArity: 0, // v0.10.1834
  logsCalls: [] as Record<string, unknown>[],
  tsCalls: [] as Record<string, unknown>[],
  logsReject: null as Error | null,
  core: null as Record<string, unknown> | null,
  logsSignal: undefined as AbortSignal | undefined,
  // v0.10.1900 — stub'sız her api çağrısı (alan listesi, alan istatistiği,
  // çevre loglar, trace önizleme …) adıyla sayılır.
  other: [] as string[],
}));

vi.mock('@/lib/api', () => {
  const T0 = 1759494780000000000;
  const stub: Record<string, (...a: unknown[]) => Promise<unknown>> = {
    logs: (p: unknown, signal?: unknown) => {
      m.logsCalls.push(p as Record<string, unknown>);
      m.logsSignal = signal as AbortSignal | undefined;
      if (m.logsReject) return Promise.reject(m.logsReject);
      return Promise.resolve({
        total: 1,
        logs: [{
          id: 1, timestamp: T0 + 1e9, service: 'orders-api', severityText: 'ERROR', severityNumber: 17,
          body: 'payment declined for branch-teller', traceId: '9c1d07aa5b3e4f0a8d217c66f201e41f', spanId: '', attributes: {}, resourceAttributes: {},
        }],
      } as unknown as LogsResponse);
    },
    logsTimeseries: (p: unknown, ...rest: unknown[]) => {
      m.tsCalls.push(p as Record<string, unknown>);
      m.tsArity = 1 + rest.length; // v0.10.1834 — gözlemcisiz çağrı (params, signal)
      return Promise.resolve([
        { name: 'ERROR', points: [{ t: T0, v: 3 }, { t: T0 + 60e9, v: 1 }] },
        { name: 'INFO', points: [{ t: T0, v: 40 }, { t: T0 + 60e9, v: 22 }] },
      ]);
    },
  };
  stub.getDisplayPref = () => { m.displayPrefCalls++; return Promise.resolve({ key: 'logs', display: null }); };
  return {
    api: new Proxy({}, { get: (_t, k: string) => stub[k] ?? (() => { m.other.push(k); return Promise.resolve({}); }) }),
    isCanceled: () => false,
  };
});
vi.mock('@/components/AuthProvider', () => ({
  useAuth: () => ({ user: { username: 'op', role: 'viewer' }, loading: false }),
}));
// Grafik motoru jsdom'da canvas istemez; çivi KABUĞU (histogram kartı)
// sayar, çizimi değil.
// v0.10.1822 — histogram motoru DEĞİŞMEDİ (TimeChart); çizim prop'ları
// yakalanır ki hata-oranı çizgisinin (v0.9.218) sekmede durduğu çivilensin.
vi.mock('@/components/charts/TimeChart', () => ({
  TimeChart: (props: Record<string, unknown>) => { m.core = props; return null; },
}));

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
class NoopResizeObserver { observe() {} unobserve() {} disconnect() {} }
(globalThis as unknown as { ResizeObserver: unknown }).ResizeObserver = NoopResizeObserver;

let root: Root | null = null;
let host: HTMLDivElement | null = null;
const settle = () => act(async () => { await new Promise(r => setTimeout(r, 30)); });

beforeEach(() => { m.logsCalls = []; m.tsCalls = []; m.logsReject = null; m.core = null; m.logsSignal = undefined; m.other = []; });
afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  root = null; host = null;
});

async function mountTab() {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => {
    root!.render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={['/services/orders-api?tab=logs']}>
          <ServiceLogsTab service="orders-api" range={{ preset: '1h' }}
            windowNs={{ from: 1759494780000000000, to: 1759497240000000000 }} />
        </MemoryRouter>
      </QueryClientProvider>,
    );
  });
  await settle();
}

describe('servis Logs sekmesi — paylaşılan log parçaları çivisi (v0.10.1817)', () => {
  // v0.10.1818 — 400 {error, pos, detail}: sekme "log yok" demez, sunucunun
  // konumlu cümlesini tablonun hata durumunda gösterir.
  it('sorgu reddi (400) → konumlu cümle, "log yok" değil', async () => {
    m.logsReject = new Error('HTTP 400: {"error":"Sorgu sözdizimi hatalı (17. karakter).","pos":16,"detail":"Cannot parse"}');
    await mountTab();
    expect(host!.textContent).toContain('Sorgu sözdizimi hatalı (17. karakter).');
    expect(host!.textContent).not.toContain('için log yok');
  });

  it('histogram + tablo çizilir; liste ve histogram service süzgecini taşır', async () => {
    host = document.createElement('div');
    document.body.appendChild(host);
    root = createRoot(host);
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    await act(async () => {
      root!.render(
        <QueryClientProvider client={qc}>
          <MemoryRouter initialEntries={['/services/orders-api?tab=logs']}>
            <ServiceLogsTab service="orders-api" range={{ preset: '1h' }}
              windowNs={{ from: 1759494780000000000, to: 1759497240000000000 }} />
          </MemoryRouter>
        </QueryClientProvider>,
      );
    });
    await settle();

    expect(m.logsCalls).toHaveLength(1);
    expect(m.logsCalls[0]).toMatchObject({ service: 'orders-api', limit: 200, from: 1759494780000000000, to: 1759497240000000000 });
    // v0.10.1831 — /logs'un örnek boyu / dar _source / sayımsız isteği sekmeye sızmaz.
    expect(m.logsCalls[0].fields).toBeUndefined();
    expect(m.logsCalls[0].skipTotal).toBeUndefined();
    // v0.10.1832 — /logs görünüm tercihi sekmeye sızmaz (tercih okunmaz).
    expect(m.displayPrefCalls).toBe(0);
    // v0.10.1833 — /logs sunucu sıralaması + sürüklenebilir başlık sekmeye sızmaz.
    expect(m.logsCalls[0].sort).toBeUndefined();
    expect(document.querySelector('thead .lt-sort')).toBeNull();
    expect(document.querySelector('thead th[draggable]')).toBeNull();
    // v0.10.1834 — akışlı yükleme göstergesi sekmeye gelmez; histogram isteği eski imza.
    expect(document.querySelector('[data-testid="logs-load-status"]')).toBeNull();
    expect(m.tsArity).toBe(2);
    expect(m.tsCalls).toHaveLength(1);
    expect(m.tsCalls[0]).toMatchObject({ service: 'orders-api', groupBy: 'severity', from: 1759494780000000000 });

    expect(host.querySelector('[data-testid="logs-histogram"]'), 'histogram kartı').not.toBeNull();
    // v0.10.1822 — histogram bugünkü gibi: hata-oranı çizgisi sağ eksende;
    // liste isteği iptal sinyali taşır.
    const series = (m.core?.series ?? []) as { key: string; axis: string }[];
    expect(series.find(sr => sr.key === 'rate')?.axis).toBe('right');
    expect(m.logsSignal).toBeInstanceOf(AbortSignal);
    expect(host.textContent).toContain('payment declined for branch-teller');
    // v0.10.1830 — /logs görünüm seçenekleri sekmeye SIZMAZ: tablo bugünkü
    // sınıflarla (satır yüksekliği / yoğunluk sınıfı ve yerel data-density yok).
    const tbl = host.querySelector('table.logtbl-dense')!;
    expect(tbl.className).not.toMatch(/lt-rh-|lt-hh-/);
    expect(tbl.closest('[data-density]')).toBeNull();
    expect(host.querySelector('.lt-den-expanded')).toBeNull();
    expect(host.querySelector('[data-testid="compact-log-search"]'), 'kompakt Log Search (v0.10.1900)').not.toBeNull();
  });
});

// v0.10.1900 (Log Search Faz 3 dilim 3.10) — kompakt Log Search'ün ES bütçesi:
// AÇILIŞTA yalnız liste + histogram (eskisiyle aynı, yukarıdaki çivi). Alan
// listesi / alan istatistiği / çevre loglar / desenler / trace bundle açılışta
// YOK; satır detayı çekmecesi ek ES isteği atmaz; alan listesi yalnız kenar
// çubuğu açılınca; arama yazarken değil Ara/Enter ile gider. Trace önizlemesi
// ClickHouse'tan (ES değil) — satırlar geldikten sonra tek istek.
const ES_ON_DEMAND = ['logsFields', 'logsFieldStats', 'logsContext', 'logsPatterns', 'logsTemplates', 'traceBundle', 'logsFieldValues', 'logsDoc'];
describe('servis Logs sekmesi — kompakt Log Search ES bütçesi (v0.10.1900)', () => {
  const click = async (el: Element | null | undefined) => {
    expect(el).toBeTruthy();
    await act(async () => { (el as HTMLElement).click(); });
    await settle();
  };
  it('açılış: liste 1 + histogram 1; isteğe bağlı ES istekleri yok; önizleme tek (CH)', async () => {
    try { localStorage.clear(); } catch { /* özel pencere */ }
    await mountTab();
    expect(m.logsCalls).toHaveLength(1);
    expect(m.tsCalls).toHaveLength(1);
    expect(m.other.filter(k => ES_ON_DEMAND.includes(k))).toEqual([]);
    expect(m.other.filter(k => k === 'logsTracePreview')).toHaveLength(1);
    // satır → detay çekmecesi: yeni ES isteği yok
    await click(host!.querySelector('tbody tr'));
    expect(document.body.textContent).toContain('payment declined for branch-teller');
    expect(m.logsCalls).toHaveLength(1);
    expect(m.other.filter(k => ES_ON_DEMAND.includes(k))).toEqual([]);
  });
  it('alan kenar çubuğu varsayılan KAPALI; açılınca alan listesi tek istek', async () => {
    try { localStorage.clear(); } catch { /* özel pencere */ }
    await mountTab();
    expect(m.other).not.toContain('logsFields');
    const rail = [...host!.querySelectorAll('button')].find(b => b.textContent?.includes('Alanlar'));
    await click(rail);
    expect(m.other.filter(k => k === 'logsFields')).toHaveLength(1);
  });
  it('arama Ara ile gider (yazarken değil); "Logs\'ta aç" tüm durumu taşır', async () => {
    await mountTab();
    const input = host!.querySelector('.cls-q input') as HTMLInputElement;
    const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!;
    await act(async () => { setter.call(input, 'timeout'); input.dispatchEvent(new Event('input', { bubbles: true })); });
    await settle();
    expect(m.logsCalls).toHaveLength(1);
    await click([...host!.querySelectorAll('button')].find(b => b.textContent === 'Ara'));
    expect(m.logsCalls).toHaveLength(2);
    expect(m.logsCalls[1]).toMatchObject({ service: 'orders-api', search: 'timeout', limit: 200 });
    const link = [...host!.querySelectorAll('a')].find(a => a.textContent?.includes("Logs'ta aç"))!;
    const p = new URLSearchParams(link.getAttribute('href')!.split('?')[1]);
    expect(p.get('service')).toBe('orders-api');
    expect(p.get('q')).toBe('timeout');
    expect(p.get('range')).toBeTruthy();
  });
});
