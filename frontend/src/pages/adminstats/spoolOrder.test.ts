// spoolOrder.test.ts — v0.10.761 runbook eylem listesi: kuyruklu tablo başa.
import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { orderSpoolTables, spoolRowBadge, startResultText } from './spoolOrder';

describe('orderSpoolTables', () => {
  it('kuyruğu olan tablolar başa (dosya azalan), gerisi ada göre; ölçüm yoksa 0', () => {
    const rows = orderSpoolTables(['spans', 'logs', 'metric_points', 'db_summary_5m'], [
      { table: 'metric_points', files: 508_000 },
      { table: 'spans', files: 12, brokenFiles: 1 },
    ]);
    expect(rows.map(r => r.table)).toEqual(['metric_points', 'spans', 'db_summary_5m', 'logs']);
    expect(rows[1]).toEqual({ table: 'spans', files: 12, brokenFiles: 1, errorCount: 0, blocked: false, hosts: [] });
    expect(rows[2].files).toBe(0);
  });
  it('boş girdiler', () => {
    expect(orderSpoolTables(null, null)).toEqual([]);
    expect(orderSpoolTables(['a'], undefined)).toEqual([{ table: 'a', files: 0, brokenFiles: 0, errorCount: 0, blocked: false, hosts: [] }]);
  });
  it('runbook satırı rozet basar ve sıralı listeyi kullanır', () => {
    const src = readFileSync(resolve(__dirname, 'panels.tsx'), 'utf8');
    expect(src).toContain('orderSpoolTables(state.tables, state.queue?.tables)');
    expect(src).toContain('spoolRowBadge(row)');
    expect(src).toContain('gönderici durmuş');
  });
  // v0.10.773 — durmuş gönderici rozette söylenir; düğüm kırılımı title'da.
  it('spoolRowBadge: durmuş gönderici, bozuk, boş', () => {
    const base = { table: 'spans', files: 514_000, brokenFiles: 0, errorCount: 28, blocked: true,
      hosts: [{ host: 'ch-02', files: 514_000, blocked: true }, { host: 'ch-01', files: 0, blocked: false }] };
    const b = spoolRowBadge(base);
    expect(b.tone).toBe('b-err');
    expect(b.text).toBe('514.000 dosya bekliyor · GÖNDERİCİ DURMUŞ');
    expect(b.title).toContain('ch-02: 514.000 (durmuş)');
    expect(b.title).toContain('28 gönderim hatası');
    expect(spoolRowBadge({ ...base, blocked: false, hosts: [] }).text).toBe('514.000 dosya bekliyor');
    expect(spoolRowBadge({ ...base, files: 0, brokenFiles: 3, blocked: false, hosts: [] }).text).toBe('3 bozuk');
    expect(spoolRowBadge({ ...base, files: 0, hosts: [] })).toMatchObject({ tone: 'b-err', text: 'kuyruk boş · gönderici durmuş' });
    expect(spoolRowBadge({ ...base, files: 0, blocked: false, errorCount: 0, hosts: [] })).toMatchObject({ tone: 'b-gray', text: 'kuyruk boş' });
  });
  // v0.10.775 — start cevabı satırın yanında, düğüm başına.
  it('startResultText: düğüm başına ok/HATA, tek düğüm kısa', () => {
    expect(startResultText({ ok: true })).toEqual({ tone: 'ok', text: 'gönderici başlatıldı' });
    expect(startResultText({ ok: false, error: 'boom' })).toEqual({ tone: 'err', text: 'boom' });
    const r = startResultText({ ok: false, hosts: [{ host: 'ch-01:9000', ok: true }, { host: 'ch-02:9000', ok: false, error: 'timeout' }] });
    expect(r.tone).toBe('err');
    expect(r.text).toBe('1/2 düğümde hata · ch-01 ok · ch-02 HATA: timeout');
    expect(startResultText({ ok: true, hosts: [{ host: 'a:9000', ok: true }] }).text).toBe('başlatıldı · a ok');
    const src = readFileSync(resolve(__dirname, 'panels.tsx'), 'utf8');
    expect(src).toContain('rowNote[t]');
    expect(src).toContain('startResultText(res)');
  });
});
