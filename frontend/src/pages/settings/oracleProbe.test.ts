// oracleProbe.test.ts — v0.10.768.
import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { ORACLE_TEST_WINDOWS, scanVerdict, summaryHeadline } from './oracleProbe';
import type { OracleScanCheck, OracleWindowSummary } from '@/lib/types';

const base: OracleScanCheck = { checked: true, tsColumn: 'ERR_TIMESTAMP', found: true, indexed: false, partitioned: false, numRows: 12000000 };

describe('oracleProbe — tam tarama hükmü', () => {
  it('indeks yeşil, partition yeşil, ikisi yok kırmızı, kontrol yok gri', () => {
    expect(scanVerdict({ ...base, indexed: true, indexName: 'IX_TS' }).tone).toBe('b-ok');
    expect(scanVerdict({ ...base, partitioned: true }).tone).toBe('b-ok');
    const risk = scanVerdict({ ...base, partitionKey: 'ERR_TYPE' });
    expect(risk.tone).toBe('b-err');
    expect(risk.text).toBe('TAM TARAMA RİSKİ');
    expect(risk.detail).toContain('ERR_TYPE');
    expect(scanVerdict({ ...base, checked: false, error: 'ORA-00942' })).toMatchObject({ tone: 'b-gray', detail: 'ORA-00942' });
    expect(scanVerdict({ ...base, found: false }).tone).toBe('b-gray');
    expect(scanVerdict(undefined).tone).toBe('b-gray');
  });
  it('özet başlığı: tavan, eşleme, trace araması üç hâl', () => {
    const sm: OracleWindowSummary = {
      windowMin: 5, rows: 500, capped: true, mapped: 480, noTimestamp: 20, badTraceId: 3,
      operations: [], errorCodes: [], traceIds: 40, lookupDone: true, tracesFound: 37, services: [],
    };
    expect(summaryHeadline(sm)).toBe('son 5 dk: 500+ satır · 480 eşlendi · 20 damgasız · 3 bozuk trace id · 37/40 trace Coremetry\'de');
    expect(summaryHeadline({ ...sm, capped: false, lookupDone: false })).toContain('40 ayrık trace id (arama yok)');
    expect(summaryHeadline({ ...sm, lookupDone: false, lookupError: 'timeout' })).toContain('trace araması başarısız (timeout)');
    expect(summaryHeadline({ ...sm, error: 'ORA-01' })).toBe('özet alınamadı: ORA-01');
    expect(summaryHeadline(undefined)).toBe('');
  });
  it('pencereler 5/15/60 ve sekme kablolaması', () => {
    expect([...ORACLE_TEST_WINDOWS]).toEqual([5, 15, 60]);
    const src = readFileSync(resolve(__dirname, 'OracleTab.tsx'), 'utf8');
    expect(src).toContain('api.testOracleSource(sourceForSave(rows[i].src, rows[i].snapshot), testWindow)');
    expect(src).toContain('scanVerdict(pr.scan)');
    expect(src).toContain('summaryHeadline(pr.summary)');
    expect(src).toContain('pr.pollQuery');
  });
});

// v0.10.845 — LONG kolon hükmü: SELECT * kipinde kırmızı + kutuyu aç; eşlenen
// kipte yalnız eşlenen LONG kırmızı; LONG yoksa hüküm yok; sözlük okunamadıysa gri.
import { longVerdict } from './oracleProbe';
import type { OracleLongCheck } from '@/lib/types';

describe('oracleProbe — LONG kolon hükmü', () => {
  const l = (o: Partial<OracleLongCheck>): OracleLongCheck => ({ checked: true, mappedOnly: false, columns: [], selected: [], ...o });
  it('SELECT * kipinde LONG kolon kırmızı ve kutuyu açmayı söyler', () => {
    const v = longVerdict(l({ columns: ['MCA_ERR_DETAIL'], selected: ['MCA_ERR_DETAIL'] }));
    expect(v?.tone).toBe('b-err');
    expect(v?.text).toContain('MCA_ERR_DETAIL');
    expect(v?.detail).toContain('yalnız eşlenen kolonlar');
  });
  it('eşlenen kipte: eşlenen LONG kırmızı, eşlenmemiş LONG yeşil', () => {
    expect(longVerdict(l({ mappedOnly: true, columns: ['D'], selected: ['D'] }))?.tone).toBe('b-err');
    const ok = longVerdict(l({ mappedOnly: true, columns: ['D'], selected: [] }));
    expect(ok?.tone).toBe('b-ok');
    expect(ok?.detail).toBe('D');
  });
  it('LONG yoksa null; kontrol yoksa gri; sonuç yoksa null', () => {
    expect(longVerdict(l({}))).toBeNull();
    expect(longVerdict(l({ checked: false, error: 'ORA-00942' }))).toMatchObject({ tone: 'b-gray', detail: 'ORA-00942' });
    expect(longVerdict(undefined)).toBeNull();
  });
});
