// @vitest-environment jsdom
//
// v0.10.2311 — pin: arayüz dili İngilizceyken ortak DependenciesTable
// (Messaging + Databases) ve dependencies çekmecesi (DetailDrawer,
// CallerSection, DegradedBand) bilinen Türkçe metin ÇİZMEZ (metin + title /
// aria-label / placeholder); Türkçe arayüzde metin aynen.
import { describe, it, expect, afterEach, vi } from 'vitest';
import { act, type ReactNode } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router';
import type { TimeRange, DBTrend } from '@/lib/types';
import { DependenciesTable, OpenProblemsMark, type DepRow } from './DependenciesTable';
import { DetailDrawer } from '@/features/dependencies/DetailDrawer';
import { DegradedBand } from '@/features/dependencies/panels/shared';

const m = vi.hoisted(() => ({ lang: 'en' as 'en' | 'tr', detail: null as unknown }));
vi.mock('@/lib/i18n', async (orig) => ({ ...(await orig<typeof import('@/lib/i18n')>()), useLang: () => m.lang }));
const TREND: DBTrend = {
  dbSystem: 'postgresql', instance: 'db-1', dbName: 'app', cluster: '',
  points: [{ t: 1, rps: 2, errorRate: 1, p99Ms: 300 }, { t: 2, rps: 3, errorRate: 1, p99Ms: 300 }],
  curRps: 3, curErrorRate: 1, curP99Ms: 300,
};
vi.mock('@/lib/queries/dependencies', () => ({
  useDepTrends: () => ({ isPending: false, data: [TREND] }),
  useDepDetail: () => ({ isPending: false, data: m.detail }),
}));
vi.mock('@/features/dependencies/KafkaTopicMetrics', () => ({ KafkaTopicMetrics: () => null }));

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const RANGE: TimeRange = { preset: '1h' };
const DB: DepRow = {
  system: 'postgresql', instance: 'db-1', dbName: 'app', spanCount: 10, errorCount: 1, errorRate: 10,
  avgDurationMs: 3, p50DurationMs: 2, p95DurationMs: 4, p99DurationMs: 5, callers: ['svc-a'],
  openProblems: 2, topSeverity: 'critical',
};
const RECV: DepRow = { ...DB, instance: 'db-2', dbName: undefined, source: 'receiver', openProblems: 1, callers: [] };
const Q: DepRow = {
  system: 'kafka', cluster: 'cluster-1', destination: 'topic-1', spanCount: 10, errorCount: 0, errorRate: 0,
  avgDurationMs: 3, p99DurationMs: 5, callers: [], lag: 5,
  kafka: { partitions: 3, producers: [], consumers: [], producerCount: 2, consumerCount: 0 } as unknown as DepRow['kafka'],
};
const Q2: DepRow = { ...Q, destination: 'topic-2', lag: undefined, kafka: { partitions: undefined, producers: [], consumers: [] } as unknown as DepRow['kafka'] };
const caller = (role: string) => ({ service: 'svc-a', pod: 'pod-1', role, spanCount: 3, errorCount: 1, errorRate: 33, avgDurationMs: 2, p99DurationMs: 4 });
const QDETAIL = {
  spanCount: 10, errorCount: 1, errorRate: 10, avgDurationMs: 3, p99DurationMs: 5, assumedCluster: true,
  callers: [caller('consumer')], topOps: [{ statement: 'topic-1 publish', count: 2, avgDurationMs: 1, operation: 'publish' }],
  operations: [{ operation: '', spanCount: 3, errorCount: 1, errorRate: 33, avgDurationMs: 1, p50DurationMs: 1, p95DurationMs: 2, p99DurationMs: 3 }],
};
const DBDETAIL = {
  spanCount: 10, errorCount: 0, errorRate: 0, avgDurationMs: 3, p99DurationMs: 5, callers: [],
  topOps: [{ statement: 'SELECT 1', count: 2, avgDurationMs: 1 }],
};

let host: HTMLDivElement | null = null;
let root: Root | null = null;
function render(node: ReactNode) {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  act(() => { root!.render(<MemoryRouter>{node}</MemoryRouter>); });
}
function close() {
  act(() => { root?.unmount(); });
  host?.remove(); host = null; root = null;
  try { localStorage.clear(); } catch { /* jsdom */ }
}
function snap(): string {
  const c = document.body;
  const attrs = [...c.querySelectorAll('[title],[aria-label],[placeholder]')]
    .flatMap(e => ['title', 'aria-label', 'placeholder'].map(a => e.getAttribute(a) ?? ''));
  return [c.textContent ?? '', ...attrs].join('\n');
}
function all(): string {
  const out: string[] = [];
  const take = (node: ReactNode) => { render(node); out.push(snap()); close(); };
  take(<DependenciesTable rows={[DB, RECV]} kind="db" range={RANGE} rowHref={r => `/database?i=${r.instance}`} />);
  take(<DependenciesTable rows={[]} kind="db" range={RANGE} />);
  take(<DependenciesTable rows={[]} kind="queue" range={RANGE} />);
  take(<DependenciesTable rows={[Q, Q2]} kind="queue" range={RANGE} />);
  take(<DependenciesTable rows={[{ ...Q, priorP99Ms: 4 }]} kind="queue" range={RANGE} compare />);
  take(<OpenProblemsMark row={DB} />);
  m.detail = QDETAIL;
  take(<DetailDrawer system="kafka" cluster="cluster-1" name="topic-1" kind="queue" source="spans" range={RANGE} />);
  m.detail = DBDETAIL;
  take(<DetailDrawer system="postgresql" cluster="(default)" name="db-1" instance="db-1" kind="db" source="spans" range={RANGE} />);
  take(<DetailDrawer system="postgresql" cluster="(default)" name="db-1" instance="db-1" dbName="app" kind="db" source="spans" range={RANGE} />);
  take(<DegradedBand reason="timeout" />);
  return out.join('\n');
}

afterEach(() => { close(); m.lang = 'en'; });

const TR_CHARS = /[çğıöşüÇĞİÖŞÜ]/;
const TR_WORDS = /\b(satır|yok|metrik|pencere|servis|kova|Kapsam|Gruplama|Detay)\b/;
// Paylaşılan DataTableHead ⋯ menüsü bu dilimin kapsamı dışında (raporlandı).
const SHARED = ['Tablo seçenekleri'];

describe('DependenciesTable + çekmece — İngilizce arayüz (v0.10.2311)', () => {
  it('Türkçe metin yok', () => {
    const text = all();
    const bad = text.split('\n').filter(l => !SHARED.includes(l) && (TR_CHARS.test(l) || TR_WORDS.test(l)));
    expect(bad).toEqual([]);
    for (const en of [
      '2 problems', '1 problem', '2 problems open right now', ' Click to open the database detail.',
      'Open the database detail page', 'Discovered via receiver', 'call rate · last complete bucket 3.0/s',
      'error rate · Last COMPLETE 5 min bucket', 'No database calls in this window', 'No messaging activity in this window',
      'Open the topic detail page (same window)', 'Number of partitions read by consumers', '2 producer services (names loading)',
      'No consumer metric in VM for this topic', 'kafka_producer_record_send_rate · topic total, records/s',
      'worst partition per topic', 'No VM lag metric for this destination', 'Comparison is off.', 'p99 5.0ms · previous 4.0ms',
      'No produce (publish) spans measured in this window.', 'No cluster given — (default) was assumed.',
      'Detail page →', 'Full-page detail of this topic', 'No producer spans to this destination in this window.',
      'Operations · MV · 1 row', '(not emitted)', '1 failed span', 'Open the traces of this operation', 'Pod detail',
      'Scope: postgresql / db-1 · all databases (every db.name on this instance)', 'Scope: postgresql / db-1 · database app',
      'Grouped by the first 80 characters of the raw statement', 'No callers in this window.',
      '⚠ The numbers in this panel are incomplete. timeout. Fields showing zero do NOT mean “no data”',
    ]) expect(text, en).toContain(en);
  });

  it('Türkçe arayüzde bilinen metin aynen', () => {
    m.lang = 'tr';
    const text = all();
    for (const tr of [
      '2 problem', 'Şu an açık 2 problem (“problem değil” işaretliler hariç). Tıklayınca veritabanı detayı açılır.',
      'Veritabanı detay sayfasını aç', 'çağrı hızı · son tam kova 3.0/s', 'hata oranı · Son TAM 5 dk kovası',
      'Bu pencerede veritabanı çağrısı yok — bu görünüm db.system özniteliği dolu span\'lerden türetilir.',
      'Bu pencerede mesajlaşma etkinliği yok', 'Topic detay sayfasını aç (aynı pencere)', '2 producer servis (adlar yükleniyor)',
      'Bu topic için VM\'de consumer metriği yok', 'kafka_consumer_records_lag_max · topic başına en kötü partition',
      'Bu destination için VM lag metriği yok', 'Karşılaştırma kapalı.', 'p99 5.0ms · önceki 4.0ms',
      'Bu pencerede produce (publish) span\'i ölçülmedi.',
      'Cluster belirtilmedi — (default) varsayıldı. Çok-cluster bir kurulumda bu topic başka bir cluster\'da yaşıyorsa aşağıdaki sayılar boş görünür.',
      'Detay sayfası →', 'Operasyonlar · MV · 1 satır', '(yaymıyor)', '1 hatalı span', 'Bu operasyonun trace\'lerini aç', 'Pod detayı',
      'Kapsam: postgresql / db-1 · tüm veritabanları (bu instance üzerindeki her db.name)', 'Kapsam: postgresql / db-1 · veritabanı app',
      'Gruplama ham ifadenin ilk 80 karakteri. Sayfadaki "En pahalı ifadeler" ve Slow queries katalogu normalize edilmiş ifadenin hash\'ine göre gruplar',
      'Bu pencerede çağıran yok.', '⚠ Bu paneldeki sayılar eksik. timeout. Sıfır görünen alanlar “veri yok” anlamına GELMEZ — okunamadılar.',
    ]) expect(text, tr).toContain(tr);
  });
});
