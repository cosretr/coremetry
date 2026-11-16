// @vitest-environment jsdom
//
// v0.10.2290 — pin: arayüz dili İngilizceyken anomali olay detayı (host
// durumları, özet cümlesi, "ne zaman" satırı, Seyir / Log hacmi / Desen sayısı,
// Kanıt, Teknik ayrıntı — olgu ızgarası, tür rozeti, yön · taban, terfi rozeti,
// davranış kutusu —, Ne yapabilirim, Triyaj) bilinen Türkçe etiket ÇİZMEZ;
// Türkçe arayüzde metin aynen kalır. Saf yardımcılar lang verilmezse Türkçe.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { AnomalyEvent } from '@/lib/types';

const m = vi.hoisted(() => {
  window.matchMedia = ((q: string) => ({
    matches: false, media: q, onchange: null,
    addListener: () => {}, removeListener: () => {},
    addEventListener: () => {}, removeEventListener: () => {}, dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
  return { lang: 'en' as 'en' | 'tr', events: {} as Record<string, unknown>, mode: 'ok' as 'ok' | 'missing' | 'fail', logEmpty: false, row: null as unknown };
});
vi.mock('@/lib/i18n', async (orig) => ({ ...(await orig<typeof import('@/lib/i18n')>()), useLang: () => m.lang }));
vi.mock('@/lib/api', () => {
  const stub: Record<string, (...a: unknown[]) => Promise<unknown>> = {
    anomalyEvent: (id: unknown) => {
      if (m.mode === 'missing') return Promise.reject(new Error('HTTP 404: anomaly not found'));
      if (m.mode === 'fail') return Promise.reject(new Error('HTTP 500'));
      return Promise.resolve(m.events[String(id)] ?? null);
    },
    problemVerdicts: async () => ({ verdicts: [], policy: { muteNotifications: false } }),
    anomalyUnifiedRow: async () => m.row, // v0.10.2302 — kanıt satırındaki ML üyeleri
    servicesMetadata: async () => ({ 'svc-a': { ownerTeam: 'team-1', sreTeam: 'team-2' } }),
    anomalyLogPatternSeries: async () => ({
      pattern: 'p', bucketSec: 60, from: 0, to: 120e9, points: [{ t: 0, v: 3 }, { t: 60e9, v: 900 }],
      topServices: [{ service: 'svc-b', count: 3 }],
    }),
  };
  return {
    api: new Proxy({}, { get: (_t, k: string) => stub[k] ?? (() => Promise.resolve(null)) }),
    isCanceled: () => false,
  };
});
vi.mock('@/components/AuthProvider', () => ({
  useAuth: () => ({ user: { username: 'op', email: 'op@x', role: 'editor' }, loading: false }),
}));
vi.mock('@/components/Topbar', () => ({ Topbar: ({ title }: { title: string }) => <div data-topbar>{title}</div> }));
vi.mock('@/components/ShareButton', () => ({ ShareButton: () => null }));
vi.mock('@/components/ai/AIExplainButton', () => ({ AIExplainButton: () => null }));
vi.mock('@/components/LogsHistogram', async () => {
  const React = await vi.importActual<typeof import('react')>('react');
  return {
    LogsHistogram: ({ onSeries }: { onSeries?: (s: { name: string; total: number }[] | null) => void }) => {
      React.useEffect(() => { onSeries?.(m.logEmpty ? [] : null); }, [onSeries]);
      return <div data-loghist />;
    },
  };
});
vi.mock('@/components/CosreChart', () => ({
  CosreChart: ({ presentation }: { presentation?: { title: string; seriesName: string; emptyNote?: string } }) =>
    <div data-chart-title={presentation?.title ?? ''} data-series={presentation?.seriesName ?? ''} data-empty={presentation?.emptyNote ?? ''} />,
}));
vi.mock('@/components/chart/corePanelEntry', () => ({
  CorePanelMulti: ({ ariaLabel, items, error }: { ariaLabel?: string; items?: { name: string }[]; error?: string }) =>
    <div data-aria={ariaLabel ?? ''} data-series={items?.[0]?.name ?? ''} data-error={error ?? ''} />,
}));
vi.mock('@/components/RootCauseRibbon', () => ({ RootCauseRibbon: () => <div data-rc /> }));

import { AnomalyEventHost } from './AnomalyEventDetail';
import { anomalyDirectionBaselineLine, anomalyKindPlain, anomalyPromotionBadge, hourOfWeekLabel, spikeDurationLabel } from './anomalyDetail';
import { anomalySummary, anomalyWhenLine, behaviorMetricLabel, recurrenceTitle } from './detailSummary';
import { bucketLabel, verifiedRatioNote } from './logPatternSeries';
import { fmtDeltaTR, fmtDurationShort, insightCells } from './problemInsight';
import { windowLabel } from './alertMetricSeries';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
class NoopResizeObserver { observe() {} unobserve() {} disconnect() {} }
(globalThis as unknown as { ResizeObserver: unknown }).ResizeObserver = NoopResizeObserver;

const T0 = 1_700_000_000e9;
const ev = (over: Partial<AnomalyEvent> = {}): AnomalyEvent => ({
  id: 'a1', kind: 'trace_op', pattern: 'GET /x', service: 'svc-a',
  startedAt: T0, lastSeen: T0 + 600e9, peakRatio: 4.2, currentRatio: 3.1, currentCount: 12,
  sample: '4bf92f3577b34da6a3ce929d0e0e4736', status: 'active',
  direction: 'up', baselineKind: 'seasonal', baselineValue: 2,
  promotion: 'shadow', subjectKind: 'endpoint', promotionGroupSize: 3,
  episodeCount: 3, firstStartedAt: T0 - 86400e9,
  ...over,
} as AnomalyEvent);
const behavior = JSON.stringify({
  metric: 'error_rate', ratio: 3, direction: 'up', baseline: 1, current: 3, unit: '%', z: 5.2, dwell: 3,
  hourOfWeek: 26, signal: 'regime', deploy: { version: 'v2', ageSeconds: 600 },
});

let host: HTMLDivElement | null = null;
let root: Root | null = null;
const settle = (ms = 40) => act(async () => { await new Promise(r => setTimeout(r, ms)); });
const close = () => { act(() => { root?.unmount(); }); host?.remove(); host = null; root = null; };

async function mount(id: string) {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false, retryDelay: 0 } } });
  await act(async () => {
    root!.render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={[`/inbox?anomaly=${id}`]}>
          <AnomalyEventHost id={id} isAdmin onBack={() => {}} />
        </MemoryRouter>
      </QueryClientProvider>,
    );
  });
  await settle();
  // Teknik ayrıntıyı aç (kapalıyken mount edilmez).
  const tech = [...host.querySelectorAll<HTMLButtonElement>('button[aria-expanded="false"]')];
  for (const b of tech) act(() => { b.click(); });
  await settle();
  return { text: host.textContent ?? '', html: host.innerHTML };
}

async function all() {
  const out: { text: string; html: string }[] = [];
  m.events = {
    a1: ev(),
    a2: ev({ id: 'a2', kind: 'behavior_change', pattern: 'error_rate', sample: behavior }),
    a3: ev({ id: 'a3', kind: 'log_template_new', pattern: 'tpl', sample: 'line', status: 'cleared' }),
    a4: ev({ id: 'a4', kind: 'log_pattern', pattern: 'pat-1', service: '', sample: 'x' }),
    a5: ev({ id: 'a5', kind: 'elastic_ml', pattern: 'job-1', sample: 'x' }),
    a6: ev({ id: 'a6', kind: 'trace_op_latency', pattern: 'GET /y', baselineKind: 'none', direction: undefined, lastSeen: T0 }),
    a7: ev({ id: 'a7', service: '' }),
  };
  for (const id of ['a1', 'a2', 'a3', 'a4', 'a6', 'a7']) { out.push(await mount(id)); close(); }
  m.logEmpty = false;
  out.push(await mount('a5')); close();
  m.logEmpty = true;
  out.push(await mount('a5')); close();
  m.mode = 'missing'; out.push(await mount('a1')); close();
  m.mode = 'fail'; out.push(await mount('a1')); close();
  m.mode = 'ok';
  return { text: out.map(r => r.text).join('\n'), html: out.map(r => r.html).join('\n') };
}

beforeEach(() => { m.lang = 'en'; m.mode = 'ok'; m.logEmpty = false; m.row = null; });
afterEach(() => { close(); document.body.innerHTML = ''; });

const TR_LABELS = [
  'Anomali', 'servisinde', 'normalin', 'katına', ' başladı ·', 'sürüyor', 'yinelenen', 'Seyir', 'Log hacmi', 'Desen sayısı',
  'tüm servisler', 'Kanıt', 'Kök neden', 'Teknik ayrıntı', 'Tür:', 'Yön · taban', 'artış', 'mevsimsel', 'Gölge', 'rota',
  'Başladı', 'Son görülme', 'Süre', 'En yüksek artış', 'Şimdiki artış', 'Penceredeki sayım', 'Kalıcı kayma', 'ŞİMDİ',
  'DEĞİŞİM', 'SÜRDÜ', 'KIYAS', 'kaymadan', 'dk önce', 'Ne yapabilirim', 'Logları aç', 'olay penceresi', "Hatalı trace",
  "Operasyonun", 'Örnek trace', 'Servis sayfası', 'Ekip', 'Kalıbı kopyala', 'servis adı taşımıyor', 'En çok',
  'Triyaj', 'Susturma', 'Tekrar dene', 'yüklenemedi', 'kaydı yok', 'hacmi okunamadı', 'satır biçimi', 'kalıp defteri', 'Kalıp metni', 'desen ',
  'pencere ', 'kimlik', 'hata sayısı', 'gecikme', 'eşleşen log', 'Pzt', 'Sal ', 'Yinelenen',
];
const leaked = (r: { text: string; html: string }) => TR_LABELS.filter(l => r.text.includes(l) || r.html.includes(l));

describe('Anomali olay detayı — İngilizce arayüz (v0.10.2290)', () => {
  it('host durumları, özet, grafikler, kanıt, teknik ayrıntı, eylemler: Türkçe etiket yok', async () => {
    const r = await all();
    expect(leaked(r), `Türkçe etiket İngilizce arayüzde: ${leaked(r).join(' | ')}`).toEqual([]);
    for (const en of [
      'In svc-a, errors for GET /x rose to 4.2× normal.', 'In svc-a, error rate rose to 3× normal (1% → 3%).',
      'This line shape was seen for the first time in this service (not in the template registry); 12 lines',
      'ongoing', 'recurring · occurrence 3', 'Trend', 'Evidence', 'Technical details', 'Kind: Operation error',
      'Direction · baseline: increase · seasonal baseline (same weekday, same hour) 2', 'Shadow: would have opened a Problem (3 routes)',
      'Started', 'Last seen', 'Duration', 'Peak increase', 'Current increase', 'Count in window',
      'Persistent shift', 'NOW', 'CHANGE', 'LASTED', '3 × 5 min', 'COMPARED HOUR', 'Tue 02:00 UTC', 'landed', '10 min', 'before the shift.',
      'What can I do', 'Open logs', "The operation's error traces", 'event window', 'Sample trace', 'Service page', 'with the event window',
      'Team', 'This event carries no service name', 'Most: svc-b', 'Pattern count', 'all services', 'Log volume',
      'No series for this window', 'Could not read the log volume', 'No anomaly record', 'Could not load the anomaly', 'Try again',
      'Kind: Operation latency', 'Direction · baseline: direction unknown · no baseline (first seen)', 'single measurement',
    ]) expect(r.text + r.html, en).toContain(en);
    for (const en of ['aria-label="Triage"', 'aria-label="Mute duration"', 'title="Copy template"', 'data-series="error count"',
      'data-chart-title="svc-a · error rate"', 'data-aria="Pattern count"', 'data-series="matching logs"']) expect(r.html, en).toContain(en);
  });

  it('saf yardımcılar: lang verilmezse Türkçe aynen', () => {
    const e = ev();
    expect(anomalySummary(e, null)).toBe('svc-a servisinde GET /x için hata normalin 4.2 katına çıktı.');
    expect(anomalySummary(e, null, 'en')).toBe('In svc-a, errors for GET /x rose to 4.2× normal.');
    expect(anomalySummary({ ...e, kind: 'log_pattern', service: '' }, null)).toBe('Loglarda bu desen normalin 4.2 katına çıktı.');
    expect(anomalySummary({ ...e, kind: 'log_pattern', service: '' }, null, 'en')).toBe('In the logs, this pattern rose to 4.2× normal.');
    expect(anomalySummary({ ...e, peakRatio: 0 }, null)).toBe('svc-a servisinde olağan dışı bir değişim saptandı.');
    expect(anomalySummary({ ...e, peakRatio: 0, service: '' }, null, 'en')).toBe('In a service, an unusual change was detected.');
    expect(behaviorMetricLabel('ext:q1')).toBe('q1 (dış kaynak)');
    expect(behaviorMetricLabel('ext:q1', 'en')).toBe('q1 (external source)');
    expect(anomalyDirectionBaselineLine({ direction: 'down', baselineKind: 'consecutive', baselineValue: 5 })).toBe('düşüş · son 24 saatin tabanı 5');
    expect(anomalyDirectionBaselineLine({ direction: 'down', baselineKind: 'consecutive', baselineValue: 5 }, 'en')).toBe('decrease · last 24 h baseline 5');
    expect(anomalyDirectionBaselineLine({ kind: 'subject_red', pattern: 'consumer_lag @ queue:kafka@t1', direction: 'up', baselineKind: 'seasonal', baselineValue: 7 }))
      .toBe('artış · mevsimsel taban (haftanın aynı günü, aynı saat) 7 kayıt');
    expect(anomalyDirectionBaselineLine({ kind: 'subject_red', pattern: 'consumer_lag @ queue:kafka@t1', direction: 'up', baselineKind: 'seasonal', baselineValue: 7 }, 'en'))
      .toBe('increase · seasonal baseline (same weekday, same hour) 7 records');
    expect(anomalyDirectionBaselineLine({})).toBe('bilinmiyor');
    expect(anomalyPromotionBadge({ promotion: 'live', subjectKind: 'endpoint', promotionGroupSize: 1 })).toBe('Problem açıldı (1 rota)');
    expect(anomalyPromotionBadge({ promotion: 'live', subjectKind: 'endpoint', promotionGroupSize: 1 }, 'en')).toBe('Problem opened (1 route)');
    expect(anomalyKindPlain('calendar_traffic_drop')).toBe('Takvim günü düşüşü');
    expect(anomalyKindPlain('zzz', 'en')).toBe('zzz');
    expect(hourOfWeekLabel(50)).toBe('Çar 02:00 UTC');
    expect(hourOfWeekLabel(50, 'en')).toBe('Wed 02:00 UTC');
    expect(spikeDurationLabel('active', 0, '')).toBe('tek ölçüm');
    expect(spikeDurationLabel('active', 5e9, '5s', 'en')).toBe('5s · ongoing');
    const rec = { count: 2, firstNs: null };
    expect(recurrenceTitle(rec)).toBe('Yinelenen anomali: bu 2. kez. Sayaç kaydın ömrüyle sınırlı (son tetiklenmeden 30 gün sonra kayıt düşer).');
    expect(recurrenceTitle(rec, undefined, { version: 'v1', ageSeconds: 120 } as never, 'en'))
      .toBe("Recurring anomaly: occurrence 2. The counter is bounded by the record's lifetime (the record drops 30 days after the last trigger).\ndeploy v1 2 min ago — it was already seen before");
    const when = anomalyWhenLine({ ...e, status: 'active' }, () => 'TS', () => 'HH');
    expect(when).toBe('TS başladı · 10m · sürüyor · yinelenen · bu 3. kez · ilk kez TS');
    expect(bucketLabel(86400)).toBe('1 gün');
    expect(bucketLabel(86400, 'en')).toBe('1 day');
    expect(verifiedRatioNote(0.4)).toBe('token eşleşmesi · örneklemde %40 doğrulandı');
    expect(windowLabel(600)).toBe('10 dk pencere');
    expect(windowLabel(3600, 'en')).toBe('1 h window');
    expect(fmtDurationShort(7200)).toBe('2.0 sa');
    expect(fmtDeltaTR(-120)).toBe('2 dk önce');
    expect(fmtDeltaTR(120, 'en')).toBe('2 min after');
    const cells = insightCells({ hypothesisComputed: false, similar: null, rollout: null, firstAnomaly: null } as never, s => s);
    expect(cells.map(c => `${c.label}=${c.text}`)).toEqual(['şüpheli=hipotez yok', 'ilk anomali=—', 'rollout=—', 'daha önce=ilk kez']);
    const cellsEn = insightCells({ hypothesisComputed: true, similar: null, rollout: null, firstAnomaly: null } as never, s => s, 'en');
    expect(cellsEn.map(c => `${c.label}=${c.text}`)).toEqual(['suspect=no clear cause', 'first anomaly=—', 'rollout=—', 'seen before=first time']);
  });
});

describe('Anomali olay detayı — Türkçe arayüzde metin aynen (v0.10.2290)', () => {
  it('özet, grafikler, kanıt, teknik ayrıntı, eylemler', async () => {
    m.lang = 'tr';
    const r = await all();
    for (const tr of [
      'svc-a servisinde GET /x için hata normalin 4.2 katına çıktı.', 'svc-a servisinde hata oranı normalin 3 katına çıktı (1% → 3%).',
      'Bu satır biçimi bu serviste ilk kez görüldü (kalıp defterinde yok); 12 satır', 'sürüyor', 'yinelenen · bu 3. kez',
      'Seyir', 'Kanıt', 'Teknik ayrıntı', 'Tür: Operasyon hatası',
      'Yön · taban: artış · mevsimsel taban (haftanın aynı günü, aynı saat) 2', 'Gölge: Problem açılırdı (3 rota)',
      'Başladı', 'Son görülme', 'Süre', 'En yüksek artış', 'Şimdiki artış', 'Penceredeki sayım',
      'Kalıcı kayma — haftanın aynı saatine göre sürüyor', 'ŞİMDİ', 'DEĞİŞİM', 'SÜRDÜ', '3 × 5 dk', 'KIYAS SAATİ', 'Sal 02:00 UTC',
      'Deploy v2 kaymadan 10 dk önce indi.',
      'Ne yapabilirim', 'Logları aç', "Operasyonun hatalı trace'leri", 'olay penceresi', 'Örnek trace', 'tespit anındaki en çarpıcı istek',
      'Servis sayfası', 'olay penceresiyle', 'Ekip', 'Bu olay bir servis adı taşımıyor — log / trace / servis pivotları bir servis adı gerektiriyor.',
      'En çok: svc-b', 'Desen sayısı', 'tüm servisler', 'Log hacmi', 'Bu pencere için seri yok — olay saklama süresinin dışında olabilir.',
      'Log hacmi okunamadı — bu bir hata, boş sonuç değil.', 'Anomali kaydı yok', 'Anomali yüklenemedi', 'Tekrar dene',
      'Tür: Operasyon gecikmesi', 'Yön · taban: yön bilinmiyor · taban yok (ilk görülme)', 'tek ölçüm',
    ]) expect(r.text + r.html, tr).toContain(tr);
    for (const tr of ['aria-label="Triyaj"', 'aria-label="Susturma süresi"', 'title="Kalıbı kopyala"', 'data-series="hata sayısı"',
      'data-chart-title="svc-a · hata oranı"', 'data-aria="Desen sayısı"', 'data-series="eşleşen log"']) expect(r.html, tr).toContain(tr);
  });
});

// v0.10.2302 — kanıt satırındaki ML üyesinin sunucu cümlesi (summary) İngilizce
// arayüzde TR işaretli; summary yoksa dedektör adı işaretsiz; katalogdan kurulan
// özet cümlesi ve eski olay satırı işaretsiz. Türkçe arayüzde işaret yok.
describe('Anomali olay detayı — ML sunucu cümlesi işareti (v0.10.2302)', () => {
  const ml = (id: string, summary: string) => ({ id, detector: 'op_latency', signal: 'trace', stage: 'shadow', service: 'svc-a',
    startedAt: T0, lastSeen: T0 + 600e9, windowSec: 300, baseline: 1, observed: 3, score: 5, direction: 'up', summary });
  const withRow = () => {
    m.events = { a1: ev() };
    m.row = { items: [], total: 0, openTotal: 0, doneTotal: 0, offset: 0, limit: 1, truncated: false,
      row: { key: 'k', service: 'svc-a', start: T0, end: T0 + 600e9, signals: ['trace'], status: 'running', mlCount: 2, legacyCount: 1,
        ml: [ml('m1', 'svc-a: GET /x yanıt süresi arttı.'), ml('m2', '')], legacy: [] } };
  };
  const rows = (html: string) => [...new DOMParser().parseFromString(html, 'text/html')
    .querySelectorAll('[data-testid="anomaly-evidence-rows"] li')];
  it('İngilizce: ML sunucu cümlesi işaretli, gerisi işaretsiz', async () => {
    withRow();
    const r = await mount('a1'); close();
    const li = rows(r.html);
    expect(li.length).toBe(3);
    const marked = li.filter(x => x.querySelector('span[lang="tr"] [data-orig-lang]'));
    expect(marked.length).toBe(1);
    expect(marked[0].querySelector('span[lang="tr"]')!.textContent).toBe('svc-a: GET /x yanıt süresi arttı.TR');
    expect(r.text).toContain('In svc-a, errors for GET /x rose to 4.2× normal.');
    expect((r.html.match(/data-orig-lang/g) ?? []).length).toBe(1);
  });
  it('Türkçe: işaret yok, satır metni aynen', async () => {
    m.lang = 'tr';
    withRow();
    const r = await mount('a1'); close();
    expect(r.html).not.toContain('data-orig-lang');
    expect(r.html).not.toContain('lang="tr"');
    const li = rows(r.html);
    expect(li[1].children[1].innerHTML).toBe('svc-a: GET /x yanıt süresi arttı.');
    expect(li[2].children[1].innerHTML).toBe('op_latency');
  });
});
