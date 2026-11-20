// @vitest-environment jsdom
//
// SafeHref.test.tsx — v0.10.1315 (olay linki XSS). API/editör kaynaklı URL
// çizen tek kapının sözleşmesi: iç yol → SPA <Link>, http(s) → güvenli
// <a target=_blank>, geri kalan her şey (javascript:, //host, /\host, data:,
// boş) → çapa YOK. openSafeLink aynı kararı programatik açışta verir.

import { describe, it, expect, afterEach, vi } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import type { ReactNode } from 'react';
import { MemoryRouter } from 'react-router';
import { SafeHref } from './SafeHref';
import { openSafeLink } from '@/lib/safeAppPath';

let host: HTMLDivElement | null = null;
let root: Root | null = null;

function render(node: ReactNode, router = true): HTMLElement {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  act(() => { root!.render(router ? <MemoryRouter>{node}</MemoryRouter> : node); });
  return host;
}

afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  root = null; host = null;
  vi.restoreAllMocks();
});

describe('SafeHref (v0.10.1315)', () => {
  it('internal path → SPA Link (same tab)', () => {
    const a = render(<SafeHref href="/problems?problem=p1">x</SafeHref>).querySelector('a')!;
    expect(a.getAttribute('href')).toBe('/problems?problem=p1');
    expect(a.getAttribute('target')).toBeNull();
  });

  it('https URL → <a target=_blank rel="noopener noreferrer">', () => {
    const a = render(<SafeHref href="https://x" title="t" className="c">x</SafeHref>).querySelector('a')!;
    expect(a.getAttribute('href')).toBe('https://x');
    expect(a.getAttribute('target')).toBe('_blank');
    expect(a.getAttribute('rel')).toBe('noopener noreferrer');
    expect(a.getAttribute('title')).toBe('t');
    expect(a.className).toBe('c');
  });

  for (const bad of ['javascript:alert(1)', '//evil.com', '/\\evil.com', 'data:text/html,<script>alert(1)</script>', '']) {
    it(`${JSON.stringify(bad)} → no anchor, fallback drawn`, () => {
      const el = render(<SafeHref href={bad} fallback={<span data-testid="fb">—</span>}>x</SafeHref>);
      expect(el.querySelector('a')).toBeNull();
      expect(el.querySelector('[data-testid="fb"]')).not.toBeNull();
    });
  }

  it('null href → default fallback is nothing', () => {
    const el = render(<SafeHref href={null}>x</SafeHref>);
    expect(el.querySelector('a')).toBeNull();
    expect(el.textContent).toBe('');
  });

  it('outside a router, internal path is a plain same-tab anchor (no crash)', () => {
    const a = render(<SafeHref href="/runbook?id=abc">x</SafeHref>, false).querySelector('a')!;
    expect(a.getAttribute('href')).toBe('/runbook?id=abc');
    expect(a.getAttribute('target')).toBeNull();
  });

  it('onClick is forwarded', () => {
    const onClick = vi.fn();
    const a = render(<SafeHref href="https://x" onClick={e => { e.preventDefault(); onClick(); }}>x</SafeHref>).querySelector('a')!;
    act(() => { a.click(); });
    expect(onClick).toHaveBeenCalledTimes(1);
  });
});

describe('openSafeLink (v0.10.1315)', () => {
  it('external → window.open(_blank, noopener,noreferrer); navigate untouched', () => {
    const open = vi.spyOn(window, 'open').mockImplementation(() => null);
    const nav = vi.fn();
    expect(openSafeLink('https://ci.example/1', nav)).toBe(true);
    expect(open).toHaveBeenCalledWith('https://ci.example/1', '_blank', 'noopener,noreferrer');
    expect(nav).not.toHaveBeenCalled();
  });

  it('internal → navigate(href); window.open untouched', () => {
    const open = vi.spyOn(window, 'open').mockImplementation(() => null);
    const nav = vi.fn();
    expect(openSafeLink('/problems?problem=p1', nav)).toBe(true);
    expect(nav).toHaveBeenCalledWith('/problems?problem=p1');
    expect(open).not.toHaveBeenCalled();
  });

  for (const bad of ['javascript:alert(1)', '//evil.com', '/\\evil.com', 'data:text/html,x', '', null, undefined]) {
    it(`${JSON.stringify(bad)} → false, nothing opened`, () => {
      const open = vi.spyOn(window, 'open').mockImplementation(() => null);
      const nav = vi.fn();
      expect(openSafeLink(bad, nav)).toBe(false);
      expect(open).not.toHaveBeenCalled();
      expect(nav).not.toHaveBeenCalled();
    });
  }
});
