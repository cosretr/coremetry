import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

// v0.10.712 — kök kapsaması paneli kaynak pini: mount, istemci ucu, İSTEĞE
// BAĞLI (mount'ta fetch yok, yoklama yok), useDataTable, dürüst boş/kapak.
const page = readFileSync(resolve(__dirname, 'AdminClickhouse.tsx'), 'utf8');
const api = readFileSync(resolve(__dirname, '../lib/api.ts'), 'utf8');

describe('AdminClickhouse kök kapsaması (v0.10.712)', () => {
  it('panel mount + istemci ucu', () => {
    expect(page).toContain('<RootCoveragePanel />');
    expect(api).toContain('/api/admin/clickhouse/root-coverage?range_s=');
  });
  it('isteğe bağlı: enabled armed, refetchInterval yok', () => {
    const i = page.indexOf("queryKey: ['ch-root-coverage', armed]");
    expect(i).toBeGreaterThan(0);
    const block = page.slice(i, i + 300);
    expect(block).toContain('enabled: armed !== null');
    expect(block).not.toContain('refetchInterval');
  });
  it('tablo useDataTable, giriş servisi yok satırı, 200 kapağı', () => {
    expect(page).toContain("storageKey: 'ch-root-coverage'");
    expect(page).toContain('(giriş servisi de yok)');
    expect(page).toContain('ilk 200 giriş servisi');
  });
});
describe('kök kapsaması 1 dk (v0.10.713)', () => {
  it('1 dk seçeneği ve kaynak rozeti', () => {
    expect(page).toContain('<option value={60}>son 1 dk</option>');
    expect(page).toContain("kaynak: {data.source === 'spans' ? 'spans' : 'MV'}");
  });
});

// v0.10.733 — kök tanımı: iki ölçü yan yana, seçici, entryRootOf saf.
describe('kök tanımı (v0.10.733)', () => {
  it('iki yüzde (tam kök / giriş kökü) + sütun + seçici + eski-tanım rozeti', () => {
    expect(page).toContain("{ id: 'entrypct', label: 'Giriş kökü %'");
    expect(page).toContain('giriş kökü {entryPct === null');
    expect(page).toContain('tam kök {pct === null');
    expect(page).toContain('aria-label="Kök tanımı"');
    // v0.10.924 — seçici SegmentedControl: 'entry' seçeneği + seçim kaydeder.
    expect(page).toContain("{ value: 'entry', label: 'giriş kökü'");
    expect(page).toContain('if (v !== def) saveDef.mutate(v);');
    expect(page).toContain('sonuç eski tanımla');
    expect(page).toContain("import { useTraceRootDef, useSaveTraceRootDef } from '@/lib/queries';");
    expect(page).toContain("import { entryRootOf } from '@/lib/rootCoverage';");
  });
  it('entryRootOf: giriş servisli satırın tamamı, olmayanın tam köklüleri', async () => {
    const { entryRootOf } = await import('../lib/rootCoverage');
    expect(entryRootOf({ entryService: 'gw', traces: 100, withRoot: 6 })).toBe(100);
    expect(entryRootOf({ entryService: '', traces: 50, withRoot: 12 })).toBe(12);
  });
  it('istemci ucu + hook + Traces başlığı', () => {
    expect(api).toContain("'/api/settings/trace-root-def'");
    const idx = readFileSync(resolve(__dirname, '../lib/queries/index.ts'), 'utf8');
    expect(idx).toContain("export { useTraceRootDef, useSaveTraceRootDef } from './traceRootDef';");
    const traces = readFileSync(resolve(__dirname, 'Traces.tsx'), 'utf8');
    expect(traces).toContain('Kök tanımı: ${rootDefLabel}');
    expect(traces).toContain("rootDefQ.data?.def === 'entry'");
  });
});
