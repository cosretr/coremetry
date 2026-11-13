// @vitest-environment jsdom
// MoreFilters — v0.10.2124 (Problems S4): kapalı gelir; etkin süzgeç varsa
// açık gelir ve sayıyı söyler; sonradan etkinleşirse yeniden açılır.
import { describe, it, expect, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MoreFilters } from './MoreFilters';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let host: HTMLDivElement | null = null;
let root: Root | null = null;
afterEach(() => { act(() => { root?.unmount(); }); host?.remove(); host = null; root = null; });

function render(n: number) {
  if (!host) {
    host = document.createElement('div');
    document.body.appendChild(host);
    root = createRoot(host);
  }
  act(() => { root!.render(<MoreFilters activeCount={n}><span data-testid="body">gövde</span></MoreFilters>); });
  return host;
}
const trig = (el: HTMLElement) => el.querySelector('[aria-expanded]') as HTMLElement;

describe('MoreFilters', () => {
  it('etkin süzgeç yokken kapalı, gövde mount edilmez', () => {
    const el = render(0);
    expect(trig(el).getAttribute('aria-expanded')).toBe('false');
    expect(trig(el).textContent).toBe('Diğer süzgeçler');
    expect(el.querySelector('[data-testid="body"]')).toBeNull();
  });
  it('etkin süzgeç varken açık ve sayılı', () => {
    const el = render(2);
    expect(trig(el).getAttribute('aria-expanded')).toBe('true');
    expect(trig(el).textContent).toContain('2 etkin');
    expect(el.querySelector('[data-testid="body"]')).not.toBeNull();
  });
  it('sonradan etkinleşince yeniden açılır; kullanıcı kapatabilir', () => {
    const el = render(0);
    render(1);
    expect(trig(el).getAttribute('aria-expanded')).toBe('true');
    act(() => { trig(el).click(); });
    expect(trig(el).getAttribute('aria-expanded')).toBe('false');
  });
});
