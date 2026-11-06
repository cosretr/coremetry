// /design hücreleri — v0.10.1619 (shadcn Faz 1 R10, plan §8.1; §12 Q7).
// Tema × yoğunluk hücreleri. CI axe kapısı temsilci temaları koşar
// (v0.10.1847: kayıttaki `ciCell`, varsayılan yoğunluk); tam matris gece/el
// ile (`DESIGN_AXE=all`, npm run design:axe:full,
// .github/workflows/design-axe-nightly.yml).
import { THEMES, type Theme } from '@/lib/theme';
import { THEME_REGISTRY } from '@/lib/themeRegistry';
import { DENSITY_STEPS, type Density } from '@/lib/density';

export interface Cell { theme: Theme; density: Density }

export const DEFAULT_DENSITY: Density = 'comfortable';

export const ALL_CELLS: readonly Cell[] = THEMES.flatMap(theme => DENSITY_STEPS.map(density => ({ theme, density })));

/** CI kümesi: kayıttaki temsilci temalar (`ciCell`), varsayılan yoğunluk. */
export const CI_CELLS: readonly Cell[] = THEME_REGISTRY.filter(e => e.ciCell).map(e => ({ theme: e.id, density: DEFAULT_DENSITY }));

export const cellKey = (c: Cell): string => `${c.theme}/${c.density}`;

/** `?cell=dark/compact` → o hücre; geçersiz/eksik → null (hepsi). */
export function parseCell(v: string | null): Cell | null {
  if (!v) return null;
  return ALL_CELLS.find(c => cellKey(c) === v) ?? null;
}

/** Axe matrisinin hücreleri: `DESIGN_AXE=all` → tam matris, aksi halde CI kümesi. */
export function axeCells(env: string | undefined): readonly Cell[] {
  return env === 'all' ? ALL_CELLS : CI_CELLS;
}
