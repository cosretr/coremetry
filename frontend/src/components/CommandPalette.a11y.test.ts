import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

// v0.10.458 (dış skill denetimi D4) — komut paleti diyalog: role/aria-modal,
// odak tuzağı, combobox → listbox/option + activedescendant; ham renk yok.
// v0.10.1658 (etkileşim P2): bu sözleşmeleri artık Radix Dialog (role=dialog,
// aria-modal, odak tuzağı + açana dönüş) ve cmdk (combobox input,
// listbox/option, aria-activedescendant) taşıyor; davranış jsdom'da
// CommandPaletteBody.contract.test.tsx'te, axe /design kataloğunda
// (`coremetry/CommandPalette`, 3 tema hücresi). Burada kaynak kablosu.
describe('CommandPalette a11y (D4 → cmdk)', () => {
  const view = readFileSync(resolve(__dirname, 'coremetry/CommandPalette/CommandPaletteView.tsx'), 'utf8');
  it('coremetry/overlay/Dialog + başlıklı içerik (aria-labelledby)', () => {
    expect(view).toContain("import { Dialog, DialogContent, DialogTitle } from '../overlay/Dialog';");
    expect(view).toContain('<DialogTitle className="sr-only">{CM_STRINGS.commandPaletteTitle}</DialogTitle>');
  });
  it('cmdk: etiketli kök + etiketli girdi + etiketli liste; süzme kapalı (sunucu sırası)', () => {
    expect(view).toMatch(/<Command shouldFilter=\{false\} loop label=\{CM_STRINGS\.commandPaletteTitle\}/);
    expect(view).toContain('aria-label={query.placeholder ?? CM_STRINGS.search}');
    expect(view).toContain('<CommandList label={CM_STRINGS.suggestions}');
  });
  it('carries no raw rgb/rgba literals or inline style', () => {
    expect(view).not.toMatch(/rgba?\(/);
    expect(view).not.toMatch(/style=\{\{/);
  });
});
