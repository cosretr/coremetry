// v0.10.811 — /ai: bySurface/byProvider null gelse de sayfa çökmez (eski sunucu).
import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

describe('v0.10.811 — /ai null kırılım koruması', () => {
  it('her byProvider/bySurface okuması ?? [] ile korunur', () => {
    const src = readFileSync(resolve(__dirname, '../AIObservability.tsx'), 'utf8');
    const bare = src.match(/stats\.(byProvider|bySurface)(\.map|\.length|\))/g) ?? [];
    expect(bare).toEqual([]);
    expect(src).toContain('for (const r of stats.byProvider ?? [])');
    expect(src).toContain('(stats.byProvider ?? []).map(');
    expect(src).toContain('(stats.bySurface ?? []).map(');
  });
});
