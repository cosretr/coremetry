// @vitest-environment jsdom
// DependenciesTable — v0.10.2205 (operatör, /databases): ad bağlantısı
// gezinme kipinde detay SAYFASINI açar (Traces'e değil); kolon sırası
// reorderable'da `deps-<kind>:col-order` kaydından, hücreler başlıkla aynı
// sırada; başlık etiketi sayfanın renderHeadLabel'ı (huni).
import { describe, it, expect, afterEach, vi } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router';
import type { TimeRange } from '@/lib/types';
import { DependenciesTable, type DepRow } from './DependenciesTable';
import { parseStoredOrder } from '@/lib/depsTable';
import { setRaw, removeRaw } from '@/lib/storage';
import { dbFilterValues } from '@/pages/databases/dbFilterValues';

vi.mock('@/lib/queries/dependencies', () => ({
  useDepTrends: () => ({ isPending: false, data: [] }),
  useDepDetail: () => ({ isPending: false, data: null }),
}));
vi.mock('@/features/dependencies/DetailDrawer', () => ({ DetailDrawer: () => null }));

// jsdom'un localStorage'ı bu ortamda eksik: Map destekli ikiz.
const store = new Map<string, string>();
vi.stubGlobal('localStorage', {
  getItem: (k: string) => store.get(k) ?? null, setItem: (k: string, v: string) => { store.set(k, v); },
  removeItem: (k: string) => { store.delete(k); }, clear: () => store.clear(), key: () => null, length: 0,
});

const RANGE: TimeRange = { preset: '1h' };
const ROW: DepRow = {
  system: 'oracle', instance: 'core-db', dbName: 'CORE', spanCount: 120, errorCount: 0, errorRate: 0,
  avgDurationMs: 12, p50DurationMs: 8, p95DurationMs: 40, p99DurationMs: 90, callers: ['svc-a'],
};
const hrefOf = (r: DepRow) => `/database?system=${r.system}&instance=${r.instance}`;

let host: HTMLDivElement | null = null;
let root: Root | null = null;
function mount(node: React.ReactNode): HTMLElement {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  act(() => { root!.render(<MemoryRouter>{node}</MemoryRouter>); });
  return host;
}
afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove(); host = null; root = null;
  removeRaw('deps-db:col-order');
});

describe('DependenciesTable v0.10.2205', () => {
  it('db gezinme kipinde ad bağlantısı detay sayfasına gider (Traces değil)', () => {
    const el = mount(<DependenciesTable rows={[ROW]} kind="db" range={RANGE} rowHref={hrefOf} />);
    const a = Array.from(el.querySelectorAll('tbody a')).find(x => x.textContent === 'core-db')!;
    expect(a.getAttribute('href')).toBe(hrefOf(ROW));
    expect(a.getAttribute('href')).not.toContain('/traces');
  });
  it('rowHref yoksa ad eski Traces pivotunda kalır', () => {
    const el = mount(<DependenciesTable rows={[ROW]} kind="db" range={RANGE} />);
    const a = Array.from(el.querySelectorAll('tbody a')).find(x => x.textContent === 'core-db')!;
    expect(a.getAttribute('href')).toContain('/traces');
  });
  it('kayıtlı kolon sırası hücrelere de uygulanır; başlık etiketi sayfadan', () => {
    setRaw('deps-db:col-order', JSON.stringify(['system', 'callers', 'name', 'database']));
    const el = mount(<DependenciesTable rows={[ROW]} kind="db" range={RANGE} rowHref={hrefOf} reorderable
      renderHeadLabel={c => <span data-h={c.id}>{c.label}</span>} />);
    const heads = Array.from(el.querySelectorAll('thead [data-h]')).map(x => x.getAttribute('data-h'));
    expect(heads.slice(0, 3)).toEqual(['system', 'callers', 'name']);
    const cells = Array.from(el.querySelector('tbody tr')!.querySelectorAll('td')).map(td => td.textContent ?? '');
    // 0 = genişlet/aç hücresi; 1 System; 2 Top callers; 3 Instance
    expect(cells[2]).toContain('svc-a');
    expect(cells[3]).toContain('core-db');
  });
  it('parseStoredOrder bozuk kayıtta null', () => {
    expect(parseStoredOrder('x')).toBeNull();
    expect(parseStoredOrder('[]')).toBeNull();
    expect(parseStoredOrder('["a",1,""]')).toEqual(['a']);
  });
  it('dbFilterValues: arama, seçili değer görünür, tavan', () => {
    const vals = Array.from({ length: 60 }, (_, i) => `db-${String(i).padStart(2, '0')}`);
    expect(dbFilterValues(vals, 'db-0', '').shown).toHaveLength(10);
    const r = dbFilterValues(vals, '', 'db-59');
    expect(r.shown[0]).toBe('db-59');
    expect(r.more).toBe(10);
  });
});
