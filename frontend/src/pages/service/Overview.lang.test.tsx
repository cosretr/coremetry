// @vitest-environment jsdom
//
// v0.10.2315 — pin: arayüz dili İngilizceyken servis genel bakışı (karolar,
// kaynak / kapsam rozetleri, RED kaynağı seçici, eksik / aile rozetleri, grafik
// başlık ve boş-durum metinleri, anomali başlığı, Top endpoints, Top DB,
// metrik throughput notu) bilinen Türkçe metin ÇİZMEZ (metin + title /
// aria-label); Türkçe arayüzde metin aynen. Tablo başlık menüsü (DataTable
// HeadMenu, ortak) bu dilimde değil: muaf.
import { describe, it, expect, afterEach, vi } from 'vitest';
import { act } from 'react';
import { createRoot } from 'react-dom/client';
import { MemoryRouter } from 'react-router';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { ReactNode } from 'react';
import { windowQueries } from '@/lib/httpMetrics';
import type { SpanMetricSeries, ServiceMetricThroughput } from '@/lib/types';

const FROM = 1_791_000_000 * 1e9;
const TO = FROM + 3600 * 1e9;
const SVC = 'svc-a';
const m = vi.hoisted(() => ({ lang: 'en' as 'en' | 'tr', c: {} as Record<string, unknown> }));
vi.mock('@/lib/i18n', async (orig) => ({ ...(await orig<typeof import('@/lib/i18n')>()), useLang: () => m.lang }));
const ser = (vals: number[]): SpanMetricSeries[] => [{ groupKey: [], points: vals.map((value, i) => ({ time: FROM + (i + 1) * 60e9, value })) }];

vi.mock('@/lib/api', async (orig) => {
  const actual = await orig<typeof import('@/lib/api')>();
  const W = windowQueries({ app: 'svc-a' }, 3600);
  const winVal: Record<string, number> = { [W.avg]: 0.123, [W.p50]: 0.04, [W.p95]: 0.3, [W.p99]: 0.456 };
  const c = () => m.c;
  return {
    ...actual,
    api: {
      serviceRedFamily: async () => c().fam ?? ({ service: 'svc-a', state: 'known', primary: 'http', http: { id: 'http', kind: 'http' }, tried: [] }),
      serviceMetricRED: async () => {
        if (c().redErr) throw new Error('HTTP 500: red');
        return {
          service: 'svc-a', source: 'vm', metricExists: c().noMetric ? false : true, latencyUnitKnown: !c().noUnit, latencyUnit: 's',
          stepSeconds: 60, note: 'srv-note', errorsUnknown: !!c().errUnknown, statusState: c().statusState,
          envAmbiguous: !!c().envAmb, envScope: c().envScope,
          series: { rate: ser([5, 5, 5]), error_rate: ser([1, 1, 1]), p50: ser([20, 20, 20]), p95: ser([80, 80, 80]), p99: ser([700, 800, 900]), avg: ser([30, 40, 50]) },
        };
      },
      serviceMetricThroughput: async () => {
        if (c().tputErr) throw new Error('HTTP 502: tput');
        return {
          metric: 'http_server_request_duration_seconds_count', rtMetric: 'http_server_request_duration_seconds',
          metricUnit: c().badUnit ? 'zz' : 's', matchedBy: 'service_name', metricExists: true, jobLabel: 'job', pattern: 'p',
          series: c().tputEmpty ? [] : [{ groupKey: ['/a'], points: ser([5, 5, 5])[0].points }],
        };
      },
      metricQueryFull: async () => (c().rtEmpty ? { series: [] } : { series: ser([0.03, 0.04, 0.05]) }),
      spanMetricBatch: async () => (c().spanEmpty ? { stepSeconds: 60, series: {} } : { stepSeconds: 60, series: { p99: ser([600, 700, 777]), p50: ser([10, 10, 10]), rate: ser([1, 1, 1]), error_rate: ser([0, 0, 0]) } }),
      httpMetricsPromql: async (p: { query: string }) => {
        if (p.query in winVal) {
          if (c().promqlFails) throw new Error('HTTP 502: vm');
          return ser([winVal[p.query]]);
        }
        if (c().totalReq && /increase|sum/.test(p.query)) return ser([123456]);
        return [];
      },
      serviceDBQueries: async () => [],
      anomalyEvent: async () => null,
    },
  };
});
vi.mock('@/lib/queries', () => ({
  useServiceDeploys: () => ({ data: [] }),
  useAnomalyEvents: () => ({ data: { items: m.c.anoms ? [{ id: 'a1', service: 'svc-a', startedAt: FROM + 60e9, lastSeen: FROM + 120e9, kind: 'latency', severity: 'warning', peakRatio: 4.2, baselineKind: 'seasonal' }] : [] } }),
  useAnomalySilences: () => ({ data: [] }),
  useCreateAnomalySilence: () => ({ mutateAsync: async () => undefined }),
  usePutAnomalyVerdict: () => ({ mutateAsync: async () => undefined }),
}));
vi.mock('@/components/AuthProvider', () => ({ useAuth: () => ({ user: { role: 'viewer' } }) }));
vi.mock('@/components/MetricPanel', () => ({
  MetricPanel: ({ children, title }: { title: string; children: ReactNode | ((d: object) => ReactNode) }) =>
    <div data-mp={title}>{typeof children === 'function' ? children({}) : children}</div>,
}));
vi.mock('@/components/chart/corePanelEntry', () => ({
  CorePanelMulti: (p: Record<string, unknown>) => <pre data-core>{JSON.stringify({ rg: p.regions, t: p.title, n: p.note, e: p.error, er: p.emptyReason, eh: p.emptyHint, q: p.queryText, items: ((p.items as { name: string }[]) ?? []).map(i => i.name) })}</pre>,
}));
vi.mock('@/components/ServiceNeighbors', () => ({ ServiceNeighbors: () => null }));
vi.mock('@/features/anomalies/AnomalyWindowTable', () => ({ AnomalyWindowTable: () => null }));

import { ServiceOverview } from '@/pages/service/Overview';
import { MetricThroughputNote } from '@/pages/service/MetricThroughputNote';
(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const EP = [{ path: 'GET /a', calls: 100, errorRate: 2, p50Ms: 10, p99Ms: 90, avgMs: 20 }, { path: 'GET /b', calls: 10, errorRate: 0, p99Ms: 9, avgMs: 2 }];
const STATES: [string, Record<string, unknown>, string][] = [
  ['&src=metric', { anoms: true }, ''],
  ['&src=metric', { promqlFails: true }, ''],
  ['&src=span', {}, ''],
  ['&src=span', { spanEmpty: true }, ''],
  ['', { noMetric: true }, ''],
  ['&src=metric', { noMetric: true }, ''],
  ['&src=metric', { errUnknown: true, statusState: 'unknown', noUnit: true, envAmb: true, envScope: { failed: true }, totalReq: true }, 'prod'],
  ['&src=metric', { errUnknown: true, envAmb: true, envScope: { applied: false } }, 'prod'],
  ['&src=metric', { envAmb: true }, ''],
  ['&src=metric&compare=1d', { redErr: true }, ''],
  ['&src=metric', { tputErr: true, rtEmpty: true, badUnit: true }, ''],
  ['&src=metric', { tputEmpty: true, rtEmpty: true }, ''],
  ['&src=metric', { fam: { service: 'svc-a', state: 'known', primary: 'grpc', grpc: { id: 'grpc', kind: 'grpc' }, tried: [] } }, ''],
  ['&src=metric', { fam: { service: 'svc-a', state: 'known', primary: 'http', http: { id: 'http', kind: 'http' }, grpc: { id: 'grpc', kind: 'grpc' }, tried: [] } }, ''],
  ['&src=metric', { fam: { service: 'svc-a', state: 'unknown', tried: [] } }, ''],
  ['&src=metric&bands=1', { anoms: true }, 'prod'],
];
const base = (o: Partial<ServiceMetricThroughput> = {}): ServiceMetricThroughput => ({ service: 'svc-a', metric: 'm.x', jobLabel: 'job', pattern: '^x$', metricExists: true, matched: 0, ...o } as ServiceMetricThroughput);
const NOTES: ServiceMetricThroughput[] = [
  base({ unsupportedInstrument: true, instrument: 'gauge', source: 'vm' } as Partial<ServiceMetricThroughput>),
  base({ metricExists: false, tried: ['a', 'b'], suggestions: ['c'], source: 'vm' } as Partial<ServiceMetricThroughput>),
  base({ instrument: 'sum', triedLabels: ['job', 'svc'], candidateErrors: ['e1'], presentKeysState: 'unknown', presentKeysReason: 'r', presentKeys: ['k1'], sampleJobs: ['j1'], sampleServices: ['s1'] } as Partial<ServiceMetricThroughput>),
  base({ presentKeys: ['k1', 'k2'], presentKeysScope: 'fleet' } as Partial<ServiceMetricThroughput>),
  base({ presentKeys: [], presentKeysScope: 'fleet' } as Partial<ServiceMetricThroughput>),
  base({}),
];

async function all(): Promise<string> {
  const out: string[] = [];
  const draw = async (node: ReactNode) => {
    const host = document.createElement('div');
    document.body.appendChild(host);
    const root = createRoot(host);
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false, retryDelay: 0 } } });
    await act(async () => { root.render(<QueryClientProvider client={qc}>{node}</QueryClientProvider>); });
    for (let i = 0; i < 8; i++) await act(async () => { await new Promise(r => setTimeout(r, 10)); });
    const attrs = [...host.querySelectorAll('[title],[aria-label]')].flatMap(e => ['title', 'aria-label'].map(a => e.getAttribute(a) ?? ''));
    out.push(host.textContent ?? '', ...attrs);
    act(() => { root.unmount(); });
    host.remove();
  };
  for (const [q, c, env] of STATES) {
    m.c = c;
    try { localStorage.clear(); } catch { /* jsdom */ }
    await draw(<MemoryRouter initialEntries={[`/service?name=${SVC}${q}`]}>
      <ServiceOverview service={SVC} range={{ preset: '1h' }} windowNs={{ from: FROM, to: TO }} info={null} operations={[]}
        endpoints={c.anoms ? EP as never : []} env={env} />
    </MemoryRouter>);
  }
  for (const d of NOTES) await draw(<MetricThroughputNote d={d} />);
  return out.join('\n');
}

afterEach(() => { m.lang = 'en'; m.c = {}; });

const TR_CHARS = /[çğıöşüÇĞİÖŞÜ]/;
const EXEMPT = /Tablo seçenekleri/;

describe('Servis genel bakış — İngilizce arayüz (v0.10.2315)', () => {
  it('Türkçe metin yok', async () => {
    const text = await all();
    expect(text.split('\n').filter(l => TR_CHARS.test(l) && !EXEMPT.test(l))).toEqual([]);
    for (const en of [
      'Golden signals', 'source: metric (vm)', 'scope: entry spans', 'scope: all spans', 'Entry points', 'Context',
      'Response time · avg · last step', 'P99 (metric · last step) · 900 ms', 'window query failed: HTTP 502: vm', 'P99 (metric) · 456 ms',
      'tiles show the window value', 'RED source', 'Source: span', 'Source: metric',
      'metric not found — showing spans', 'missing: failure rate (status query failed), P50/P95/P99 (unit unknown), env narrowing (could not be resolved)',
      'env narrowing (this environment is not in the metric)', 'Total requests', 'Total requests in the selected window: ',
      'metric RED could not be read', 'gRPC too → HTTP/GRPC', 'The service writes both an HTTP and a gRPC server histogram',
      'metric family unknown', 'Metric throughput could not be read.',
      'No data for this filter', 'unit not recognized', 'Throughput · metric', 'Failure rate · metric',
      '1 anomaly · bands on charts: off · ', 'turn on', ' · row → drawer', 'Anomalies · this window', 'Show/hide anomaly bands',
      'Time share', 'entry spans', 'total time in the window ≈ ', 'No db.statement spans for svc-a in this window',
      'Throughput cannot be derived from this metric.', 'Rate is meaningful only for counters', 'searched in VictoriaMetrics',
      'The metric is not in this installation.', 'Names tried:', ' parameter to the endpoint.', 'Similar names in the catalog',
      'The metric exists but no series matches this service.', '· pattern ', 'Identity labels tried:', 'Some candidates returned an ERROR',
      'Key presence COULD NOT BE DETERMINED (r) — this does NOT mean the label is missing.', 'Keys seen:',
      'Keys that EXIST in this metric (fleet-wide):', 'The key exists but nothing matches',
      'None of the identity keys tried exist in this metric (fleet-wide) — the collector sends the service identity in another field.',
      'Values of job found in the installation', 'The metric carries no job label at all',
    ]) expect(text, en).toContain(en);
  }, 30000);

  it('Türkçe arayüzde bilinen metin aynen', async () => {
    m.lang = 'tr';
    const text = await all();
    for (const tr of [
      'Altın sinyaller', 'kaynak: metrik (vm)', 'kapsam: giriş span\u2019leri', 'kapsam: tüm span\u2019ler', 'Giriş noktaları', 'Bağlam',
      'Response time · avg · son adım', 'P99 (metrik · son adım) · 900 ms', 'pencere sorgusu başarısız: HTTP 502: vm', 'P99 (metrik) · 456 ms',
      'karolar pencere değeri', 'RED kaynağı', 'Kaynak: span', 'Kaynak: metrik',
      'metrik bulunamadı — span gösteriliyor', 'eksik: failure rate (durum sorgusu başarısız), P50/P95/P99 (birim bilinmiyor), env daraltması (çözülemedi)',
      'env daraltması (metrikte bu ortam yok)', 'Toplam istek', 'Seçili pencerede toplam istek: ',
      'metrik RED okunamadı', 'gRPC de var → HTTP/GRPC', 'metrik ailesi bilinemedi', 'Metrik throughput okunamadı.',
      'Bu filtrede veri yok', 'birim tanınmadı', 'Throughput · metrik', 'Failure rate · metrik',
      '1 anomali · bantlar grafiklerde: kapalı · ', ' · satır → çekmece', 'Anomaliler · bu pencere',
      'Süre payı', "giriş span'leri", 'Bu pencerede svc-a için db.statement span\'i yok',
      'Bu metrikten throughput türetilemiyor.', 'Metrik bu kurulumda yok.', 'Denenen adlar:', ' parametresi geçilebilir.',
      'Metrik var ama bu servise eşleşen seri yok.', '· desen ', 'Denenen kimlik etiketleri:', 'Bazı adaylar HATA verdi',
      'Anahtar varlığı BİLİNEMEDİ (r) — etiket yok DEĞİL.', 'Bu metrikte VAR OLAN anahtarlar (filo geneli):',
      'Denenen kimlik anahtarlarının hiçbiri bu metrikte yok (filo geneli) — collector servis kimliğini başka bir alanda gönderiyor.',
      'Kurulumda bulunan job değerleri ve bunlardan çözülen servis adları:', 'Metrik hiç job etiketi taşımıyor',
    ]) expect(text, tr).toContain(tr);
  }, 30000);
});
