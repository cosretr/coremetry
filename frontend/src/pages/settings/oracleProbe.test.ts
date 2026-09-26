// oracleProbe.test.ts — v0.10.768.
import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { ORACLE_TEST_WINDOWS, mappingVerdict, scanVerdict, summaryHeadline } from './oracleProbe';
import type { OracleMappingCheck, OracleScanCheck, OracleWindowSummary } from '@/lib/types';

const base: OracleScanCheck = { checked: true, tsColumn: 'ERR_TIMESTAMP', found: true, indexed: false, partitioned: false, numRows: 12000000 };

describe('oracleProbe — tam tarama hükmü', () => {
  it('indeks nötr, partition nötr, ikisi yok kırmızı, kontrol yok gri', () => {
    // v0.10.929 (K5) — iyi yapılandırma hükmü nötr (b-gray), yeşil değil.
    expect(scanVerdict({ ...base, indexed: true, indexName: 'IX_TS' }).tone).toBe('b-gray');
    expect(scanVerdict({ ...base, partitioned: true }).tone).toBe('b-gray');
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
  it('eşlenen kipte: eşlenen LONG kırmızı, eşlenmemiş LONG nötr', () => {
    expect(longVerdict(l({ mappedOnly: true, columns: ['D'], selected: ['D'] }))?.tone).toBe('b-err');
    const ok = longVerdict(l({ mappedOnly: true, columns: ['D'], selected: [] }));
    expect(ok?.tone).toBe('b-gray'); // v0.10.929 (K5)
    expect(ok?.detail).toBe('D');
  });
  it('LONG yoksa null; kontrol yoksa gri; sonuç yoksa null', () => {
    expect(longVerdict(l({}))).toBeNull();
    expect(longVerdict(l({ checked: false, error: 'ORA-00942' }))).toMatchObject({ tone: 'b-gray', detail: 'ORA-00942' });
    expect(longVerdict(undefined)).toBeNull();
  });
});

// v0.10.886 — eşleme hükmü: trace/servis/kod eksikse kırmızı + öneri cümlesi;
// yalnız yan alan eksikse sarı; eksik yoksa null; kontrol yoksa gri.
describe('oracleProbe — eşleme hükmü', () => {
  const ok: OracleMappingCheck = { checked: true, present: 16, missing: [] };
  it('eksik yoksa null, kontrol yoksa gri', () => {
    expect(mappingVerdict(ok)).toBeNull();
    expect(mappingVerdict(undefined)).toBeNull();
    expect(mappingVerdict({ ...ok, checked: false, error: 'yetki' })?.tone).toBe('b-gray');
  });
  it('traceId eksik → kırmızı, önekli öneri metinde', () => {
    const v = mappingVerdict({ checked: true, present: 2, prefix: 'MCA_',
      missing: [{ field: 'traceId', column: 'ERR_TRACEID', suggest: 'MCA_ERR_TRACEID' }, { field: 'host', column: 'ERR_HOSTNAME' }] });
    expect(v?.tone).toBe('b-err');
    expect(v?.text).toContain('trace/servis/hata kodu okunmaz');
    expect(v?.detail).toContain('traceId→ERR_TRACEID');
    expect(v?.detail).toContain('MCA_ önekli karşılıkları var (1/2)');
  });
  it('yalnız yan alan eksik → sarı, öneri yoksa eşleme formuna yönlendirir', () => {
    const v = mappingVerdict({ checked: true, present: 15, missing: [{ field: 'location', column: 'ERR_LOCATION' }] });
    expect(v?.tone).toBe('b-warn');
    expect(v?.detail).toContain('Alan eşlemesi');
  });
});

// v0.10.902 — özel kipte zaman / trace listesi eksikliği kırmızı.
describe('mappingVerdict — sorgu çıktısı kaynağı', () => {
  it('traceIds ya da timestamp eksikse b-err, cümle "sorgu çıktısında"', () => {
    const v = mappingVerdict({ checked: true, present: 5, source: 'query', missing: [{ field: 'traceIds', column: 'TRACEIDS' }] });
    expect(v?.tone).toBe('b-err');
    expect(v?.text).toMatch(/sorgu çıktısında/);
    const t = mappingVerdict({ checked: true, present: 5, source: 'query', missing: [{ field: 'timestamp', column: 'TIMESLICE' }] });
    expect(t?.tone).toBe('b-err');
    const h = mappingVerdict({ checked: true, present: 5, source: 'query', missing: [{ field: 'host', column: 'HOSTNAME' }] });
    expect(h?.tone).toBe('b-warn');
  });
});

// v0.10.907 — eşlenmemiş alan (kolon boş) "eşlenmedi (öneri X)" yazar, kırmızı.
describe('mappingVerdict — eşlenmemiş alanlar', () => {
  it('kolonsuz eksikler: kırmızı, öneri + buton ipucu', () => {
    const v = mappingVerdict({ checked: true, present: 1, source: 'query', missing: [
      { field: 'traceIds', column: '', suggest: 'TRACEIDS' },
      { field: 'service', column: '', suggest: 'OPERATIONCODE' },
    ] });
    expect(v?.tone).toBe('b-err');
    expect(v?.text).toMatch(/eşlenmemiş/);
    expect(v?.detail).toMatch(/traceIds: eşlenmedi \(öneri TRACEIDS\)/);
    expect(v?.detail).toMatch(/Önerilen eşlemeyi uygula/);
  });
});
