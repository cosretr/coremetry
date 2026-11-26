// @vitest-environment jsdom
//
// v0.10.2473 — servisler arası birleştirme kipi: kayıtlı değer forma gelir
// (varsayılan on), değiştirilip kaydedilince PUT tek alanla gider; kip
// değişmediyse Kaydet kapalı.
import { describe, it, expect, vi, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router';

const m = vi.hoisted(() => ({ put: vi.fn(), stored: 'on' as 'off' | 'shadow' | 'on' }));
vi.mock('@/lib/api', () => ({
  api: {
    problemGraphMerge: async () => ({ mode: m.stored }),
    putProblemGraphMerge: (mode: string) => { m.put(mode); return Promise.resolve({ mode, updatedBy: 'admin@example.com', updatedAt: 1 }); },
  },
}));
import { ProblemGraphMergeSection } from './ProblemGraphMergeSection';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
let root: Root | null = null;
let host: HTMLElement | null = null;
afterEach(() => { act(() => root?.unmount()); host?.remove(); root = null; host = null; m.put.mockReset(); m.stored = 'on'; });
const settle = () => act(async () => { await new Promise(r => setTimeout(r, 20)); });

async function mount() {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  await act(async () => { root!.render(<MemoryRouter><ProblemGraphMergeSection /></MemoryRouter>); });
  await settle();
  return host;
}

describe('ProblemGraphMergeSection', () => {
  it('stored value wins; save sends the chosen mode', async () => {
    m.stored = 'shadow';
    const d = await mount();
    const sel = d.querySelector('select') as HTMLSelectElement;
    expect(sel.value).toBe('shadow');
    const save = [...d.querySelectorAll('button')].find(b => b.textContent === 'Kaydet') as HTMLButtonElement;
    expect(save.disabled).toBe(true);
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, 'value')!.set!.call(sel, 'on');
      sel.dispatchEvent(new Event('change', { bubbles: true }));
    });
    expect(save.disabled).toBe(false);
    await act(async () => { save.click(); });
    await settle();
    expect(m.put).toHaveBeenCalledWith('on');
    expect(d.textContent).toContain('bildirimler değişmez');
  });
  it('default on when nothing stored', async () => {
    const d = await mount();
    expect((d.querySelector('select') as HTMLSelectElement).value).toBe('on');
  });
});
