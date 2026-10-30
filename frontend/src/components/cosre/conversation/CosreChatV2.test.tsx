// @vitest-environment jsdom
//
// CosreChatV2.test — v0.10.1647 (CoSRE v2 Faz 2b B1, plan §4.1). Boş v2 kabuğu
// gerçek Radix (coremetry Sheet) ile:
//   • non-modal: perde yok; dışarı-tık Radix'le KAPATMAZ, yalnız BOŞ alan kapatır
//   • Esc lib/escLayer yığınından kapatır (escBridge)
//   • genişlik v1 anahtarından, inline style={{ olmadan CSS var'la
//   • başlık AiMark + CoSRE + Yeni + Tam sayfa + Türkçe Kapat; composer pasif
//   • açılış yolları: FAB, ?chat=, ai-ask/prefill; `?ai=` gelince kapanır;
//     yieldToV1 iken olay açmaz; ?chat= adreste kalır
import { describe, it, expect, vi, beforeAll, beforeEach, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter, useSearchParams } from 'react-router-dom';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { useShortcuts } from '@/lib/keyboard';
import { __resetEscLayers } from '@/lib/escLayer';

vi.mock('@/lib/queries', () => ({ useOpenCriticalCount: () => ({ data: 0 }) }));
vi.mock('@/components/ai/TraceExplainNudge', () => ({ TraceExplainNudge: () => null }));

import { CosreChatV2 } from './CosreChatV2';

let host: HTMLDivElement | null = null;
let root: Root | null = null;
let setSp: ((s: string) => void) | null = null;
let readSp: (() => string) | null = null;
const store = new Map<string, string>();

beforeAll(() => {
  (globalThis as unknown as { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
});
beforeEach(() => {
  __resetEscLayers();
  store.clear();
  vi.stubGlobal('localStorage', {
    getItem: (k: string) => store.get(k) ?? null,
    setItem: (k: string, v: string) => { store.set(k, v); },
    removeItem: (k: string) => { store.delete(k); },
  });
});
afterEach(async () => {
  await act(async () => { root?.unmount(); });
  host?.remove();
  root = null; host = null; setSp = null; readSp = null;
  document.body.innerHTML = '';
  vi.unstubAllGlobals();
});

function Harness() {
  useShortcuts([]); // keyboard.ts'in tek document dinleyicisi (Esc yığını)
  const [sp, set] = useSearchParams();
  setSp = (s: string) => set(new URLSearchParams(s), { replace: true });
  readSp = () => sp.toString();
  return null;
}

async function render(props: { defaultOpen?: boolean; yieldToV1?: boolean } = {}, url = '/') {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  await act(async () => {
    root!.render(<MemoryRouter initialEntries={[url]}><Harness /><CosreChatV2 {...props} /></MemoryRouter>);
  });
  await tick();
}
const tick = () => act(async () => { await new Promise(r => setTimeout(r, 0)); });
const sheet = () => document.querySelector<HTMLElement>('[data-cosre-v2]');
const fab = () => document.querySelector<HTMLElement>('[data-tour="cosre-fab"]');

describe('CosreChatV2 kabuğu', () => {
  it('defaultOpen → Sheet açık, non-modal (perde yok), TR metinler, pasif composer', async () => {
    await render({ defaultOpen: true });
    const el = sheet()!;
    expect(el).not.toBeNull();
    expect(el.getAttribute('role')).toBe('dialog');
    expect(el.getAttribute('aria-label')).toBe('CoSRE sohbeti');
    expect(document.querySelector('[data-slot="sheet-overlay"]')).toBeNull();
    expect(el.textContent).toContain('CoSRE');
    expect(el.textContent).toContain('Yeni');
    expect(el.textContent).toContain('Kapat');
    expect(el.textContent).not.toMatch(/\bClose\b/);
    expect(el.querySelector('a[href="/cosre"]')?.textContent).toBe('Tam sayfa');
    const ta = el.querySelector('textarea')!;
    expect(ta.disabled).toBe(true);
    expect(ta.placeholder).toBe('Soru yazın…');
    expect(fab()).toBeNull();
  });

  it('kapalı başlar → FAB; FAB tıklaması açar', async () => {
    await render();
    expect(sheet()).toBeNull();
    await act(async () => { fab()!.click(); });
    await tick();
    expect(sheet()).not.toBeNull();
  });

  it('genişlik: v1 anahtarındaki değer CSS var ile, varsayılan 620', async () => {
    await render({ defaultOpen: true });
    expect(sheet()!.style.getPropertyValue('--cosre-sheet-w')).toBe('620px');
    expect(sheet()!.className).toContain('sm:w-(--cosre-sheet-w)');
    await act(async () => { root!.unmount(); });
    document.body.innerHTML = '';
    store.set('coremetry-cosre-drawer-w', '800');
    await render({ defaultOpen: true });
    expect(sheet()!.style.getPropertyValue('--cosre-sheet-w')).toBe('800px');
  });

  it('boş alana tıklama kapatır; Sheet içine ve etkileşimli öğeye tıklama kapatmaz', async () => {
    await render({ defaultOpen: true });
    const btn = document.createElement('button');
    document.body.appendChild(btn);
    await act(async () => { sheet()!.querySelector('p')!.click(); });
    await act(async () => { btn.click(); });
    expect(sheet()).not.toBeNull();
    const empty = document.createElement('div');
    document.body.appendChild(empty);
    await act(async () => { empty.click(); });
    await tick();
    expect(sheet()).toBeNull();
  });

  it('Esc kapatır (escBridge → lib/escLayer)', async () => {
    await render({ defaultOpen: true });
    const ev = new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true });
    await act(async () => { (document.activeElement ?? document.body).dispatchEvent(ev); });
    await tick();
    expect(sheet()).toBeNull();
  });

  it('ai-ask / prefill olayı açar; yieldToV1 iken açmaz', async () => {
    await render();
    await act(async () => { window.dispatchEvent(new CustomEvent('coremetry:ai-ask', { detail: { question: 'p1 var mı' } })); });
    await tick();
    expect(sheet()).not.toBeNull();
    await act(async () => { root!.unmount(); });
    document.body.innerHTML = '';
    await render({ yieldToV1: true });
    await act(async () => { window.dispatchEvent(new CustomEvent('coremetry:ai-prefill', { detail: { question: 'x' } })); });
    await tick();
    expect(sheet()).toBeNull();
  });

  it('Q1-a geçiş: v1 açıklama çekmecesi açıkken v2 FAB\'ı gizli (visibility — FAB satır-içi display taşır)', () => {
    const css = readFileSync(resolve(__dirname, '../../../styles/globals.css'), 'utf8');
    expect(css).toContain('body:has([data-tour="cosre-drawer"]) .cm-ai-fab,\nbody:has([data-tour="cosre-drawer"]) .cm-ai-nudge { visibility: hidden; }');
    const v1 = readFileSync(resolve(__dirname, '../../CopilotChat.tsx'), 'utf8');
    expect(v1).toContain('data-tour="cosre-drawer"');
    const fabSrc = readFileSync(resolve(__dirname, '../../ai/CopilotFab.tsx'), 'utf8');
    expect(fabSrc).toContain("'cm-ai-fab'");
    expect(fabSrc).not.toMatch(/visibility:/);
  });

  it('?chat= açar ve adreste KALIR; ?ai= gelince kapanır', async () => {
    await render({}, '/services?chat=c1');
    expect(sheet()).not.toBeNull();
    expect(readSp!()).toBe('chat=c1');
    await act(async () => { setSp!('chat=c1&ai=trace'); });
    await tick();
    expect(sheet()).toBeNull();
  });
});
