// @vitest-environment jsdom
//
// v0.10.2314 — pin: arayüz dili İngilizceyken /traces sayfası (şerit, filtre
// ipuçları, liste / toplu / şekil durum satırları, sayım ve kimlik rozetleri,
// boş-sonuç teşhisleri) bilinen Türkçe metin ÇİZMEZ (metin + title /
// aria-label / placeholder); Türkçe arayüzde metin aynen. Ortak bileşenler
// (Pager, FilterQueryBox, tablo başlık menüsü, dil düğmesi) bu dilimde değil:
// muaf listesinde.
import '@/test/pageJsdom';
import { describe, it, expect, afterEach, vi } from 'vitest';
import { createRoot } from 'react-dom/client';
import { act } from 'react';
import { BrowserRouter, Routes, Route } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ConfirmProvider } from '@/components/ui/ConfirmDialog';
import { settleUntil } from '@/test/actWait';
import Traces from './Traces';

const m = vi.hoisted(() => ({ lang: 'en' as 'en' | 'tr', cfg: {} as Record<string, unknown> }));
vi.mock('@/lib/i18n', async (orig) => ({ ...(await orig<typeof import('@/lib/i18n')>()), useLang: () => m.lang }));
vi.mock('@/lib/api', () => {
  const rows = (n: number) => Array.from({ length: n }, (_, i) => ({
    traceId: String(i + 1).padStart(32, '0'), serviceName: 'svc-a', rootName: `GET /x/${i}`,
    startTime: 1.7e18 - i * 1e9, durationMs: 12 + i, spanCount: 4 + i, hasError: i === 2,
  }));
  const c = () => m.cfg;
  const stub: Record<string, (...a: unknown[]) => Promise<unknown>> = {
    traces: () => (c().listErr ? Promise.reject(new Error('boom'))
      : Promise.resolve({ traces: rows((c().n as number) ?? 6), hasMore: c().hasMore ?? true, ...((c().extra as object) ?? {}) })),
    tracesExtras: () => Promise.resolve({ extras: {} }),
    spanMetricBatch: () => Promise.resolve({ series: { count: [], errors: [], rt: [] } }),
    attributeKeys: () => Promise.resolve([{ key: 'http.route' }, { key: 'User.Id' }]),
    tracesCount: () => Promise.resolve(c().count ?? { value: 10000, atLeast: true }),
    tracesAggregate: () => (c().aggErr ? Promise.reject(new Error('timeout exceeded')) : Promise.resolve([])),
    getTraceRootDef: () => Promise.resolve({ def: 'entry' }),
    servicesPage: () => Promise.resolve({ services: [{ name: 'svc-a', spanCount: 50 }] }),
    savedViews: () => Promise.resolve([]),
    serviceNames: () => Promise.resolve({ names: [], total: 0 }),
    operationNames: () => Promise.resolve({ names: [], total: 0 }),
    putPreference: () => Promise.resolve({}),
    getPreference: () => Promise.resolve(null),
  };
  return { api: new Proxy({}, { get: (_t, k: string) => stub[k] ?? (() => Promise.resolve({})) }), isCanceled: () => false };
});
vi.mock('@/components/AuthProvider', () => ({ useAuth: () => ({ user: { username: 'op', role: 'admin' }, loading: false }) }));
(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const STATES: [string, Record<string, unknown>][] = [
  ['/traces?range=1h&service=svc-a&hasError=true', { extra: { mvGap: true, identity: { hits: 3, matchedKey: 'user.id', keys: ['user.id'], bounded: true } } }],
  ['/traces?range=1h', { count: { reason: 'duration-filter' }, extra: { emptyDiag: { promotedFallback: true, promotedKeys: ['k'], promotedHosts: [{ host: 'h1', col: 0, arr: 5 }] } } }],
  ['/traces?range=1h', { listErr: true }],
  ['/traces?range=1h&service=svc-a&search=abc', { n: 0, hasMore: false, extra: { emptyDiag: { matchingSpans: 0, promotedKeys: ['k'], promotedHosts: [] }, identity: { hits: 0, keys: ['a'], skipped: ['z'] } } }],
  ['/traces?range=1h&search=0af7651916cd43dd8448eb211c80319c', { n: 0, hasMore: false, extra: { identity: { traceId: true, hits: 0, keys: [] } } }],
  ['/traces?range=1h&view=aggregate&groupBy=attr&groupAttr=user.id', {}],
  ['/traces?range=1h&view=aggregate', {}],
  ['/traces?range=1h&view=aggregate', { aggErr: true }],
  ['/traces?range=1h&view=shapes', { n: 0, hasMore: false }],
];

async function all(): Promise<string> {
  const out: string[] = [];
  for (const [url, cfg] of STATES) {
    m.cfg = cfg;
    window.history.replaceState(null, '', url);
    const host = document.createElement('div');
    document.body.appendChild(host);
    const root = createRoot(host);
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    await act(async () => {
      root.render(<QueryClientProvider client={qc}><BrowserRouter><ConfirmProvider>
        <Routes><Route path="/traces" element={<Traces />} /></Routes>
      </ConfirmProvider></BrowserRouter></QueryClientProvider>);
    });
    await settleUntil(() => qc.isFetching() === 0, 'trace sorguları yerleşti');
    await act(async () => { await new Promise(r => setTimeout(r, 30)); });
    const attrs = [...document.body.querySelectorAll('[title],[aria-label],[placeholder]')]
      .flatMap(e => ['title', 'aria-label', 'placeholder'].map(a => e.getAttribute(a) ?? ''));
    out.push(document.body.textContent ?? '', ...attrs);
    act(() => { root.unmount(); });
    host.remove();
    document.body.innerHTML = '';
  }
  return out.join('\n');
}

afterEach(() => { m.lang = 'en'; m.cfg = {}; });

const TR_CHARS = /[çğıöşüÇĞİÖŞÜ]/;
// Bu dilimin dışındaki ortak bileşenler (Pager, FilterQueryBox, DataTable başlık menüsü, dil düğmesi).
const EXEMPT = /Türkçe'ye geç|Tablo seçenekleri|Hızlı:|vazgeç|Son sayfa|Sondan bir sonraki/;

describe('Traces sayfası — İngilizce arayüz (v0.10.2314)', () => {
  it('Türkçe metin yok', async () => {
    const text = await all();
    expect(text.split('\n').filter(l => TR_CHARS.test(l) && !EXEMPT.test(l))).toEqual([]);
    for (const en of [
      'Collapse chart', 'Chart type', 'Response time statistic', 'Trace view',
      'Failed traces only. SERVER filter', 'Root definition: entry root (full root or server/consumer entry span)',
      `Counting stopped at ${(10000).toLocaleString()} traces — the real number is larger.`, 'diagnosis', '· MV gap: raw path',
      'identity: user.id', ' (capped). Keys tried: user.id',
      'Promoted column mismatch:', ' filter found 0 spans on the column path, on the array path',
      ' · total cannot be counted', 'The total cannot be counted cheaply while the duration filter',
      'The trace query failed or timed out', 'No traces found', 'Identity search: ', ' tried for equality on these keys — ',
      'no match; the substring search came back empty too', ' Spellings that could not be compiled: ',
      'No anchor (the value carries no yyyyMMddHHmmss)', 'Promoted column probe (', 'no host returned rows',
      'Diagnosis (explain) →', 'was not found in the trace summary (trace_summary_5m) of the last 90 days',
      'is not present on any span in this window.', 'There is a key that differs only in letter case:', '— attribute keys are case-sensitive.',
      'No groups in this window — the aggregated view needs at least one trace',
      'The aggregate query timed out', 'No shapes in this window', 'm bucket · drag = select time',
    ]) expect(text, en).toContain(en);
  }, 30000);

  it('Türkçe arayüzde bilinen metin aynen', async () => {
    m.lang = 'tr';
    const text = await all();
    for (const tr of [
      'Grafiği katla', 'Grafik türü', 'Yanıt süresi istatistiği', 'Trace görünümü',
      "Yalnız hatalı trace'ler. SUNUCU filtresi", "Kök tanımı: giriş kökü (tam kök ya da server/consumer giriş span'i)",
      `Sayım ${(10000).toLocaleString()} trace'te durduruldu — gerçek sayı daha büyük.`, 'teşhis', '· MV boşluğu: ham yol',
      'kimlik: user.id', ' (tavan). Denenen anahtarlar: user.id',
      'Terfi kolonu uyuşmazlığı:', ' filtresi kolon yolunda 0 span buldu, dizi yolunda',
      ' · toplam sayılamıyor', 'Süre filtresi (min/max ms) açıkken toplam ucuza sayılamıyor',
      'Trace sorgusu hata verdi ya da zaman aşımına uğradı', 'Trace bulunamadı', 'Kimlik araması: ', ' şu anahtarlarda eşitlikle denendi — ',
      'eşleşme yok; alt-dize araması da boş döndü', ' Derlenemeyen yazımlar: ',
      'Çapa yok (değer yyyyMMddHHmmss taşımıyor)', 'Terfi kolonu probu (', 'hiçbir host satır döndürmedi',
      'Teşhis (explain) →', 'son 90 günün trace özetinde (trace_summary_5m) bulunamadı',
      "bu pencerede hiçbir span'de yok.", 'Yalnız harf düzeni farklı olan bir anahtar var:', '— attribute anahtarları harf duyarlıdır.',
      'Bu pencerede grup yok — toplu görünüm gruplayacak en az bir trace ister',
      'Toplu sorgu zaman aşımına uğradı', 'Bu pencerede şekil yok', 'm bucket · sürükle = zaman seç',
    ]) expect(text, tr).toContain(tr);
  }, 30000);
});
