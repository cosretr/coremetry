// @vitest-environment jsdom
// GroupTable.state.test.tsx — v0.10.954 (tablo standardı T12, çapraz
// inceleme): GroupTable `state` prop'u aldı ve dtNoState mandalı onu
// "benimsedi" sayıyor; ama Explore `state=` vermediği sürece seri yokken
// tablo eskisi gibi HİÇ çizilmez — kullanıcıya görünen değişiklik yok.
// Bu dosya iki şeyi çiviler:
//   1. prop'un davranışı: state yok + satır yok → null; state var → başlık
//      durur, durum satırı tablonun içinde.
//   2. dürüstlük: Explore `state=` vermiyorsa tableUnityRatchet'in dtNoState
//      notu bunu söylemek ZORUNDA — bir sonraki dilim tavanı bu sahte -1'e
//      dayanarak düşürmesin. Explore `state=` geçtiğinde not silinir.
import { describe, it, expect, afterEach } from 'vitest';
import { act, type ComponentProps } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { GroupTable } from './GroupTable';
import { jsxOpenTags } from '@/styles/jsxTags';

let host: HTMLElement | null = null;
let root: Root | null = null;
function mount(state?: ComponentProps<typeof GroupTable>['state']): HTMLElement {
  host = document.createElement('div');
  document.body.appendChild(host);
  act(() => {
    root = createRoot(host!);
    root.render(
      <MemoryRouter>
        <GroupTable panels={[]} hiddenKeys={new Set()} onToggleHidden={() => {}}
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

describe('GroupTable state prop (v0.10.954)', () => {
  it('state yok + seri yok → hiç çizilmez (Explore bugünkü yolu)', () => {
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
});
