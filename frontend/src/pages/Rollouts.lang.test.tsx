// @vitest-environment jsdom
//
// v0.10.2304 — pin: arayüz dili İngilizceyken Rollouts sayfası (Canlı +
// Toplu, kapalı durum, platform namespace şeridi), rollout çekmecesi ve servis
// GitOps sekmesi bilinen Türkçe metin ÇİZMEZ (metin + title / aria-label /
// placeholder); sunucu notları "TR" işaretli (lang="tr" altında, kontrol
// dışı). Türkçe arayüzde metin aynen. Saf yardımcılar lang verilmezse Türkçe.
import { describe, it, expect, vi, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { RolloutDetail, RolloutListResponse, RolloutStats, ServiceGitOpsResponse, WorkloadRollout } from '@/lib/types';

const h = vi.hoisted(() => {
  window.matchMedia = ((q: string) => ({
    matches: false, media: q, onchange: null,
    addListener: () => {}, removeListener: () => {},
    addEventListener: () => {}, removeEventListener: () => {}, dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
  return {
    lang: 'en' as 'en' | 'tr',
    list: { data: undefined, isPending: false, error: null } as { data: unknown; isPending: boolean; error: Error | null },
    stats: { data: undefined, isPending: false, error: null } as { data: unknown; isPending: boolean; error: Error | null },
    detail: null as unknown,
    gitops: null as unknown,
  };
});
vi.mock('@/lib/i18n', async (orig) => ({ ...(await orig<typeof import('@/lib/i18n')>()), useLang: () => h.lang }));
vi.mock('@/lib/queries', async (importOriginal) => {
  const mod = await importOriginal<Record<string, unknown>>();
  return {
    ...mod,
    useRollouts: () => h.list,
    useRolloutStats: () => h.stats,
    useRolloutRuns: () => ({ data: { runs: [{ startedAt: 1_790_000_000_000, status: 'ok', worker: 'ksm' }] }, isPending: false, error: null }),
    useEntityClusters: () => ({ data: { clusters: [{ id: 'c1', name: 'cluster-1' }] }, isPending: false, error: null }),
    useRolloutDetail: () => ({ data: h.detail, isPending: false, isError: false }),
    useServiceGitOps: () => ({ data: h.gitops, isPending: false, error: null, refetch: () => Promise.resolve() }),
  };
});
vi.mock('@/components/AuthProvider', () => ({
  useAuth: () => ({ user: { username: 'op', role: 'admin' }, loading: false }),
}));
vi.mock('@/components/Topbar', () => ({ Topbar: () => null }));

import RolloutsPage from './Rollouts';
import { RolloutDrawer } from '@/components/RolloutDrawer';
import { ServiceGitOpsTab } from './service/ServiceGitOpsTab';
import { statusTitle, changeKindTitle, originTitle, originAppLine, rolloutTimeline } from '@/lib/rolloutRow';
import { autoSyncLabel, argoSourceMeta } from '@/lib/serviceGitOps';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
class NoopResizeObserver { observe() {} unobserve() {} disconnect() {} }
(globalThis as unknown as { ResizeObserver: unknown }).ResizeObserver = NoopResizeObserver;

const T0 = 1_790_000_000_000;
const SERVER_NOTE = 'sunucu notu: kesik okuma';
const ROW: WorkloadRollout = {
  clusterId: 'c1', namespace: 'ns-1', workload: 'wl-a', kind: 'Deployment', revision: 'wl-a-7f9c', startedAt: T0,
  status: 'completed', prevRevision: 'wl-a-6a1b', image: 'reg/wl-a', imageTag: '1.2.3', prevImage: 'reg/wl-a', prevImageTag: '1.2.2',
  firstSpanAt: T0 + 5_000, trafficConfirmedAt: 0, ksmStartedAt: 0, podsReadyAt: T0 + 3_000, ksmNotReadySince: 0, completedAt: T0 + 60_000,
  detectedBy: 'span', spanCount: 10, note: SERVER_NOTE, updatedAt: 1, problemsCaused: 2, service: 'svc-a', services: ['svc-a', 'svc-b'],
  origin: { kind: 'rollback', confidence: 80, reason: 'git revision seen before', basis: 'argocd_webhook', prevRevision: 'aaaaaaaaaa', newRevision: 'bbbbbbbbbb',
    firstSyncedAt: T0 - 86_400_000, automated: false, initiator: 'op', historyDepth: 4, app: { instanceId: 'i1', appNamespace: 'argocd', appName: 'app-a' } },
};
const STATS: RolloutStats = {
  total: 4, completed: 3, rolledBack: 1, inProgress: 0, stalled: 0, superseded: 0, from: 0, to: 1,
  perDay: 4, rollbackRate: 0.25, meanDurationSec: 60, p95DurationSec: 120,
  topRollback: [], topDeploy: [],
  byDay: [],
};
const DETAIL: RolloutDetail = {
  rollout: { ...ROW, generation: 3, incarnationAt: T0, observedGeneration: 3, specReplicas: 2, updatedReplicas: 2, availableReplicas: 2,
    stuckReason: 'ProgressDeadlineExceeded', images: ['reg/wl-a:1.2.3', 'reg/side:1'], succeededAt: T0 + 50_000 },
  services: [{
    service: 'svc-a', health: 'yellow',
    before: { errorRate: 1, p99Ms: 10, throughput: 1 }, after: { errorRate: 2, p99Ms: 12, throughput: 0 },
    problems: [], anomalies: [], newErrors: [],
  }],
  since: T0 * 1e6, generatedAt: T0 * 1e6, note: SERVER_NOTE,
};
const GITOPS: ServiceGitOpsResponse = {
  service: 'svc-a', rolloutsFrom: 0, workloadsFrom: 0, to: 0,
  workloads: [{ clusterId: 'c1', namespace: 'ns-1', workload: 'wl-a', clusterName: 'cluster-1' }],
  unmappedClusters: ['dc-x'],
  argo: { configured: true, source: 'mapper', note: SERVER_NOTE, otherInNamespace: 2,
    hubs: [{ hubClusterId: 'h1', hubName: 'hub-1', status: 'error', error: 'HTTP 403', truncated: true }],
    apps: [{ hubClusterId: 'h1', appNamespace: 'argocd', name: 'app-a', project: 'proj-1', autoSync: false, syncStatus: 'Synced', healthStatus: 'Healthy',
      workloads: [{ clusterId: 'c1', namespace: 'ns-1', workload: 'wl-a' }], match: 'manual', confidence: 100 }] },
  rollouts: { enabled: true, rows: [ROW], capped: true },
} as unknown as ServiceGitOpsResponse;

let host: HTMLDivElement | null = null;
let root: Root | null = null;
function mount(node: React.ReactNode, url = '/') {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  act(() => { root!.render(<QueryClientProvider client={qc}><MemoryRouter initialEntries={[url]}>{node}</MemoryRouter></QueryClientProvider>); });
}
function close() { act(() => { root?.unmount(); }); host?.remove(); host = null; root = null; document.body.innerHTML = ''; }
// Görünen metin + ipuçları; OrigLang ile işaretli sunucu metni (lang="tr") hariç.
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
  h.list = { data: { rollouts: [ROW], from: 0, to: 1, limit: 200, capped: true, excludeNamespaces: ['ns-sys-*'], excludeApplied: false } as RolloutListResponse, isPending: false, error: null };
  mount(<RolloutsPage />, '/rollouts?namespace=ns-sys-1'); take();
  h.list = { data: { rollouts: [ROW], from: 0, to: 1, limit: 200, v2: true, excludeNamespaces: [] } as RolloutListResponse, isPending: false, error: null };
  mount(<RolloutsPage />, '/rollouts'); take();
  h.list = { data: { rollouts: [], from: 0, to: 1, limit: 200 } as RolloutListResponse, isPending: false, error: null };
  mount(<RolloutsPage />, '/rollouts'); take();
  h.list = { data: { rollouts: [], from: 0, to: 1, limit: 200, v2: true } as RolloutListResponse, isPending: false, error: null };
  mount(<RolloutsPage />, '/rollouts'); take();
  h.list = { data: undefined, isPending: false, error: new Error('HTTP 502') };
  mount(<RolloutsPage />, '/rollouts'); take();
  h.list = { data: { disabled: true } as RolloutListResponse, isPending: false, error: null };
  mount(<RolloutsPage />, '/rollouts'); take();
  h.stats = { data: STATS, isPending: false, error: null };
  mount(<RolloutsPage />, '/rollouts?tab=stats'); take();
  h.stats = { data: undefined, isPending: false, error: new Error('HTTP 500') };
  mount(<RolloutsPage />, '/rollouts?tab=stats'); take();
  h.detail = DETAIL;
  mount(<RolloutDrawer id={{ clusterId: 'c1', namespace: 'ns-1', workload: 'wl-a', revision: 'wl-a-7f9c', startedAt: T0 }} onClose={() => {}} />); take();
  h.detail = { ...DETAIL, services: [], note: '' };
  mount(<RolloutDrawer id={{ clusterId: 'c1', namespace: 'ns-1', workload: 'wl-a', revision: 'wl-a-7f9c', startedAt: T0 }} onClose={() => {}} />); take();
  h.gitops = GITOPS;
  mount(<ServiceGitOpsTab service="svc-a" />); take();
  h.gitops = { ...GITOPS, argo: { ...GITOPS.argo, note: '', apps: [] }, rollouts: { enabled: true, rows: [] } };
  mount(<ServiceGitOpsTab service="svc-a" />); take();
  h.gitops = { ...GITOPS, workloads: [], argo: { ...GITOPS.argo, note: '', apps: [] }, rollouts: { enabled: false, rows: [] } };
  mount(<ServiceGitOpsTab service="svc-a" />); take();
  return { text: out.join('\n'), html: html.join('\n') };
}

afterEach(() => { close(); h.lang = 'en'; });

const TR_CHARS = /[çğıöşüÇĞİÖŞÜ]/;
const TR_WORDS = /\b(tümü|yok|kapalı|açık|Geçiş|tür|durum|kaynak|nesil|replika|imaj|yeni|zaman|başladı|tamamlandı|Toplu|Canlı|Uygulama|Bağlantı|sayfası|Düzenle|uygulama|neden|güven|elle|geçmiş|senkron|oran|gün|Servis|Not)\b/;

describe('Rollouts / çekmece / GitOps — İngilizce arayüz (v0.10.2304)', () => {
  it('Türkçe metin yok (sunucu notu işaretli)', async () => {
    const r = await all();
    // Paylaşılan bileşenlerin kendi etiketleri (tablo menüsü, çekmece, bölüm başlığı) bu dilimin kapsamı dışında.
    const SHARED = ['Tablo seçenekleri', 'Detay çekmecesi', 'Bu bölümün okuduğu kaynak'];
    const bad = r.text.split('\n').filter(l => !SHARED.includes(l) && (TR_CHARS.test(l) || TR_WORDS.test(l)));
    expect(bad).toEqual([]);
    for (const en of ['Live', 'Summary', 'Change', 'Image (old → new)', 'Status', 'Note', 'Generation',
      'Could not load the rollout list: HTTP 502', 'The reconciler saw no new revision', 'The KSM detector saw no generation increase',
      'Changes is off', 'Enable', 'The list was truncated on the server', 'Hidden platform namespaces: ',
      'overrides this hiding', 'detector last run', 'Workloads with the most rollbacks', 'rate 25.0%', 'Avg. duration',
      'Could not load statistics', 'Transition', 'Timeline', 'Service health — before/after deploy (1)', 'previous: ', 'new: ',
      'Started (new ReplicaSet)', 'Completed (old version taken out)', 'Argo returned to a previously synced revision',
      'confidence 80%', 'manual sync · op', 'history 4 syncs', ' · reason ProgressDeadlineExceeded', 'Error% b/a', '1.0%',
      "Could not resolve this revision's service", 'Argo CD applications', '1 application', 'query failed',
      'result truncated at 50 applications', 'Span cluster values that do not map', 'Details →', 'Rollouts is off.',
      'No Argo application is mapped', '2 applications deploy to the same namespace']) expect(r.text, en).toContain(en);
    // Sunucu notu İngilizce arayüzde "TR" işaretli (çekmece, Canlı Not hücresi, Argo notu).
    expect((r.html.match(/<span lang="tr">sunucu notu: kesik okuma<span data-orig-lang=""/g) ?? []).length).toBeGreaterThanOrEqual(3);
  });

  it('saf yardımcılar: lang verilmezse Türkçe aynen', () => {
    expect(statusTitle('completed')).toBe('Yeni revizyon tek aktif revizyon; eskileri çekildi');
    expect(statusTitle('stalled', true)).toBe('İlerlemiyor: progress deadline aşıldı ya da takılma süresi (stuckAfter) doldu');
    expect(statusTitle('nope')).toBe('');
    expect(changeKindTitle('unknown')).toBe("İmaj bilgisi eksik (span'ler container.image taşımıyor) — tür belirlenemedi");
    expect(originTitle(ROW.origin!).startsWith('Argo daha önce senkronlanmış bir revizyona döndü: aaaaaaa → bbbbbbb (ilk senkron ')).toBe(true);
    expect(originTitle(ROW.origin!).endsWith(' · güven %80 · git revision seen before')).toBe(true);
    expect(originAppLine(ROW.origin!)).toBe('argocd/app-a · elle sync · op · geçmiş 4 senkron');
    expect(rolloutTimeline(ROW).map(s => s.label)).toEqual(['Başladı (yeni ReplicaSet)', "Pod'lar hazır", 'Trafik yeni sürümde (ilk span)', 'Completed (eski sürüm devreden çıktı)']);
    expect(autoSyncLabel(true)).toBe('açık');
    expect(autoSyncLabel(false, 'en')).toBe('off');
    expect(argoSourceMeta('live')).toBe('canlı sorgu');
  });
});

describe('Rollouts / çekmece / GitOps — Türkçe arayüzde metin aynen (v0.10.2304)', () => {
  it('bilinen Türkçe metin', async () => {
    h.lang = 'tr';
    const r = await all();
    for (const tr of ['Canlı', 'Toplu', 'Değişiklik', 'İmaj (eski → yeni)', 'Durum', 'Not', 'Nesil',
      'Rollout listesi yüklenemedi: HTTP 502', 'Reconciler aktif kümeye giren yeni revizyon görmedi', 'KSM dedektörü bu pencerede nesil artışı görmedi',
      'Değişiklikler kapalı', 'Etkinleştir', 'Liste sunucuda kesildi (limit 200) — pencereyi daralt.', "Gizlenen platform namespace'leri: ",
      ' — açık namespace süzgeci «ns-sys-1» bu gizlemeyi geçersiz kılıyor', 'dedektör son koşu', "En çok rollback alan workload'lar", 'oran %25.0', 'Ort. süre',
      'İstatistik yüklenemedi', 'Geçiş', 'Zaman çizelgesi', 'Servis sağlığı — deploy öncesi/sonrası (1)', 'önceki: ', 'yeni: ',
      'Başladı (yeni ReplicaSet)', 'Completed (eski sürüm devreden çıktı)', 'güven %80', 'elle sync · op', 'geçmiş 4 senkron', ' · neden ProgressDeadlineExceeded',
      'Hata% ö/s', '%1.0', 'Bu revizyonun servisi çözülemedi — MV bu revizyon için servis kaydı taşımıyor', 'Argo CD uygulamaları', '1 uygulama',
      "aynı namespace'te eşleşmeyen 2", 'sorgu başarısız', 'sonuç 50 uygulamada kesildi', 'Remote Cluster kaydına eşlenemeyen span cluster değerleri atlandı: dc-x',
      'Ayrıntı →', 'Rollouts kapalı.', "aynı namespace'e deploy eden 2 uygulama var", SERVER_NOTE,
      "Bu workload'a süz", 'Servis sayfasını açar: svc-a', 'Diğer servisler: svc-b', 'tamamlandı ',
    ]) expect(r.text, tr).toContain(tr);
    // TR arayüzde işaret yok.
    expect(r.html).not.toContain('data-orig-lang');
    expect(r.html).not.toContain('lang="tr"');
  });
});
