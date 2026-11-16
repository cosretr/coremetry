// @vitest-environment jsdom
//
// v0.10.2288 — pin: arayüz dili İngilizceyken inceleme sonucu kartları
// (ProblemInvestigationCards: hipotezler, korelatör kutusu, zaman çizelgesi,
// işaret formu; ProblemInvestigationRecommendations: öneri kartları, rozet,
// runbook bağlantıları, Uygulandı / Ele) bilinen Türkçe etiket ÇİZMEZ; Türkçe
// arayüzde metin aynen kalır. Sunucu metni (hipotez adı, kontrol gerekçesi,
// olay etiketi, öneri metni / gerekçesi) kapsam dışı — fikstürde nötr.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { SREHypothesis, SREInvestigation, SRERecommendation } from '@/lib/types';

const m = vi.hoisted(() => ({ lang: 'en' as 'en' | 'tr' }));
vi.mock('@/lib/i18n', async (orig) => ({ ...(await orig<typeof import('@/lib/i18n')>()), useLang: () => m.lang }));
vi.mock('@/lib/api', () => ({ api: new Proxy({}, { get: () => () => Promise.resolve({}) }) }));

import { ProblemInvestigationCards } from './ProblemInvestigationCards';
import { ProblemInvestigationRecommendations } from './ProblemInvestigationRecommendations';
import { bandLabel, humanLabel, outcomeView, timelineKindLabel } from './hypothesisView';
import { REC_PENDING, recHumanLabel } from './recommendationView';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const NOW_NS = Date.now() * 1e6;
const hyp = (kind: SREHypothesis['kind'], band: SREHypothesis['band'], extra: Partial<SREHypothesis> = {}): SREHypothesis => ({
  kind, label: `H-${kind}`, score: 0.5, band, category: 'cat-1', prior: 0.2, checks: [], ...extra,
});
const recs: SRERecommendation[] = [
  { id: 'r1', text: 'text-1', rationale: 'why-1', hypothesisKind: 'deploy', state: REC_PENDING,
    rollout: { workload: 'svc-a', from: 'rev 2', to: 'rev 1' }, argo: { app: 'app-1', status: 'Synced', prevRevision: 'rev 1' },
    runbook: { id: 'rb-1', title: 'rb-1', url: '/runbook?id=rb-1&problemId=p1' } },
  { id: 'r2', text: 'text-2', rationale: '', state: REC_PENDING, runbook: { title: 'rb-2', url: 'https://wiki.example/rb' } },
  { id: 'r3', text: 'text-3', rationale: '', state: 'ack', ackBy: 'op@x', ackAt: 1 },
  { id: 'r4', text: 'text-4', rationale: '', state: 'dismissed', ackBy: 'op@x', ackAt: 1 },
];
const inv: SREInvestigation = {
  problemId: 'p1', service: 'svc-a', kind: 'verify', hypothesis: 'deploy', state: 'done', attempts: 1, run: 1,
  enqueuedAt: NOW_NS - 300e9, finishedAt: NOW_NS - 60e9, problemStartedAt: NOW_NS - 900e9, anchor: { from: 0, to: 0 },
  steps: [], findings: [],
  hypotheses: [
    hyp('deploy', 'yüksek', { checks: [
      { name: 'c1', outcome: 1, weight: 0.35, reason: 'reason-1' },
      { name: 'c2', outcome: 0, weight: 0.25, reason: 'reason-2' },
      { name: 'c3', outcome: -1, weight: 0.15, reason: 'reason-3' },
    ] }),
    hyp('dependency', 'orta', { humanState: { state: 'dismissed', by: 'op@x', note: 'note-1', at: NOW_NS } }),
    hyp('saturation', 'düşük', { humanState: { state: 'confirmed', by: 'op@x', at: NOW_NS } }),
  ],
  timeline: (['problem_start', 'deploy', 'spike_start', 'first_error', 'alert', 'alert_resolved', 'incident_attach'] as const)
    .map((kind, i) => ({ at: NOW_NS - (900 - i) * 1e9, kind, label: `ev-${i}` })),
  synthRef: { topSuspect: 'svc-b', confidence: 0.58, computedAt: NOW_NS },
  recommendations: recs,
};

let host: HTMLDivElement | null = null;
let root: Root | null = null;
const snap = () => ({ text: host!.textContent ?? '', html: host!.innerHTML });
async function mount(node: React.ReactNode) {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => {
    root!.render(<QueryClientProvider client={qc}><MemoryRouter>{node}</MemoryRouter></QueryClientProvider>);
  });
}
const close = () => { act(() => { root?.unmount(); }); host?.remove(); };
const clickFirst = (re: RegExp) => act(() => {
  [...host!.querySelectorAll<HTMLElement>('button')].find(b => re.test(b.textContent?.trim() ?? ''))?.click();
});
async function all() {
  const out = [];
  await mount(<ProblemInvestigationCards inv={inv} canEdit />);
  out.push(snap());
  clickFirst(/^(Dismiss|Ele)$/);
  out.push(snap());
  clickFirst(/^(Cancel|Vazgeç)$/); clickFirst(/^(Confirm|Onayla)$/);
  out.push(snap()); close();
  await mount(<ProblemInvestigationRecommendations inv={inv} canEdit />);
  out.push(snap()); close();
  return { text: out.map(r => r.text).join('\n'), html: out.map(r => r.html).join('\n') };
}

beforeEach(() => { m.lang = 'en'; });
afterEach(() => { close(); host = null; root = null; });

const TR_LABELS = [
  'Hipotezler', 'kod skoru', 'son koşu', 'doğrulaması', 'Korelatör', 'sentezleyicisi', 'şüpheli', 'güven', 'Kök neden paneline',
  'Zaman çizelgesi', 'olay)', 'Başlangıç', 'Log artışı', 'İlk hata', 'Alarm', 'yüksek', 'orta<', 'düşük', 'doğrular', 'çürütür',
  'bilinmiyor', 'operatör', 'öncül', 'ağırlık', 'kategori', 'Not:', 'Doğrula', '>Ele<', 'Onayla', 'Vazgeç', 'Eleme notu',
  'Onay notu', 'isteğe bağlı', 'İşaret yalnız', 'Öneriler', 'yalnız öneri', 'öneri — uygulanmadı', 'Gerekçe', "Runbook'u aç",
  'Runbook sayfası', 'yeni sekme', 'Uygulandı', 'uyguladıysanız',
];
const leaked = (r: { text: string; html: string }) => TR_LABELS.filter(l => r.text.includes(l) || r.html.includes(l));

describe('İnceleme sonucu kartları — İngilizce arayüz (v0.10.2288)', () => {
  it('hipotez kartları, korelatör, zaman çizelgesi, işaret formu, öneri kartları: Türkçe etiket yok', async () => {
    const r = await all();
    expect(leaked(r), `Türkçe etiket İngilizce arayüzde: ${leaked(r).join(' | ')}`).toEqual([]);
    for (const en of [
      'Hypotheses — AI SRE agent · code score', ' · last run: verification of Deploy', 'Correlator', 'Dependency failure', 'Saturation / OOM',
      '(root-cause synthesizer — a separate engine, not the agent\'s score):', 'suspect svc-b (confidence 58%)',
      'Go to the root cause panel', 'Timeline (7 events)', 'Start', 'Log spike', 'First error', 'Alert resolved', 'Incident',
      'high', 'medium', 'low', 'operator dismissed · op@x', 'operator confirmed · op@x', 'category: cat-1', 'Note: note-1',
      'Verify', 'Dismiss', 'Confirm', 'Dismissal note (optional)', 'Confirmation note (optional)', 'Cancel',
      'A mark is only a label', 'Recommendations — AI SRE agent · advice only', 'advice — not applied', 'Rationale: why-1',
      'Open runbook', 'Open runbook ↗', 'Applied', 'operator marked it applied · op@x', 'Rollout:', 'Argo CD',
    ]) expect(r.text, en).toContain(en);
    for (const en of ['AI SRE timeline', 'prior 0.20 + Σ weight × outcome', 'supports · weight 0.35', 'refutes · weight 0.15',
      'unknown · weight 0.25', 'The agent only produces advice', 'The runbook page opens linked to the problem',
      "The problem's runbook link (new tab)", 'Mark it if you applied the recommendation']) expect(r.html, en).toContain(en);
  });

  it('saf yardımcılar: lang verilmezse Türkçe aynen', () => {
    expect(bandLabel('yüksek')).toBe('yüksek');
    expect(bandLabel('düşük', 'en')).toBe('low');
    expect(outcomeView(-1).label).toBe('çürütür');
    expect(outcomeView(1, 'en').label).toBe('supports');
    expect(timelineKindLabel('first_error')).toBe('İlk hata');
    expect(timelineKindLabel('first_error', 'en')).toBe('First error');
    expect(timelineKindLabel('other')).toBe('other');
    expect(humanLabel(inv.hypotheses![1])).toBe('operatör eledi · op@x');
    expect(humanLabel(inv.hypotheses![1], 'en')).toBe('operator dismissed · op@x');
    expect(recHumanLabel(recs[2])?.text).toBe('operatör uyguladığını işaretledi · op@x');
    expect(recHumanLabel(recs[2], 'en')?.text).toBe('operator marked it applied · op@x');
  });
});

describe('İnceleme sonucu kartları — Türkçe arayüzde metin aynen (v0.10.2288)', () => {
  it('kartlar ve öneriler', async () => {
    m.lang = 'tr';
    const r = await all();
    for (const tr of [
      'Hipotezler — AI SRE ajanı · kod skoru (öncül + kontrol ağırlıkları; AI ve işaretler skoru değiştirmez) · son koşu: deploy doğrulaması',
      'Korelatör (kök-neden sentezleyicisi — ayrı motor, ajanın skoru değil): şüpheli svc-b (güven %58) Kök neden paneline git',
      'Zaman çizelgesi (7 olay)', 'Başlangıç', 'Log artışı', 'İlk hata', 'Alarm kapandı', 'Incident',
      'yüksek', 'orta', 'düşük', 'operatör eledi · op@x', 'operatör onayladı · op@x', 'kategori: cat-1', 'Not: note-1',
      'Doğrula', 'Ele', 'Onayla', 'Eleme notu (isteğe bağlı)', 'Onay notu (isteğe bağlı)', 'Vazgeç',
      'İşaret yalnız etikettir — ajanın skoru değişmez.',
      'Öneriler — AI SRE ajanı · yalnız öneri: ajan hiçbir şeyi uygulamaz; kararı ve uygulamayı insan verir',
      'öneri — uygulanmadı', 'Gerekçe: why-1', "Runbook'u aç", "Runbook'u aç ↗", 'Uygulandı',
      'operatör uyguladığını işaretledi · op@x', 'operatör eledi · op@x',
    ]) expect(r.text, tr).toContain(tr);
    for (const tr of ['AI SRE zaman çizelgesi', 'öncül 0.20 + Σ ağırlık × sonuç', 'doğrular · ağırlık 0.35', 'çürütür · ağırlık 0.15',
      'bilinmiyor · ağırlık 0.25', 'Ajan yalnız öneri üretir — hiçbir şeyi uygulamaz',
      'Runbook sayfası problem bağlı açılır — çalıştırmayı siz başlatırsınız', 'Problemin runbook bağlantısı (yeni sekme)',
      'Öneriyi siz uyguladıysanız işaretleyin — yalnız etiket']) expect(r.html, tr).toContain(tr);
  });
});
