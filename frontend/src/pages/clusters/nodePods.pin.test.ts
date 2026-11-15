import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';

// Clusters → Nodes: node başına pod sayısı (operatör isteği, 2026-10-15).
// Ölçülemeyen node '—' (0 gerçek değer, bilinmeyen sıralamada en altta).
// Clusters S6 (D3): kolon nodeColumns.ts'te, hücre "N / kapasite".
describe('Nodes tablosu Pods kolonu', () => {
  const cols = readFileSync(new URL('./nodeColumns.ts', import.meta.url), 'utf8');
  const page = readFileSync(new URL('../Clusters.tsx', import.meta.url), 'utf8');
  it('kolon tanımı + hücre', () => {
    expect(cols).toContain("{ id: 'pods',     label: 'Pods',    sortValue: r => r.pods ?? -1");
    expect(cols).toContain("if (r.pods == null) return '';");
    expect(page).toContain("case 'pods': return dash(nodePodsText(r));");
  });
  it('hücreler kolon kimliğiyle, görünür kolon sırasıyla çizilir', () => {
    // Clusters S12 — coremetry/DataTable renderCell (görünür kolonlar primitifte).
    expect(page).toContain('renderCell={(r, c) => nodeCellContent(r, c.id, lang)}');
  });
});
