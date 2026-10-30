// @vitest-environment jsdom
//
// coremetry/FilterBar API — v0.10.1617 (shadcn Faz 1 R9, plan §6).
import { describe, it, expect, afterEach, vi } from 'vitest';
import { act, type ReactNode } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { FilterBar, type FilterBarProps, type FilterChip } from './index';

// Bileşim kanıtı: FilterBar PageControls'ü sarıyor (karar k).
vi.mock('@/components/coremetry/PageControls', () => ({
  PageControls: ({ children, actions, sticky }: { children?: ReactNode; actions?: ReactNode; sticky?: boolean }) =>
    <div data-testid="page-controls" data-sticky={String(!!sticky)}>{children}<span data-testid="actions">{actions}</span></div>,
}));

let host: HTMLDivElement | null = null;
let root: Root | null = null;
afterEach(() => { act(() => { root?.unmount(); }); host?.remove(); root = null; host = null; });
function render(p: FilterBarProps) {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  act(() => { root!.render(<FilterBar {...p} />); });
  return host;
}
const CHIPS: FilterChip[] = [{ key: 'service.name', op: '=', value: 'checkout' }, { key: 'db.system', op: 'exists' }];

describe('coremetry/FilterBar', () => {
  it('PageControls içinde; sticky ve actions yuvası geçiyor', () => {
    const h = render({ chips: [], onChange: () => {}, sticky: true, actions: <i>act</i> });
    const pc = h.querySelector('[data-testid="page-controls"]')!;
    expect(pc.getAttribute('data-sticky')).toBe('true');
    expect(h.querySelector('[data-testid="actions"]')!.textContent).toBe('act');
  });
  it('çipler lib/filterChips biçimiyle; kaldırma onChange(kalanlar)', () => {
    const onChange = vi.fn();
    const h = render({ chips: CHIPS, onChange });
    expect([...h.querySelectorAll('li')].map(li => li.textContent)).toEqual(['service.name=checkout', 'db.system exists']);
    act(() => { (h.querySelector('button[aria-label="Filtreyi kaldır: service.name=checkout"]') as HTMLButtonElement).click(); });
    expect(onChange).toHaveBeenCalledWith([CHIPS[1]]);
  });
  it('sorgu kutusu: shadcn input (data-slot), değer + onChange', () => {
    const onQ = vi.fn();
    const h = render({ chips: [], onChange: () => {}, query: { value: 'abc', onChange: onQ, placeholder: 'Search logs' } });
    const inp = h.querySelector('input[data-slot="input"]') as HTMLInputElement;
    expect(inp.value).toBe('abc');
    expect(inp.getAttribute('aria-label')).toBe('Search logs');
  });
  it('çip yokken liste basılmaz', () => {
    expect(render({ chips: [], onChange: () => {} }).querySelector('ul')).toBeNull();
  });
});
