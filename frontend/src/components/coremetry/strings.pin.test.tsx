// @vitest-environment jsdom
//
// strings.pin — v0.10.1619 (shadcn Faz 1 R10, operatör 2026-10-30). shadcn'in
// sabit İngilizce metinleri TEK Türkçe varsayılandan (coremetry/strings) ve
// YALNIZ sarmalayıcı düzeyinde: üretilen dosyalar olduğu gibi kalır (orada
// "Close" durur), sarmalayıcı üretileni kapatır ve kendi etiketini çizer.
import { describe, it, expect, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { CM_STRINGS } from './strings';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogTitle } from './overlay/Dialog';
import { Sheet, SheetContent, SheetDescription, SheetTitle } from './overlay/Sheet';

const here = (p: string) => readFileSync(resolve(__dirname, p), 'utf8');

let host: HTMLDivElement | null = null;
let root: Root | null = null;
afterEach(async () => {
  await act(async () => { root?.unmount(); });
  host?.remove(); host = null; root = null;
  document.body.innerHTML = '';
});
async function render(node: React.ReactNode) {
  (globalThis as unknown as { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  await act(async () => { root!.render(node); });
}
const srOnly = () => [...document.body.querySelectorAll('.sr-only')].map(e => e.textContent);

describe('coremetry/strings — tek Türkçe varsayılan', () => {
  it('üretilen dosyalar dokunulmamış (İngilizce metin orada)', () => {
    expect(here('../shadcn/dialog.tsx')).toContain('<span className="sr-only">Close</span>');
    expect(here('../shadcn/sheet.tsx')).toContain('<span className="sr-only">Close</span>');
  });

  it('Dialog: kapat düğmesi + footer kapatı Türkçe, İngilizce yok', async () => {
    await render(
      <Dialog open onOpenChange={() => {}}>
        <DialogContent>
          <DialogTitle>Başlık</DialogTitle>
          <DialogDescription>Açıklama</DialogDescription>
          <DialogFooter showCloseButton />
        </DialogContent>
      </Dialog>,
    );
    expect(srOnly()).toEqual([CM_STRINGS.close]);
    expect(document.body.querySelector('[data-slot="dialog-footer"] button')?.textContent).toBe(CM_STRINGS.close);
    expect(document.body.textContent).not.toContain('Close');
    expect(document.body.querySelectorAll('[data-slot="dialog-close"]')).toHaveLength(2);
  });

  it('Sheet: kapat düğmesi Türkçe; showCloseButton={false} hiç çizmez', async () => {
    await render(
      <Sheet open onOpenChange={() => {}}>
        <SheetContent><SheetTitle>T</SheetTitle><SheetDescription>D</SheetDescription></SheetContent>
      </Sheet>,
    );
    expect(srOnly()).toEqual([CM_STRINGS.close]);
    expect(document.body.textContent).not.toContain('Close');
  });

  it('sarmalayıcı varsayılanları CM_STRINGS\'ten (İngilizce düz metin yok)', () => {
    const fb = here('FilterBar/FilterBar.tsx');
    const trp = here('TimeRangePicker/TimeRangePicker.tsx');
    for (const s of [fb, trp]) expect(s).toContain("import { CM_STRINGS } from '../strings';");
    expect(fb).not.toMatch(/'Remove filter'|'Filter'|"Filters"/);
    expect(trp).not.toMatch(/'Window'/);
    expect(here('Toaster.tsx')).toContain('containerAriaLabel={CM_STRINGS.notifications}');
  });
});
