// @vitest-environment jsdom
// triggerButtonSelectors.test.tsx — v0.10.2116. Operator-reported: /traces
// satırları shadcn geçişinden (v0.10.2095, S4) beri ~60 px (36 yerine), Name
// metni yukarıda. Kök neden: shadcn TooltipTrigger / PopoverTrigger `asChild`
// iken çocuğa kendi data-slot'unu geçirir; components/shadcn/button.tsx
// data-slot="button"ı {...props}'tan ÖNCE yazdığı için DOM'daki düğme
// "tooltip-trigger" (ya da "popover-trigger") taşır. tokens.css'teki
// `[data-row-kiosk][data-slot="button"]` seçicisi eşleşmiyordu → kiosk düğmesi
// hücre akışına düştü (satır +24 px). Operatör kararıyla düğme kaldırıldı;
// aynı sınıf hata başlık hunisinde (`[data-tcf-btn][data-slot="button"]`,
// 20 yerine 24 px) seçiciyle düzeltildi.
//
// jsdom yerleşim ölçmez; pin: tokens.css'teki GERÇEK seçici metni, gerçek
// tetikleyici sarımıyla render edilen düğmeyle `matches()` ediyor mu.
import { describe, it, expect, afterEach } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import type { ReactNode } from 'react';
import '@/test/radixJsdom';
import { Button } from '@/components/shadcn/button';
import { Popover, PopoverTrigger, Tooltip, TooltipTrigger } from '@/components/coremetry/overlay';

const tokens = readFileSync(resolve(__dirname, 'tokens.css'), 'utf8');

// selectorFor — tokens.css'te öznitelik kancasını taşıyan kuralın seçicisi.
function selectorFor(hook: string): string {
  const line = tokens.split('\n').find(l => l.includes(`[${hook}]`) && l.includes('{'));
  if (!line) throw new Error(`tokens.css'te ${hook} kuralı yok`);
  return line.slice(0, line.indexOf('{')).trim();
}

let host: HTMLDivElement | null = null; let root: Root | null = null;
function render(node: ReactNode): HTMLElement {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  host = document.createElement('div'); document.body.appendChild(host); root = createRoot(host);
  act(() => { root!.render(node); });
  return host;
}
afterEach(() => { act(() => { root?.unmount(); }); host?.remove(); root = null; host = null; });

describe('asChild tetikleyici altındaki shadcn Button seçicileri', () => {
  it('tuzak: TooltipTrigger asChild altındaki Button data-slot="button" TAŞIMAZ', () => {
    const el = render(
      <Tooltip><TooltipTrigger asChild>
        <Button variant="ghost" size="icon-xs" data-probe="" aria-label="p">p</Button>
      </TooltipTrigger></Tooltip>,
    );
    const btn = el.querySelector('[data-probe]')!;
    expect(btn.tagName).toBe('BUTTON');
    expect(btn.getAttribute('data-slot')).toBe('tooltip-trigger');
  });

  it('kolon süzgeci hunisi (TooltipTrigger > PopoverTrigger asChild): boyut kuralı eşleşir', () => {
    const el = render(
      <Popover open={false} onOpenChange={() => {}}><Tooltip><TooltipTrigger asChild><PopoverTrigger asChild>
        <Button variant="ghost" size="icon-xs" data-tcf-btn="" className="tcf-btn" aria-label="f">f</Button>
      </PopoverTrigger></TooltipTrigger></Tooltip></Popover>,
    );
    const btn = el.querySelector('[data-tcf-btn]')!;
    expect(btn.tagName).toBe('BUTTON');
    expect(btn.getAttribute('data-slot')).not.toBe('button');
    expect(btn.matches(selectorFor('data-tcf-btn'))).toBe(true);
  });

  it('styles/ altında asChild ile ezilebilen [data-slot="button"] kancası kalmadı', () => {
    expect(tokens).not.toMatch(/\[data-[a-z-]+\]\[data-slot="?button"?\]/);
  });
});
