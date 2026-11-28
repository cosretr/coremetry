// @vitest-environment jsdom
//
// v0.10.2284 — pin: arayüz dili İngilizceyken Problem detayı (AlertProblemDetail:
// başlık özeti, üst veri satırı, triyaj, dört kısa kutu, kısa geçmiş, Teknik
// kanıt altındaki standart kartlar, kural, ilişkili sinyaller, bölüm başlıkları;
// AlertProblemHost boş / hata durumları) bilinen Türkçe etiket ÇİZMEZ; Türkçe
// arayüzde metin aynen kalır. AnomalyStreamsPage.lang.test şeklinde. Ağır
// alt paneller (kök neden, araştırma, log kanıtı, bildirim, runbook, vaka
// kaydı) burada taklit; kapsam dışı ve ayrıca listelendi.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { Problem, ProblemStandard } from '@/lib/types';

const m = vi.hoisted(() => {
  window.matchMedia = ((q: string) => ({
    matches: false, media: q, onchange: null,
    addListener: () => {}, removeListener: () => {},
    addEventListener: () => {}, removeEventListener: () => {}, dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
  return { lang: 'en' as 'en' | 'tr', std: null as unknown, problems: { items: [] as unknown[] }, byId: null as unknown };
});
vi.mock('@/lib/i18n', async (orig) => ({ ...(await orig<typeof import('@/lib/i18n')>()), useLang: () => m.lang }));
vi.mock('@/lib/api', () => {
  const stub: Record<string, (...a: unknown[]) => Promise<unknown>> = {
    serviceOperations: async () => [],
    problemVerdicts: async () => ({ verdicts: [] }),
    servicesMetadata: async () => ({}),
    problemStandard: async () => m.std,
    problemConversations: async () => [],
    problemNotes: async () => [],
    problems: async () => m.problems,
    problemByID: async () => m.byId,
  };
  return {
    api: new Proxy({}, { get: (_t, k: string) => stub[k] ?? (() => Promise.resolve(null)) }),
    isCanceled: () => false,
  };
});
vi.mock('@/components/AuthProvider', () => ({
  useAuth: () => ({ user: { username: 'op', email: 'op@x', role: 'admin' }, loading: false }),
}));
vi.mock('./ProblemInsightStrip', () => ({ ProblemInsightStrip: () => null }));
vi.mock('@/components/RootCausePanel', () => ({ RootCausePanel: () => null }));
vi.mock('./ExternalEvidencePanel', () => ({ ExternalEvidencePanel: () => null }));
vi.mock('@/components/AffectedEntitiesList', () => ({ AffectedEntitiesList: () => null }));
vi.mock('./ProblemLogEvidence', () => ({ ProblemLogEvidence: () => null }));
vi.mock('./ProblemNotifyPanel', () => ({ ProblemNotifyPanel: () => null }));
vi.mock('./ProblemInvestigationPanel', () => ({ ProblemInvestigationPanel: () => null }));
vi.mock('./ProblemNewLogTemplates', () => ({ ProblemNewLogTemplates: () => null }));
vi.mock('./IncidentCaseCard', () => ({ IncidentCaseCard: () => null }));
vi.mock('@/components/ProblemRunbookPanel', () => ({ ProblemRunbookPanel: () => null }));
vi.mock('@/components/ai/AIExplainButton', () => ({ AIExplainButton: () => null }));
vi.mock('@/components/ShareButton', () => ({ ShareButton: () => null }));
vi.mock('./AlertMetricChartSection', () => ({ AlertMetricChartSection: () => null }));

import { AlertProblemDetail } from './ProblemDetail';
import { AlertProblemHost } from './ProblemsSection';
import { alertProblemSummary, problemWhenLine } from './detailSummary';
import { alertCause, alertWhoText } from './alertDetailBoxes';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const NOW_NS = Date.now() * 1e6;
const prob = (over: Partial<Problem> = {}): Problem => ({
  id: 'p1', displayId: 'P-1001', ruleId: 'builtin:p99_ms', ruleName: 'Slow checkout', severity: 'critical', priority: 'P1',
  service: 'svc-a', metric: 'p99_ms', value: 812, threshold: 500, status: 'acknowledged', assignee: 'team-x',
  description: 'p99 above 500 ms', startedAt: NOW_NS - 95 * 60e9, priorityReason: 'sustained 30 min',
  ownerTeam: 'team-a', sreTeam: 'team-b', pod: 'svc-a-7d9f', clusters: ['cluster-1'],
  rootCause: { topSuspect: 'svc-b', confidence: 0.82 }, recentDeploy: { version: 'v2.3.1', ageSeconds: 600 },
  runbookUrl: 'https://runbook.example/x',
  ...over,
} as Problem);
const STD = {
  family: 'latency', kind: 'rule', subject: 'svc-a', headline: '',
  impact: { primary: { text: 'p99 812 ms', value: 812, normal: 300 }, known: true, failedPerMin: 12, callers: ['svc-c', 'svc-d'] },
  since: { startedAt: NOW_NS - 95 * 60e9, durationSec: 95 * 60, ongoing: true },
  evidence: { sentence: 'above the threshold for 30 min', source: 'span', sampled: true },
  confidence: { level: 'medium', gates: [{ id: 'G1', name: 'traffic', status: 'pass' }] },
  nextStep: { text: 'check the slow operations' }, context: ['recent deploy v2.3.1'],
} as unknown as ProblemStandard;

let host: HTMLDivElement | null = null;
let root: Root | null = null;
const settle = () => act(async () => { await new Promise(r => setTimeout(r, 30)); });

async function mount(node: React.ReactNode): Promise<HTMLElement> {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => {
    root!.render(<QueryClientProvider client={qc}><MemoryRouter>{node}</MemoryRouter></QueryClientProvider>);
  });
  await settle();
  return host;
}
async function openTech(el: HTMLElement) {
  const t = [...el.querySelectorAll<HTMLElement>('.pb-sect')].find(x => (x.querySelector(':scope > .h')?.textContent ?? '').match(/Technical evidence|Teknik kanıt/))!;
  act(() => { t.querySelector<HTMLButtonElement>(':scope > .h button[aria-expanded]')!.click(); });
  await settle();
}
const snap = (el: HTMLElement) => ({ text: el.textContent ?? '', html: el.innerHTML });

beforeEach(() => { m.lang = 'en'; m.std = STD; m.problems = { items: [] }; m.byId = null; });
afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  host = null; root = null;
});

// Bu ekranda bugün Türkçe çizilen etiketler (sunucu verisi değil).
const TR_LABELS = [
  'Servis', 'Ekip', 'Tür', 'Alarm', 'Ne zamandır', 'İlgilenen', 'kimse', 'başladı', 'sürüyor', 'Ben ilgileniyorum', 'Sende',
  'Başkasına ata', 'Gerçek problem', 'Problem değil', 'İşareti kaldır', 'işaretlenmedi', 'Aynısı yeniden', 'Ne oluyor',
  'Kimi etkiliyor', 'Olası neden', 'Ne yapılabilir', 'değeri', 'eşik', 'Öncelik gerekçesi', 'etkileniyor', 'çağıran',
  'küme:', 'En olası', 'deploy edildi', 'güven', "Yavaş trace", 'Loglar', 'Servis sayfası', 'penceresi', 'Kısa geçmiş',
  'Teknik kanıt', 'Görüntü kimliği', 'Triyaj', 'Kural', 'Kimlik', 'Şiddet', 'Etki', 'Ne zamandan beri', 'Güven', 'ORTA',
  'Kaynak', 'örneklenmiş', 'Neden böyle', 'Bağlam', 'önceliği etkilemez', 'Sonraki adım', 'öneri', 'hatalı istek',
  'doğrulanmış', 'Operasyonlar', 'yalnız hatalar', 'desenler', 'Log kanıtı', 'Bildirim', 'runbook bağlantısı',
  'Bu podun', 'CoSRE sohbetleri', 'konuşmalarınız', 'Bu probleme bağlı', 'Yinelenen',
];
const leaked = (r: { text: string; html: string }) => TR_LABELS.filter(l => r.text.includes(l) || r.html.includes(l));

describe('Problem detayı — İngilizce arayüz (v0.10.2284)', () => {
  it('başlık, üst veri, triyaj, kutular ve Teknik kanıt: Türkçe etiket yok', async () => {
    const el = await mount(<AlertProblemDetail problem={prob()} isAdmin onBack={() => {}} onChanged={() => {}} />);
    await openTech(el);
    const r = snap(el);
    expect(leaked(r), `Türkçe etiket İngilizce arayüzde: ${leaked(r).join(' | ')}`).toEqual([]);
    for (const en of [
      'Slow checkout — svc-a: p99_ms value 812.00, threshold 500.00.',
      'Service', 'Team', 'SRE', 'Kind', 'Alert', 'Since', 'Assignee', 'started ', 'ongoing',
      "I'm on it", 'Assign to someone else…', 'Is this a real problem?', 'Real problem', 'Not a problem',
      'If the same thing comes again it falls into the same class; can be undone.',
      'What is happening', 'Priority reason: sustained 30 min.', 'Who is affected',
      'svc-a is affected (p99 812 ms) · 2 calling services: svc-c, svc-d · pod svc-a-7d9f · cluster: cluster-1.',
      'Likely cause', 'Most likely source: svc-b. v2.3.1 was deployed 10 min before the start.', '82% confidence',
      'What can be done', 'Slow traces', '≥ 500 ms (threshold), problem window', 'Logs (errors only)', 'Service page',
      'Recent history', 'Technical evidence', 'Impact', '~12 failed requests/min · 2 callers (verified)', 'Confidence', 'MEDIUM',
      'Source', 'span', 'sampled', 'Why we assess it this way', 'Context', '(does not affect priority)', 'Next step',
      '(suggestion — the system changes nothing)', 'Details', '≡ Logs (errors only)', '≡ Logs (patterns)',
      'service, problem window, Patterns panel open', 'Log evidence', 'ERROR+ patterns, problem window · loads when opened',
      'Notifications', 'has a runbook link', 'CoSRE conversations',
    ]) expect(r.text, en).toContain(en);
    for (const en of ['Display id — click to copy', 'aria-label="Triage"', 'aria-label="Problem summary"',
      "This pod's JMX/runtime charts", 'Confidence: MEDIUM (measured gates only)']) expect(r.html, en).toContain(en);
  });

  it('host: kayıt yok / yüklenemedi İngilizce', async () => {
    const el = await mount(<AlertProblemHost id="gone" isAdmin onBack={() => {}} />);
    const r = snap(el);
    expect(r.text).toContain('No problem record');
    expect(r.text).toContain('No problem was found with this id');
    expect(r.text).not.toContain('Problem kaydı yok');
  });

  it('saf yardımcılar: lang verilince İngilizce, verilmezse Türkçe aynen', () => {
    const p = prob();
    expect(alertProblemSummary(p, 'en')).toBe('Slow checkout — svc-a: p99_ms value 812.00, threshold 500.00.');
    expect(alertProblemSummary(p)).toBe('Slow checkout — svc-a: p99_ms değeri 812.00, eşik 500.00.');
    expect(alertCause(p, 'en')?.confidence).toBe('82% confidence');
    expect(alertCause(p)?.confidence).toBe('%82 güven');
    expect(alertWhoText(p, STD)).toBe('svc-a etkileniyor (p99 812 ms) · çağıran 2 servis: svc-c, svc-d · pod svc-a-7d9f · küme: cluster-1.');
    const fmt = () => '16.11 10:00';
    const rec = { ...p, ruleId: 'anomaly-auto:x', episodeCount: 3, firstStartedAt: NOW_NS - 86400e9, recentDeploy: undefined, priorDeploy: { version: 'v1', ageSeconds: 120 } } as Problem;
    expect(problemWhenLine(rec, NOW_NS, fmt, fmt, 'en')).toMatch(/^started 16\.11 10:00 · .* · ongoing · recurring · occurrence 3 · first 16\.11 10:00 · deploy v1 2 min ago \(seen before it too\)$/);
    expect(problemWhenLine(rec, NOW_NS, fmt, fmt)).toMatch(/^16\.11 10:00 başladı · .* · sürüyor · yinelenen · bu 3\. kez · ilk kez 16\.11 10:00 · deploy v1 2 dk önce \(öncesinde de görülüyordu\)$/);
  });
});

describe('Problem detayı — Türkçe arayüzde metin aynen (v0.10.2284)', () => {
  it('sayfa', async () => {
    m.lang = 'tr';
    const el = await mount(<AlertProblemDetail problem={prob()} isAdmin onBack={() => {}} onChanged={() => {}} />);
    await openTech(el);
    const r = snap(el);
    for (const tr of [
      'Slow checkout — svc-a: p99_ms değeri 812.00, eşik 500.00.', 'Servis', 'Ekip', 'Tür', 'Alarm', 'Ne zamandır', 'İlgilenen',
      'başladı', 'sürüyor', 'Ben ilgileniyorum', 'Başkasına ata…', 'Gerçek problem mi?', 'Gerçek problem', 'Problem değil',
      'Aynısı yeniden gelirse aynı sınıfa düşer; geri alınabilir.', 'Ne oluyor', 'Öncelik gerekçesi: sustained 30 min.',
      'Kimi etkiliyor', 'svc-a etkileniyor (p99 812 ms) · çağıran 2 servis: svc-c, svc-d · pod svc-a-7d9f · küme: cluster-1.',
      'Olası neden', 'En olası kaynak: svc-b. Başlangıçtan 10 dk önce v2.3.1 deploy edildi.', '%82 güven', 'Ne yapılabilir',
      "Yavaş trace'ler", '≥ 500 ms (eşik), problem penceresi', 'Loglar (yalnız hatalar)', 'Servis sayfası', 'problem penceresiyle',
      'Kısa geçmiş', 'Teknik kanıt', 'Etki', '~12 hatalı istek/dk · 2 çağıran (doğrulanmış)', 'Ne zamandan beri', 'Güven', 'ORTA',
      'Kaynak', 'örneklenmiş', 'Neden böyle değerlendiriyoruz', 'Bağlam', '(önceliği etkilemez)', 'Sonraki adım',
      '(öneri — sistem hiçbir şeyi değiştirmez)', 'Ayrıntılar', '≡ Logs (yalnız hatalar)', '≡ Loglar (desenler)',
      'service, problem window, Desenler paneli açık', 'Log kanıtı', 'ERROR+ desenler, problem penceresi · açılınca yüklenir',
      'Bildirim', 'runbook bağlantısı var', 'CoSRE sohbetleri',
    ]) expect(r.text, tr).toContain(tr);
    for (const tr of ['Görüntü kimliği — kopyalamak için tıkla', 'aria-label="Triyaj"', 'aria-label="Problem özeti"',
      'Bu podun JMX/runtime grafikleri', 'Güven: ORTA (yalnız ölçülmüş kapılardan)']) expect(r.html, tr).toContain(tr);
  });
});
