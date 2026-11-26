// @vitest-environment jsdom
//
// v0.10.2291 — pin: arayüz dili İngilizceyken ML olay sayfası (eski kart:
// bölüm başlıkları, kanıt satırları, eylemler, Teknik ayrıntı — kart, bağlam,
// örnekler, what-if —, oynatıcı, trace noktaları / ısı haritası, log yığını,
// kanıt tabloları, kaynak grafiği) bilinen Türkçe etiket ÇİZMEZ; Türkçe
// arayüzde metin aynen kalır. Sunucu metni (kart cümleleri) iki dilde aynen;
// v0.10.2302'den beri İngilizce arayüzde TR işaretli (OrigLang).
// Saf yardımcılar lang verilmezse Türkçe.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { ExplainCard, MLReplay, MLSnapshot } from '@/lib/types';

const m = vi.hoisted(() => {
  window.matchMedia = ((q: string) => ({
    matches: false, media: q, onchange: null, addListener: () => {}, removeListener: () => {},
    addEventListener: () => {}, removeEventListener: () => {}, dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
  return { lang: 'en' as 'en' | 'tr', replay: null as unknown };
});
vi.mock('@/lib/i18n', async (orig) => ({ ...(await orig<typeof import('@/lib/i18n')>()), useLang: () => m.lang }));
vi.mock('@/lib/api', () => ({
  api: new Proxy({}, { get: (_t, k: string) => (k === 'mlReplay' ? async () => m.replay : async () => null) }),
  isCanceled: () => false,
}));
vi.mock('@/components/AuthProvider', () => ({ useAuthRole: () => 'admin' }));
vi.mock('@/components/charts/TimeChart', () => ({
  TimeChart: ({ series, regions }: { series?: { label: string }[]; regions?: { label: string }[] }) =>
    <div data-series={(series ?? []).map(s => s.label).join('|')} data-regions={(regions ?? []).map(r => r.label).join('|')} />,
}));
vi.mock('@/pages/service/httpMetricsViz', () => ({ HttpChartPanel: () => null }));

import { MLReplayContent, backtestLine, cardRows, contextRows } from './MLReplaySheet';
import { contributorLine, liveGapLine, serviceRedAggFor, sourcePanelFor, thanosSourceText } from './mlSourcePanel';
import { evidenceSource, groupGateLine, groupProblemChip, svcGateLine } from './mlGroupDetail';
import { legacyEvidenceGroups, legacySource, legacyStatusText, stripServicePrefix } from './unifiedRows';
import { fmtEvidenceMs } from './MLSignalEvidence';
import { SVC_DIFF_TR } from './mlSvcCompare';
import { compactRegions, dayClassLabel } from '@/lib/mlReplay';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

// Sunucu metni nötr (iki dilde aynen basılır; sızıntı listesine girmez).
const card = (kind: string): ExplainCard => ({
  summary: 'svc-a: x1', what: 'x2', howMuch: 'x3', since: 'x4', impact: 'x5', whyItMatters: 'x6',
  whatChanged: [], whereToLook: [{ label: 'L1', href: '/service?name=svc-a' }],
  technical: { detector: 'd1', score: '9', signature: 's1', thresholds: 't1', expected: 'e1', revision: 'r1', measured: 'm1' },
  header: { kind, detector: 'error_rate', service: 'svc-a', serviceHref: '/service?name=svc-a', open: true, status: 'st-1', profile: 'always_on', profileLabel: '7/24' },
} as ExplainCard);
const T0 = 1_800_000_000;
const snap: MLSnapshot = {
  eventId: 'e1', detector: 'error_rate', signal: 'log', service: 'svc-a', part: '_error', detectedAt: T0 * 1e9, lastSeen: (T0 + 600) * 1e9,
  bucketSec: 300, series: JSON.stringify([{ t: T0, v: 5 }, { t: T0 + 300, v: 60 }]), baselineMed: 23, baselineMad: 4, thresholds: '{"criticalZ":4}',
  configHash: '', dayClass: 'weekday', dwell: JSON.stringify([T0 + 300]), confirmIds: null,
  evidence: JSON.stringify({ bubbleUp: [{ key: 'k', value: 'v', selPct: 80, basePct: 10 }], bubbleUnreadable: false }),
  samples: JSON.stringify([{ t: T0 * 1e9, traceId: 'abcdef0123456789ff', kind: 'span' }]), deploys: '[]', complete: false,
};
const related = [{ ...snap, eventId: 'e2', detector: 'op_latency', signal: 'trace' }];
const tcHeat = { view: 'heatmap', service: 'svc-a', fromNs: 1, toNs: 2, onsetNs: 1, endNs: 2 };
const tcDots = { view: 'dots', service: 'svc-a', fromNs: 1, toNs: 2, onsetNs: 1, endNs: 2, dots: [{ t: 1000, ms: 600 }], dotsCapped: true };
const log = {
  times: [T0, T0 + 300], shadow: [7, 7],
  byPattern: [{ key: 'p1', label: 'p1', new: true, values: [0, 40] }],
  byLevel: [{ key: '_error', label: 'lv1', values: [5, 60] }],
};

let host: HTMLDivElement | null = null;
let root: Root | null = null;
const close = () => { act(() => { root?.unmount(); }); host?.remove(); host = null; root = null; };

async function render(replay: MLReplay) {
  m.replay = replay;
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => {
    root!.render(<QueryClientProvider client={qc}><MemoryRouter><MLReplayContent id="e1" /></MemoryRouter></QueryClientProvider>);
  });
  await act(async () => { await new Promise(r => setTimeout(r, 10)); });
  await act(async () => { await new Promise(r => setTimeout(r, 10)); });
  const out = { text: host.textContent ?? '', html: host.innerHTML };
  close();
  return out;
}

async function all() {
  const out = [
    await render({ snapshot: snap, related, card: card('log'), verdict: { verdict: 'anomaly', by: 'op@x' } } as MLReplay),
    await render({ snapshot: snap, related: [], card: card('trace'), course: { trace: tcHeat } } as MLReplay),
    await render({ snapshot: snap, related: [], card: card('metric'), course: { trace: tcDots } } as MLReplay),
    await render({ snapshot: snap, related: [], card: card('log'), course: { log } } as MLReplay),
    await render({ snapshot: { ...snap, series: '[]' }, related: [], card: card('log') } as MLReplay),
    await render({ snapshot: null, related: [], card: card('log') } as MLReplay),
    await render({ snapshot: null, related: [], card: null } as unknown as MLReplay),
  ];
  return { text: out.map(r => r.text).join('\n'), html: out.map(r => r.html).join('\n') };
}

beforeEach(() => { m.lang = 'en'; });
afterEach(() => { close(); document.body.innerHTML = ''; });

const TR_LABELS = [
  'Olayın seyri', 'Kanıt', 'Ne yapmak', 'Örnek trace', 'Teknik ayrıntı', 'Dedektörler', 'Problem kapısı', 'karar yok',
  'Ne oldu', 'Ne kadar', 'Ne zamandan', 'Neden önemli', 'Yakında', 'eşlenen değişiklik', 'Nereye bakmalı', 'dakikalık kova',
  '>Saat<', '>Satır<', 'Örnek trace ve log', 'Yoğunlaşma', 'taban', 'İlişki', 'Ne zaman', '>Git<', 'Olayın bağlamı', 'Aynı servis',
  'ön kayıt', 'Dedektör', 'Sapma skoru', 'İmza', 'Eşikler', 'Beklenen aralık', 'Ayar revizyonu', 'Takvim sınıfı', 'iş günü',
  'Taban medyan', 'Eşik z', 'Teyit eden', 'Kritik z', 'Dwell (kova)', 'Etki', 'Karşılaştır', 'Gerçek anomali', 'Sorun değil',
  'Etiketiniz', 'gerçek ✓', 'istek süreleri', 'örneklenmiş', 'anomali penceresi', 'Anomali ↗', 'regions="anomali', 'Her kare', 'tek tek çağrılar', 'Her nokta', 'ilk 1 çağrı',
  'Yığın türü', 'Mesaj kalıbı', 'Seviye', '(yeni)', 'gölge çubuk', 'olay anı kaydı yok', 'Canlı grafik', 'endpoint kaydı',
  'Komşu metrik', 'başka metrik', 'Yeni ve artan', 'log deposundan', 'Olay anı kaydı yok', 'bulunamadı', 'saklanır', 'tetik',
  'beklenen aralık', 'alarm sınırı', 'Oynat', 'Seri boş', 'düz çizgi',
];
const leaked = (r: { text: string; html: string }) => TR_LABELS.filter(l => r.text.includes(l) || r.html.includes(l));

describe('ML olay sayfası — İngilizce arayüz (v0.10.2291)', () => {
  it('bölümler, kanıt, teknik ayrıntı, oynatıcı, iz grafikleri, log yığını: Türkçe etiket yok', async () => {
    const r = await all();
    expect(leaked(r), `Türkçe etiket İngilizce arayüzde: ${leaked(r).join(' | ')}`).toEqual([]);
    for (const en of [
      'How it unfolded', 'Evidence', 'What do you want to do?', 'Technical details', 'Detectors', 'Problem gate', 'no decision',
      'What happened', 'How much', 'Since when', 'Impact', 'Why it matters', 'What changed recently', 'No changes matched in this window.',
      'Where to look', 'Sample trace and log lines', 'Concentration:', '80% (baseline 10%)', 'Relation', 'Event context', 'Same service',
      'preliminary record', 'Deviation score', 'Signature', 'Thresholds', 'Expected range', 'Settings revision', 'Detector measured',
      'Calendar class', 'weekday', 'Baseline median / MAD', 'Threshold z (critical)', 'Confirmed by', 'Critical z', 'Dwell (buckets)',
      'Impact (relative)', 'Compare', 'Real anomaly', 'real ✓', 'request durations · sampled spans', 'anomaly window',
      'Each cell shows', 'individual calls · sampled spans', 'showing the first 1 calls', 'Each dot is one call', 'Message template',
      'Level', '(new)', 'shadow bar: expected in this bucket', 'No event-moment snapshot for this event', 'No other metric series are tracked',
      'No endpoint records in this window.', 'New and rising messages', 'from the log store', 'No event-moment snapshot',
      'Event not found', 'Event records are kept for 30 days', 'Series empty', 'solid line: value',
    ]) expect(r.text + r.html, en).toContain(en);
    for (const en of ['data-regions="detected ', 'aria-label="Stack type"', 'bound upper|', 'aria-label="Endpoint breakdown"'])
      expect(r.html, en).toContain(en);
  });

  it('saf yardımcılar: lang verilmezse Türkçe aynen', () => {
    const bt = { detectors: [{ detector: 'd', fired: 3, tp: 1, fp: 1, unlabeled: 1, precision: 0.5 }] } as never;
    expect(backtestLine(bt, 'd')).toBe('3 isabet (1 gerçek, 1 değil, 1 etiketsiz) · isabet %50');
    expect(backtestLine(bt, 'd', 'en')).toBe('3 hits (1 real, 1 not, 1 unlabeled) · precision 50%');
    expect(backtestLine(undefined, 'd')).toBe('0 isabet');
    expect(cardRows(card('log')).map(r => r.k)).toEqual(['Ne oldu', 'Ne kadar', 'Ne zamandan beri', 'Etki', 'Neden önemli']);
    expect(cardRows(card('log'), 'en').map(r => r.k)).toEqual(['What happened', 'How much', 'Since when', 'Impact', 'Why it matters']);
    expect(contextRows(null, related).map(r => r.rel)).toEqual(['Aynı servis']);
    expect(contextRows(null, related, 'en').map(r => r.rel)).toEqual(['Same service']);
    const single = { kind: 'single' as const, name: 'c1', value: 1200, others: 1, othersZero: false };
    expect(contributorLine(single, 'istemci')).toBe('Tepe anında neredeyse tamamı tek istemci: c1 (1.200); diğer 1 istemci daha düşük.');
    expect(contributorLine(single, 'client', 'en', 'clients')).toBe('At the peak almost all from a single client: c1 (1,200); the other 1 client is lower.');
    expect(contributorLine({ kind: 'spread', count: 4 }, 'istemci')).toBe('Tepe anında 4 istemciye yayılmış; tek bir öne çıkan yok.');
    expect(contributorLine({ kind: 'spread', count: 4 }, 'client', 'en', 'clients')).toBe('At the peak spread across 4 clients; no single one stands out.');
    expect(liveGapLine()).toBe('Canlı grafik çizilemedi: kaynak okunamadı. Aşağıdaki, olay anında kaydedilen 5 dakikalık görüntü.');
    expect(liveGapLine(undefined, 'en')).toBe('Live chart could not be drawn: the source could not be read. Below is the 5-minute snapshot recorded at the event.');
    expect(thanosSourceText()).toBe("servisin pod regex'i (namespace bilinmiyor)");
    expect(thanosSourceText('pods', 'en')).toBe('Pod set of the Service › Pods tab');
    const ev = { detector: 'svc_breadth', signal: 'trace', code: '', service: '', pod: '', evidence: '' };
    expect(sourcePanelFor(ev)).toEqual({ kind: 'snapshot', reason: 'olay kaydında servis yok' });
    expect(sourcePanelFor(ev, 1, undefined, 'en')).toEqual({ kind: 'snapshot', reason: 'the event record has no service' });
    expect(sourcePanelFor({ ...ev, service: 'svc-a' })).toMatchObject({ kind: 'service-red', label: 'p99 yanıt süresi' });
    expect(serviceRedAggFor('behavior_change', 'error_rate')).toEqual({ agg: 'error_rate', label: 'hata oranı' });
    expect(serviceRedAggFor('calendar_traffic_drop', undefined, 'en')).toEqual({ agg: 'rate', label: 'request rate' });
    expect(evidenceSource('trace_error_count', 'trace')).toBe("ClickHouse span'leri");
    expect(evidenceSource('op_latency', 'trace', undefined, 'en')).toBe('VictoriaMetrics · span RED metrics');
    const d = { live: false, wouldOpen: true, priority: 'P2', reason: 'r1', singleWouldOpen: true } as never;
    expect(groupGateLine(d)).toBe('açardı (tek sinyal) · P2 — r1');
    expect(groupGateLine(d, 'en')).toBe('would open (single signal) · P2 — r1');
    expect(groupGateLine(undefined)).toBe('karar yok');
    expect(groupProblemChip({ live: true, problemId: 'p1', priority: 'P1' } as never)).toMatchObject({ label: 'Problem açtı · P1' });
    expect(groupProblemChip({ wouldOpen: true } as never, 'en')).toEqual({ label: 'Would open a Problem (live mode off)' });
    expect(svcGateLine({ live: false, wouldOpen: true, joinsRule: true, reason: 'r2' } as never)).toBe("grup Problem'ine üye olurdu (gölge) — r2");
    expect(svcGateLine({ live: false, wouldOpen: false, reason: 'r2' } as never, 'en')).toBe('would not open — r2');
    expect(legacySource({ kind: 'behavior_change' })).toBe('davranış motoru (28 gün)');
    expect(legacySource({ kind: 'log_pattern' }, 'en')).toBe('log pattern count');
    const le = { id: 'x', kind: 'log_template_new', service: 'svc-a', pattern: 'p', currentCount: 1234, sample: 'line' } as never;
    expect(legacyEvidenceGroups([le])[0].text).toBe('1.234 satır; örnek:');
    expect(legacyEvidenceGroups([le], 'en')[0].text).toBe('1,234 lines; sample:');
    expect(legacyStatusText({ verdict: 'not_anomaly', status: 'cleared', startedAt: 0, lastSeen: 0 } as never, 0, 'en')).not.toMatch(/dendi/);
    expect(stripServicePrefix('svc-a ilk', 'svc-a')).toBe('İlk');
    expect(stripServicePrefix('svc-a increase', 'svc-a', 'en')).toBe('Increase');
    expect(fmtEvidenceMs(2300)).toBe('2,3 sn');
    expect(fmtEvidenceMs(2300, 'en')).toBe('2.3 s');
    expect(SVC_DIFF_TR).toEqual({ matched: 'Eşleşti', missed: 'Kaçırdı', extra: 'Fazladan', late: 'Geç (15 dk+)' });
    expect(dayClassLabel('month_end')).toBe('ay sonu benzeri');
    expect(dayClassLabel('holiday', 'en')).toBe('holiday');
    expect(dayClassLabel('zz', 'en')).toBe('zz');
    const reg = [{ fromSec: 60, toSec: 120, label: 'anomali' }];
    expect(compactRegions(reg, () => '10:00')[0].label).toBe('tespit 10:00');
    expect(compactRegions(reg, () => '10:00', 'en')[0].label).toBe('detected 10:00');
  });
});

describe('ML olay sayfası — Türkçe arayüzde metin aynen (v0.10.2291)', () => {
  it('bölümler, kanıt, teknik ayrıntı, oynatıcı, iz grafikleri, log yığını', async () => {
    m.lang = 'tr';
    const r = await all();
    for (const tr of [
      'Olayın seyri', 'Kanıt', 'Ne yapmak istersiniz?', 'Teknik ayrıntı', 'Dedektörler', 'Problem kapısı', 'karar yok',
      'Ne oldu', 'Ne kadar', 'Ne zamandan beri', 'Etki', 'Neden önemli', 'Yakında ne değişti', 'Bu pencerede eşlenen değişiklik yok.',
      'Nereye bakmalı', 'Örnek trace ve log satırları', 'Yoğunlaşma:', '%80 (taban %10)', 'İlişki', 'Olayın bağlamı', 'Aynı servis',
      'ön kayıt · epizot kapanınca tamamlanır', 'Sapma skoru', 'İmza', 'Eşikler', 'Beklenen aralık', 'Ayar revizyonu', 'Dedektörün ölçtüğü',
      'Takvim sınıfı', 'iş günü', 'Taban medyan / MAD', 'Eşik z (kritik)', 'Teyit eden', 'Kritik z', 'Dwell (kova)', 'Etki (göreli)',
      'Karşılaştır', 'Gerçek anomali', 'gerçek ✓', 'Etiketiniz bu dedektörün isabet ölçüsüne (ML karnesi) girer.',
      "istek süreleri · örneklenmiş span'ler", 'anomali penceresi', 'Her kare o dakikada', "tek tek çağrılar · örneklenmiş span'ler",
      'ilk 1 çağrı gösteriliyor', 'Her nokta bir çağrı.', 'Mesaj kalıbı', 'Seviye', '(yeni)', 'gölge çubuk: bu kovada beklenen',
      'Bu olay için olay anı kaydı yok (kaydedici ilk tespit tikinde yazar', 'Bu servis için izlenen başka metrik serisi yok.',
      'Bu pencerede endpoint kaydı yok.', 'Yeni ve artan mesajlar', '· log deposundan, açınca getirilir',
      'Olay anı kaydı yok; örnek satır gösterilemiyor.', 'Bu olay bulunamadı', 'Olay kaydı 30, olay anı kaydı 120 gün saklanır',
      'Seri boş (ml_series bu pencereyi taşımıyor).', 'düz çizgi: değer · ince çizgiler: normal aralık',
    ]) expect(r.text + r.html, tr).toContain(tr);
    for (const tr of ['data-regions="tespit ', 'aria-label="Yığın türü"', 'sınırı üst|', 'aria-label="Endpoint dağılımı"'])
      expect(r.html, tr).toContain(tr);
  });
});

// v0.10.2302 — eski (kayıtsız) kartın sunucu cümleleri (başlık + kanıt satırı)
// İngilizce arayüzde TR işaretli; kayıttan çizilen kart işaretsiz; Türkçe
// arayüzde işaret YOK ve hücre içeriği düz metin (innerHTML aynen).
describe('ML olay sayfası — sunucu cümlesi işareti (v0.10.2302)', () => {
  const NOW = T0 * 1e9;
  const v = Array.from({ length: 20 }, (_, i) => (i >= 10 ? 135 : 43));
  const record = {
    v: 1, detector: 'op_latency', subject: { service: 'svc-a', operation: 'GET /x', resolved: true },
    series: { source: 'vm', query: 'q', aggregation: 'p95', unit: 'ms', stepSec: 60 },
    baseline: { expected: 43, lo: 31, hi: 58, method: 'm', stepSec: 60 }, direction: 'up',
    onsetAt: NOW - 12 * 60e9, detectedAt: NOW - 10 * 60e9, peakValue: 135, valueAtDetection: 107, expectedValue: 43, volume: 1240,
    evidence: [], class: 'service_anomaly', title: '',
    snapshot: { t0: T0 - 1200, step: 60, aggregation: 'p95', v, lo: v.map(() => 31), hi: v.map(() => 58) },
  };
  async function legacy() {
    const out = await render({ snapshot: null, related: [], card: card('log') } as MLReplay);
    const doc = new DOMParser().parseFromString(out.html, 'text/html');
    return { doc, html: out.html };
  }
  it('İngilizce: eski kart başlığı ve kanıt satırı işaretli', async () => {
    const { doc } = await legacy();
    const sum = doc.querySelector('[data-testid="ml-summary"]')!;
    expect(sum.querySelector('span[lang="tr"]')?.textContent).toBe('X1TR');
    expect(sum.querySelector('[data-orig-lang]')).not.toBeNull();
    const rows = doc.querySelectorAll('[data-testid="ml-evidence-rows"] li span[lang="tr"]');
    expect(rows.length).toBe(1);
    expect(rows[0].textContent).toBe('x3TR');
  });
  it('İngilizce: kayıttan çizilen kart işaretsiz', async () => {
    const out = await render({ snapshot: null, related: [], card: card('trace'), record } as unknown as MLReplay);
    expect(out.html).toContain('data-testid="ml-replay"');
    expect(out.html).toContain('data-testid="ml-record-card"'); // kayıt dalı (eski kart değil)
    expect(out.html).not.toContain('data-orig-lang');
    expect(out.html).not.toContain('lang="tr"');
  });
  it('Türkçe: işaret yok, metin aynen', async () => {
    m.lang = 'tr';
    const { doc, html } = await legacy();
    expect(html).not.toContain('data-orig-lang');
    expect(html).not.toContain('lang="tr"');
    expect(doc.querySelector('[data-testid="ml-summary"]')!.innerHTML).toBe('X1');
    const li = doc.querySelector('[data-testid="ml-evidence-rows"] li')!;
    expect(li.querySelector('span.min-w-0')!.innerHTML).toBe('x3');
  });
});
