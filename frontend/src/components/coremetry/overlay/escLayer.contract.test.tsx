// @vitest-environment jsdom
//
// escLayer.contract — v0.10.1615 (shadcn Faz 1 R7, plan §7.2). Radix
// overlay sarmalayıcıları + lib/escLayer yığını + keyboard.ts'in tek
// document dinleyicisi BİRLİKTE (jsdom, gerçek Radix 1.8.1). escLayer.ts'in
// iki hatası (:4-10) burada test olarak duruyor:
//   #1 iki dinleyici aynı Esc'te iki katmanı kapatıyordu;
//   #2 katman içindeki input'ta Esc alanı değil katmanı kapatıyordu.
import { describe, it, expect, afterEach, beforeEach, vi } from 'vitest';
import { act, useState, type ReactNode } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { useEscLayer, escLayerDepth, __resetEscLayers } from '@/lib/escLayer';
import { useShortcuts } from '@/lib/keyboard';
import * as RawDialog from '@/components/shadcn/dialog';
import { Dialog, DialogContent, DialogTitle, DialogDescription } from './Dialog';
import { Popover, PopoverContent, PopoverAnchor } from './Popover';
import { DropdownMenu, DropdownMenuContent, DropdownMenuTrigger, DropdownMenuItem } from './DropdownMenu';
import { Sheet, SheetContent, SheetTitle, SheetDescription } from './Sheet';

let host: HTMLDivElement | null = null;
let root: Root | null = null;

function KeyboardHost() { useShortcuts([]); return null; } // keyboard.ts dinleyicisini kurar
function HandDrawer({ onClose }: { onClose(): void }) {
  useEscLayer(true, onClose);
  return <div data-testid="drawer">drawer</div>;
}

async function render(node: ReactNode) {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  await act(async () => { root!.render(<><KeyboardHost />{node}</>); });
  await act(async () => { await new Promise(r => setTimeout(r, 0)); });
}
async function esc(target: Element = document.activeElement ?? document.body) {
  const ev = new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true });
  await act(async () => { target.dispatchEvent(ev); });
  await act(async () => { await Promise.resolve(); });
  return ev;
}

beforeEach(() => { __resetEscLayers(); });
afterEach(async () => {
  await act(async () => { root?.unmount(); });
  host?.remove();
  root = null; host = null;
  document.body.innerHTML = '';
});

function ControlledDialog({ spy, children }: { spy: (o: boolean) => void; children?: ReactNode }) {
  const [open, setOpen] = useState(true);
  return (
    <Dialog open={open} onOpenChange={(o) => { spy(o); setOpen(o); }}>
      <DialogContent>
        <DialogTitle>Başlık</DialogTitle>
        <DialogDescription>Açıklama</DialogDescription>
        {children}
      </DialogContent>
    </Dialog>
  );
}

describe('Radix varsayımı (sarmalayıcının neden yutma yaptığı)', () => {
  it('çıplak shadcn Dialog Esc\'te olayı KENDİSİ preventDefault ediyor (keyboard.ts bayrağı zehirlenir)', async () => {
    const spy = vi.fn();
    await render(
      <RawDialog.Dialog open onOpenChange={spy}>
        <RawDialog.DialogContent><RawDialog.DialogTitle>t</RawDialog.DialogTitle><RawDialog.DialogDescription>d</RawDialog.DialogDescription></RawDialog.DialogContent>
      </RawDialog.Dialog>,
    );
    const ev = await esc();
    expect(ev.defaultPrevented).toBe(true);
    expect(spy).toHaveBeenCalledWith(false);
  });
});

describe('coremetry/overlay Esc sözleşmesi', () => {
  it('(3) Dialog yalnız: Esc onu TAM BİR kez kapatır; (4) yığın derinliği 0\'a döner', async () => {
    const spy = vi.fn();
    await render(<ControlledDialog spy={spy} />);
    expect(escLayerDepth()).toBe(1);
    await esc();
    expect(spy.mock.calls).toEqual([[false]]);
    expect(document.querySelector('[data-slot="dialog-content"]')).toBeNull();
    expect(escLayerDepth()).toBe(0);
  });

  it('(1) Dialog + SONRA açılan elle yazılmış Drawer: Esc yalnız Drawer\'ı kapatır, ikinci Esc Dialog\'u', async () => {
    const spy = vi.fn();
    function Both() {
      const [drawer, setDrawer] = useState(false);
      return (
        <>
          <ControlledDialog spy={spy}>
            <button type="button" onClick={() => setDrawer(true)}>aç</button>
          </ControlledDialog>
          {drawer && <HandDrawer onClose={() => setDrawer(false)} />}
        </>
      );
    }
    await render(<Both />);
    await act(async () => { (document.querySelector('[data-slot="dialog-content"] button:not([data-slot])') as HTMLButtonElement).click(); });
    expect(document.querySelector('[data-testid="drawer"]')).not.toBeNull();
    expect(escLayerDepth()).toBe(2);
    await esc();
    expect(document.querySelector('[data-testid="drawer"]')).toBeNull();
    expect(spy).not.toHaveBeenCalled();
    expect(document.querySelector('[data-slot="dialog-content"]')).not.toBeNull();
    await esc();
    expect(spy.mock.calls).toEqual([[false]]);
    expect(escLayerDepth()).toBe(0);
  });

  it('(2) içteki input Esc\'i harcarsa (preventDefault) Dialog AÇIK kalır; sonraki Esc kapatır', async () => {
    const spy = vi.fn();
    let claim = true;
    await render(
      <ControlledDialog spy={spy}>
        <input aria-label="ara" onKeyDown={(e) => { if (e.key === 'Escape' && claim) e.preventDefault(); }} />
      </ControlledDialog>,
    );
    const input = document.querySelector('input[aria-label="ara"]') as HTMLInputElement;
    input.focus();
    const ev = await esc(input);
    expect(ev.defaultPrevented).toBe(true); // GERÇEK iddia — Radix'inki yutuldu
    expect(spy).not.toHaveBeenCalled();
    expect(document.querySelector('[data-slot="dialog-content"]')).not.toBeNull();
    claim = false;
    await esc(input);
    expect(spy.mock.calls).toEqual([[false]]);
  });

  it('yutma tek seferlik: Radix\'in preventDefault\'undan sonra olayın preventDefault\'u yine gerçek', async () => {
    const { swallowNextPreventDefault } = await import('./escBridge');
    const ev = new KeyboardEvent('keydown', { key: 'Escape', cancelable: true });
    swallowNextPreventDefault(ev);
    ev.preventDefault();
    expect(ev.defaultPrevented).toBe(false);
    ev.preventDefault();
    expect(ev.defaultPrevented).toBe(true);
  });

  it('Popover, DropdownMenu, Sheet: Esc yığından bir kez kapatır', async () => {
    const spies = { pop: vi.fn(), dd: vi.fn(), sh: vi.fn() };
    function One({ which }: { which: keyof typeof spies }) {
      const [open, setOpen] = useState(true);
      const on = (o: boolean) => { spies[which](o); setOpen(o); };
      if (which === 'pop') return <Popover open={open} onOpenChange={on}><PopoverAnchor /><PopoverContent>p</PopoverContent></Popover>;
      if (which === 'dd') return <DropdownMenu open={open} onOpenChange={on}><DropdownMenuTrigger>m</DropdownMenuTrigger><DropdownMenuContent><DropdownMenuItem>x</DropdownMenuItem></DropdownMenuContent></DropdownMenu>;
      return <Sheet open={open} onOpenChange={on}><SheetContent><SheetTitle>s</SheetTitle><SheetDescription>d</SheetDescription></SheetContent></Sheet>;
    }
    for (const w of ['pop', 'dd', 'sh'] as const) {
      __resetEscLayers();
      await render(<One which={w} />);
      expect(escLayerDepth(), w).toBe(1);
      await esc();
      expect(spies[w].mock.calls, w).toEqual([[false]]);
      expect(escLayerDepth(), w).toBe(0);
      await act(async () => { root?.unmount(); });
      host?.remove();
      document.body.innerHTML = '';
    }
  });
});
