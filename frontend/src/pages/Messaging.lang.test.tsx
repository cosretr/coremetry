// @vitest-environment jsdom
//
// v0.10.2306 — pin: arayüz dili İngilizceyken Messaging listesi (Kafka şeridi,
// Topics / Consumers görünümü, tavan şeridi, hata), topic sayfası (bağlantısız
// adres, Overview / Consumers / Traces sekmeleri, uçtan uca gecikme,
// operasyon ve span adı tabloları) ve servis Messaging sekmesi (panel
// başlıkları lib/kafkaMetrics'ten) bilinen Türkçe metin ÇİZMEZ (metin + title /
// aria-label / placeholder); Türkçe arayüzde metin aynen.
import '@/test/pageJsdom'; // statik sayfa import'undan ÖNCE (matchMedia modül kapsamında)
import { describe, it, expect, afterEach, vi } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act, type ReactNode } from 'react';
import { BrowserRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ConfirmProvider } from '@/components/ui/ConfirmDialog';
import MessagingPage from './Messaging';
import MessagingTopicPage from './MessagingTopic';
import { ServiceMessagingTab } from './service/ServiceMessagingTab';
import { kafkaPanels, kafkaSectionTitle, KAFKA_SECTIONS } from '@/lib/kafkaMetrics';
import { kafkaTopicPanels } from '@/lib/kafkaTopicMetrics';
import { opLabelTR, opMissingTitle, OP_MISSING_LABEL, OP_MISSING_TITLE } from '@/features/dependencies/msgOperations';

const m = vi.hoisted(() => ({ lang: 'en' as 'en' | 'tr', kafka: true, overviewFail: false }));
vi.mock('@/lib/i18n', async (orig) => ({ ...(await orig<typeof import('@/lib/i18n')>()), useLang: () => m.lang }));
vi.mock('@/lib/api', () => {
  const series = [{ groupKey: ['topic="topic-1"', 'service_name="svc-a"', 'client_id="c1"', 'host_name="pod-1"', 'partition="0"'], points: [{ time: 1, value: 12000 }, { time: 61, value: 12000 }] }];
  const caller = (role: string) => ({ service: 'svc-a', pod: 'pod-1', role, spanCount: 10, errorCount: 1, errorRate: 10, avgDurationMs: 4, p50DurationMs: 3, p95DurationMs: 5, p99DurationMs: 6 });
  const stub: Record<string, (...a: unknown[]) => Promise<unknown>> = {
    httpMetricsPromql: () => Promise.resolve(m.kafka ? series : []),
    messaging: () => (m.overviewFail ? Promise.reject(new Error('CH 500')) : Promise.resolve({
      rows: [{ system: 'kafka', cluster: 'cluster-1', destination: 'topic-1', spanCount: 100, errorCount: 2, errorRate: 2, avgDurationMs: 3, p99DurationMs: 9, callers: [] }],
      rowsCapped: true, rowLimit: 200,
    })),
    messagingDetail: () => Promise.resolve({
      system: 'kafka', cluster: 'cluster-1', destination: 'topic-1', spanCount: 100, errorCount: 2, errorRate: 2, avgDurationMs: 3, p99DurationMs: 9,
      callers: [caller('producer'), caller('consumer'), caller('internal')], topOps: [{ statement: '', count: 3, avgDurationMs: 2 }, { statement: 'topic-1 publish', count: 5, avgDurationMs: 1, operation: 'publish' }],
      operations: [{ operation: '', spanCount: 3, errorCount: 1, errorRate: 33, avgDurationMs: 1, p50DurationMs: 1, p95DurationMs: 2, p99DurationMs: 3 }],
      e2e: { count: 4, p50Ms: 5, p95Ms: 9, p99Ms: 12, series: [{ timeS: 1, count: 2, avgMs: 4 }], slowestLagMs: 12, slowestConsumerTraceId: 'abc', slowestProducerTraceId: 'def' },
    }),
    messagingClients: () => Promise.resolve({ available: true, blocks: [], note: '' }),
    savedViews: () => Promise.resolve([]),
    getPreference: (k: unknown) => Promise.resolve({ key: k, model: null }),
  };
  return {
    api: new Proxy({}, { get: (_t, k: string) => stub[k] ?? (() => Promise.resolve(null)) }),
    isCanceled: () => false,
  };
});
vi.mock('@/components/AuthProvider', () => ({
  useAuth: () => ({ user: { username: 'op', role: 'viewer' }, loading: false }),
}));

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
class NoopResizeObserver { observe() {} unobserve() {} disconnect() {} }
(globalThis as unknown as { ResizeObserver: unknown }).ResizeObserver = NoopResizeObserver;

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
  await settle(); await settle();
}
function close() {
  act(() => { root?.unmount(); });
  host?.remove(); root = null; host = null;
  document.body.innerHTML = '';
}
function snap(): string {
  const c = document.body.cloneNode(true) as HTMLElement;
  c.querySelectorAll('[lang="tr"]').forEach(n => n.remove());
  const attrs = [...c.querySelectorAll('[title],[aria-label],[placeholder]')]
    .flatMap(e => ['title', 'aria-label', 'placeholder'].map(a => e.getAttribute(a) ?? ''));
  return [c.textContent ?? '', ...attrs].join('\n');
}

const TOPIC = '/messaging/topic?system=kafka&cluster=cluster-1&destination=topic-1&range=1h';
async function all(): Promise<{ text: string; html: string }> {
  const out: string[] = []; const html: string[] = [];
  const take = () => { out.push(snap()); html.push(document.body.innerHTML); close(); };
  m.kafka = true; m.overviewFail = false;
  await mount('/messaging', <MessagingPage />); take();
  await mount('/messaging?view=consumers', <MessagingPage />); take();
  m.overviewFail = true;
  await mount('/messaging', <MessagingPage />); take();
  m.overviewFail = false;
  await mount('/messaging/topic', <MessagingTopicPage />); take();
  await mount(TOPIC, <MessagingTopicPage />); take();
  await mount(`${TOPIC}&tab=consumers`, <MessagingTopicPage />); take();
  await mount(`${TOPIC}&tab=producers`, <MessagingTopicPage />); take();
  await mount(`${TOPIC}&tab=traces`, <MessagingTopicPage />); take();
  await mount('/service', <ServiceMessagingTab service="svc-a" range={{ preset: '1h' }} />); take();
  m.kafka = false;
  await mount(TOPIC, <MessagingTopicPage />); take();
  await mount('/service', <ServiceMessagingTab service="svc-a" range={{ preset: '1h' }} />); take();
  m.kafka = true;
  return { text: out.join('\n'), html: html.join('\n') };
}

afterEach(() => { close(); m.lang = 'en'; });

const TR_CHARS = /[çğıöşüÇĞİÖŞÜ]/;
const TR_WORDS = /\b(satır|yok|metrik|pencere|sekme|servis|Kaynak|istemci|Hepsi|alarmı|aşıldı|korele|adları|Operasyon)\b/;
// Paylaşılan bileşenlerin kendi etiketleri bu dilimin kapsamı dışında (raporlandı).
// v0.10.2311 — DependenciesTable ipuçları ve 'Pod detayı' artık İngilizce (muafiyet kalktı).
const SHARED = ['Tablo seçenekleri', 'Ara — komut paletini açar (⌘K)', "Türkçe'ye geç", 'Servis', 'Linki kopyala'];

describe('Messaging / topic / servis Messaging sekmesi — İngilizce arayüz (v0.10.2306)', () => {
  it('Türkçe metin yok', async () => {
    const r = await all();
    const bad = r.text.split('\n').filter(l => !SHARED.includes(l) && (TR_CHARS.test(l) || TR_WORDS.test(l)));
    expect(bad).toEqual([]);
    for (const en of ['Click a row → publishers', 'To inspect consumers from the entry-point view', '⚠ Only the 200 busiest destinations',
      'Lag ≥ 1,000', 'behind', 'Last poll', 'VictoriaMetrics · end of window', 'Could not read the messaging overview',
      'No topic in this link', 'The address does not carry ', 'Back to the Messaging list →', ' › topic detail', 'Trace side (span)',
      'Consumers · span · 1 row', 'Producers · span · 1 row', ' · other clients ', 'The series has a single bucket', '4 correlated pairs', 'slowest ',
      'Operations · MV · 1 rows', '(not emitted)', 'Span names · 2 rows', '(empty)', 'threshold exceeded',
      'Source: VictoriaMetrics, Kafka client metrics labelled ', 'No Kafka client metric for this topic', 'No Kafka client metric for this service',
      'Overview', 'Health', 'Highest consumer lag', 'Producer buffer usage %', 'Charts are per client (client_id)',
    ]) expect(r.text, en).toContain(en);
  });

  it('saf yardımcılar: lang verilmezse Türkçe aynen; EN her panel katalogda', () => {
    expect(opLabelTR('')).toBe('(yaymıyor)');
    expect(opLabelTR(' ', 'en')).toBe('(not emitted)');
    expect([OP_MISSING_LABEL, OP_MISSING_TITLE, opMissingTitle()]).toEqual(['(yaymıyor)', 'SDK messaging.operation.type/.name/.operation yaymıyor', 'SDK messaging.operation.type/.name/.operation yaymıyor']);
    expect(KAFKA_SECTIONS.map(s => kafkaSectionTitle(s.id))).toEqual(['Genel bakış', 'Sağlık', 'Ayrıntı', 'Diğer pano panelleri']);
    const tr = kafkaPanels({ service: 'svc-a' });
    const en = kafkaPanels({ service: 'svc-a' }, 'en');
    expect(tr.find(p => p.id === 'lag')!.title).toBe('Consumer lag (en kötü partition)');
    expect(en.map(p => p.id)).toEqual(tr.map(p => p.id));
    expect(en.flatMap(p => p.queries.map(q => q.expr))).toEqual(tr.flatMap(p => p.queries.map(q => q.expr)));
    for (const p of [...en, ...kafkaTopicPanels('topic-1', undefined, 'en')]) {
      for (const s of [p.title, p.hint, ...p.queries.map(q => q.legend)]) {
        expect(s, p.id).not.toMatch(/^k(afka|topic)\.|[çğıöşüÇĞİÖŞÜ]|\bhata\b/);
      }
    }
    expect(kafkaTopicPanels('topic-1').find(p => p.id === 'errors_by_client')!.queries[0].legend).toBe('hata - {{service_name}} / {{client_id}}');
  });
});

describe('Messaging / topic / servis Messaging sekmesi — Türkçe arayüzde metin aynen (v0.10.2306)', () => {
  it('bilinen Türkçe metin', async () => {
    m.lang = 'tr';
    const r = await all();
    for (const tr of ["Satıra tıkla → yayıncılar, tüketiciler ve uçtan uca gecikme; trace'ler için satırdaki destination bağlantısını kullan.",
      "Consumer'ları giriş-noktası görünümünde incelemek için", '⚠ Yalnız en yoğun 200 destination gösteriliyor', 'Lag ≥ 1.000', 'geride',
      'Son poll', 'VictoriaMetrics · pencere sonu', 'Mesajlaşma özeti okunamadı', 'Bu linkte topic yok', 'Adres ', 'Messaging listesine dön →',
      ' › topic detayı', 'Trace tarafı (span)', 'Consumers · span · 1 satır', ' · diğer istemci ', '◌ Seri tek kovada', '4 korele çift',
      'Operasyonlar · MV · 1 satır', '(yaymıyor)', 'Span adları · 2 satır', '(boş)', 'eşik aşıldı',
      "etiketli Kafka istemci metrikleri. Bağlantı, broker gecikmesi ve rebalance topic'e değil istemciye aittir — servis satırından Service → Messaging sekmesinde.",
      'Bu topic için Kafka istemci metriği yok', 'Bu servis için Kafka istemci metriği yok', 'Genel bakış', 'Sağlık', 'En yüksek consumer lag',
      'Producer buffer doluluğu %', 'Grafikler istemci (client_id) başınadır',
    ]) expect(r.text, tr).toContain(tr);
  });
});
