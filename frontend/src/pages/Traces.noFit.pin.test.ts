// v0.10.813 — "veriye sığdır" düğmesi KALDIRILDI (operatör 2026-09-19: "böyle
// bir seçeneğe gerek yok; her trace gelsin"). Yeniden eklemeden önce
// feedback-traces-no-fit-to-data hafızasına bak.
import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

describe('v0.10.813 — Traces: veriye sığdır yok', () => {
  it('Traces.tsx düğmeyi ve fitExtent hesabını taşımaz; sürükle-seçim kalır', () => {
    const src = readFileSync(resolve(__dirname, './Traces.tsx'), 'utf8');
    expect(src).not.toContain('veriye sığdır\n');
    expect(src).not.toContain('const fitExtent');
    expect(src).toContain('onBrush={applyBrush}');
  });
});
