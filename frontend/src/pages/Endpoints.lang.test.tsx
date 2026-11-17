// @vitest-environment jsdom
//
// v0.10.2305 — pin: arayüz dili İngilizceyken Endpoints listesi (span +
// metrik kipi), endpoint detayı (metrik kipi) ve servis HTTP metrikleri
// sekmesi bilinen Türkçe metin ÇİZMEZ (metin + title / aria-label /
// placeholder); Türkçe arayüzde metin aynen. Saf yardımcılar (httpMetrics
// biçimleri, redFamily / ortam bandı notları, endpointHonesty) lang
// verilmezse Türkçe.
import '@/test/pageJsdom'; // statik sayfa import'undan ÖNCE (matchMedia modül kapsamında)
import { describe, it, expect, afterEach, vi } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act, type ReactNode } from 'react';
import { BrowserRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ConfirmProvider } from '@/components/ui/ConfirmDialog';
import { removeRaw, STORAGE_KEYS } from '@/lib/storage';
import type { EndpointRow, SpanMetricSeries } from '@/lib/types';
import EndpointsPage from './Endpoints';
import EndpointDetailPage from './EndpointDetail';
import { ServiceHttpMetricsTab } from './service/ServiceHttpMetricsTab';
import { fmtCount, fmtRatio, fmtShare, methodRouteLegend, barsOf, httpQuantileNote, HTTP_QUANTILE_NOTE } from '@/lib/httpMetrics';
import { httpEnvScopeNote } from '@/lib/httpEnvScopeNote';
import { familyUnknownNote, type RedFamilyView } from '@/lib/redFamily';
import { endpointsSourceNote, LIST_NEW_LABEL } from '@/lib/endpointHonesty';

const m = vi.hoisted(() => ({ lang: 'en' as 'en' | 'tr', spanRows: [] as unknown[] }));
const ser = (labels: Record<string, string>, v: number): SpanMetricSeries => ({
  groupKey: Object.entries(labels).map(([k, x]) => `${k}="${x}"`),
  points: [{ time: 1e9, value: v }],
});

vi.mock('@/lib/i18n', async (orig) => ({ ...(await orig<typeof import('@/lib/i18n')>()), useLang: () => m.lang }));
vi.mock('@/lib/api', () => {
  const MR = (method: string, extra: Record<string, string> = {}) => ({ http_request_method: method, http_route: '/orders', ...extra });
  const answer = (q: string): SpanMetricSeries[] => {
    const two = (g: number, p: number) => [ser(MR('GET'), g), ser(MR('POST'), p)];
    if (q.startsWith('topk(')) return two(1200, 300);
    if (q.includes('label_replace(')) return [ser(MR('GET', { cls: '2xx' }), 1188), ser(MR('GET', { cls: '5xx' }), 12)];
    if (q.startsWith('sum by (http_response_status_code)')) return [ser({ http_response_status_code: '200' }, 1188), ser({ http_response_status_code: '' }, 4)];
    if (q.includes('histogram_quantile')) return two(0.2, 0.5);
    if (q.includes('_sum{')) return two(0.05, 0.3);
    if (q.includes('=~"5.."')) return two(12, 30);
    if (q.startsWith('sum by (http_request_method, http_route)')) return [ser({ http_request_method: 'GET' }, 3), ...two(5, 2)];
    return two(1200, 300);
  };
  const stub: Record<string, (...a: unknown[]) => Promise<unknown>> = {
    endpoints: () => Promise.resolve({ rows: m.spanRows, pool: 1, poolCapped: m.spanRows.length > 0 }),
    httpMetricsPromql: (p: unknown) => Promise.resolve(answer((p as { query: string }).query)),
    savedViews: () => Promise.resolve([]),
    serviceNames: () => Promise.resolve({ names: [], total: 0 }),
    clusters: () => Promise.resolve(['cluster-1']),
    getPreference: (k: unknown) => Promise.resolve({ key: k, model: null }),
    endpointDetail: () => Promise.resolve(null),
    serviceRedFamily: (svc: unknown) => Promise.resolve({ service: svc, state: 'known', primary: 'http', http: { id: 'http', kind: 'http' }, tried: [] }),
  };
  return {
    api: new Proxy({}, { get: (_t, k: string) => stub[k] ?? (() => Promise.resolve(null)) }),
    isCanceled: () => false,
  };
});
vi.mock('@/components/AuthProvider', () => ({
  useAuth: () => ({ user: { username: 'op', role: 'editor' }, loading: false }),
}));
vi.mock('@/components/MultiLineChart', () => ({
  MultiLineChart: ({ series }: { series: SpanMetricSeries[] }) => <div data-testid="mlc">{series.map(s => s.groupKey[0]).join('|')}</div>,
}));

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
class NoopResizeObserver { observe() {} unobserve() {} disconnect() {} }
(globalThis as unknown as { ResizeObserver: unknown }).ResizeObserver = NoopResizeObserver;

const SPAN_ROW: EndpointRow = { service: 'svc-a', path: '/orders', calls: 120, errors: 3, errorRate: 2.5, avgMs: 40, p99Ms: 300, p50Ms: 20, p95Ms: 200, reqPerMin: 2 };

let root: Root | null = null;
let host: HTMLDivElement | null = null;
const settle = () => act(async () => { await new Promise(r => setTimeout(r, 30)); });
async function mount(url: string, node: ReactNode) {
  window.history.replaceState(null, '', url);
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => {
    root!.render(<QueryClientProvider client={qc}><BrowserRouter><ConfirmProvider>{node}</ConfirmProvider></BrowserRouter></QueryClientProvider>);
  });
  await settle(); await settle(); await settle();
}
function close() {
  act(() => { root?.unmount(); });
  host?.remove(); root = null; host = null;
  document.body.innerHTML = '';
  removeRaw(STORAGE_KEYS.env);
}
function snap(): string {
  const c = document.body.cloneNode(true) as HTMLElement;
  c.querySelectorAll('[lang="tr"]').forEach(n => n.remove());
  const attrs = [...c.querySelectorAll('[title],[aria-label],[placeholder]')]
    .flatMap(e => ['title', 'aria-label', 'placeholder'].map(a => e.getAttribute(a) ?? ''));
  return [c.textContent ?? '', ...attrs].join('\n');
}

async function all(): Promise<{ text: string; html: string }> {
  const out: string[] = []; const html: string[] = [];
  const take = () => { out.push(snap()); html.push(document.body.innerHTML); close(); };
  m.spanRows = [];
  await mount('/endpoints?src=metric', <EndpointsPage />); take();
  await mount('/endpoints?src=metric&service=svc-a', <EndpointsPage />); take();
  await mount('/endpoints', <EndpointsPage />); take();
  await mount('/endpoints?entry=rpc&cluster=cluster-1', <EndpointsPage />); take();
  m.spanRows = [SPAN_ROW];
  await mount('/endpoints?cluster=cluster-1&compare=1&sort=p99Delta', <EndpointsPage />); take();
  await mount('/endpoint?service=svc-a&path=%2Forders&range=3h', <EndpointDetailPage />); take();
  await mount('/service?range=3h', <ServiceHttpMetricsTab service="svc-a" range={{ preset: '3h' } as never} />); take();
  return { text: out.join('\n'), html: html.join('\n') };
}

afterEach(() => { close(); m.lang = 'en'; });

const TR_CHARS = /[çğıöşüÇĞİÖŞÜ]/;
const TR_WORDS = /\b(Kaynak|metrik|yok|okunamadı|pencere|süzgeç|Tümü|Trafik|Hatalar|Ortalama|bazında|gecikme|seçin|satır|çağrı|hata|listede|yeni|Rastgele)\b/;
// Paylaşılan bileşenlerin kendi etiketleri bu dilimin kapsamı dışında (raporlandı).
const SHARED = ['Tablo seçenekleri', 'Bu bölümün okuduğu kaynak', "Türkçe'ye geç"];

describe('Endpoints / endpoint detayı / HTTP metrikleri — İngilizce arayüz (v0.10.2305)', () => {
  it('Türkçe metin yok', async () => {
    const r = await all();
    const bad = r.text.split('\n').filter(l => !SHARED.includes(l) && (TR_CHARS.test(l) || TR_WORDS.test(l)));
    expect(bad).toEqual([]);
    for (const en of ['Pick a service in metric mode', 'Source: span', 'Source: metric', 'Data source',
      'Total of the 2 listed rows: ', ' calls · ', 'The top 100 rows by calls.', 'No endpoints in this window',
      'Overview', 'Total requests', 'Traffic', 'Request Rate (by Route)', 'Request distribution (Route, top 10)',
      'Status code distribution', 'Latency percentiles (p50 / p90 / p95 / p99)', 'P95 latency (route)', '5xx errors (by Route)',
      'Endpoint details', 'selected time range', 'Downstream dependencies (svc-a →)', 'Connection errors', 'Error types (error_type)',
      'Source: VictoriaMetrics · OpenTelemetry HTTP semconv metrics', 'Percentiles are computed from histogram buckets',
      'Latency percentile', 'Source of the RED strip', 'Source: RAW spans', 'The ranking pool is limited to',
      'Compare every number with the preceding window']) expect(r.text, en).toContain(en);
  });

  it('saf yardımcılar: EN biçim / metin; lang verilmezse Türkçe aynen', () => {
    expect([fmtRatio(0.1234), fmtRatio(0.1234, 'en')]).toEqual(['%12.34', '12.34%']);
    expect([fmtShare(0.33), fmtShare(0.33, 'en'), fmtShare(0.001, 'en'), fmtShare(0)]).toEqual(['%0,33', '0.33%', '<0.01%', '%0']);
    expect([fmtCount(1234.5, 1), fmtCount(1234.5, 1, 'en')]).toEqual(['1.234,5', '1,234.5']);
    expect([methodRouteLegend({ http_request_method: 'GET' }), methodRouteLegend({ http_request_method: 'GET' }, false, undefined, 'en')]).toEqual(['GET (route yok)', 'GET (no route)']);
    expect(barsOf([ser({ x: '' }, 1)], 'x')[0].label).toBe('(boş)');
    expect(HTTP_QUANTILE_NOTE).toBe(httpQuantileNote());
    expect(LIST_NEW_LABEL).toBe('listede yeni');
    expect(endpointsSourceNote('c', 'e')).toBe("Kaynak: HAM spans — env + cluster filtresi spanmetrics rollup'ında olmayan bir boyut, okuma ham span'lere düşüyor. Bu liste MV yolunun birebir aynısı DEĞİL: rotasız client span'leri (url.path / http.target) buraya dahil olur.");
    expect(endpointsSourceNote('c', '', 'en')).toContain('Source: RAW spans — the cluster filter');
    const fam = { state: 'unknown' } as RedFamilyView;
    expect(familyUnknownNote(fam)).toBe('Metrik ailesi BİLİNEMEDİ (okunamadı) — HTTP sorguları koşuyor; gRPC yok DEĞİL');
    expect(familyUnknownNote(fam, 'en')).toBe('Metric family COULD NOT BE DETERMINED (unreadable) — HTTP queries are running; that does NOT mean there is no gRPC');
    const un = { state: 'unresolved', scope: { label: 'env', source: 'configured', terms: [{ role: 'env', want: 'env-1', label: 'env', tier: 'none', values: [] }] } } as never;
    expect(httpEnvScopeNote(un)!.text).toBe('Ortam süzgeci uygulanamadı (metrikte bu ortam yok: env değerleri arasında "env-1" eşleşmedi) — seriler TÜM ortamları kapsıyor');
    expect(httpEnvScopeNote(un, 'en')!.text).toBe('Environment filter could not be applied (this environment is not in the metric: "env-1" did not match among the env values) — series cover ALL environments');
    expect(httpEnvScopeNote(un)!.title.startsWith("Ortam etiketi env (Ayarlar → Metrik backend'i → Ortam); ")).toBe(true);
    expect(TR_CHARS.test(httpEnvScopeNote(un, 'en')!.title)).toBe(false);
  });
});

describe('Endpoints / endpoint detayı / HTTP metrikleri — Türkçe arayüzde metin aynen (v0.10.2305)', () => {
  it('bilinen Türkçe metin', async () => {
    m.lang = 'tr';
    const r = await all();
    for (const tr of ["Metrik kipinde servis seçin — liste, seçili servisin HTTP/GRPC sekmesiyle aynı PromQL'i okur", 'Kaynak: span', 'Kaynak: metrik',
      'Listelenen 2 satırın toplamı: ', ' çağrı · ', 'En çok çağrılan ilk 100 satır.', 'Bu pencerede endpoint yok',
      'Genel Bakış', 'Toplam İstek', 'Trafik', 'Request Rate (Route bazında)', 'İstek Dağılımı (Route, Top 10)', 'Status Code Dağılımı',
      'Latency Yüzdelikleri (p50 / p90 / p95 / p99)', 'P95 gecikme (route)', '5xx Hataları (Route bazında)', 'Endpoint Detayları',
      'seçili zaman aralığı', 'Downstream Bağımlılıkları (svc-a →)', 'Bağlantı Hataları', 'Hata Tipleri (error_type)',
      'Kaynak: VictoriaMetrics · OpenTelemetry HTTP semconv metrikleri', 'Yüzdelikler histogram kovalarından hesaplanır',
      'Gecikme yüzdeliği', 'RED şeridinin kaynağı', 'Kaynak: HAM spans', 'Sıralama havuzu',
    ]) expect(r.text, tr).toContain(tr);
  });
});
