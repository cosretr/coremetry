// @vitest-environment jsdom
// ThemeToggle — v0.10.1849 (themes-proposal §6.4, operatör onayı 2026-11-06):
// üst şerit düğmesi döngü yerine tema listesini açar; gruplar (Açık / Koyu)
// ve sıra kayıttan; seçim data-theme + data-scheme + kalıcı tercih yazar.
import { describe, it, expect, afterEach, vi } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { ThemeToggle } from './ThemeToggle';
import { THEME_GROUPS, THEME_LABEL, SCHEME_LABEL } from '@/lib/theme';
import { themeEntry } from '@/lib/themeRegistry';

const mem = vi.hoisted(() => new Map<string, string>());
vi.mock('@/lib/storage', () => ({
  getRaw: (k: string) => mem.get(k) ?? null,
  setRaw: (k: string, v: string) => { mem.set(k, v); },
}));

let host: HTMLDivElement | null = null;
let root: Root | null = null;
const flush = async () => { for (let i = 0; i < 5; i++) await act(async () => { await new Promise(r => setTimeout(r, 0)); }); };
/** Tembel menü yığını (dinamik import) yüklenene dek bekler. */
async function waitFor<T>(get: () => T | null, ms = 3000): Promise<T | null> {
  const end = Date.now() + ms;
  for (;;) {
    const v = get();
    if (v || Date.now() > end) return v;
    await act(async () => { await new Promise(r => setTimeout(r, 20)); });
  }
}

afterEach(async () => {
  await act(async () => { root?.unmount(); });
  host?.remove();
  root = null; host = null;
  document.body.innerHTML = '';
});

describe('ThemeToggle → tema listesi (v0.10.1849)', () => {
  it('tık listeyi açar: Açık/Koyu grupları kayıt sırasıyla; seçim temayı uygular', async () => {
    document.documentElement.setAttribute('data-theme', 'redhat');
    host = document.createElement('div');
    document.body.appendChild(host);
    root = createRoot(host);
    await act(async () => { root!.render(<ThemeToggle />); });
    const btn = host.querySelector<HTMLButtonElement>('button.theme-toggle')!;
    expect(btn.getAttribute('aria-haspopup')).toBe('menu');
    expect(btn.getAttribute('aria-expanded')).toBe('false');

    await act(async () => { btn.click(); });
    expect(btn.getAttribute('aria-expanded')).toBe('true');
    const menu = (await waitFor(() => document.querySelector('[role="menu"]')))!;
    expect(menu).not.toBeNull();
    const labels = [...menu.querySelectorAll('[data-slot="dropdown-menu-label"]')].map(e => e.textContent);
    expect(labels).toEqual(THEME_GROUPS.map(g => SCHEME_LABEL[g.scheme]));
    const items = [...menu.querySelectorAll('[role="menuitemradio"]')];
    expect(items.map(e => e.textContent)).toEqual(THEME_GROUPS.flatMap(g => g.themes.map(t => THEME_LABEL[t])));
    expect(items.find(e => e.getAttribute('aria-checked') === 'true')?.textContent).toBe(THEME_LABEL.redhat);

    const slate = items.find(e => e.textContent === THEME_LABEL.slate) as HTMLElement;
    await act(async () => { slate.click(); });
    await flush();
    expect(document.documentElement.getAttribute('data-theme')).toBe('slate');
    expect(document.documentElement.getAttribute('data-scheme')).toBe(themeEntry('slate').scheme);
    expect(mem.get('coremetry-theme')).toBe('slate');
    expect(document.querySelector('[role="menu"]')).toBeNull();
  });

  it('döngü kalktı: kaynak cycleTheme / THEME_NEXT çağırmaz, menü tembel yüklenir', () => {
    const src = readFileSync(resolve(__dirname, 'ThemeToggle.tsx'), 'utf8');
    expect(src).not.toMatch(/cycleTheme|THEME_NEXT/);
    expect(src).toContain("lazy(() => import('@/components/coremetry/ThemeMenu'))");
    expect(src).not.toMatch(/from '@\/components\/coremetry\/(overlay|ThemeMenu)'/);
  });
});
