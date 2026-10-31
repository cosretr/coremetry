// @vitest-environment jsdom
//
// OnlineUsersSheet.test.tsx — v0.10.1736 "Kim çevrimiçi" her role.
//   • boş liste → "Şu an kimse çevrimiçi değil" (asla boş panel);
//   • yükleniyor → Spinner, hata → Empty;
//   • admin'de ad /users/:id bağlantısı, diğer rollerde düz metin;
//   • satır ad · takım · göreli son görülme taşır;
//   • kabuk (Sidebar) listeyi yalnız tembel import() ile anar.
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { OnlineUsersBody, ONLINE_STRINGS } from './OnlineUsersSheet';
import type { OnlineUser } from '@/lib/types';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const NOW = Date.UTC(2026, 9, 31, 12, 0, 0);
const rows: OnlineUser[] = [
  { id: 'u1', name: 'ada', team: 'sre', lastSeenAt: (NOW - 2 * 60_000) * 1e6 },
  { id: 'u2', name: 'Bora K', lastSeenAt: (NOW - 10_000) * 1e6 },
];

let host: HTMLDivElement;
let root: Root;
beforeEach(() => { host = document.createElement('div'); document.body.appendChild(host); root = createRoot(host); });
afterEach(() => { act(() => root.unmount()); host.remove(); });

function render(props: Partial<Parameters<typeof OnlineUsersBody>[0]>) {
  act(() => root.render(
    <MemoryRouter>
      <OnlineUsersBody isLoading={false} isError={false} rows={rows} isAdmin={false} nowMs={NOW} {...props} />
    </MemoryRouter>,
  ));
}

describe('OnlineUsersBody', () => {
  it('boş liste boş-durum metnini gösterir', () => {
    render({ rows: [] });
    expect(host.textContent).toContain(ONLINE_STRINGS.empty);
    expect(ONLINE_STRINGS.empty).toBe('Şu an kimse çevrimiçi değil');
  });

  it('yükleniyor ve hata durumları boş panel değil', () => {
    render({ isLoading: true, rows: undefined });
    expect(host.querySelector('.spinner')).not.toBeNull();
    render({ isError: true, rows: undefined });
    expect(host.textContent).toContain(ONLINE_STRINGS.error);
  });

  it('viewer: ad düz metin, bağlantı yok; takım + göreli zaman var', () => {
    render({ isAdmin: false });
    expect(host.querySelectorAll('a').length).toBe(0);
    const items = host.querySelectorAll('li');
    expect(items.length).toBe(2);
    expect(items[0].textContent).toContain('ada');
    expect(items[0].textContent).toContain('· sre');
    expect(items[0].textContent).toContain('2 dk önce');
    expect(items[1].textContent).toContain('az önce');
  });

  it('admin: ad /users/:id bağlantısı', () => {
    render({ isAdmin: true });
    const links = [...host.querySelectorAll('a')].map(a => a.getAttribute('href'));
    expect(links).toEqual(['/users/u1', '/users/u2']);
  });
});

describe('Sidebar kim çevrimiçi kablolaması', () => {
  const src = readFileSync(resolve(__dirname, '../Sidebar.tsx'), 'utf8');
  it('liste yalnız tembel import() ile; statik import yok', () => {
    expect(src).toMatch(/lazy\(\(\) => import\('\.\/coremetry\/OnlineUsersSheet'\)\)/);
    expect(src).not.toMatch(/^import .*OnlineUsersSheet/m);
  });
  it('sayı menü/liste açıkken çekilir (poll yok)', () => {
    expect(src).toContain('usePresenceOnline(menuOpen || onlineOpen)');
  });
});
