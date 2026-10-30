// @vitest-environment jsdom
// coremetry/Confirm sözleşmesi — Faz 2 dilim 1 R3 (plan G2). ui/ConfirmDialog
// ile aynı karar sözleşmesi, shadcn alert-dialog üstünde, tembel çizici:
//  - Action = true, Cancel = false; Esc (lib/escLayer yığını, keyboard.ts) = false;
//  - açılış odağı Cancel'da (yıkıcı yol Enter'a hazır değil);
//  - tek uçuş: ikinci istek birinciyi false ile kapatır;
//  - danger → destructive düğme; varsayılan iptal metni CM_STRINGS.cancel;
//  - kapı dosyaları Radix'i STATİK içe aktarmaz (ilk yük).
import { describe, it, expect, afterEach, beforeEach } from 'vitest';
import { act, type ReactNode } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { __resetEscLayers, escLayerDepth } from '@/lib/escLayer';
import { useShortcuts } from '@/lib/keyboard';
import { ConfirmGate, useConfirm, type ConfirmOptions } from './index';
import { CM_STRINGS } from '../strings';

let host: HTMLDivElement | null = null;
let root: Root | null = null;
let ask: ((o: ConfirmOptions) => Promise<boolean>) | null = null;

function KeyboardHost() { useShortcuts([]); return null; }
function Grab() { ask = useConfirm(); return null; }

async function render(node: ReactNode) {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  await act(async () => { root!.render(<><KeyboardHost /><ConfirmGate><Grab />{node}</ConfirmGate></>); });
}
async function settleUi() {
  // tembel çizici: dinamik import + Suspense + Radix odak (mikro/makro görevler)
  for (let i = 0; i < 5; i++) await act(async () => { await new Promise(r => setTimeout(r, 0)); });
}
/** İlk istekte chunk (vitest'te modül dönüşümü) gelene dek bekle. */
async function opened() {
  for (let i = 0; i < 200 && !document.querySelector('[role="alertdialog"]'); i++) {
    await act(async () => { await new Promise(r => setTimeout(r, 10)); });
  }
  await settleUi();
}
const OPTS: ConfirmOptions = { title: 'Remove cluster?', body: 'prod-ist kaydı silinir.', confirmLabel: 'Remove' };
const btn = (text: string) => [...document.querySelectorAll<HTMLButtonElement>('[role="alertdialog"] button')].find(b => b.textContent?.trim() === text);

beforeEach(() => { (globalThis as unknown as { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true; __resetEscLayers(); });
afterEach(async () => {
  await act(async () => { root?.unmount(); });
  host?.remove(); root = null; host = null; ask = null;
  document.body.innerHTML = '';
});

describe('coremetry/Confirm', () => {
  it('Action → true; başlık + gövde; odak Cancel; iptal metni varsayılanı', async () => {
    await render(null);
    let res: boolean | undefined;
    await act(async () => { void ask!(OPTS).then(v => { res = v; }); });
    await opened();
    const dlg = document.querySelector('[role="alertdialog"]')!;
    expect(dlg.textContent).toContain('Remove cluster?');
    expect(dlg.textContent).toContain('prod-ist kaydı silinir.');
    expect(document.activeElement?.textContent).toBe(CM_STRINGS.cancel);
    await act(async () => { btn('Remove')!.click(); });
    await settleUi();
    expect(res).toBe(true);
    expect(document.querySelector('[role="alertdialog"]')).toBeNull();
  });

  it('Cancel → false; özel iptal metni', async () => {
    await render(null);
    let res: boolean | undefined;
    await act(async () => { void ask!({ ...OPTS, cancelLabel: 'Hayır' }).then(v => { res = v; }); });
    await opened();
    await act(async () => { btn('Hayır')!.click(); });
    await settleUi();
    expect(res).toBe(false);
  });

  it('Esc → false (escLayer yığını), yığın 0\'a döner', async () => {
    await render(null);
    let res: boolean | undefined;
    await act(async () => { void ask!(OPTS).then(v => { res = v; }); });
    await opened();
    expect(escLayerDepth()).toBe(1);
    const ev = new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true });
    await act(async () => { (document.activeElement ?? document.body).dispatchEvent(ev); });
    await settleUi();
    expect(res).toBe(false);
    expect(escLayerDepth()).toBe(0);
    expect(document.querySelector('[role="alertdialog"]')).toBeNull();
  });

  it('tek uçuş: ikinci istek birinciyi false ile kapatır, ikinci açık kalır', async () => {
    await render(null);
    const seen: string[] = [];
    await act(async () => { void ask!(OPTS).then(v => { seen.push(`a:${v}`); }); });
    await opened();
    await act(async () => { void ask!({ ...OPTS, title: 'İkinci?' }).then(v => { seen.push(`b:${v}`); }); });
    await opened();
    expect(seen).toEqual(['a:false']);
    expect(document.querySelector('[role="alertdialog"]')!.textContent).toContain('İkinci?');
    await act(async () => { btn('Remove')!.click(); });
    await settleUi();
    expect(seen).toEqual(['a:false', 'b:true']);
  });

  it('danger → destructive varyant', async () => {
    await render(null);
    await act(async () => { void ask!({ ...OPTS, danger: true }); });
    await opened();
    expect(btn('Remove')!.getAttribute('data-variant')).toBe('destructive');
  });

  it('kapı Radix / shadcn / overlay içe aktarmaz; çizici tembel import', () => {
    for (const f of ['ConfirmGate.tsx', 'context.ts']) {
      const src = readFileSync(resolve(__dirname, f), 'utf8');
      expect(src, f).not.toMatch(/from ['"](radix-ui|@radix-ui\/[^'"]+|@\/components\/shadcn\/[^'"]+|\.\.\/overlay[^'"]*|\.\/ConfirmDialog)['"]/);
    }
    expect(readFileSync(resolve(__dirname, 'ConfirmGate.tsx'), 'utf8')).toContain("lazy(() => import('./ConfirmDialog'))");
  });
});
