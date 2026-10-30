import { describe, it, expect, expectTypeOf } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import type { CommandPaletteProps } from './index';

// coremetry/CommandPalette API — v0.10.1617 (shadcn Faz 1 R9, plan §6).
// Cephe: bugünkü bileşen + açıcı AYNI modülden yeniden dışa aktarılıyor
// (kaynak metin — bileşeni node'da içe aktarmak tüm uygulama ağacını
// çekerdi); dondurulmuş tip kabuk diliminin cmdk gövdesinin sözleşmesi.
describe('coremetry/CommandPalette cephesi', () => {
  it('bugünkü CommandPalette + openCommandPalette yeniden dışa aktarımı', () => {
    const s = readFileSync(resolve(__dirname, 'CommandPalette.ts'), 'utf8');
    expect(s).toContain("export { CommandPalette, openCommandPalette } from '@/components/CommandPalette';");
    const src = readFileSync(resolve(__dirname, '../../CommandPalette.tsx'), 'utf8');
    expect(src).toMatch(/export function openCommandPalette\(\)/);
    expect(src).toMatch(/export function CommandPalette\(\)/);
  });
  it('dondurulmuş tip: open/onOpenChange/groups/query zorunlu', () => {
    expectTypeOf<CommandPaletteProps>().toHaveProperty('groups');
    expectTypeOf<CommandPaletteProps['query']>().toEqualTypeOf<{ value: string; onChange(v: string): void; placeholder?: string }>();
    // @ts-expect-error — groups olmadan derlenmez
    const p: CommandPaletteProps = { open: true, onOpenChange: () => {}, query: { value: '', onChange: () => {} } };
    expect(p).toBeTruthy();
  });
});
