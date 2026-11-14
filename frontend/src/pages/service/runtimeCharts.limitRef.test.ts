import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

// S0 karakterizasyon (docs/frontend/service-tabs-plan.md §5, K11–K13):
// JVM bellek kartlarında limit serisi referans (ref) ve KESİK çizgi; pod
// başına çizgi tavanı 40 (v0.9.95 operatör: "sadece 8 pod gösteriyor"),
// tavan yalnız ÇİZİMDE uygulanır (fetch tüm serileri getirir); pod çizgi
// etiketi podLineLabel sözleşmesinden. S9 heap kartını zarfa çevirirken
// "Pod başına çizgiler" kipi bu sözleşmeyi korur.
const src = readFileSync(resolve(__dirname, 'RuntimeCharts.tsx'), 'utf8');

describe('RuntimeCharts limit / tavan / etiket (S0)', () => {
  it('heap ve non-heap limit serisi ref:true', () => {
    const refs = src.match(/metric: 'jvm\.memory\.limit'[^}]*ref: true/g) ?? [];
    expect(refs.length).toBe(2);
  });
  it('referans çizgi kesik [5, 4]', () => {
    expect(src).toContain('dash: line.ref ? [5, 4] : undefined');
  });
  it('pod tavanı 40, kesim çizimde, not "N / M pod"', () => {
    expect(src).toContain('const FANOUT_MAX = 40;');
    expect(src).toContain('data.slice(0, FANOUT_MAX)');
    expect(src).toContain('`${FANOUT_MAX} / ${podTotal} pod gösteriliyor`');
  });
  it('pod çizgileri podLineLabel ile etiketlenir', () => {
    expect((src.match(/labelOf: podLineLabel/g) ?? []).length).toBeGreaterThanOrEqual(5);
  });
});
