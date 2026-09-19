// v0.10.797 — Incidents listesi "Problems" sütunu (bağlı problem toplamı / açık).
import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

describe('v0.10.797 — incident problem count column', () => {
  const src = readFileSync(resolve(__dirname, './Incidents.tsx'), 'utf8');
  it('column defined after Service and cell rendered after the service cell', () => {
    expect(src).toContain("{ id: 'problems', label: 'Problems'");
    expect(src.indexOf("id: 'service'")).toBeLessThan(src.indexOf("id: 'problems'"));
    expect(src.indexOf("id: 'problems'")).toBeLessThan(src.indexOf("id: 'cause'"));
    const cellIdx = src.indexOf("i.problemCount === undefined ? '—'");
    expect(cellIdx).toBeGreaterThan(src.indexOf('<ClusterChipsRef clusters={i.clusters} />'));
    expect(cellIdx).toBeLessThan(src.indexOf('<IncidentCause rc={i.rootCause} />'));
  });
  it('type carries the optional counts (old server → "—")', () => {
    const t = readFileSync(resolve(__dirname, '../lib/types.ts'), 'utf8');
    expect(t).toContain('problemCount?: number;\n  unresolvedProblems?: number;');
  });
});
