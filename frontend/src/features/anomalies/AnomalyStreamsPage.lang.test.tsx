// @vitest-environment jsdom
//
// v0.10.2282 — pin: arayüz dili İngilizceyken /anomalies ekranı (AnomalyStreamsPage:
// birleşik liste + kapalı "Teknik ayrıntı" altındaki ML dedektör durumu, eski
// dedektör kartları, servis düzeyi karşılaştırma ve Problem birleştirme gölge
// raporu; ayrıca streams.tsx olay geçmişi tablosu) bilinen Türkçe etiket ÇİZMEZ;
// Türkçe arayüzde metin aynen kalır. UnifiedAnomalies.lang.test şeklinde.
import { describe, it, expect, vi, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import type { AnomalyEvent, LogPatternAnomaly, UnifiedAnomaliesPage, UnifiedAnomalyRow } from '@/lib/types';

const m = vi.hoisted(() => ({ lang: 'en' as 'en' | 'tr', mode: 'rows' as 'rows' | 'empty' }));
vi.mock('@/lib/i18n', async (orig) => ({ ...(await orig<typeof import('@/lib/i18n')>()), useLang: () => m.lang }));

const NOW_NS = Date.now() * 1e6;
const NOW_S = Math.floor(Date.now() / 1000);
const SVC = 'checkout-api';
const base = (key: string): UnifiedAnomalyRow => ({
  key, service: SVC, start: NOW_NS - 12 * 60e9, end: NOW_NS, signals: ['trace'], status: 'running', mlCount: 1, legacyCount: 0,
});
function mlRow(): UnifiedAnomalyRow {
  const v = Array.from({ length: 20 }, (_, i) => (i >= 10 ? 135 : 43));
  const record = { v: 1, subject: { service: SVC, operation: 'GET /cart', resolved: true },
    series: { source: 'vm', query: 'q', aggregation: 'p95', unit: 'ms', stepSec: 60 },
    baseline: { expected: 43, lo: 31, hi: 58, method: 'm', stepSec: 60 }, direction: 'up',
    onsetAt: NOW_NS - 12 * 60e9, detectedAt: NOW_NS - 10 * 60e9, peakValue: 135, valueAtDetection: 107, expectedValue: 43, volume: 1240,
    evidence: [], class: 'service_anomaly', title: '',
    snapshot: { t0: NOW_S - 1200, step: 60, aggregation: 'p95', v, lo: v.map(() => 31), hi: v.map(() => 58) } };
  return { ...base('ml'), mlLead: { id: 'ev1', detector: 'op_latency', signal: 'trace', stage: 'shadow', service: SVC, startedAt: NOW_NS - 12 * 60e9,
    lastSeen: NOW_NS, windowSec: 300, baseline: 43, observed: 135, score: 5, direction: 'up', summary: 'sunucu cümlesi', record: JSON.stringify(record) } };
}
const legacyEv = (id: string, kind: string, pattern: string, status: 'active' | 'cleared' = 'active'): AnomalyEvent => ({
  id, kind, pattern, service: SVC, startedAt: NOW_NS - 75 * 60e9, lastSeen: NOW_NS - 5 * 60e9,
  peakRatio: 4, currentRatio: 3, currentCount: 1240, sample: 'ERROR db timeout\nnext', status,
} as AnomalyEvent);
const legacyRow = (): UnifiedAnomalyRow => ({ ...base('lg'), mlCount: 0, legacyCount: 1, legacyLead: legacyEv('le1', 'op_latency', 'POST /pay') });

const scorecard = {
  windowDays: 30, signals: { op_latency: 'trace', log_volume: 'log', metric_jvm: 'metric' }, enabled: { op_latency: true, log_volume: true },
  detectors: [{ detector: 'op_latency', events: 4, samples: 2, hitRate: 0.5 }],
  bySignal: [{ detector: 'trace', events: 4, samples: 2, hitRate: 0.5 }],
  backfill: [{ signal: 'log', done: 10, total: 24, running: true, complete: false, missing: ['h1'] }],
  baseline: { lastDay: '2026-11-15', last: { day: '2026-11-15', rows: 1200, durationMs: 2300 }, profiles: { week: '2026-W46', services: 3, changed: 1 } },
  tick: {
    at: NOW_S - 120, durationMs: 4000, events: 3, docs: 1500, partial: 1, batchCut: 2, contribQueries: 4, logSvcUnmatched: 1, logSvcViaPod: 2,
    backoff: [{ family: 'jvm', until: NOW_S + 300 }],
    shadowGroups: 2, shadowWouldOpen: 1, shadowSingleWouldOpen: 1, shadowSvcWouldOpen: 1, shadowSvcP1: 1,
    thanos: { queries: 5, ms: 1500, maxMs: 700, errors: 1, budgetCut: 1, unmatched: 1, failed: ['q1'] },
    kinds: { trace: { services: 3, series: 10, seriesRows: 200, es: 0, vm: 2, ch: 1, durationMs: 1200, dropped: 1 } },
    signals: { trace_red: { ran: true, services: 3, candidates: 2, written: 1, sourceMs: 900 } },
  },
};
const svcCompare = { days: 14, truncated: true, counts: { matched: 1, missed: 1 }, rows: [
  { service: SVC, oldRule: 'svc-slowdown:x', oldStart: NOW_NS, oldPriority: 'P2', newDetector: 'svc_breadth', newStart: NOW_NS, newPriority: 'P2', diff: 'matched' },
  { service: SVC, oldRule: 'anomaly:x:error_rate', oldStart: NOW_NS, diff: 'missed' },
] };
const graphShadow = { available: true,
  report: { at: NOW_S, since: NOW_S - 3600, windowH: 24, units: 4, callStrict: 1, graphMissing: 2, links: { 'M-CALL': 2 }, sizes: { '2': 1 }, age: { '0-30': 1 },
    originConf: { strong: 1 }, reads: { passes: 3, skippedFew: 1, refreshes: 1, relations: 5, maxRelPerPass: 3, runs: 1, evidence: 1, errors: 0 },
    frequentReport: { frequent: 2, wouldHold: 1, severityUp: 0, heldBecameP1: 0, atThreshold: { '3': 1 },
      reads: { passes: 1, reads: 2, rows: 10, maxRows: 8, capped: 0, errors: 0 } } },
  largest: [{ ownerId: 'o1', ownerName: SVC, entities: 3, startedAt: NOW_NS, rules: { 'M-CALL': 2 }, confidence: 'weak' }] };
const logPatterns: LogPatternAnomaly[] = [{ pattern: 'ERROR db timeout', service: SVC, kind: 'spike', ratio: 3, currentCount: 30, baselineCount: 10, sample: 'ERROR db timeout' } as LogPatternAnomaly];

vi.mock('@/lib/queries', () => {
  const mut = { mutateAsync: async () => {} };
  return {
    useUnifiedAnomalies: (p: { section: 'open' | 'done' }) => {
      const items = m.mode === 'empty' || p.section === 'done' ? [] : [mlRow(), legacyRow()];
      const data: UnifiedAnomaliesPage = { items, total: items.length, openTotal: items.length, doneTotal: 0, offset: 0, limit: 50, truncated: false };
      return { data, isLoading: false, isError: false };
    },
    useMLScorecard: () => ({ data: scorecard }),
    useLogPatternAnomalies: () => ({ data: logPatterns }),
    useMetricAnomalies: () => ({ data: [] }),
    useAnomalyEvents: () => ({ data: { items: [legacyEv('h1', 'op_latency', 'POST /pay'), legacyEv('h2', 'log_template_new', 'ERROR db timeout', 'cleared')], activeTotal: 1, clearedTotal: 1, truncated: true }, isError: false }),
    useAnomalySilences: () => ({ data: [] }),
    useCreateAnomalySilence: () => mut, useDeleteAnomalySilence: () => mut, useBulkDeleteAnomalySilences: () => mut,
    useMLSvcShadowCompare: () => ({ data: svcCompare, isLoading: false, error: null }),
    useProblemGraphShadow: () => ({ data: graphShadow, isLoading: false, error: null }),
  };
});
vi.mock('@/components/AuthProvider', () => ({
  useAuth: () => ({ user: { username: 'op', role: 'admin' }, loading: false }),
  useAuthRole: () => 'admin',
}));
vi.mock('@/components/Topbar', () => ({ Topbar: () => null }));
vi.mock('@/components/SavedViewsBar', () => ({ SavedViewsBar: () => null }));
vi.mock('@/components/RootCauseRibbon', () => ({ RootCauseRibbon: () => null }));
vi.mock('@/components/ai/AIExplainButton', () => ({ AIExplainButton: () => null }));

import AnomalyStreamsPage from './AnomalyStreamsPage';
import { AnomalyStreams } from './streams';
import { ConfirmProvider } from '@/components/ui/ConfirmDialog';
import { legacyEvidenceText, rowHeadline } from '@/components/coremetry/anomalies/unifiedRows';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let root: Root | null = null;
let host: HTMLDivElement | null = null;
afterEach(() => { act(() => root?.unmount()); host?.remove(); root = null; host = null; m.lang = 'en'; m.mode = 'rows'; });

function render(node: React.ReactNode): { text: string; html: string } {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  act(() => { root!.render(<MemoryRouter><ConfirmProvider>{node}</ConfirmProvider></MemoryRouter>); });
  return { text: host.textContent ?? '', html: host.innerHTML };
}

// Bu ekranlarda bugün Türkçe çizilen etiketler (sunucu verisi değil).
const TR_LABELS = [
  'Sürüyor', 'Bitti', ' dk', ' sn', ' sa ', 'Ne oldu', 'yavaşladı', 'log kalıbı', 'Örnek satır', 'ilk 2 satır', 'Son 24 saatte',
  'Anomali tespiti', 'Son kontrol', 'Dedektör', 'İzlediği', 'Değerlendirme', 'Son 30 günde', 'Açık', 'Kapalı', 'Teknik detay',
  'Son tik', 'Log geçmişi', 'Olay · 30', 'VM bekleme', 'aile', 'belge', 'kısmi', 'batch kuralı', 'suçlu seri', 'katalogda', 'çözülen',
  'açardı', 'gölge terfi', 'sorgu', 'yanıtsız', 'servis', 'satır', 'son gün', 'profil haftası', 'Endpoint gecikmesi', 'Log hacmi',
  'Servis düzeyi', 'Eski', 'Yeni', 'Fark', 'Eşleşti', 'Kaçırdı', 'Yaygın yavaşlama', 'Hata oranı', 'Okuma tavanına', 'eşleşti',
  'Problem birleştirme', 'gölge raporu', 'yalnız ölçülür', 'En büyük kümeler', 'varlık', 'köken', 'Pencere', 'Kurulurdu', 'Bağlanmazdı',
  'Küme boyu', 'Graf verisi', 'Olası köken', 'güçlü', 'Okuma:', 'Sık tekrar', 'Tutulacakken', 'imzalar', 'Geçmiş okuması', 'yok',
  'ilişkili sinyalleri', 'Ne oldu?', 'Hepsi', 'Metrik',
];
const leaked = (r: { text: string; html: string }) => TR_LABELS.filter(l => r.text.includes(l) || r.html.includes(l));

describe('/anomalies ekranı — İngilizce arayüz (v0.10.2282)', () => {
  it('ML ve eski dedektör satırlı sayfa + teknik ayrıntı: Türkçe etiket yok', () => {
    const r = render(<AnomalyStreamsPage />);
    expect(leaked(r), `Türkçe etiket İngilizce arayüzde: ${leaked(r).join(' | ')}`).toEqual([]);
    for (const en of [
      'GET /cart p95 latency now 135 ms', 'POST /pay slowed down',
      'Anomaly detection is running. The last check ran 2 minutes ago, took 4 s and found 3 events.',
      'Log history is not complete yet: 11 / 24 hours, running.', 'Detectors', 'Endpoint latency', 'Log volume', 'Technical detail',
      'Last tick', 'VM backoff', 'shadow promotion: 2 groups, would open 1 Problems (not opened)', 'Thanos 5 queries · 1.5 s (slowest 0.7 s)',
      'Trace · 3 services · 10 series (200 rows)', 'Baseline · last day 2026-11-15 · 1,200 rows · 2.3 s',
      'Service level: legacy Problem ↔ new shadow (14 days)', 'Last 14 days: 1 matched, 1 missed, 0 extra, 0 late. Hit the read cap',
      'Widespread slowdown (legacy)', 'Error rate (legacy)', 'Widespread slowdown', 'Matched', 'Missed',
      'Problem merge (entity graph): shadow report', 'Would link (distinct links): M-CALL 2', 'Cluster size, plain M-CALL: 2 entities 1',
      'confidence strong 1', 'Frequent (shadow, distinct Problems): 2', 'Largest clusters (now, at most 20)', '3 entities', 'origin unclear (weak)',
      'What happened?',
    ]) expect(r.text, en).toContain(en);
    expect(r.html).toContain('Gather related signals for this pattern and explain what happened (AI)');
  });
  it('boş liste İngilizce', () => {
    m.mode = 'empty';
    const r = render(<AnomalyStreamsPage />);
    expect(r.text).toContain('No ongoing anomalies right now.');
    expect(leaked(r)).toEqual([]);
  });
  it('olay geçmişi tablosu (streams.tsx): durum, süre, başlık İngilizce', () => {
    const r = render(<AnomalyStreams />);
    expect(leaked(r), leaked(r).join(' | ')).toEqual([]);
    for (const en of ['Ongoing', 'Ended', '1 h 10 min', 'POST /pay slowed down', 'a new log template appeared', 'first 2 rows loaded']) {
      expect(r.text, en).toContain(en);
    }
  });
  it('saf yardımcılar: lang verilince İngilizce, verilmezse Türkçe aynen', () => {
    const e = legacyEv('x', 'log_template_new', 'ERROR db timeout');
    expect(legacyEvidenceText(e, 'en')).toBe(`${SVC}: a new log template appeared, 1,240 lines. Sample line: ERROR db timeout`);
    expect(legacyEvidenceText(e)).toBe(`${SVC}: yeni bir log kalıbı belirdi, 1.240 satır. Örnek satır: ERROR db timeout`);
    const noRec = mlRow(); noRec.mlLead = { ...noRec.mlLead!, summary: '', record: undefined };
    expect(rowHeadline(noRec, 'en')).toBe(`${SVC} · Endpoint latency`);
    expect(rowHeadline(noRec)).toBe(`${SVC} · Endpoint gecikmesi`);
  });
});

describe('/anomalies ekranı — Türkçe arayüzde metin aynen (v0.10.2282)', () => {
  it('sayfa', () => {
    m.lang = 'tr';
    const r = render(<AnomalyStreamsPage />);
    for (const tr of [
      'POST /pay yavaşladı', 'Anomali tespiti çalışıyor. Son kontrol 2 dakika önce yapıldı, 4 saniye sürdü ve 3 olay buldu.',
      'Log geçmişi henüz tamamlanmadı: 11 / 24 saat, sürüyor.', 'Dedektörler', 'Endpoint gecikmesi', 'Teknik detay', 'Son tik', '2 dk önce',
      'VM bekleme', 'gölge terfi: 2 grup, 1 Problem açardı (açılmadı)', 'Thanos 5 sorgu · 1.5 sn (en yavaş 0.7 sn)',
      'Trace · 3 servis · 10 seri (200 satır)', 'Baseline · son gün 2026-11-15 · 1.200 satır · 2.3 sn',
      'Servis düzeyi: eski Problem ↔ yeni gölge (14 gün)', 'Son 14 gün: 1 eşleşti, 1 kaçırdı, 0 fazladan, 0 geç. Okuma tavanına değdi',
      'Yaygın yavaşlama (eski)', 'Eşleşti', 'Problem birleştirme (entity grafı): gölge raporu', 'Kurulurdu (farklı bağ): M-CALL 2',
      'güven güçlü 1', 'En büyük kümeler (şu an, en çok 20)', '3 varlık', 'köken belirsiz (weak)', 'Ne oldu?',
    ]) expect(r.text, tr).toContain(tr);
    expect(r.html).toContain('Bu desen için ilişkili sinyalleri topla ve ne olduğunu anlat (AI)');
  });
  it('olay geçmişi tablosu', () => {
    m.lang = 'tr';
    const r = render(<AnomalyStreams />);
    for (const tr of ['Sürüyor', 'Bitti', '1 sa 10 dk', 'POST /pay yavaşladı', 'ilk 2 satır yüklendi']) expect(r.text, tr).toContain(tr);
  });
});
