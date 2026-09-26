// @vitest-environment jsdom
//
// RecentQueries.contract.test.tsx — v0.10.927 (buton bütünlüğü, Faz 2 artığı).
// Satırlar SEÇİM değil EYLEM (tık = sorguyu geri yükle): kap role=menu,
// satırlar MenuItem (role=menuitem). Geri yüklenemeyen kayıt disabled.
// Dışarı tık + Esc katmanı kapatır — davranış göçte değişmedi.
import { describe, it, expect, afterEach, beforeEach, vi } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import type { ReactNode } from 'react';
import { __resetEscLayers, topEscLayer } from '@/lib/escLayer';
import { RecentQueries } from './RecentQueries';
import type { QueryHistoryEntry } from './useQueryHistory';

let host: HTMLDivElement | null = null;
let root: Root | null = null;
function render(node: ReactNode): HTMLElement {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  act(() => { root!.render(node); });
  return host;
}
beforeEach(() => { __resetEscLayers(); });
afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  root = null; host = null;
});

const now = Date.now();
const history: QueryHistoryEntry[] = [
  { desc: 'spans · count by service', state: '?src=spans&agg=count', tm: now - 60_000 },
  { desc: 'eski kayıt', state: { legacy: true }, tm: now - 120_000 },
];

function openMenu(el: HTMLElement) {
  const trigger = el.querySelector('button[aria-expanded]') as HTMLButtonElement;
  act(() => { trigger.click(); });
  return trigger;
}

describe('RecentQueries', () => {
  it('menü: role=menu + aria-label, satırlar menuitem; tetik aria-haspopup=menu', () => {
    const el = render(<RecentQueries history={history} onApply={() => {}} />);
    const trigger = openMenu(el);
    expect(trigger.getAttribute('aria-haspopup')).toBe('menu');
    expect(trigger.getAttribute('aria-expanded')).toBe('true');
    const menu = el.querySelector('[role="menu"]')!;
    expect(menu.getAttribute('aria-label')).toBe('Son sorgular');
    expect(el.querySelector('[role="listbox"], [role="option"]')).toBeNull();
    const items = menu.querySelectorAll<HTMLButtonElement>('button.menuitem[role="menuitem"]');
    expect(items.length).toBe(2);
    // İki sütun: ad + zaman, .menuitem-label içinde.
    const label = items[0].querySelector('.menuitem-label')!;
    expect(label.textContent).toContain('spans · count by service');
    expect(label.textContent).toContain('önce');
  });

  it('tık geri yükler ve kapanır; geri yüklenemeyen kayıt disabled + açıklamalı title', () => {
    const onApply = vi.fn();
    const el = render(<RecentQueries history={history} onApply={onApply} />);
    openMenu(el);
    const items = el.querySelectorAll<HTMLButtonElement>('[role="menuitem"]');
    expect(items[1].disabled).toBe(true);
    expect(items[1].title).toContain('geri yüklenemiyor');
    expect(items[0].disabled).toBe(false);
    act(() => { items[0].click(); });
    expect(onApply).toHaveBeenCalledWith('?src=spans&agg=count');
    expect(el.querySelector('[role="menu"]')).toBeNull();
  });

  it('dışarı tık ve Esc katmanı menüyü kapatır', () => {
    const el = render(<RecentQueries history={history} onApply={() => {}} />);
    openMenu(el);
    act(() => { document.body.dispatchEvent(new MouseEvent('mousedown', { bubbles: true })); });
    expect(el.querySelector('[role="menu"]')).toBeNull();
    openMenu(el);
    expect(el.querySelector('[role="menu"]')).not.toBeNull();
    act(() => { topEscLayer()!(); });
    expect(el.querySelector('[role="menu"]')).toBeNull();
  });
});
