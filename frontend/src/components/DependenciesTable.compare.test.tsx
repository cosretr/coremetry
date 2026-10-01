// @vitest-environment jsdom
// DependenciesTable.compare — v0.10.1025 (Databases dilim 3).
//
// NE ÇİVİLİYOR: kind="db" tablosu `compare` açıkken prior* taşıyan satırda
// delta rozetlerini GERÇEKTEN çizer, kapalıyken çizmez. /databases'in
// "Compare vs prior" kutusu v0.9.433'ten beri ölüydü (mount `compare`
// geçmiyordu); kaynak pini (pages/databases/compareWiring.pin.test.ts)
// bağlantıyı, bu test de bağlantının UCUNDAKİ davranışı tutuyor — prop
// geçse bile rozetler bir gün db türü için kapanırsa burada kızarır.
import { describe, it, expect, afterEach, vi } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import type { TimeRange } from '@/lib/types';
import { DependenciesTable, type DepRow } from './DependenciesTable';

// Trend sütunu ayrı bir uçtan (/api/databases/trends) gelir; bu test onu
// ölçmüyor. Ağ yok, React Query sağlayıcısı yok: kanca sabit döner.
vi.mock('@/lib/queries/dependencies', () => ({
  useDepTrends: () => ({ isPending: false, data: [] }),
  useDepDetail: () => ({ isPending: false, data: null }),
}));
// db tarafında satır-altı çekmece çalışma zamanında erişilemez
// (onRowNavigate); modülün ağır panel ağacını yüklemeye gerek yok.
vi.mock('@/features/dependencies/DetailDrawer', () => ({ DetailDrawer: () => null }));

const RANGE: TimeRange = { preset: '1h' };

const ROW: DepRow = {
  system: 'postgresql', instance: 'pg-1', dbName: 'orders',
  spanCount: 120, errorCount: 6, errorRate: 5, avgDurationMs: 12,
  p50DurationMs: 8, p95DurationMs: 40, p99DurationMs: 90,
  callers: ['svc-a'],
  // Her metrikte ≥ %5 fark — TrendDelta "·" yerine ok + yüzde çizer.
  priorSpanCount: 100, priorErrorCount: 2, priorAvgMs: 10, priorP50Ms: 6, priorP99Ms: 60,
};

let host: HTMLDivElement | null = null;
let root: Root | null = null;
function mount(compare: boolean, rows: DepRow[] = [ROW]): HTMLElement {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  act(() => {
    root!.render(
      <MemoryRouter>
        <DependenciesTable rows={rows} kind="db" range={RANGE} compare={compare} onRowNavigate={() => {}} />
      </MemoryRouter>,
    );
  });
  return host;
}
afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  host = null; root = null;
  try { localStorage.clear(); } catch { /* jsdom */ }
});

const deltas = (el: HTMLElement) => el.querySelectorAll('tbody [title^="Prior window"]');

describe('DependenciesTable kind="db" — compare delta rozetleri (v0.10.1025)', () => {
  it('compare açık + prior taşıyan satır → rozetler DOM\'da (çağrı, hata, avg, p50, p99)', () => {
    const el = mount(true);
    expect(deltas(el).length).toBe(5);
    // Çağrı +%20 (neutral) — ok ve yüzde yazılı.
    expect(Array.from(deltas(el)).some(n => n.textContent === '20%')).toBe(true);
  });

  it('compare kapalı → aynı satırda HİÇ rozet yok (kutu kapalıyken tablo bugünkü gibi)', () => {
    const el = mount(false);
    expect(deltas(el).length).toBe(0);
  });

  // v0.10.1025 (inceleme R2) — EŞLEŞEN satırda 0 → N hata regresyonu.
  // Sayfa omitempty priorErrorCount'u eşleşen satır için 0'a onarıyor; hücre
  // eskiden varsayılan kipte "listede yeni" (mavi, ipucu: önceki listede
  // YOKTU) basıyordu — satır eşleşmişken. Doğrusu: "önce 0", kötüleşme rengi.
  it('eşleşen satır, priorErrorCount 0 → errorCount 3: "önce 0" kırmızı, "listede yeni" YOK', () => {
    const el = mount(true, [{ ...ROW, errorCount: 3, priorErrorCount: 0 }]);
    const badge = el.querySelector('tbody [data-trend-delta="was-zero"]') as HTMLElement | null;
    expect(badge, '"önce 0" rozeti yok').not.toBeNull();
    expect(badge!.textContent).toContain('önce 0');
    expect(badge!.style.color).toBe('var(--err)');
    expect(el.textContent).not.toContain('listede yeni');
  });

  it('kind="queue": hata hücresi ve üretim/dk hücresi de "önce 0" der (Messaging aynı onarımı yapıyor)', () => {
    const q: DepRow = {
      system: 'kafka', cluster: '(default)', destination: 'orders',
      spanCount: 120, errorCount: 3, errorRate: 2.5, avgDurationMs: 12,
      p50DurationMs: 8, p95DurationMs: 40, p99DurationMs: 90, callers: [],
      producePerMin: 4, consumePerMin: 0, produceCount: 240, consumeCount: 0,
      priorSpanCount: 100, priorErrorCount: 0, priorProducePerMin: 0, priorConsumePerMin: 0,
      priorAvgMs: 10, priorP50Ms: 6, priorP99Ms: 60,
    };
    host = document.createElement('div');
    document.body.appendChild(host);
    root = createRoot(host);
    act(() => {
      root!.render(
        <MemoryRouter>
          <DependenciesTable rows={[q]} kind="queue" range={RANGE} compare onRowNavigate={() => {}} />
        </MemoryRouter>,
      );
    });
    const badges = Array.from(host.querySelectorAll('tbody [data-trend-delta="was-zero"]')) as HTMLElement[];
    // hata (lowerBetter → kırmızı) + üretim (neutral → yön tonu); tüketim 0→0 çizilmez.
    expect(badges.length).toBe(2);
    expect(badges.map(b => b.style.color).sort()).toEqual(['var(--accent2)', 'var(--err)']);
    expect(host.textContent).not.toContain('listede yeni');
  });

  it('prior ikizi olmayan satır compare açıkken de rozetsiz (undefined → gizli)', () => {
    const bare: DepRow = { ...ROW,
      priorSpanCount: undefined, priorErrorCount: undefined, priorAvgMs: undefined,
      priorP50Ms: undefined, priorP99Ms: undefined };
    const el = mount(true, [bare]);
    expect(deltas(el).length).toBe(0);
    expect(el.textContent).not.toContain('listede yeni');
  });
});
