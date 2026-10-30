// @vitest-environment jsdom
//
// CommandPaletteBody — v0.10.1658 (etkileşim dilimi P2, plan §1.1/§1.4).
// cmdk gövdesinin davranış paritesi (gerçek cmdk 1.1.1 + Radix Dialog, jsdom):
//   • diyalog etiketli, combobox girdisi açılışta odakta, gruplu sonuçlar;
//   • servis araması SUNUCUDA (debounce sonrası api.serviceNames(q, 20)),
//     ↓/↵ seçim navHref üstünden gezinir ve paleti kapatır;
//   • eylem kipi: ↵ eyleme girer, Esc LİSTEYE döner (palet açık), ikinci Esc
//     kapatır (Q3);
//   • Esc sırası: palet açık bir alt katmanın (çekmece) üstünde — Esc yalnız
//     paleti kapatır (escLayer olayı #1, cmdk vakası);
import '@/test/radixJsdom';
import { describe, it, expect, vi, afterEach, beforeEach } from 'vitest';
import { act, useState } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter, useLocation } from 'react-router-dom';
import { useEscLayer, __resetEscLayers } from '@/lib/escLayer';
import { useShortcuts } from '@/lib/keyboard';

const apiMock = vi.hoisted(() => ({
  serviceNames: vi.fn(async (q: string) => ({ names: q.startsWith('check') ? ['checkout', 'checkout-worker'] : [] })),
  operationNames: vi.fn(async () => ({ names: [] as string[] })),
  oracleOperations: vi.fn(async () => ({ hits: [], attrKey: '' })),
  copilotConfig: vi.fn(async () => ({ enabled: false })),
}));
vi.mock('@/lib/api', () => ({ api: apiMock }));
vi.mock('@/components/AuthProvider', () => ({ useAuth: () => ({ user: { id: 'u1', role: 'editor' } }) }));

import { CommandPaletteBody } from './CommandPaletteBody';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let host: HTMLDivElement | null = null;
let root: Root | null = null;
let loc = '';
function Probe() { const l = useLocation(); loc = l.pathname + l.search; return null; }
function KeyboardHost() { useShortcuts([]); return null; } // keyboard.ts dinleyicisini kurar
function Underlay({ onEsc }: { onEsc(): void }) { useEscLayer(true, onEsc); return null; }

function Harness({ onClose, underlay }: { onClose(): void; underlay?: () => void }) {
  const [open, setOpen] = useState(true);
  return (
    <MemoryRouter initialEntries={['/services']}>
      <Probe />
      <KeyboardHost />
      {underlay && <Underlay onEsc={underlay} />}
      <button type="button" id="opener">opener</button>
      <CommandPaletteBody open={open} openSeq={1} takeBufferedQuery={() => ''}
        onOpenChange={o => { setOpen(o); if (!o) onClose(); }} />
    </MemoryRouter>
  );
}

async function mount(node: React.ReactNode) {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  await act(async () => { root!.render(node); });
  await act(async () => { await new Promise(r => setTimeout(r, 0)); });
}
const input = () => document.querySelector<HTMLInputElement>('[cmdk-input]')!;
const headings = () => [...document.querySelectorAll('[cmdk-group-heading]')].map(h => h.textContent);
const key = async (k: string) => {
  await act(async () => {
    (document.activeElement ?? document.body).dispatchEvent(new KeyboardEvent('keydown', { key: k, bubbles: true, cancelable: true }));
  });
};
async function type(v: string) {
  await act(async () => {
    const el = input();
    const set = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!;
    set.call(el, v);
    el.dispatchEvent(new Event('input', { bubbles: true }));
  });
}
const settle = (ms = 260) => act(async () => { await new Promise(r => setTimeout(r, ms)); });

beforeEach(() => { apiMock.serviceNames.mockClear(); });
afterEach(async () => {
  await act(async () => { root?.unmount(); });
  host?.remove(); host = null; root = null;
  document.body.innerHTML = '';
  __resetEscLayers();
});

describe('CommandPaletteBody (cmdk)', () => {
  it('etiketli diyalog, girdi odakta, boş sorguda Pages grubu', async () => {
    await mount(<Harness onClose={() => {}} />);
    const dlg = document.querySelector('[role="dialog"]')!;
    expect(dlg.getAttribute('aria-labelledby')).toBeTruthy();
    expect(document.getElementById(dlg.getAttribute('aria-labelledby')!)!.textContent).toBe('Komut paleti');
    expect(document.activeElement).toBe(input());
    expect(input().getAttribute('role')).toBe('combobox');
    expect(headings()).toContain('Pages');
  });

  it('servis araması sunucuda; ↓↵ navHref ile gezinir ve kapatır', async () => {
    const onClose = vi.fn();
    await mount(<Harness onClose={onClose} />);
    await type('check');
    await settle();
    expect(apiMock.serviceNames).toHaveBeenCalledWith('check', 20);
    expect(headings()[0]).toBe('Services');
    const opts = [...document.querySelectorAll('[cmdk-item]')].map(o => o.textContent);
    expect(opts.slice(0, 2)).toEqual(['checkoutService', 'checkout-workerService']);
    await key('ArrowDown');
    await key('Enter');
    expect(loc).toBe('/service?name=checkout-worker');
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('eylem kipi: ↵ girer, Esc listeye döner, ikinci Esc kapatır (Q3)', async () => {
    const onClose = vi.fn();
    await mount(<Harness onClose={onClose} />);
    await type('ack');
    await settle();
    expect(headings()[0]).toBe('Actions');
    await key('Enter');
    expect(headings()[0]).toMatch(/^Acknowledge problem — esc: back to search/);
    expect(input().placeholder).toBe('problem-1234');
    await key('Escape');
    expect(onClose).not.toHaveBeenCalled();
    expect(headings()).not.toContain('Acknowledge problem — esc: back to search');
    await key('Escape');
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('yerel eylem (P3): "dense" → ↵ yoğunluğu uygular ve kapatır; parametre kipi yok', async () => {
    const onClose = vi.fn();
    await mount(<Harness onClose={onClose} />);
    await type('dense');
    await settle();
    expect(headings()[0]).toBe('Actions');
    await key('Enter');
    expect(document.documentElement.getAttribute('data-density')).toBe('dense');
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('Esc yalnız paleti kapatır; alttaki katman açık kalır', async () => {
    const onClose = vi.fn();
    const underlay = vi.fn();
    await mount(<Harness onClose={onClose} underlay={underlay} />);
    await key('Escape');
    expect(onClose).toHaveBeenCalledTimes(1);
    expect(underlay).not.toHaveBeenCalled();
  });
});
