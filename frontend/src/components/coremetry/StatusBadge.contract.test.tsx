// @vitest-environment jsdom
//
// StatusBadge sözleşmesi — v0.10.1616 (shadcn Faz 1 R8, plan §6).
import { describe, it, expect, afterEach, vi } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { StatusBadge } from './StatusBadge';
import { effectiveSize, statusBadgeVariants } from './statusBadgeVariants';
import { STATUSES, statusToken, fromLegacy } from '@/lib/statusTone';

vi.mock('@/lib/branding', () => ({ useBranding: () => ({ language: 'en' }) }));

let host: HTMLDivElement | null = null;
let root: Root | null = null;
afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  root = null; host = null;
  document.documentElement.removeAttribute('data-density');
});
function render(node: React.ReactNode): HTMLElement {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  act(() => { root!.render(node); });
  return host.querySelector('[data-slot="badge"]')!;
}

describe('StatusBadge', () => {
  it('her durum yalnız kendi --status-* Tailwind sınıflarını taşır; shadcn varsayılan rengi düşer', () => {
    for (const s of STATUSES) {
      const el = render(<StatusBadge status={s} />);
      expect(el.dataset.status).toBe(s);
      expect(el.className).toContain(`bg-status-${s}-bg`);
      expect(el.className).toContain(`text-status-${s}`);
      expect(el.className).not.toMatch(/(^|\s)(bg-primary|text-foreground|border-border|rounded-full|font-medium|text-xs)(\s|$)/);
      act(() => { root?.unmount(); }); host?.remove();
    }
  });

  it('etiket: verilmezse status.<s> çevirisi, verilirse çocuk', () => {
    expect(render(<StatusBadge status="critical" />).textContent).toBe('Critical');
    act(() => { root?.unmount(); }); host?.remove();
    expect(render(<StatusBadge status="ok">3 healthy</StatusBadge>).textContent).toBe('3 healthy');
  });

  it('yoğunluk: compact → sm (9px), dense → xs (8px); size="sm" en fazla sm', () => {
    document.documentElement.setAttribute('data-density', 'dense');
    expect(render(<StatusBadge status="warn" />).className).toContain('text-[8px]');
    expect(effectiveSize('md', 'md')).toBe('md');
    expect(effectiveSize('md', 'sm')).toBe('sm');
    expect(effectiveSize('sm', 'md')).toBe('sm');
    expect(effectiveSize('xs', 'sm')).toBe('xs');
    expect(statusBadgeVariants({ size: 'md' })).toContain('text-[11px]');
    expect(statusBadgeVariants({ size: 'sm' })).toContain('text-[9px]');
  });

  it('ölçü bugünkü .badge ile aynı: globals.css 11px / 2px 8px, compact 9px / 1px 5px, dense 8px / 1px 4px', () => {
    const g = readFileSync(resolve(__dirname, '../../styles/globals.css'), 'utf8');
    expect(g).toMatch(/\.badge \{[^}]*padding: 2px 8px;[^}]*font-size: 11px; font-weight: 700;/);
    expect(g).toContain('[data-density="compact"] .badge { font-size: 9px; padding: 1px 5px; }');
    expect(g).toContain('[data-density="dense"] .badge { font-size: 8px; padding: 1px 4px; }');
  });

  it('dot: aria-hidden nokta, metin rengi (currentColor)', () => {
    const el = render(<StatusBadge status="ok" dot />);
    const dot = el.querySelector('span[aria-hidden="true"]')!;
    expect(dot.className).toContain('bg-current');
  });
});

describe('statusTone', () => {
  it('token adları tokens.css\'te bildirili', () => {
    const tokens = readFileSync(resolve(__dirname, '../../styles/tokens.css'), 'utf8');
    for (const s of STATUSES) for (const v of Object.values(statusToken(s))) {
      const name = /^var\((--[\w-]+)\)$/.exec(v)![1];
      expect(tokens, `${s}: ${name}`).toContain(`${name}:`);
    }
  });
  it('fromLegacy: error→critical, no-data→unknown, warn/ok aynen', () => {
    expect(fromLegacy('error')).toBe('critical');
    expect(fromLegacy('no-data')).toBe('unknown');
    expect(fromLegacy('warn')).toBe('warn');
    expect(fromLegacy('ok')).toBe('ok');
  });
});
