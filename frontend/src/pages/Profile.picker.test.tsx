// @vitest-environment jsdom
// Profile.picker.test.tsx — v0.10.954 (tablo standardı T12 göçünün iki
// gerilemesi, çapraz incelemede yakalandı):
//
//   1. Boş sonuçta ÇİFT /profiles isteği. `recentProfiles` üç hâle geçince
//      efekt bağımlılığı `recentProfiles?.length` undefined → 0 değişip efekti
//      yeniden koşturuyordu; `(0) > 0` koruması tutmadığı için ikinci istek
//      gidiyordu. Koşul artık "yüklendi mi" (undefined).
//   2. Hata sonrası yeniden açışta tablo yeni istek uçuştayken BAYAT hata
//      satırını gösteriyordu. Açış düğmesi null → undefined sıfırlar; tablo
//      "okunuyor" der.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import type { ProfileDetail, ProfileRow } from '@/lib/types';

const m = vi.hoisted(() => ({
  profilesCalls: 0,
  // Sıradaki /profiles cevabı: dizi, 'fail' ya da elle çözülen söz.
  next: [] as Array<ProfileRow[] | 'fail' | Promise<ProfileRow[]>>,
}));
const calls = vi.hoisted(() => ({
  profile: async (id: string): Promise<ProfileDetail> => ({
    meta: {
      profileId: id, serviceName: 'svc-a', hostName: 'h1', profileType: 'cpu',
      startTime: 1, durationMs: 1000, sampleCount: 10,
    },
    flame: { name: 'root', value: 1 },
  }),
  profiles: async (): Promise<ProfileRow[]> => {
    m.profilesCalls++;
    const n = m.next.shift() ?? [];
    if (n === 'fail') throw new Error('boom');
    return n;
  },
}));
vi.mock('@/lib/api', async (importOriginal) => {
  const mod = await importOriginal<Record<string, unknown>>();
  return { ...mod, api: { ...(mod.api as Record<string, unknown>), ...calls } };
});
// Ağır görseller bu testin konusu değil.
vi.mock('@/components/Topbar', () => ({ Topbar: () => null }));
vi.mock('@/components/FlameGraph', () => ({ FlameGraph: () => null }));
vi.mock('@/components/FlameDiff', () => ({ FlameDiff: () => null }));
vi.mock('@/components/MethodHotspots', () => ({ MethodHotspots: () => null }));

import ProfilePage from './Profile';

const wait = () => act(async () => { await new Promise(r => setTimeout(r, 30)); });

let host: HTMLElement | null = null;
let root: Root | null = null;
async function mount(): Promise<HTMLElement> {
  host = document.createElement('div');
  document.body.appendChild(host);
  act(() => {
    root = createRoot(host!);
    root.render(
      <MemoryRouter initialEntries={['/profile?id=p-current']}>
        <ProfilePage />
      </MemoryRouter>,
    );
  });
  await wait();
  return host!;
}
const toggle = (el: HTMLElement) => {
  const b = Array.from(el.querySelectorAll('button'))
    .find(x => x.textContent === 'Compare with…' || x.textContent === 'Cancel');
  if (!b) throw new Error('seçici düğmesi yok');
  act(() => { b.click(); });
};
const stateOf = (el: HTMLElement) =>
  el.querySelector('tr[data-dt-state]')?.getAttribute('data-dt-state') ?? null;

beforeEach(() => {
  m.profilesCalls = 0;
  m.next = [];
  try { localStorage.clear(); } catch { /* jsdom */ }
});
afterEach(() => {
  if (root) act(() => root!.unmount());
  host?.remove(); host = null; root = null;
});

describe('Profile baseline seçici — tek istek, bayat hata yok (v0.10.954)', () => {
  it('boş sonuç: tek /profiles isteği, tablo "boş" der', async () => {
    m.next = [[]];
    const el = await mount();
    toggle(el);
    await wait();
    await wait();
    expect(m.profilesCalls).toBe(1);
    expect(stateOf(el)).toBe('empty');
  });

  it('boş sonuç önbellekte kalır: kapat-aç yeniden çekmez', async () => {
    m.next = [[]];
    const el = await mount();
    toggle(el);
    await wait();
    toggle(el);
    toggle(el);
    await wait();
    expect(m.profilesCalls).toBe(1);
    expect(stateOf(el)).toBe('empty');
  });

  it('hata sonrası yeniden açış: "okunuyor" gösterir ve yeniden çeker', async () => {
    m.next = ['fail'];
    const el = await mount();
    toggle(el);
    await wait();
    expect(stateOf(el)).toBe('error');
    expect(m.profilesCalls).toBe(1);

    toggle(el); // kapat
    let resolve: (rows: ProfileRow[]) => void = () => {};
    m.next = [new Promise<ProfileRow[]>(r => { resolve = r; })];
    toggle(el); // yeniden aç — istek uçuşta
    expect(m.profilesCalls).toBe(2);
    expect(stateOf(el)).toBe('loading');

    await act(async () => { resolve([]); await new Promise(r => setTimeout(r, 30)); });
    expect(stateOf(el)).toBe('empty');
    expect(m.profilesCalls).toBe(2);
  });
});
