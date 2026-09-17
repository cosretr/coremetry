// spoolOrder.test.ts — v0.10.761 runbook eylem listesi: kuyruklu tablo başa.
import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { orderSpoolTables } from './spoolOrder';

describe('orderSpoolTables', () => {
  it('kuyruğu olan tablolar başa (dosya azalan), gerisi ada göre; ölçüm yoksa 0', () => {
    const rows = orderSpoolTables(['spans', 'logs', 'metric_points', 'db_summary_5m'], [
      { table: 'metric_points', files: 508_000 },
      { table: 'spans', files: 12, brokenFiles: 1 },
    ]);
    expect(rows.map(r => r.table)).toEqual(['metric_points', 'spans', 'db_summary_5m', 'logs']);
    expect(rows[1]).toEqual({ table: 'spans', files: 12, brokenFiles: 1, errorCount: 0 });
    expect(rows[2].files).toBe(0);
  });
  it('boş girdiler', () => {
    expect(orderSpoolTables(null, null)).toEqual([]);
    expect(orderSpoolTables(['a'], undefined)).toEqual([{ table: 'a', files: 0, brokenFiles: 0, errorCount: 0 }]);
  });
  it('runbook satırı rozet basar ve sıralı listeyi kullanır', () => {
    const src = readFileSync(resolve(__dirname, 'panels.tsx'), 'utf8');
    expect(src).toContain('orderSpoolTables(state.tables, state.queue?.tables)');
    expect(src).toContain('dosya bekliyor');
  });
});
