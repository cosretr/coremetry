// @vitest-environment jsdom
// GroupTable.state.test.tsx — v0.10.954 (tablo standardı T12, çapraz
// inceleme) → v0.10.967 (dilim 5): GroupTable `state` prop'u aldı ve
// dtNoState mandalı onu "benimsedi" sayıyordu; Explore `state=` vermediği
// için göç gerçek değildi. v0.10.967'de Explore `groupTableState(panels)`
// veriyor. Bu dosya üç şeyi çiviler:
//   1. prop'un davranışı: state yok + satır yok → null (giriş ekranı
//      kapısı); state var → başlık durur, durum satırı tablonun içinde;
//      satır varsa satırlar kazanır.
//   2. groupTableState: panellerin durumundan yükleniyor / hata / boş
//      (sıra, metin, idle → undefined).
//   3. dürüstlük: Explore `state=` vermiyorsa tableUnityRatchet'in dtNoState
//      notu bunu söylemek ZORUNDA. Explore `state=` geçtiğinde not silinir.
import { describe, it, expect, afterEach } from 'vitest';
import { act, type ComponentProps } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { GroupTable, groupTableState } from './GroupTable';
import type { PanelData } from './PanelStack';
import { jsxOpenTags } from '@/styles/jsxTags';

let host: HTMLElement | null = null;
let root: Root | null = null;
function mount(state?: ComponentProps<typeof GroupTable>['state'], panels: PanelData[] = []): HTMLElement {
  host = document.createElement('div');
  document.body.appendChild(host);
  act(() => {
    root = createRoot(host!);
    root.render(
      <MemoryRouter>
        <GroupTable panels={panels} hiddenKeys={new Set()} onToggleHidden={() => {}}
          onIsolate={() => {}} onFocus={() => {}} state={state} />
      </MemoryRouter>,
    );
  });
  return host!;
}
afterEach(() => {
  if (root) act(() => root!.unmount());
  host?.remove(); host = null; root = null;
  try { localStorage.clear(); } catch { /* jsdom */ }
});

const panel = (letter: string, p: Partial<PanelData>): PanelData => ({
  key: letter, letter, desc: `q${letter}`, unit: '', isFormula: false,
  state: 'ready', series: [], more: 0, ...p,
});
const series = (label: string) => ({ label, color: 'var(--accent2)', points: [{ time: 1, value: 2 }] });

describe('GroupTable state prop (v0.10.954)', () => {
  it('state yok + seri yok → hiç çizilmez (giriş ekranı kapısı)', () => {
    const el = mount();
    expect(el.querySelector('table')).toBeNull();
    expect(el.innerHTML).toBe('');
  });
  it('state verilirse başlık durur, durum satırı tablonun içinde', () => {
    const el = mount({ kind: 'error', message: 'A: boom' });
    expect(el.querySelector('table thead')).not.toBeNull();
    const row = el.querySelector('tbody tr[data-dt-state]') as HTMLElement | null;
    expect(row?.dataset.dtState).toBe('error');
    expect(row?.textContent).toContain('A: boom');
  });
  it('v0.10.967 — satır varken durum satırı çizilmez (bir harf patlamış olsa da)', () => {
    const panels = [
      panel('A', { series: [series('svc-a')] }),
      panel('B', { state: 'error', errorMessage: 'boom' }),
    ];
    const el = mount(groupTableState(panels), panels);
    expect(el.querySelector('tbody tr[data-dt-state]')).toBeNull();
    expect(el.querySelectorAll('tbody tr').length).toBe(1);
    expect(el.textContent).toContain('svc-a');
  });
  it('v0.10.967 — yükleniyor: başlık + iskelet satırı, CSV düğmesi yok', () => {
    const panels = [panel('A', { state: 'loading' })];
    const el = mount(groupTableState(panels), panels);
    expect(el.querySelector('table thead')).not.toBeNull();
    const row = el.querySelector('tbody tr[data-dt-state]') as HTMLElement | null;
    expect(row?.dataset.dtState).toBe('loading');
    expect(el.textContent).not.toContain('CSV');
  });
});

describe('groupTableState (v0.10.967)', () => {
  it('hiç panel ya da hepsi idle → undefined (tablo çizilmez)', () => {
    expect(groupTableState([])).toBeUndefined();
    expect(groupTableState([panel('A', { state: 'idle', emptyReason: 'Sorgunu kur' })])).toBeUndefined();
  });
  it('bir harf yükleniyorsa yükleniyor (hata ve boştan önce)', () => {
    expect(groupTableState([
      panel('A', { state: 'error', errorMessage: 'x' }),
      panel('B', { state: 'loading' }),
    ])).toEqual({ kind: 'loading' });
  });
  it('hata: harfin kendi mesajı; formülün girdi cümlesi tekrar basılmaz', () => {
    const s = groupTableState([
      panel('A', { state: 'error', errorMessage: 'DSL parse error' }),
      panel('B', {}),
      panel('ƒ', { isFormula: true, state: 'error', errorMessage: 'Girdi sorgusu A hata verdi' }),
    ]);
    expect(s).toEqual({ kind: 'error', message: 'Sorgu A hata verdi: DSL parse error' });
  });
  it('hata: iki harf birlikte patlarsa ikisi de söylenir', () => {
    const s = groupTableState([
      panel('A', { state: 'error', errorMessage: 'one' }),
      panel('B', { state: 'error' }),
    ]);
    expect(s).toEqual({ kind: 'error', message: 'Sorgu A hata verdi: one · Sorgu B hata verdi' });
  });
  it('boş: panelin emptyReason metni; aynı sebep bir kez, farklıysa harf harf', () => {
    const same = 'Bu pencerede veri yok — aralığı genişlet veya filtreleri azalt';
    expect(groupTableState([panel('A', { emptyReason: same }), panel('B', { emptyReason: same })]))
      .toEqual({ kind: 'empty', message: same });
    expect(groupTableState([panel('A', { emptyReason: same }), panel('B', { emptyReason: '_bucket serisi yok' })]))
      .toEqual({ kind: 'empty', message: `A: ${same} · B: _bucket serisi yok` });
    expect(groupTableState([panel('A', {})])).toEqual({ kind: 'empty' });
  });
});

describe('dtNoState kredisi dürüst (v0.10.954)', () => {
  const src = (...p: string[]) => readFileSync(resolve(__dirname, ...p), 'utf8');
  it('Explore state= vermiyorsa mandal notu GroupTable göçünün gerçek olmadığını söyler', () => {
    const tags = jsxOpenTags(src('..', 'Explore.tsx'), 'GroupTable');
    expect(tags.length).toBeGreaterThan(0);
    const passesState = tags.some(t => /\sstate=\{/.test(t.tag));
    if (passesState) return; // göç gerçek — not silinebilir
    const ratchet = src('..', '..', 'styles', 'tableUnityRatchet.test.ts');
    expect(ratchet, 'Explore <GroupTable> state= vermiyor: tableUnityRatchet dtNoState notu bunu söylemeli')
      .toMatch(/explore\/GroupTable[^]*?GERÇEK DEĞİL/);
  });
  it('v0.10.967 — Explore her <GroupTable>e panellerden türetilen durumu verir', () => {
    const explore = src('..', 'Explore.tsx');
    const tags = jsxOpenTags(explore, 'GroupTable');
    expect(tags.every(t => /\sstate=\{groupState\}/.test(t.tag))).toBe(true);
    expect(explore).toContain('useMemo(() => groupTableState(panels), [panels])');
  });
});
