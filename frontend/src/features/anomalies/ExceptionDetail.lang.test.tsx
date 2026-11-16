// @vitest-environment jsdom
//
// v0.10.2287 — pin: arayüz dili İngilizceyken exception detayı (ProblemDetail +
// ExceptionMeta, /problems?exc=; doğrudan çizdiği örnek satırı, Pods · nodes,
// Oracle grubu kartı, sıklık bandı, katlı stack) ve ortak FacetMultiSelect özeti
// bilinen Türkçe etiket ÇİZMEZ; Türkçe arayüzde metin aynen kalır. Sunucu metni
// (tip, mesaj, stack, AI özeti, branding etiketi) kapsam dışı.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { ExceptionGroup } from '@/lib/types';

const m = vi.hoisted(() => {
  window.matchMedia = ((q: string) => ({
    matches: false, media: q, onchange: null,
    addListener: () => {}, removeListener: () => {},
    addEventListener: () => {}, removeEventListener: () => {}, dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
  return { lang: 'en' as 'en' | 'tr' };
});
vi.mock('@/lib/i18n', async (orig) => ({ ...(await orig<typeof import('@/lib/i18n')>()), useLang: () => m.lang }));

const NOW_NS = Date.now() * 1e6;
vi.mock('@/lib/api', () => {
  const now = Date.now() * 1e6;
  const stack = ['java.lang.IllegalStateException: boom', ...Array.from({ length: 24 }, (_, i) => `\tat a.b.C.m${i}(C.java:${i + 1})`)].join('\n');
  const stub: Record<string, (...a: unknown[]) => Promise<unknown>> = {
    exceptionGroupSamples: async () => ({ samples: [
      { traceId: 'aaaaaaaaaaaaaaaa1111', time: now - 60e9, stacktrace: stack, traceInCoremetry: false },
      { traceId: 'bbbbbbbbbbbbbbbb2222', time: now - 120e9, stacktrace: stack, traceSource: 'tempo' },
    ] }),
    exceptionGroupOccurrences: async () => [
      { time: now - 600e9, count: 3 }, { time: now - 540e9, count: 5 }, { time: now - 480e9, count: 2 },
    ],
    servicesMetadata: async () => ({ 'svc-a': { ownerTeam: 'team-a', sreTeam: 'team-b' } }),
    problemVerdicts: async () => ({ verdicts: [] }),
    entityClusters: async () => ({ clusters: [{ id: 'c1', name: 'cluster-1' }] }),
    exceptionGroupPods: async () => ({
      fingerprint: 'fp', total: 10, scanned: 10, sampled: true, truncated: true, noContext: 2,
      from: new Date(Date.now() - 3600e3).toISOString(), to: new Date().toISOString(), unmappedClusters: ['cluster-x'],
      rows: Array.from({ length: 9 }, (_, i) => ({
        cluster: 'cluster-1', clusterId: i === 0 ? undefined : 'c1', namespace: 'ns-1', pod: `svc-a-${i}`, node: 'node-1',
        occurrences: 1, lastSeen: new Date().toISOString(), hostOnly: i === 1,
      })),
    }),
    exceptionGroupOracle: async () => ({ sourceName: 'src-1', code: 'E1', operation: 'OP_1', channels: [{ name: 'CH1', count: 3 }],
      services: [{ name: 'svc-a', count: 2 }], serviceCount: 1, known: false, lastHour: 4, prevHour: 1 }),
  };
  return {
    api: new Proxy({}, { get: (_t, k: string) => stub[k] ?? (() => Promise.resolve(null)) }),
    isCanceled: () => false,
  };
});
vi.mock('@/components/AuthProvider', () => ({
  useAuth: () => ({ user: { username: 'op', email: 'op@x', role: 'viewer' }, loading: false }),
}));
vi.mock('@/components/ai/AIExplainButton', () => ({ AIExplainButton: () => null }));
vi.mock('@/components/ShareButton', () => ({ ShareButton: () => null }));
vi.mock('@/components/charts/TimeChart', () => ({ TimeChart: () => null }));
vi.mock('./IncidentCaseCard', () => ({ IncidentCaseCard: () => null }));

import { ProblemDetail } from './ProblemDetail';
import { FacetMultiSelect, facetSummary } from '@/components/ui/FacetMultiSelect';
import { emptySamplesNote } from './exceptionSamples';
import { occRegion } from './occRegion';
import { exceptionOpenForLine } from './detailSummary';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
class NoopResizeObserver { observe() {} unobserve() {} disconnect() {} }
(globalThis as unknown as { ResizeObserver: unknown }).ResizeObserver = NoopResizeObserver;

const span: ExceptionGroup = {
  fingerprint: '0a1b2c3d4e5f6a7b', type: 'java.lang.IllegalStateException', message: 'x'.repeat(400), service: 'svc-a',
  priority: 'P1', state: 'new', assignee: '', firstSeen: NOW_NS - 3 * 3600e9, lastSeen: NOW_NS - 60e9, occurrences: 10, notes: '',
};
const ora: ExceptionGroup = {
  fingerprint: 'ora:aaaaaaaaaaaaaaaa', type: 'E1', message: 'OP_1', service: 'oracle:src-1',
  priority: 'P2', state: 'new', assignee: '', firstSeen: NOW_NS - 3600e9, lastSeen: NOW_NS - 60e9, occurrences: 4, notes: '',
  oracle: { sourceName: 'src-1', code: 'E1', operation: 'OP_1', channels: [], services: [], serviceCount: 0, known: false },
};

let host: HTMLDivElement | null = null;
let root: Root | null = null;
const settle = () => act(async () => { await new Promise(r => setTimeout(r, 30)); });
const snap = () => ({ text: host!.textContent ?? '', html: host!.innerHTML });
async function mount(node: React.ReactNode) {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => {
    root!.render(<QueryClientProvider client={qc}><MemoryRouter>{node}</MemoryRouter></QueryClientProvider>);
  });
  await settle(); await settle();
}
const close = () => { act(() => { root?.unmount(); }); host?.remove(); };
const clickText = (re: RegExp) => act(() => {
  [...host!.querySelectorAll<HTMLElement>('button')].find(b => re.test(b.textContent ?? ''))?.click();
});
async function all() {
  const out = [];
  await mount(<ProblemDetail group={span} isAdmin={false} onBack={() => {}} onChanged={() => {}} />);
  out.push(snap());
  clickText(/Show all|Tamamı/); clickText(/more lines|satır daha/); clickText(/all \(9\)|tümü \(9\)/);
  out.push(snap()); close();
  await mount(<ProblemDetail group={ora} isAdmin={false} onBack={() => {}} onChanged={() => {}} />);
  out.push(snap()); close();
  await mount(<FacetMultiSelect label="Kind" options={[{ value: 'a', label: 'A', count: 1 }, { value: 'b', label: 'B', count: 2 }, { value: 'c', label: 'C', count: 0 }, { value: 'd', label: 'D', count: 0 }]}
    selected={new Set(['a', 'b', 'c'])} onToggle={() => {}} onSolo={() => {}} onAll={() => {}} />);
  clickText(/Kind:/);
  out.push(snap());
  return { text: out.map(r => r.text).join('\n'), html: out.map(r => r.html).join('\n') };
}

beforeEach(() => { m.lang = 'en'; });
afterEach(() => { close(); host = null; root = null; });

const TR_LABELS = [
  'Servis', 'Ekip', 'Tür', 'Ne zamandır', 'İlgilenen', 'kimse', 'açık · ilk', 'Kısalt', 'Tamamı', 'Servis sayfasını', 'operasyon',
  'satır', 'katla', 'yerinde aç', 'daha az', 'gösteriliyor', 'tamamını kopyalar', 'Triyaj', 'sürüyor', 'son görülme',
  'Coremetry\'de yok', 'kopyala', 'örneklemeye', 'kanıt', 'oluşum', 'bağlamsız', 'Son<', 'eşlenmemiş', 'yedeğinden', 'yedeği',
  'hatalı trace', 'tümü', 'pod daha', 'ilk 50', 'Remote Cluster kaydı', 'grubu', 'stack yok', 'Kaynak', 'kaynak bulunamadı',
  'Hata kodu', 'Operasyon', 'Son 1 saat', 'önceki saat', 'Öncelik girdisi', 'Kanal', 'Etkilenen servisler', 'oyu', 'servis çözümü',
  "Trace'ler", "Trace'i aç", 'kapanmış dakikalar', 'seçili', 'kapalı', 'Yalnız', 'sadece', 'tümünü seç', 'Oracle hata tablosu',
];
const leaked = (r: { text: string; html: string }) => TR_LABELS.filter(l => r.text.includes(l) || r.html.includes(l));

describe('Exception detayı ve filtre özeti — İngilizce arayüz (v0.10.2287)', () => {
  it('detay, Oracle kartı, Pods · nodes, örnek satırı, FacetMultiSelect: Türkçe etiket yok', async () => {
    const r = await all();
    expect(leaked(r), `Türkçe etiket İngilizce arayüzde: ${leaked(r).join(' | ')}`).toEqual([]);
    for (const en of [
      'Service', 'Team', 'SRE', 'Kind', 'Since', 'open for 3 hours · first', 'Assignee', 'nobody', 'Shorten', '25 lines',
      'less', 'showing the first 12 lines', ' · Copy copies all of it', 'Triage', 'Not in Coremetry', 'Tempo',
      'occurrences', 'Last', 'first 50 pods', '▴ less', 'have no Kubernetes context', 'unmapped: cluster-x',
      'Teknik hata group' /* etiket branding'den (oracleGroupLabel), kapsam dışı */, 'no stack — Oracle error table rows (code × operation)', 'Source', 'src-1 (source not found)',
      'Error code', 'Operation', 'Channel', 'Affected services', 'Traces', 'Only closed minutes are counted', 'operation',
      'Kind: 3 selected +1 off', 'only', 'select all',
    ]) expect(r.text, en).toContain(en);
    for (const en of ['Open the service page', 'Copy the trace id (full)', 'This trace was not found in Coremetry',
      'This trace was not sampled; it opens from Grafana Tempo', 'Fold to the first lines', '(cluster not mapped)',
      'host.name fallback (no k8s context)', "This pod's failed traces", 'There are more pods', 'Only A',
      'Group built from Oracle error table rows', 'Oracle operation code']) expect(r.html, en).toContain(en);
  });

  it('saf yardımcılar: lang verilmezse Türkçe aynen', () => {
    expect(facetSummary(['a', 'b', 'c'], 4)).toBe('3 seçili +1 kapalı');
    expect(facetSummary(['a', 'b', 'c'], 4, 'en')).toBe('3 selected +1 off');
    expect(facetSummary(['a'], 1, 'en')).toBe('all');
    expect(emptySamplesNote({ windowExhausted: true, scanned: 5 }, 'x').text).toMatch(/^Grubun zaman aralığı baştan sona tarandı \(5 aday\)/);
    expect(emptySamplesNote({ scanCapped: true, scanned: 5 }, 'x', 'en').text).toMatch(/^⚠ Scanned the newest 5 candidates/);
    const g = { state: 'new' as const, firstSeen: 1e18, resolvedAt: 0 };
    expect(exceptionOpenForLine(g, 1e18 + 3 * 3600e9, () => 'T')).toBe('3 saattir açık · ilk T');
    expect(exceptionOpenForLine(g, 1e18 + 3 * 3600e9, () => 'T', 'en')).toBe('open for 3 hours · first T');
    const now = 1_700_000_000;
    const reg = (lang?: 'en' | 'tr') => occRegion({ firstSeenNs: (now - 600) * 1e9, lastSeenNs: now * 1e9, stateLabel: 'NEW', occTimes: [now - 600, now - 540, now - 480], nowSec: now, lang });
    expect(reg()?.label).toBe('NEW · sürüyor');
    expect(reg('en')?.label).toBe('NEW · ongoing');
  });
});

describe('Exception detayı ve filtre özeti — Türkçe arayüzde metin aynen (v0.10.2287)', () => {
  it('detay ve özet', async () => {
    m.lang = 'tr';
    const r = await all();
    for (const tr of [
      'Servis', 'Ekip', 'SRE', 'Tür', 'Ne zamandır', '3 saattir açık · ilk', 'İlgilenen', 'kimse', 'Kısalt', '25 satır',
      'daha az', 'ilk 12 satır gösteriliyor', ' · Copy tamamını kopyalar', 'Triyaj', "Coremetry'de yok", 'Tempo',
      'oluşum', 'Son', 'ilk 50 pod', '▴ daha az', 'Kubernetes bağlamsız', 'unmapped: cluster-x',
      'Teknik hata grubu', 'stack yok — Oracle hata tablosu satırları (kod × operasyon)', 'Kaynak', 'src-1 (kaynak bulunamadı)',
      'Hata kodu', 'Operasyon', 'Kanal', 'Etkilenen servisler', "Trace'ler", 'Yalnız kapanmış dakikalar sayılır', 'operasyon',
      'Kind: 3 seçili +1 kapalı', 'sadece', 'tümünü seç',
    ]) expect(r.text, tr).toContain(tr);
    for (const tr of ['Servis sayfasını aç', "Trace id'yi kopyala (tam)", "Bu trace Coremetry'de bulunamadı",
      "Bu trace örneklemeye girmedi, Grafana Tempo'dan açılır", 'İlk satırlara katla', '(cluster eşlenmemiş)',
      'host.name yedeği (k8s bağlamı yok)', "Bu pod'un hatalı trace'leri", 'Daha fazla pod var', 'Yalnız A',
      'Oracle hata tablosu satırlarından oluşan grup', 'Oracle operasyon kodu']) expect(r.html, tr).toContain(tr);
  });
});
