// traceHealth.test.ts — v0.10.757 panel saf yardımcıları + kablolama pini.
import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { bucketBars, lossVerdict, nameTone, pctOf } from './traceHealth';

describe('traceHealth — saf', () => {
  it('lossVerdict: kalıcı kayıp err, kalite işareti warn, temiz ok', () => {
    expect(lossVerdict({ accepted: 10, dropped: 0, writeFailed: 0 })).toEqual({ tone: 'b-ok', text: 'kayıp yok' });
    expect(lossVerdict({ accepted: 10, dropped: 0, writeFailed: 0, rejects: { span_empty_id: 3 } }).tone).toBe('b-warn');
    expect(lossVerdict({ accepted: 10, dropped: 2, writeFailed: 0 }).tone).toBe('b-err');
    expect(lossVerdict({ accepted: 10, dropped: 0, writeFailed: 0, rejects: { http_decode_failed: 1, span_empty_id: 9 } }).text).toBe('1 kayıp');
    // v0.10.760 — spool tıkalıysa sayaçlar sıfır olsa da kırmızı; api-rolü pod "kayıp yok" demez.
    expect(lossVerdict({ accepted: 0, dropped: 0, writeFailed: 0 }, true)).toEqual({ tone: 'b-err', text: 'spool tıkalı' });
    expect(lossVerdict({ accepted: 0, dropped: 0, writeFailed: 0 }, false, false)).toEqual({ tone: 'b-gray', text: 'bu pod ingest değil' });
    expect(lossVerdict({ accepted: 0, dropped: 3, writeFailed: 0 }, false, false).tone).toBe('b-gray');
  });
  it('pctOf / nameTone / bucketBars', () => {
    expect(pctOf(1, 0)).toBeNull();
    expect(pctOf(25, 100)).toBe(25);
    expect(nameTone(null)).toBe('b-ok');
    expect(nameTone(4.9)).toBe('b-ok');
    expect(nameTone(5)).toBe('b-warn');
    expect(nameTone(20)).toBe('b-err');
    expect(bucketBars([])).toEqual([]);
    expect(bucketBars([{ t: 1, spans: 0 }, { t: 2, spans: 50 }, { t: 3, spans: 100 }]).map(b => b.h)).toEqual([2, 50, 100]);
    expect(bucketBars([{ t: 1, spans: 0 }])[0].h).toBe(0);
  });
});

describe('traceHealth — kablolama', () => {
  const page = readFileSync(resolve(__dirname, '../AdminClickhouse.tsx'), 'utf8');
  const api = readFileSync(resolve(__dirname, '../../lib/api.ts'), 'utf8');
  it('panel kök kapsamasının yanında, istek isteğe bağlı, istemci ucu var', () => {
    expect(page).toContain('<TraceHealthPanel />');
    expect(page.indexOf('<RootCoveragePanel />')).toBeLessThan(page.indexOf('<TraceHealthPanel />'));
    expect(page).toContain("queryKey: ['ch-trace-health'");
    expect(page).toContain('lossVerdict(data.pod, data.spoolDegraded, data.pod.ingestRole)');
    expect(api).toContain('/api/admin/clickhouse/trace-health?range_s=');
  });
});
