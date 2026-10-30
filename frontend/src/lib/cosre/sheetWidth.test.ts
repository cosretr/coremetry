// sheetWidth.test — v0.10.1647 (CoSRE v2 Faz 2b B1). v2 Sheet'i v1 çekmecesinin
// saklanan genişliğini okur; anahtar v1'deki literal ile aynı (bayrak çevrimi
// genişliği kaybettirmez).
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { COSRE_DRAWER_WIDTH_KEY, cosreSheetWidth } from './sheetWidth';
import { DRAWER_MIN_WIDTH } from '@/lib/drawerWidth';

describe('cosreSheetWidth', () => {
  it.each([
    [null, 1600, 620],
    ['', 1600, 620],
    ['abc', 1600, 620],
    [String(DRAWER_MIN_WIDTH - 1), 1600, 620],
    ['800', 1600, 800],
    // Saklanan genişlik ekrandan büyük → viewport − 160.
    ['1500', 1200, 1040],
    // Dar ekran: taban korunur.
    ['900', 400, DRAWER_MIN_WIDTH],
  ] as const)('raw=%s viewport=%d → %d', (raw, vp, want) => {
    expect(cosreSheetWidth(raw, 620, vp)).toBe(want);
  });

  it('anahtar v1 CopilotChat resizeKey ile aynı', () => {
    const v1 = readFileSync(resolve(__dirname, '../../components/CopilotChat.tsx'), 'utf8');
    expect(v1).toContain(`resizeKey="${COSRE_DRAWER_WIDTH_KEY}"`);
  });
});
