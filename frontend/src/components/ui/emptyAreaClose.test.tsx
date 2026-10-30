// @vitest-environment jsdom
//
// v0.10.1296 — operatör 2026-10-17: "CoSRE açıldığında illa çarpıya değil,
// ekranda solda boş bir yere basınca da kapansın". Perdesiz çekmece yalnız
// BOŞ alana tıklamada kapanır; etkileşimli öğeler sohbeti açık bırakır.
import { describe, it, expect, vi, afterEach } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import { Drawer } from './Drawer';

let root: Root | null = null;
function render(ui: React.ReactElement) {
  const host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  act(() => { root!.render(ui); });
}
function cleanup() { if (root) act(() => { root!.unmount(); }); root = null; }
import { isEmptyAreaClick } from '@/lib/emptyAreaClose';

afterEach(() => { cleanup(); document.body.innerHTML = ''; });

function dom(html: string) {
  const host = document.createElement('div');
  host.innerHTML = html;
  document.body.appendChild(host);
  return host;
}

describe('isEmptyAreaClick', () => {
  it('panel dışındaki boş div → kapat', () => {
    const h = dom('<div id="panel"><p id="in">x</p></div><main><div id="empty"><span id="txt">metin</span></div></main>');
    const panel = h.querySelector('#panel');
    expect(isEmptyAreaClick(h.querySelector('#empty'), panel)).toBe(true);
    expect(isEmptyAreaClick(h.querySelector('#txt'), panel)).toBe(true);
  });
  it('panel içi → kapatma', () => {
    const h = dom('<div id="panel"><p id="in">x</p></div>');
    expect(isEmptyAreaClick(h.querySelector('#in'), h.querySelector('#panel'))).toBe(false);
  });
  it.each([
    // href="#x": spaLinks ratchet'i SPA rotasına giden çıplak <a href="/…"> arar; bu yalnız DOM fikstürü.
    ['link', '<a href="#x"><span id="t">a</span></a>'],
    ['düğme', '<button><span id="t">b</span></button>'],
    ['girdi', '<input id="t" />'],
    ['tablo satırı', '<table><tbody><tr><td id="t">c</td></tr></tbody></table>'],
    ['grafik', '<svg><g><rect id="t"></rect></g></svg>'],
    ['menü öğesi', '<div role="menuitem"><span id="t">m</span></div>'],
    ['başka katman', '<div role="dialog"><div id="t">d</div></div>'],
    ['odaklanabilir', '<div tabindex="0"><span id="t">f</span></div>'],
    ['işaretli', '<div data-keep-drawer><span id="t">k</span></div>'],
  ])('%s → açık kalır', (_n, html) => {
    const h = dom(`<div id="panel"></div>${html}`);
    expect(isEmptyAreaClick(h.querySelector('#t'), h.querySelector('#panel'))).toBe(false);
  });
  it('DOM\'dan kalkmış hedef → kapatma (menü tıklamada kapanmış olabilir)', () => {
    const el = document.createElement('div');
    expect(isEmptyAreaClick(el, null)).toBe(false);
  });
});

describe('Drawer closeOnEmptyClick', () => {
  it('perdesiz + closeOnEmptyClick: boş alana tık kapatır, düğme kapatmaz', async () => {
    vi.useFakeTimers();
    const onClose = vi.fn();
    const page = dom('<div id="empty">boş</div><button id="btn">iş</button>');
    render(<Drawer onClose={onClose} backdrop={false} closeOnEmptyClick header="h">gövde</Drawer>);
    await act(async () => { vi.runAllTimers(); });
    page.querySelector<HTMLButtonElement>('#btn')!.click();
    expect(onClose).not.toHaveBeenCalled();
    page.querySelector<HTMLDivElement>('#empty')!.click();
    expect(onClose).toHaveBeenCalledTimes(1);
    vi.useRealTimers();
  });
  it('bayrak yoksa perdesiz çekmece dış tıkla kapanmaz (eski davranış)', async () => {
    vi.useFakeTimers();
    const onClose = vi.fn();
    const page = dom('<div id="empty">boş</div>');
    render(<Drawer onClose={onClose} backdrop={false} header="h">gövde</Drawer>);
    await act(async () => { vi.runAllTimers(); });
    page.querySelector<HTMLDivElement>('#empty')!.click();
    expect(onClose).not.toHaveBeenCalled();
    vi.useRealTimers();
  });
  it('panel içine tık kapatmaz', async () => {
    vi.useFakeTimers();
    const onClose = vi.fn();
    render(<Drawer onClose={onClose} backdrop={false} closeOnEmptyClick header="h"><p data-testid="in">gövde</p></Drawer>);
    await act(async () => { vi.runAllTimers(); });
    document.querySelector<HTMLElement>('[data-testid="in"]')!.click();
    expect(onClose).not.toHaveBeenCalled();
    vi.useRealTimers();
  });
});
