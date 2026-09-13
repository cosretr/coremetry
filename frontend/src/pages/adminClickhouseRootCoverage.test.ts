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
