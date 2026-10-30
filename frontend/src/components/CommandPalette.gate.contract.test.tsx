// @vitest-environment jsdom
//
// CommandPalette kapısı — v0.10.1653 (etkileşim dilimi P1, plan §1.3/§4 P1).
// Sözleşme: gövde chunk'ı yüklenmeden basılan ⌘K + yazılan "che" kaybolmaz —
// gövde açık mount olur ve sorgusu "che" olur; tampon tuşları alttaki sayfaya
// sızmaz. Esc tampondayken tek Esc yığınından (useEscLayer) yalnız açmayı
// iptal eder — alttaki katman açık kalır.
import { describe, it, expect, vi, afterEach } from 'vitest';
import { act, useEffect, useState } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { useEscLayer, __resetEscLayers } from '@/lib/escLayer';

const chunk = vi.hoisted(() => {
  let release!: () => void;
  const ready = new Promise<void>(r => { release = r; });
  return { ready, release: () => release() };
});

vi.mock('@/components/coremetry/CommandPalette/CommandPaletteBody', async () => {
  await chunk.ready;
  type P = { open: boolean; onOpenChange(o: boolean): void; openSeq: number; takeBufferedQuery(): string };
  function CommandPaletteBody({ open, openSeq, takeBufferedQuery }: P) {
    const [q, setQ] = useState<string | null>(null);
    useEffect(() => {
      if (open && q === null) setQ(takeBufferedQuery());
    }, [open, q, takeBufferedQuery]);
    return <div data-testid="body">{open ? 'open' : 'closed'}|{q ?? ''}|{openSeq}</div>;
  }
  return { CommandPaletteBody };
});

import { CommandPalette, bufferKey, openCommandPalette } from './CommandPalette';

let host: HTMLDivElement | null = null;
let root: Root | null = null;
function mount(node: React.ReactNode) {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  act(() => { root!.render(node); });
}
afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  host = null; root = null;
  __resetEscLayers();
});
(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const key = (k: string, init: KeyboardEventInit = {}) =>
  document.body.dispatchEvent(new KeyboardEvent('keydown', { key: k, bubbles: true, cancelable: true, ...init }));
const body = () => document.querySelector('[data-testid="body"]')?.textContent ?? null;

function Underlay({ onEsc }: { onEsc(): void }) {
  useEscLayer(true, onEsc);
  return null;
}

describe('CommandPalette kapısı', () => {
  it('⌘K + "che" chunk gelmeden → gövde "che" ile açılır, tuşlar sayfaya sızmaz; Esc yalnız açmayı iptal eder', async () => {
    const pageKeys = vi.fn();
    document.addEventListener('keydown', pageKeys);
    const underlayEsc = vi.fn();
    mount(<><Underlay onEsc={underlayEsc} /><CommandPalette /></>);
    expect(body()).toBeNull();

    // Önce iptal: ⌘K + Esc → palet kapanır, alttaki katman kapanmaz.
    act(() => { key('k', { ctrlKey: true }); });
    act(() => { key('z'); key('Escape'); });
    expect(underlayEsc).not.toHaveBeenCalled();

    // Sonra yeniden aç + yaz; chunk bu arada gelmiyor.
    act(() => { key('k', { ctrlKey: true }); });
    act(() => { key('c'); key('h'); key('x'); key('Backspace'); key('e'); });
    const leaked = pageKeys.mock.calls.map(([e]) => (e as KeyboardEvent).key).filter(k => k !== 'k' && k !== 'Escape');
    expect(leaked).toEqual([]);

    await act(async () => { chunk.release(); await chunk.ready; await new Promise(r => setTimeout(r, 0)); });
    expect(body()).toBe('open|che|2');
    document.removeEventListener('keydown', pageKeys);
  });

  it('görünür kapı (openCommandPalette) aynı açma yolunu kullanır', async () => {
    mount(<CommandPalette />);
    await act(async () => { openCommandPalette(); await new Promise(r => setTimeout(r, 0)); });
    expect(body()).toMatch(/^open\|/);
  });

  it('bufferKey: karakter / Backspace / Esc / değiştiricili tuş', () => {
    const rows: [string, KeyboardEventInit, string, string | null][] = [
      ['ab', { key: 'c' }, 'karakter eklenir', 'abc'],
      ['ab', { key: 'Backspace' }, 'Backspace siler', 'a'],
      ['ab', { key: 'Escape' }, 'Esc yığına bırakılır', null],
      ['ab', { key: 'v', metaKey: true }, '⌘V dokunulmaz', null],
      ['ab', { key: 'ArrowDown' }, 'gezinme tuşu dokunulmaz', null],
    ];
    for (const [buf, e, name, want] of rows) {
      const ev = { key: '', ctrlKey: false, metaKey: false, altKey: false, ...e } as KeyboardEvent;
      expect(bufferKey(buf, ev), name).toBe(want);
    }
  });

  it('kapı tembel: gövdeyi yalnız dinamik import() ile çeker, ui/ ve <input> yok', () => {
    const src = readFileSync(resolve(__dirname, 'CommandPalette.tsx'), 'utf8').replace(/\/\/.*$/gm, '');
    expect(src).toMatch(/import\('@\/components\/coremetry\/CommandPalette\/CommandPaletteBody'\)/);
    expect(src).not.toMatch(/^import[^;]*CommandPaletteBody/m);
    expect(src).not.toMatch(/@\/components\/ui/);
    expect(src).not.toMatch(/<input/);
  });
});
