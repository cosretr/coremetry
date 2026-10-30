// /design hücreleri — v0.10.1619 (shadcn Faz 1 R10, plan §8.1; §12 Q7).
// Tema × yoğunluk = 9 hücre. CI axe kapısı 3 hücre koşar (tema başına bir,
// varsayılan yoğunluk); tam 9'lu matris gece/el ile (`DESIGN_AXE=all`,
// npm run design:axe:full, .github/workflows/design-axe-nightly.yml).
import { THEMES, type Theme } from '@/lib/theme';
import { DENSITY_STEPS, type Density } from '@/lib/density';

export interface Cell { theme: Theme; density: Density }

export const DEFAULT_DENSITY: Density = 'comfortable';

export const ALL_CELLS: readonly Cell[] = THEMES.flatMap(theme => DENSITY_STEPS.map(density => ({ theme, density })));

/** CI kümesi: tema başına bir hücre, varsayılan yoğunluk. */
export const CI_CELLS: readonly Cell[] = THEMES.map(theme => ({ theme, density: DEFAULT_DENSITY }));

export const cellKey = (c: Cell): string => `${c.theme}/${c.density}`;

/** `?cell=dark/compact` → o hücre; geçersiz/eksik → null (hepsi). */
export function parseCell(v: string | null): Cell | null {
  if (!v) return null;
  return ALL_CELLS.find(c => cellKey(c) === v) ?? null;
}

/** Axe matrisinin hücreleri: `DESIGN_AXE=all` → 9, aksi halde CI'nin 3'ü. */
export function axeCells(env: string | undefined): readonly Cell[] {
  return env === 'all' ? ALL_CELLS : CI_CELLS;
}
