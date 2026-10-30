// @vitest-environment jsdom
//
// shadcn/button sözleşmesi — v0.10.1613 (shadcn Faz 1 R5, plan §5.4).
// button.tsx'teki İZİNLİ düzenlemelerin (`// coremetry:` işaretleri) kanıtı:
// ui/Button.contract.test.tsx'in vakalarının portu. tsc negatif testi:
// ikon boyutu erişilebilir ad İSTER (ui/IconButton sözleşmesi).
import { describe, it, expect, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import type { ReactNode } from 'react';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { Button, buttonVariants } from './button';

let host: HTMLDivElement | null = null;
let root: Root | null = null;
function render(node: ReactNode): HTMLElement {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  act(() => { root!.render(node); });
  return host;
}
afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  root = null; host = null;
});
const btn = (el: HTMLElement) => el.querySelector('button')!;

describe('shadcn Button — Coremetry düzenlemeleri', () => {
  it('varsayılan type="button" (formda sessiz submit yok); açık submit korunur', () => {
    expect(btn(render(<Button>Go</Button>)).type).toBe('button');
    act(() => { root?.unmount(); }); host?.remove();
    expect(btn(render(<form><Button type="submit">Save</Button></form>)).type).toBe('submit');
  });

  it('asChild: type eklenmez (bağlantıya type="button" yazılmaz)', () => {
    const a = render(<Button asChild><a href="https://example.com/x">Link</a></Button>).querySelector('a')!;
    expect(a.hasAttribute('type')).toBe(false);
    expect(a.getAttribute('data-slot')).toBe('button');
  });

  it('loading: aria-busy + disabled, etiket DOM\'da (opacity-0, erişilebilir ad düşmez), spinner aria-hidden', () => {
    const b = btn(render(<Button loading>Kaydet</Button>));
    expect(b.getAttribute('aria-busy')).toBe('true');
    expect(b.disabled).toBe(true);
    expect(b.textContent).toBe('Kaydet');
    const label = b.querySelector('span.opacity-0')!;
    expect(label.textContent).toBe('Kaydet');
    expect(b.querySelector('span[aria-hidden="true"].animate-spin')).not.toBeNull();
  });

  it('loading değilken çocuklar sarılmaz (has-[>svg] dolgu kuralı bozulmasın)', () => {
    const b = btn(render(<Button><svg /> Kaydet</Button>));
    expect(b.firstElementChild?.tagName.toLowerCase()).toBe('svg');
    expect(b.hasAttribute('aria-busy')).toBe(false);
    expect(b.disabled).toBe(false);
  });

  it('className cn ile BİRLEŞİR: çakışan yardımcı çağıranınki, gerisi atomunki', () => {
    const b = btn(render(<Button className="px-9 extra">Go</Button>));
    expect(b.className).toContain('px-9');
    expect(b.className).not.toMatch(/(^|\s)px-4(\s|$)/);
    expect(b.className).toContain('extra');
    expect(b.className).toContain('inline-flex');
  });

  it('cva: accent + ghost-danger varyantları, xs boyutu (Q9: dense → xs) var', () => {
    expect(buttonVariants({ variant: 'accent' })).toContain('bg-primary');
    expect(buttonVariants({ variant: 'ghost-danger' })).toContain('text-status-critical');
    expect(buttonVariants({ size: 'xs' })).toContain('h-6');
  });

  it('ikon boyutu aria-label olmadan TİP hatası (tsc negatif testi)', () => {
    // @ts-expect-error — size="icon" erişilebilir ad ister
    const bad = <Button size="icon"><svg /></Button>;
    const ok = <Button size="icon-sm" aria-label="Kapat"><svg /></Button>;
    expect(bad && ok).toBeTruthy();
    expect(btn(render(ok)).getAttribute('aria-label')).toBe('Kapat');
  });

  it('düzenlemeler // coremetry: … // /coremetry işaretleri arasında (shadcn diff okunur kalsın)', () => {
    const src = readFileSync(resolve(__dirname, 'button.tsx'), 'utf8');
    const open = src.match(/\/\/ coremetry:/g)?.length ?? 0;
    const close = src.match(/\/\/? ?\/coremetry|\/\* \/coremetry \*\//g)?.length ?? 0;
    expect(open).toBeGreaterThan(0);
    expect(close).toBe(open);
  });
});
