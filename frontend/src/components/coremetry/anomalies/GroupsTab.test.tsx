// @vitest-environment jsdom
//
// GroupsTab — v0.10.1828 (Incidents emekliliği E6): /anomalies?tab=groups
// boş sayfa değil, tasarlanmış boş durum (Faz 3'e kadar veri yok).
import { describe, it, expect } from 'vitest';
import { act } from 'react';
import { createRoot } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import { GroupsTab, GROUPS_EMPTY_BODY } from './GroupsTab';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

describe('GroupsTab', () => {
  it('boş durum başlık + açıklama + geri bağlantı çizer', () => {
    const host = document.createElement('div');
    document.body.appendChild(host);
    const root = createRoot(host);
    act(() => { root.render(<MemoryRouter><GroupsTab /></MemoryRouter>); });
    expect(host.querySelector('.empty h3')?.textContent).toBe('Henüz anomali grubu yok');
    expect(host.textContent).toContain(GROUPS_EMPTY_BODY);
    expect(host.querySelector('a')?.getAttribute('href')).toBe('/anomalies');
    act(() => { root.unmount(); });
    host.remove();
  });
});
