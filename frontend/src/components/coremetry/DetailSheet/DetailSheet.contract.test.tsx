// @vitest-environment jsdom
// coremetry/DetailSheet — v0.10.1669 (etkileşim S1, plan §2.2 parite testleri,
// ilk göçten ÖNCE yazıldı): Esc yalnız en üst katmanı kapatır (tek yığın),
// modal: etiketli diyalog + odak içeri + kapanışta açana döner; eşlik kipi: boş
// alana tık kapatır, KEEP_OPEN öğesine tık kapatmaz, odak tuzağı yok; tutamak
// sürüklenen genişliği anahtara yazar, ekrana kelepçeler, çift tık sıfırlar;
// pageHref "Sayfayı aç" linki; inline stil yok; useSheetParam açılış push
// (Geri kapatır), kapanış/özne değişimi replace, yabancı paramlar korunur.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { act, useState } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter, useLocation, useNavigate } from 'react-router-dom';
import { useEscLayer, __resetEscLayers } from '@/lib/escLayer';
import { useShortcuts } from '@/lib/keyboard';
import { readFileSync, readdirSync } from 'node:fs';
import { join } from 'node:path';

const mem = vi.hoisted(() => new Map<string, string>());
vi.mock('@/lib/storage', () => ({
  getRaw: (k: string) => mem.get(k) ?? null,
  setRaw: (k: string, v: string) => { if (v === '') mem.delete(k); else mem.set(k, v); },
}));

import { DetailSheet, SheetSection, useSheetParam, nextSheetParams } from './index';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let host: HTMLDivElement | null = null;
let root: Root | null = null;
async function mount(node: React.ReactNode) {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  await act(async () => { root!.render(<MemoryRouter initialEntries={['/hosts?x=1']}>{node}</MemoryRouter>); });
  await act(async () => { await new Promise(r => setTimeout(r, 10)); });
}
const tick = (ms = 10) => act(async () => { await new Promise(r => setTimeout(r, ms)); });
const panel = () => document.querySelector<HTMLElement>('[data-detail-sheet]');
const key = async (k: string) => {
  await act(async () => {
    (document.activeElement ?? document.body).dispatchEvent(new KeyboardEvent('keydown', { key: k, bubbles: true, cancelable: true }));
  });
};

function KeyboardHost() { useShortcuts([]); return null; }
function Underlay({ onEsc }: { onEsc(): void }) { useEscLayer(true, onEsc); return null; }

function Harness(p: { modal?: boolean; closeOnEmptyClick?: boolean; resizeKey?: string; pageHref?: string; underlay?: () => void; onClose?: () => void }) {
  const [open, setOpen] = useState(false);
  return (
    <>
      <KeyboardHost />
      {p.underlay && <Underlay onEsc={p.underlay} />}
      <button type="button" id="opener" onClick={() => setOpen(true)}>opener</button>
      <span id="empty">boş alan</span>
      <DetailSheet open={open} onOpenChange={o => { setOpen(o); if (!o) p.onClose?.(); }}
        title="host-1" badges={<span>ok</span>} pageHref={p.pageHref} modal={p.modal}
        closeOnEmptyClick={p.closeOnEmptyClick} resizeKey={p.resizeKey}>
        <SheetSection title="Özet"><button type="button" id="inner">iç</button></SheetSection>
      </DetailSheet>
    </>
  );
}
async function openVia() {
  const o = document.getElementById('opener') as HTMLButtonElement;
  o.focus();
  await act(async () => { o.click(); });
  await tick(20);
}

beforeEach(() => { mem.clear(); });
afterEach(async () => {
  await act(async () => { root?.unmount(); });
  host?.remove(); host = null; root = null;
  document.body.innerHTML = '';
  document.body.removeAttribute('style');
  __resetEscLayers();
});

describe('DetailSheet (modal, varsayılan)', () => {
  it('etiketli diyalog, başlık, odak içeri; Esc kapatır ve odak açana döner', async () => {
    const onClose = vi.fn();
    await mount(<Harness onClose={onClose} />);
    await openVia();
    const p = panel()!;
    expect(p.getAttribute('role')).toBe('dialog');
    expect(p.getAttribute('aria-label')).toBe('Detay çekmecesi');
    expect(p.getAttribute('data-modal')).toBe('true');
    expect(p.textContent).toContain('host-1');
    expect(p.contains(document.activeElement)).toBe(true);
    expect(document.querySelector('[data-slot="sheet-overlay"]')).not.toBeNull();
    await key('Escape');
    await tick(20);
    expect(onClose).toHaveBeenCalledTimes(1);
    expect(panel()).toBeNull();
    expect(document.activeElement?.id).toBe('opener');
  });

  it('Esc yalnız en üst katmanı kapatır (alttaki escLayer açık kalır)', async () => {
    const onClose = vi.fn();
    const underlay = vi.fn();
    await mount(<Harness onClose={onClose} underlay={underlay} />);
    await openVia();
    await key('Escape');
    expect(onClose).toHaveBeenCalledTimes(1);
    expect(underlay).not.toHaveBeenCalled();
  });

  it('pageHref → "Sayfayı aç ↗" linki; verilmezse yok', async () => {
    await mount(<Harness pageHref="/host?name=host-1" />);
    await openVia();
    const a = document.querySelector<HTMLAnchorElement>('[data-sheet-page-link]')!;
    expect(a.textContent).toBe('Sayfayı aç ↗');
    expect(a.getAttribute('href')).toBe('/host?name=host-1');
  });

  it('pageHref yok → link yok; tutamak yalnız resizeKey ile', async () => {
    await mount(<Harness />);
    await openVia();
    expect(document.querySelector('[data-sheet-page-link]')).toBeNull();
    expect(document.querySelector('[data-sheet-resize]')).toBeNull();
  });
});

describe('DetailSheet (eşlik kipi)', () => {
  it('boş alana tık kapatır; düğmeye tık kapatmaz; perde yok', async () => {
    const onClose = vi.fn();
    await mount(<Harness modal={false} closeOnEmptyClick onClose={onClose} />);
    await openVia();
    expect(panel()!.getAttribute('data-modal')).toBe('false');
    expect(document.querySelector('[data-slot="sheet-overlay"]')).toBeNull();
    await act(async () => { (document.getElementById('opener') as HTMLElement).click(); });
    expect(onClose).not.toHaveBeenCalled();
    await act(async () => { document.getElementById('empty')!.dispatchEvent(new MouseEvent('click', { bubbles: true, button: 0 })); });
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('closeOnEmptyClick olmadan boş alan tıkı kapatmaz', async () => {
    const onClose = vi.fn();
    await mount(<Harness modal={false} onClose={onClose} />);
    await openVia();
    await act(async () => { document.getElementById('empty')!.dispatchEvent(new MouseEvent('click', { bubbles: true, button: 0 })); });
    await act(async () => { document.getElementById('empty')!.dispatchEvent(new PointerEvent('pointerdown', { bubbles: true })); });
    expect(onClose).not.toHaveBeenCalled();
  });
});

describe('DetailSheet tutamak (Q9)', () => {
  it('sürükleme genişliği anahtara yazar + --sheet-w; çift tık sıfırlar', async () => {
    Object.defineProperty(window, 'innerWidth', { value: 1600, configurable: true });
    await mount(<Harness resizeKey="cm-test-w" />);
    await openVia();
    const h = document.querySelector<HTMLElement>('[data-sheet-resize]')!;
    expect(h.getAttribute('role')).toBe('separator');
    expect(panel()!.style.getPropertyValue('--sheet-w')).toBe('560px');
    await act(async () => {
      h.dispatchEvent(new PointerEvent('pointerdown', { bubbles: true, clientX: 1000 }));
      window.dispatchEvent(new PointerEvent('pointermove', { clientX: 900 }));
      window.dispatchEvent(new PointerEvent('pointerup', {}));
    });
    expect(mem.get('cm-test-w')).toBe('660');
    expect(panel()!.style.getPropertyValue('--sheet-w')).toBe('660px');
    // Ekran kelepçesi: 1600 - 160 tavan.
    await act(async () => {
      h.dispatchEvent(new PointerEvent('pointerdown', { bubbles: true, clientX: 1000 }));
      window.dispatchEvent(new PointerEvent('pointermove', { clientX: -2000 }));
      window.dispatchEvent(new PointerEvent('pointerup', {}));
    });
    expect(mem.get('cm-test-w')).toBe('1440');
    await act(async () => { h.dispatchEvent(new MouseEvent('dblclick', { bubbles: true })); });
    expect(mem.has('cm-test-w')).toBe(false);
    expect(panel()!.style.getPropertyValue('--sheet-w')).toBe('560px');
  });

  it('saklı genişlik açılışta okunur', async () => {
    mem.set('cm-test-w', '700');
    await mount(<Harness resizeKey="cm-test-w" />);
    await openVia();
    expect(panel()!.style.getPropertyValue('--sheet-w')).toBe('700px');
  });
});

describe('nextSheetParams (saf)', () => {
  it.each([
    ['x=1', 'h1', 'x=1&host=h1'],
    ['x=1&host=h1', 'h2', 'x=1&host=h2'],
    ['x=1&host=h1', null, 'x=1'],
    ['x=1&host=h1', '', 'x=1'],
    ['x=1&host=h1', 'h1', null],
    ['x=1', null, null],
  ])('%s + %j', (prev, v, want) => {
    const out = nextSheetParams(new URLSearchParams(prev), 'host', v);
    expect(out === null ? null : out.toString()).toBe(want);
  });
});

describe('useSheetParam', () => {
  let loc = '';
  let nav: ReturnType<typeof useNavigate> | null = null;
  function Probe() { const l = useLocation(); loc = l.pathname + l.search; nav = useNavigate(); return null; }
  let setter: ((v: string | null) => void) | null = null;
  let value: string | null = null;
  function Consumer() { const [v, s] = useSheetParam('host'); value = v; setter = s; return null; }

  it('açılış push (Geri kapatır); özne değişimi replace; yabancı param korunur', async () => {
    await mount(<><Probe /><Consumer /></>);
    expect(value).toBeNull();
    await act(async () => { setter!('h1'); });
    expect(loc).toBe('/hosts?x=1&host=h1');
    expect(value).toBe('h1');
    await act(async () => { setter!('h2'); });
    expect(loc).toBe('/hosts?x=1&host=h2');
    await act(async () => { nav!(-1); });
    expect(loc).toBe('/hosts?x=1');
    expect(value).toBeNull();
  });
});

describe('inline stil yok', () => {
  it('coremetry/DetailSheet/** kaynaklarında style={{ yok', () => {
    const dir = join(__dirname);
    const files = readdirSync(dir).filter(f => f.endsWith('.tsx') && !f.includes('.test.'));
    expect(files.length).toBeGreaterThan(0);
    for (const f of files) expect(readFileSync(join(dir, f), 'utf8')).not.toMatch(/style=\{\{/);
  });
});
