// danglingMv.test.ts — v0.10.762 sarkan MV sihirbazı kablolama pinleri.
import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

describe('Sarkan MV onarımı — kablolama', () => {
  const page = readFileSync(resolve(__dirname, '../AdminClickhouse.tsx'), 'utf8');
  const api = readFileSync(resolve(__dirname, '../../lib/api.ts'), 'utf8');
  const types = readFileSync(resolve(__dirname, '../../lib/types.ts'), 'utf8');
  it('panel Trace hattı sağlığının altında; onay diyaloğu + audit\'li onarım ucu', () => {
    expect(page).toContain('<DanglingMVPanel />');
    expect(page.indexOf('<TraceHealthPanel />')).toBeLessThan(page.indexOf('<DanglingMVPanel />'));
    expect(page).toContain('api.chDanglingMVs()');
    expect(page).toContain('api.chDanglingMVRepair(d.host, d.view)');
    expect(page).toContain("title={`Sarkan view'ı onar");
    expect(api).toContain("'/api/admin/clickhouse/dangling-mv'");
    expect(api).toContain("'/api/admin/clickhouse/dangling-mv/repair'");
    expect(types).toContain('export interface CHDanglingMV {');
  });
});
