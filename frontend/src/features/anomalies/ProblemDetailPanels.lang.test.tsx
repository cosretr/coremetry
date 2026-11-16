// @vitest-environment jsdom
//
// v0.10.2285 — pin: arayüz dili İngilizceyken Problem detayının alt panelleri
// (RootCausePanel, ProblemInvestigationPanel + InvestigationResult,
// IncidentCaseCard, ExternalEvidencePanel) bilinen Türkçe etiket ÇİZMEZ;
// Türkçe arayüzde metin aynen kalır. AlertProblemDetail.lang.test şeklinde.
// Sunucu metni (adım özeti, bulgu gerekçesi, kanıt notları) kapsam dışı.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { Problem } from '@/lib/types';

const m = vi.hoisted(() => {
  window.matchMedia = ((q: string) => ({
    matches: false, media: q, onchange: null,
    addListener: () => {}, removeListener: () => {},
    addEventListener: () => {}, removeEventListener: () => {}, dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
  return { lang: 'en' as 'en' | 'tr', rc: null as unknown, inv: null as unknown, kase: null as unknown };
});
vi.mock('@/lib/i18n', async (orig) => ({ ...(await orig<typeof import('@/lib/i18n')>()), useLang: () => m.lang }));
vi.mock('@/components/charts/TimeChart', () => ({ TimeChart: () => null }));
vi.mock('./ProblemInvestigationCards', () => ({ ProblemInvestigationCards: () => null }));
vi.mock('./ProblemInvestigationRecommendations', () => ({ ProblemInvestigationRecommendations: () => null }));
vi.mock('@/lib/api', () => {
  const stub: Record<string, (...a: unknown[]) => Promise<unknown>> = {
    problemRootCauseCore: async () => m.rc,
    problemRootCauseBubbleUp: async () => null,
    problemInvestigation: async () => m.inv,
    sreStatus: async () => ({ enabled: true, launcher: 'none', roleSeen: false, pods: [], queueDepth: 3, running: 0 }),
    incidentCase: async () => m.kase,
    metricQueryFull: async () => ({ series: [] }),
  };
  return {
    api: new Proxy({}, { get: (_t, k: string) => stub[k] ?? (() => Promise.resolve(null)) }),
    isCanceled: () => false,
  };
});
vi.mock('@/components/AuthProvider', () => ({
  useAuth: () => ({ user: { username: 'op', email: 'op@x', role: 'editor' }, loading: false }),
}));

import { RootCausePanel } from '@/components/RootCausePanel';
import { ProblemInvestigationPanel } from './ProblemInvestigationPanel';
import { IncidentCaseCard } from './IncidentCaseCard';
import { ExternalEvidencePanel } from './ExternalEvidencePanel';
import { caseSignalLines, independenceNote } from './incidentCaseView';
import { fmtStepDuration } from './investigationResultView';
import { externalSummaryNote } from '@/lib/problemSubject';
import { localizedNote } from '@/lib/rootCauseCandidates';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const NOW_NS = Date.now() * 1e6;
const chg = (service: string, direction: string) => ({
  service, direction, causeEligible: false, score: 30, reasons: ['p99 up'],
  baselineRate: 1, currentRate: 1, rateDeltaPct: 0, baselineErrorRate: 0, currentErrorRate: 0, errDeltaPct: 0,
  baselineP99Ms: 1, currentP99Ms: 2, p99DeltaPct: 100, relation: 'downstream',
});
const RC = {
  problemId: 'p1', service: 'svc-a', metric: 'p99_ms', startedAt: NOW_NS, fromNs: NOW_NS - 6e11, toNs: NOW_NS,
  topologyKnown: false,
  correlations: [chg('svc-b', 'better'), chg('svc-c', 'lost'), chg('svc-d', 'quieter')],
  blastRadius: { service: 'svc-a', windowSec: 600, totalCallers: 2, cascadingCallers: 0, totalRps: 1, totalErrorsPerSec: 0, callers: [] },
  hypothesis: { deep: {
    rollouts: [{ clusterId: 'cluster-1', namespace: 'ns-1', workload: 'svc-a', revision: 'r2', startedAtNs: NOW_NS, status: 'completed',
      imageTag: 'v2', prevImageTag: 'v1', matchedBy: 'pod', ageMin: 50, band: 'low', score: 0.4, reason: 'r' }],
    templates: [{ id: 't1', template: 'conn reset <*>', sample: 'conn reset 1', totalCount: 12, lastSeen: NOW_NS }],
    checked: [{ family: 'k8s', found: false, detail: '' }],
  } },
};
const INV = {
  state: 'done', enabled: true, investigation: {
    problemId: 'p1', service: 'svc-a', kind: 'manual', requestBy: 'op', attempts: 0, run: 1, state: 'done',
    enqueuedAt: NOW_NS - 60e9, finishedAt: NOW_NS - 120e9, problemStartedAt: NOW_NS - 120e9, anchor: { from: 0, to: 0 },
    steps: [
      { name: 'sweep', state: 'ok', summary: 'x', durationMs: 1200, at: NOW_NS },
      { name: 'deps', state: 'partial', summary: 'y', durationMs: 900, at: NOW_NS },
      { name: 'summary', state: 'error', summary: '', durationMs: 0, at: NOW_NS },
    ],
    findings: [{ kind: 'dependency', name: 'svc-b', level: 'warn', reasons: ['r'] }],
    summary: { text: 'body', gated: false, model: 'm1', at: NOW_NS - 60e9 },
  },
};
const CASE = {
  caseId: 'c1', source: 'problem', sourceId: 'p1', title: 't', service: 'svc-a', openedAt: '', closedAt: '',
  errorCodes: ['E1'], functionCodes: ['F1'], channelCodes: ['C1'], errRateBefore: 0.01, errRateDuring: 0.2,
  p99BeforeMs: 0, p99DuringMs: 0, rateBefore: 0, rateDuring: 0, deployInWindow: true, deployVersion: '',
  suggestedCause: 'svc-b slow', suggestedSource: 'hypothesis', finalCause: '', category: '', resolution: '',
  cosreCorrect: -1, status: 'draft', provenance: 'cosre', createdAt: '', confirmedAt: '',
};
const EXT_DEEP = {
  external: { source: 'src-1', query: 'q1', labels: { k: 'v' }, current: 42, median: 3, mad: 1, z: 9.5,
    windowFromNs: 1, windowToNs: 2, rows: 3, updatedNs: 3, subjectNote: 'svc-a', subjectSource: 'learned' },
  traceIds: ['abcdef0123456789'],
};
const extProblem = (ruleId: string) => ({
  id: 'p1', ruleId, ruleName: 'r', severity: 'critical', service: 'src-1', kind: 'external', metric: 'q1',
  value: 42, threshold: 10, status: 'open', description: '', startedAt: NOW_NS,
} as unknown as Problem);

let host: HTMLDivElement | null = null;
let root: Root | null = null;
const settle = () => act(async () => { await new Promise(r => setTimeout(r, 30)); });

async function mount(node: React.ReactNode) {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => {
    root!.render(<QueryClientProvider client={qc}><MemoryRouter>{node}</MemoryRouter></QueryClientProvider>);
  });
  await settle();
  await settle();
  return { text: host.textContent ?? '', html: host.innerHTML };
}
async function all() {
  const out = [];
  out.push(await mount(<RootCausePanel problemId="p1" service="svc-a" />));
  act(() => { (host!.querySelector('[data-co-movers] button') as HTMLButtonElement | null)?.click(); });
  out.push({ text: host!.textContent ?? '', html: host!.innerHTML });
  act(() => { root?.unmount(); }); host?.remove();
  out.push(await mount(<ProblemInvestigationPanel problemId="p1" />));
  act(() => { root?.unmount(); }); host?.remove();
  out.push(await mount(<IncidentCaseCard source="problem" sourceId="p1" />));
  act(() => { root?.unmount(); }); host?.remove();
  m.rc = { hypothesis: { deep: EXT_DEEP } };
  out.push(await mount(<ExternalEvidencePanel problem={extProblem('anomaly:ext:src-1:q1')} />));
  act(() => { root?.unmount(); }); host?.remove();
  m.rc = {};
  out.push(await mount(<ExternalEvidencePanel problem={extProblem('anomaly:ext:src-1:q1')} />));
  return { text: out.map(r => r.text).join('\n'), html: out.map(r => r.html).join('\n') };
}

beforeEach(() => {
  m.lang = 'en'; m.rc = RC; m.inv = INV;
  m.kase = { case: CASE, categories: ['deploy', 'config', 'noise', 'other'] };
});
afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  host = null; root = null;
});

// Bu panellerde bugün Türkçe çizilen etiketler (sunucu verisi değil).
const TR_LABELS = [
  'çağıran yok', 'giriş noktası', 'İlgili dağıtımlar', 'İş yükü', 'Geçiş', 'Ne zaman', 'Durum', 'Puan', 'dk önce',
  'revizyonda', 'Log şablonları', 'Şablon', 'Toplam', 'Son görülme', 'Neye bakıldı', 'denetim izi', 'kayıt bulunamadı',
  'iyileşti', 'trafik kesildi', 'trafik azaldı', 'doğrulanamadı',
  'araştırması', 'tamam', 'kısmi', 'okunamadı', 'Servis taraması', 'Adım', 'Süre', 'Özet', 'Bağımlılık', 'bağımlılık',
  'AI özeti', 'Öneri niteliğinde', 'İsteyen', 'Sohbette sor', 'Yeniden araştır',
  'Vaka kaydı', 'taslağı', 'Hata kodları', 'Hata veren', 'Kanal', 'Hata oranı', 'Pencerede deploy', 'Kök neden',
  'önerisiyle', 'Öneri', 'Kategori', 'Yapılandırma', 'Gürültü', 'Diğer', 'Çözüm', 'Onayla', 'temizle',
  'Zaman', 'Kök servis', 'Hata servis', 'Hatalı', 'Satır', 'Şiddet', 'İmza', 'Kayıt', 'güncellendi', 'özne:',
  'Seri okunamadı', 'SORGU', 'satır', 'bulundu', 'imzası', 'İlgili trace', 'kanıtı yok', "CH'de yok", 'İlgili endpoint',
  'Etkilenen pod', 'Log imzaları', 'henüz toplanmadı', 'Kanıt',
];
const leaked = (r: { text: string; html: string }) => TR_LABELS.filter(l => r.text.includes(l) || r.html.includes(l));

describe('Problem detayı alt panelleri — İngilizce arayüz (v0.10.2285)', () => {
  it('kök neden, araştırma, vaka kaydı, dış kanıt: Türkçe etiket yok', async () => {
    const r = await all();
    expect(leaked(r), `Türkçe etiket İngilizce arayüzde: ${leaked(r).join(' | ')}`).toEqual([]);
    for (const en of [
      'No callers in this window — svc-a is an entry point', 'Related rollouts', 'Workload', 'Change', 'When', 'Score',
      '50 min ago', 'Log templates (this service)', 'Template', 'Total', 'Last seen', 'What was checked',
      'checked — no record found', 'improved', 'traffic lost', 'traffic down', 'link could not be verified',
      'AI SRE investigation', 'done', 'Step', 'Status', 'Duration', 'Summary', 'Service sweep', 'Dependency', 'ok',
      'partial', 'unreadable', '1.2 s', 'AI summary · m1', '1 min ago', 'dependency · warn',
      'Advisory only — no change was applied. Requested by: op.', 'Ask in chat', 'Investigate again',
      'Case record', 'CoSRE draft — not counted as fact until confirmed', 'Error codes: E1', 'Failing function code: F1',
      'Channel: C1', 'Error rate 1.0% → 20%', 'Deploy in window: yes', 'Root cause',
      'Prefilled with the CoSRE suggestion', 'The suggestion is accepted as is', 'Category', 'Configuration',
      'Noise (not a real incident)', 'Other', 'Resolution (optional)', 'Confirm', 'Clear suggestion', 'Noise',
      'updated ', 'subject: ≈ svc-a', 'Could not read the series', 'QUERY 2: 3 rows', '0 traces found in CH',
      '0 log signatures', 'Related traces', 'Not in CH', 'Related endpoints', 'No endpoint evidence', 'Affected pods',
      'No pod evidence', 'Log signatures (WARN+)', 'No log signatures', 'Time', 'Root service · op', 'Error service · op',
      'Service', 'Errored', 'Lines', 'Severity', 'Records', 'Evidence not collected yet',
    ]) expect(r.text, en).toContain(en);
    for (const en of ["the problem's pod is on this revision", 'traffic dropped ≥90%', 'Delete the suggestion',
      'Logs tab (Oracle lines)']) expect(r.html, en).toContain(en);
  });

  it('saf yardımcılar: lang verilince İngilizce, verilmezse Türkçe aynen', () => {
    const c = CASE as unknown as Parameters<typeof caseSignalLines>[0];
    expect(caseSignalLines(c, 'en')).toEqual(['Error codes: E1', 'Failing function code: F1', 'Channel: C1', 'Error rate 1.0% → 20%', 'Deploy in window: yes']);
    expect(caseSignalLines(c)).toEqual(['Hata kodları: E1', 'Hata veren function code: F1', 'Kanal: C1', 'Hata oranı %1.0 → %20', 'Pencerede deploy: evet']);
    expect(independenceNote('', 'x').text).toBe('CoSRE öneri vermemişti — onay bağımsız vaka sayılır');
    expect(fmtStepDuration(1200)).toBe('1,2 sn');
    expect(fmtStepDuration(1200, 'en')).toBe('1.2 s');
    expect(externalSummaryNote('down').title).toBe('Kaynak erişilemiyor — seri kanıtı yok');
    expect(externalSummaryNote('down', 'en').title).toBe('Source unreachable — no series evidence');
    const rc = RC as unknown as Parameters<typeof localizedNote>[0];
    expect(localizedNote(rc, 'svc-a')).toBe('; services below moved in the same window — bağlantı doğrulanamadı (topology unavailable)');
    expect(localizedNote(rc, 'svc-a', undefined, 'en')).toBe('; services below moved in the same window — link could not be verified (topology unavailable)');
  });
});

describe('Problem detayı alt panelleri — Türkçe arayüzde metin aynen (v0.10.2285)', () => {
  it('paneller', async () => {
    m.lang = 'tr';
    const r = await all();
    for (const tr of [
      'Bu pencerede çağıran yok — svc-a bir giriş noktası', 'İlgili dağıtımlar', 'İş yükü', 'Geçiş', 'Ne zaman', 'Puan',
      '50 dk önce', 'Log şablonları (bu servis)', 'Şablon', 'Toplam', 'Son görülme', 'Neye bakıldı',
      'bakıldı — kayıt bulunamadı', 'iyileşti', 'trafik kesildi', 'trafik azaldı', 'bağlantı doğrulanamadı',
      'AI SRE araştırması', 'tamam', 'Adım', 'Durum', 'Süre', 'Özet', 'Servis taraması', 'Bağımlılık', 'kısmi',
      'okunamadı', '1,2 sn', 'AI özeti · m1', '1 dk önce', 'bağımlılık · warn',
      'Öneri niteliğinde — hiçbir değişiklik uygulanmadı. İsteyen: op.', 'Sohbette sor', 'Yeniden araştır',
      'Vaka kaydı', 'CoSRE taslağı — onaylanana dek olgu sayılmaz', 'Hata kodları: E1', 'Hata veren function code: F1',
      'Kanal: C1', 'Hata oranı %1.0 → %20', 'Pencerede deploy: evet', 'Kök neden', 'CoSRE önerisiyle dolu',
      'Öneri aynen kabul ediliyor', 'Kategori', 'Yapılandırma', 'Gürültü (gerçek olay değil)', 'Diğer',
      'Çözüm (isteğe bağlı)', 'Onayla', 'Öneriyi temizle', 'Gürültü', 'güncellendi ', 'özne: ≈ svc-a', 'Seri okunamadı',
      'SORGU 2: 3 satır', "0 trace CH'de bulundu", '0 log imzası', "İlgili trace'ler", "CH'de yok", "İlgili endpoint'ler",
      'Endpoint kanıtı yok', "Etkilenen pod'lar", 'Pod kanıtı yok', 'Log imzaları (WARN+)', 'Log imzası yok', 'Zaman',
      'Kök servis · op', 'Hata servis · op', 'Servis', 'Hatalı', 'Satır', 'Şiddet', 'Kayıt', 'Kanıt henüz toplanmadı',
    ]) expect(r.text, tr).toContain(tr);
    for (const tr of ["problemin pod'u bu revizyonda", 'trafik ≥%90 düştü', 'Öneriyi silip', 'Logs sekmesi (Oracle satırları)'])
      expect(r.html, tr).toContain(tr);
  });
});
