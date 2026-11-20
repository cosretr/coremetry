// @vitest-environment jsdom
//
// Rollouts.states — v0.10.967 (tablo standardı dilim 5, P-2).
//
// NE ÇİVİLİYOR:
//   • Toplu sekmesinin iki statik top-N tablosu ("En çok rollback / deploy
//     alan workload'lar") boşken standart DataTableState satırı basar
//     (dilim 4'ün elle yazılmış ara işaretlemesi değil — DOM ikisini
//     ayıramaz, aynı işaretleme; bu ayrımı kaynak çivisi tutar); başlık durur.
//   • Canlı liste (dilim 4) — yükleniyor / hata / eşleşme yok / boş tablonun
//     İÇİNDE; hata, önbellekteki bayat satırları da gizler (düşen tazeleme
//     "güncel liste" gibi okunmasın); sunucu süzgeci (cluster / durum /
//     namespace) sıfır satırda "eşleşme yok", süzgeçsiz sıfır satırda "boş".
//
// NEDEN GERÇEK MOUNT: kapılar tip-doğru biçimde yanlış yazılabilir; tsc ve
// eslint sessiz kalır. Yalnız veri kancaları sahte, tablo/primitif gerçek.
import { describe, it, expect, afterEach, vi } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { RolloutListResponse, RolloutStats, WorkloadRollout } from '@/lib/types';

interface FakeQuery<T> { data: T | undefined; isPending: boolean; error: Error | null }
const h = vi.hoisted(() => ({
  list: { data: undefined, isPending: true, error: null } as FakeQuery<unknown>,
  stats: { data: undefined, isPending: true, error: null } as FakeQuery<unknown>,
}));

// v0.10.2304 — metinler arayüz dilinde; bu test bugünkü Türkçe metni pinler.
vi.mock('@/lib/i18n', async (orig) => ({ ...(await orig<typeof import('@/lib/i18n')>()), useLang: () => 'tr' as const }));
vi.mock('@/lib/queries', async (importOriginal) => {
  const mod = await importOriginal<Record<string, unknown>>();
  return {
    ...mod,
    useRollouts: () => h.list,
    useRolloutStats: () => h.stats,
    useRolloutRuns: () => ({ data: undefined, isPending: false, error: null }),
    useEntityClusters: () => ({ data: { clusters: [{ id: 'c1', name: 'prod-east' }] }, isPending: false, error: null }),
  };
});
vi.mock('@/components/AuthProvider', () => ({
  useAuth: () => ({ user: { username: 'op', role: 'viewer' }, loading: false }),
}));
vi.mock('@/components/Topbar', () => ({ Topbar: () => null }));
vi.mock('@/components/RolloutDrawer', () => ({ RolloutDrawer: () => null }));

import RolloutsPage from './Rollouts';
import { SvcBadge } from '@/components/traces/shared';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
window.matchMedia = ((q: string) => ({
  matches: false, media: q, onchange: null,
  addListener: () => {}, removeListener: () => {},
  addEventListener: () => {}, removeEventListener: () => {}, dispatchEvent: () => false,
})) as unknown as typeof window.matchMedia;
class NoopResizeObserver { observe() {} unobserve() {} disconnect() {} }
(globalThis as unknown as { ResizeObserver: unknown }).ResizeObserver = NoopResizeObserver;

const ROLLOUT: WorkloadRollout = {
  clusterId: 'c1', namespace: 'shop', workload: 'checkout', kind: 'Deployment', revision: 'checkout-7f9c',
  startedAt: 1_758_000_000_000, status: 'completed', prevRevision: 'checkout-6a1b',
  image: 'reg/checkout:1.2.3', imageTag: '1.2.3', prevImage: 'reg/checkout:1.2.2', prevImageTag: '1.2.2',
  firstSpanAt: 0, trafficConfirmedAt: 0, ksmStartedAt: 0, podsReadyAt: 0, ksmNotReadySince: 0, completedAt: 0,
  detectedBy: 'ksm', spanCount: 10, note: '', updatedAt: 0,
};
const LIST: RolloutListResponse = { rollouts: [ROLLOUT], from: 0, to: 1, limit: 200 };
const STATS: RolloutStats = {
  total: 4, completed: 3, rolledBack: 0, inProgress: 1, stalled: 0, superseded: 0, from: 0, to: 1,
  perDay: 4, rollbackRate: 0, meanDurationSec: 60, p95DurationSec: 120,
  topRollback: [],
  topDeploy: [{ clusterId: 'c1', namespace: 'shop', workload: 'checkout', n: 4 }],
  byDay: [{ day: '2026-09-27', total: 4, rolledBack: 0 }],
};

let host: HTMLDivElement | null = null;
let root: Root | null = null;

function mount(url: string): HTMLElement {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  act(() => {
    root!.render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={[url]}><RolloutsPage /></MemoryRouter>
      </QueryClientProvider>,
    );
  });
  return host;
}

afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  host = null; root = null;
  h.list = { data: undefined, isPending: true, error: null };
  h.stats = { data: undefined, isPending: true, error: null };
  try { localStorage.clear(); } catch { /* özel pencere */ }
});

const tableByHead = (el: HTMLElement, h3: string) => {
  const title = Array.from(el.querySelectorAll('h3')).find(x => x.textContent === h3);
  return title?.parentElement?.querySelector('table') ?? null;
};
const stateRows = (t: Element) => t.querySelectorAll('tbody tr[data-dt-state]');
const dataRows = (t: Element) => Array.from(t.querySelectorAll('tbody tr')).filter(tr => !tr.hasAttribute('data-dt-state'));

describe('Rollouts Toplu — statik top-N tabloları (P-2)', () => {
  it('rollback alan yok: başlık durur, tek DataTableState boş satırı colSpan = 2 <th>', () => {
    h.stats = { data: STATS, isPending: false, error: null };
    const el = mount('/rollouts?tab=stats');
    const t = tableByHead(el, "En çok rollback alan workload'lar");
    expect(t, 'rollback tablosu çizilmedi').not.toBeNull();
    expect(t!.querySelectorAll('thead th')).toHaveLength(2);
    expect(stateRows(t!)).toHaveLength(1);
    const row = t!.querySelector<HTMLTableRowElement>('tbody tr[data-dt-state="empty"]')!;
    expect(row.querySelector('td')!.colSpan).toBe(2);
    expect(row.querySelector('.dt-state-body')).not.toBeNull();
    expect(row.textContent).toBe('Bu pencerede rollback alan workload yok');
    expect(dataRows(t!)).toHaveLength(0);
  });

  it('deploy alan var: satırlar, durum satırı yok', () => {
    h.stats = { data: STATS, isPending: false, error: null };
    const el = mount('/rollouts?tab=stats');
    const t = tableByHead(el, "En çok deploy alan workload'lar")!;
    expect(stateRows(t)).toHaveLength(0);
    expect(dataRows(t)).toHaveLength(1);
    expect(t.textContent).toContain('prod-east');
  });

  it('deploy alan da yok: ikinci tablo da kendi boş satırını basar', () => {
    h.stats = { data: { ...STATS, topDeploy: [] }, isPending: false, error: null };
    const el = mount('/rollouts?tab=stats');
    const t = tableByHead(el, "En çok deploy alan workload'lar")!;
    expect(t.querySelector('tbody tr[data-dt-state="empty"]')!.textContent).toBe('Bu pencerede deploy alan workload yok');
  });

  // v0.10.1436 — sayaç kutuları durum rozetiyle aynı İngilizce sözlükten
  // (statusLabel, v0.10.1421); Türkçe başlık (Tamamlanan / Geri alınan /
  // Devralınan / Sürüyor / Takılı) geri gelmez.
  it('sayaç kutusu başlıkları İngilizce (v0.10.1436)', () => {
    h.stats = { data: STATS, isPending: false, error: null };
    const el = mount('/rollouts?tab=stats');
    const text = el.textContent ?? '';
    for (const want of ['Completed', 'Rolled back', 'Superseded', 'In progress', 'Stalled']) expect(text).toContain(want);
    for (const tr of ['Tamamlanan', 'Geri alınan', 'Devralınan', 'Sürüyor', 'Takılı']) expect(text).not.toContain(tr);
  });

  // DOM, DataTableState'in boş satırını dilim 4'ün elle yazılmış ara
  // işaretlemesinden AYIRAMAZ (ikisi aynı <tr data-dt-state><td.dt-state>);
  // göçün geri alınmasını yalnız kaynak yakalar.
  it('kaynak: iki statik top-N durum satırı DataTableState, elle yazılmış data-dt-state yok', () => {
    const src = readFileSync(resolve(__dirname, './Rollouts.tsx'), 'utf8');
    expect(src).not.toMatch(/data-dt-state=/);
    expect(src.match(/<DataTableState colSpan=\{2\}/g) ?? []).toHaveLength(2);
  });
});

describe('Rollouts Canlı — durumlar tablonun içinde', () => {
  const liveTable = (el: HTMLElement) => el.querySelector('.table-wrap table')!;

  it('yükleniyor: başlık durur, iskelet satırı', () => {
    const el = mount('/rollouts');
    expect(liveTable(el).querySelector('thead')).not.toBeNull();
    expect(liveTable(el).querySelector('tbody tr[data-dt-state="loading"]')).not.toBeNull();
  });

  it('satırlar: durum satırı yok', () => {
    h.list = { data: LIST, isPending: false, error: null };
    const el = mount('/rollouts');
    expect(stateRows(liveTable(el))).toHaveLength(0);
    expect(dataRows(liveTable(el))).toHaveLength(1);
  });

  it('tazeleme düştü, önbellek eski listeyi tutuyor: hata satırı, bayat satır yok', () => {
    h.list = { data: LIST, isPending: false, error: new Error('HTTP 502: upstream') };
    const el = mount('/rollouts');
    const row = liveTable(el).querySelector('tbody tr[data-dt-state="error"]');
    expect(row, 'hata satırı bayat satırlar yüzünden görünmüyor').not.toBeNull();
    expect(row!.textContent).toContain('Rollout listesi yüklenemedi: HTTP 502: upstream');
    expect(dataRows(liveTable(el))).toHaveLength(0);
  });

  it('süzgeç yok + sıfır satır: boş (eşleşme yok değil), sunucu notu korunur', () => {
    h.list = { data: { ...LIST, rollouts: [], note: 'reconciler ilk turda' }, isPending: false, error: null };
    const el = mount('/rollouts');
    const t = liveTable(el);
    expect(t.querySelector('tbody tr[data-dt-state="no-match"]')).toBeNull();
    expect(t.querySelector('tbody tr[data-dt-state="empty"]')!.textContent).toBe('Bu pencerede rollout yok — reconciler ilk turda');
  });

  it('sunucu süzgeci (namespace) + sıfır satır: eşleşme yok', () => {
    h.list = { data: { ...LIST, rollouts: [] }, isPending: false, error: null };
    const el = mount('/rollouts?namespace=shop');
    const t = liveTable(el);
    expect(t.querySelector('tbody tr[data-dt-state="no-match"]')).not.toBeNull();
    expect(t.querySelector('tbody tr[data-dt-state="empty"]')).toBeNull();
  });
});

// v0.10.984 — Rollouts v2 P2.3: sunucu `v2: true` döndürünce v2 kolon seti
// (karar 4: Kaynak/detectedBy ve Span yok, Nesil var) ayrı storageKey'le;
// v1 cevabında kolonlar bugünkü gibi. Satır kimliği / çekmece bağlantısı 6
// parça. Kapalı durum sayfa adını söyler.
describe('Rollouts Canlı — v2 kaynağı (P2.3)', () => {
  const liveTable = (el: HTMLElement) => el.querySelector('.table-wrap table')!;
  const heads = (el: HTMLElement) => Array.from(liveTable(el).querySelectorAll('thead th')).map(th => th.textContent ?? '');
  const V2ROW: WorkloadRollout = {
    ...ROLLOUT, revision: 'checkout-8b2d', detectedBy: 'ksm', spanCount: 0, generation: 7, incarnationAt: 1_757_990_000_000,
    changeType: 'rollout', v2Status: 'succeeded', specReplicas: 3, updatedReplicas: 3, availableReplicas: 3,
  };

  it('v1 cevabı: Span + Kaynak kolonları, Nesil yok', () => {
    h.list = { data: LIST, isPending: false, error: null };
    const hs = heads(mount('/rollouts'));
    expect(hs.some(x => x.includes('Span'))).toBe(true);
    expect(hs.some(x => x.includes('Kaynak'))).toBe(true);
    expect(hs.some(x => x.includes('Nesil'))).toBe(false);
  });

  it('v2 cevabı: Kaynak ve Span düşer, Nesil + replika; hücre sayısı başlıkla eşit', () => {
    h.list = { data: { ...LIST, rollouts: [V2ROW], v2: true }, isPending: false, error: null };
    const el = mount('/rollouts');
    const hs = heads(el);
    expect(hs.some(x => x.includes('Span'))).toBe(false);
    expect(hs.some(x => x.includes('Kaynak'))).toBe(false);
    expect(hs.some(x => x.includes('Nesil'))).toBe(true);
    const row = dataRows(liveTable(el))[0];
    expect(row.querySelectorAll('td')).toHaveLength(hs.length);
    expect(row.textContent).toContain('3/3/3');
    expect(row.textContent).toContain('Deployment'); // değişiklik türü change_type'tan
  });

  it('v2 boş liste: dedektör metni (sunucu notu yoksa)', () => {
    h.list = { data: { ...LIST, rollouts: [], v2: true }, isPending: false, error: null };
    const el = mount('/rollouts');
    expect(liveTable(el).querySelector('tbody tr[data-dt-state="empty"]')!.textContent).toContain('KSM dedektörü');
  });

  it('kapalı: sayfa adı «Deployment/Rollouts kapalı», Ayarlar sekmesine yönlendirmez', () => {
    h.list = { data: { disabled: true } as RolloutListResponse, isPending: false, error: null };
    const el = mount('/rollouts');
    expect(el.textContent).toContain('Deployment/Rollouts kapalı');
    expect(el.textContent).not.toContain('Settings → Rollouts');
  });
});

// v0.10.1328 (operatör-raporlu, prod ekran görüntüsü): Canlı tablosunun
// Workload hücresi `a.sec` kutu düğmesi gibi çiziliyordu ("coremetry-api"
// çerçeveli dikdörtgende). Operatör: "Deployment Rollouts sayfasında servis
// ismi gibi yazsın. Traces sayfasındaki farklı renkler güzeldi." Sözleşme:
// hücre Traces Service kolonunun PAYLAŞILAN hapını (SvcBadge — aynı hash →
// aynı renk) gerçek <Link> olarak basar; `.sec` yok. Toplu top-N ve çekmece
// servis hücresi de aynı hap.
describe('Rollouts — workload hücresi servis hapı (v0.10.1328)', () => {
  const liveTable = (el: HTMLElement) => el.querySelector('.table-wrap table')!;
  const refStyle = (name: string): string => {
    const d = document.createElement('div');
    const r = createRoot(d);
    act(() => { r.render(<SvcBadge name={name} />); });
    const s = d.querySelector('span')!.getAttribute('style') ?? '';
    act(() => { r.unmount(); });
    return s;
  };

  it('Canlı: Workload hücresi SvcBadge stilinde gerçek link, kutu (.sec) değil; title tam bağlam', () => {
    h.list = { data: LIST, isPending: false, error: null };
    const el = mount('/rollouts');
    const row = dataRows(liveTable(el))[0];
    const a = Array.from(row.querySelectorAll('a')).find(x => x.textContent === 'checkout')!;
    expect(a, 'workload linki çizilmedi').toBeTruthy();
    expect(a.classList.contains('sec')).toBe(false);
    expect(a.getAttribute('href')).toBeTruthy();
    expect(a.getAttribute('style')).toBe(refStyle('checkout'));
    expect(a.style.color).not.toBe('');
    // v0.10.1420 — title bağlamla başlar, ardından hedefi söyler (servis / entity yedeği).
    expect(a.title.split('\n')[0]).toBe('prod-east / shop / checkout');
    expect(a.closest('td')!.title).toBe('prod-east / shop / checkout');
  });

  it('Toplu: top-N workload adı aynı hap', () => {
    h.stats = { data: STATS, isPending: false, error: null };
    const el = mount('/rollouts?tab=stats');
    const t = tableByHead(el, "En çok deploy alan workload'lar")!;
    const chip = Array.from(t.querySelectorAll('tbody span')).find(x => x.textContent === 'checkout')!;
    expect(chip.getAttribute('style')).toBe(refStyle('checkout'));
  });

  it('kaynak: workload / çekmece servis adı artık a.sec değil', () => {
    const src = readFileSync(resolve(__dirname, './Rollouts.tsx'), 'utf8');
    expect(src).not.toContain('className="sec">{r.workload}');
    // v0.10.1420 — birincil tık satırın servisi; eşleşme yoksa entity (wlHref).
    expect(src).toContain('<SvcBadge name={r.workload} to={r.service ? serviceHref(r.service, { range }) : wlHref}');
    const drawer = readFileSync(resolve(__dirname, '../components/RolloutDrawer.tsx'), 'utf8');
    expect(drawer).not.toContain('className="sec">{s.service}');
    expect(drawer).toContain('<SvcBadge name={s.service} to={serviceHref(');
  });
});
