// @vitest-environment jsdom
// DependenciesTable.problems — v0.10.1027 (Databases dilim 4).
//
// NE ÇİVİLİYOR: kind="db" satırı açık problem taşıyorsa ad hücresinde, adın
// ÖNÜNDE "2 problem" işareti çizilir — en ağır ÖNEME göre tonlu (öncelik
// değil: /inbox exception dışını P3'e çiviliyor) — ve satır tıkının gittiği
// detay sayfasına bağlanır; problemsiz satırda ve messaging (kind="queue")
// tablosunda HİÇ çizilmez. Yeni kolon DEĞİL (kolon kümesi değişseydi `deps-db`
// altında kayıtlı genişlikler sıfırlanırdı).
import { describe, it, expect, afterEach, vi } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import type { TimeRange } from '@/lib/types';
import { DependenciesTable, OpenProblemsMark, type DepRow } from './DependenciesTable';

// Trend sütunu ayrı bir uçtan gelir; bu test onu ölçmüyor.
vi.mock('@/lib/queries/dependencies', () => ({
  useDepTrends: () => ({ isPending: false, data: [] }),
  useDepDetail: () => ({ isPending: false, data: null }),
}));
vi.mock('@/features/dependencies/DetailDrawer', () => ({ DetailDrawer: () => null }));

const RANGE: TimeRange = { preset: '1h' };

const BASE: DepRow = {
  system: 'oracle', instance: 'core-db', dbName: 'CORE',
  spanCount: 120, errorCount: 0, errorRate: 0, avgDurationMs: 12,
  p50DurationMs: 8, p95DurationMs: 40, p99DurationMs: 90,
  callers: ['svc-a'],
};
const WITH: DepRow = { ...BASE, openProblems: 2, topSeverity: 'critical' };
const WITHOUT: DepRow = { ...BASE, instance: 'quiet-db', dbName: 'QUIET' };
const hrefOf = (r: DepRow) => `/database?system=${r.system}&instance=${r.instance}`;
const TIP = 'Şu an açık 2 problem (“problem değil” işaretliler hariç). Tıklayınca veritabanı detayı açılır.';

let host: HTMLDivElement | null = null;
let root: Root | null = null;
function render(node: React.ReactNode): HTMLElement {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  act(() => { root!.render(<MemoryRouter>{node}</MemoryRouter>); });
  return host;
}
const mount = (kind: 'db' | 'queue', rows: DepRow[], withHref = true) => render(
  <DependenciesTable rows={rows} kind={kind} range={RANGE} onRowNavigate={() => {}}
    rowHref={withHref ? hrefOf : undefined} />);
afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  host = null; root = null;
  try { localStorage.clear(); } catch { /* jsdom */ }
});

const marks = (el: HTMLElement) => el.querySelectorAll('tbody .dep-prob-mark');
const rowOf = (el: HTMLElement, instance: string) =>
  Array.from(el.querySelectorAll('tbody tr')).find(tr => tr.textContent?.includes(instance))!;

describe('DependenciesTable — açık problem işareti (v0.10.1027)', () => {
  it('kind="db" + 2 açık / critical → "2 problem" kırmızı, detay sayfasına bağlı', () => {
    const el = mount('db', [WITH, WITHOUT]);
    const mark = rowOf(el, 'core-db').querySelector('.dep-prob-mark') as HTMLAnchorElement | null;
    expect(mark).not.toBeNull();
    expect(mark!.tagName).toBe('A');
    expect(mark!.getAttribute('href')).toBe(hrefOf(WITH));
    expect(mark!.getAttribute('title')).toBe(TIP);
    const badge = mark!.querySelector('.badge')!;
    expect(badge.textContent).toBe('2 problem');
    expect(badge.className).toBe('badge b-err');
    // Problemsiz satırda işaret yok.
    expect(rowOf(el, 'quiet-db').querySelector('.dep-prob-mark')).toBeNull();
    expect(marks(el).length).toBe(1);
  });

  it('aria-label görünen metinle BAŞLAR (WCAG label-in-name)', () => {
    const el = mount('db', [WITH]);
    const label = el.querySelector('tbody .dep-prob-mark')!.getAttribute('aria-label')!;
    expect(label.startsWith('2 problem')).toBe(true);
    expect(label).toContain('şu an açık');
  });

  it('ipucu pencere / "detayda listelenir" vaadi vermez', () => {
    const el = mount('db', [WITH]);
    const tip = el.querySelector('tbody .dep-prob-mark')!.getAttribute('title')!;
    expect(tip).not.toMatch(/listelenir|pencere/);
  });

  it('önem tonları: warning sarı, info nötr (renk yalnız sapan değerde)', () => {
    const w = mount('db', [{ ...WITH, topSeverity: 'warning', openProblems: 4 }]);
    const wb = w.querySelector('tbody .dep-prob-mark .badge')!;
    expect(wb.textContent).toBe('4 problem');
    expect(wb.className).toBe('badge b-warn');
    act(() => { root?.unmount(); });
    host?.remove();
    const i = mount('db', [{ ...WITH, topSeverity: 'info', openProblems: 1 }]);
    expect(i.querySelector('tbody .dep-prob-mark .badge')!.className).toBe('badge b-gray');
  });

  it('rowHref verilmezse işaret bağlantısız, ipucu tık vaadi vermez', () => {
    const el = mount('db', [WITH], false);
    const mark = el.querySelector('tbody .dep-prob-mark')!;
    expect(mark.tagName).toBe('SPAN');
    expect(mark.textContent).toBe('2 problem');
    expect(mark.getAttribute('title')).not.toContain('Tıklayınca');
  });

  it('aynı satır nesnesi kind="queue" altında → işaret YOK', () => {
    const el = mount('queue', [{ ...WITH, destination: 'orders-topic' }]);
    expect(marks(el).length).toBe(0);
    expect(el.textContent).not.toContain('2 problem');
  });

  it('yeni kolon yok; işaret ad hücresinde, adın ÖNÜNDE', () => {
    const el = mount('db', [WITH]);
    const heads = Array.from(el.querySelectorAll('thead th'));
    expect(rowOf(el, 'core-db').querySelectorAll(':scope > td').length).toBe(heads.length);
    expect(heads.some(th => /problem|önem|öncelik|priority/i.test(th.textContent ?? ''))).toBe(false);
    // Hücre nowrap + overflow:hidden: uzun bir adın arkasında işaret kırpılırdı.
    const mark = el.querySelector('tbody .dep-prob-mark')!;
    const cell = mark.closest('td')!;
    const nameLink = Array.from(cell.querySelectorAll('a')).find(a => a.textContent === 'core-db')!;
    expect(mark.compareDocumentPosition(nameLink) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });
});

// İşaretin KENDİ yayılım kesmesi: yayılımı durdurmayan bir hücrede çizilir.
// Mark'ın onClick'i kalkarsa satır tıkı tetiklenir ve bu test kırılır (tablo
// içindeki hücre de kestiği için orada ölçülemezdi).
describe('OpenProblemsMark — kendi tıkı satıra yayılmaz', () => {
  it('yayılımı kesmeyen bir hücrede tık satır işleyicisine ulaşmaz', () => {
    const onRow = vi.fn();
    const el = render(
      <table><tbody><tr onClick={onRow}><td>
        <OpenProblemsMark row={WITH} href={hrefOf(WITH)} />
      </td></tr></tbody></table>,
    );
    const mark = el.querySelector('.dep-prob-mark') as HTMLElement;
    act(() => { mark.click(); });
    expect(onRow).not.toHaveBeenCalled();
    // Kontrol: aynı hücredeki işaret DIŞI bir tık satıra ulaşır (düzenek gerçek).
    act(() => { (el.querySelector('td') as HTMLElement).click(); });
    expect(onRow).toHaveBeenCalledTimes(1);
  });
});
