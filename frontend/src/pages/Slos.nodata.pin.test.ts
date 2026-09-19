// v0.10.801 (denetim S2) — olaysız SLO "Olay yok" (gri), %100 değil.
import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

const read = (rel: string) => readFileSync(resolve(__dirname, rel), 'utf8');

describe('v0.10.801 — SLO no-data state', () => {
  it('/slos: status badge, SLI/budget/burn cells and sort honour noData', () => {
    const src = read('./Slos.tsx');
    expect(src).toContain('<span className="badge b-gray" title={o.status.hint}>Olay yok</span>');
    expect(src).toContain("o.status && !o.status.noData ? (o.status.sli * 100).toFixed(3) + '%' : '—'");
    expect(src).toContain('o.status && !o.status.noData ? <BudgetBar');
    expect(src).toContain('o.status && !o.status.noData ? <BurnBadge');
    expect(src).toContain('o.status.noData ? 0.5 : o.status.healthy ? 1 : 0');
    expect(src).toContain('⚠ tanım');
  });
  it('service SLO chip renders grey "Olay yok" and hides SLI/budget when noData', () => {
    const src = read('./Service.tsx');
    expect(src).toContain('const noData = !!st?.noData;');
    expect(src).toContain("{noData ? 'Olay yok' : healthy ? 'Healthy' : 'Breached'}");
    expect(src).toContain('{st && !noData && (');
  });
  it('type carries noData + hint', () => {
    const t = read('../lib/types.ts');
    expect(t).toContain('noData?: boolean;\n  hint?: string;\n}\nexport interface SLORow');
  });
});
