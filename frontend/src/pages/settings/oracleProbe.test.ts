// oracleProbe.test.ts — v0.10.768.
import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { ORACLE_TEST_WINDOWS, coverageOpLine, coverageText, livePreviewText, mappingVerdict, scanVerdict, summaryHeadline } from './oracleProbe';
import type { OracleLivePreview, OracleMappingCheck, OracleScanCheck, OracleSubjectCoverage, OracleWindowSummary } from '@/lib/types';

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

// v0.10.998 — canlıya geçiş önizlemesi (operatör kararı: kaynak kipi gölge → canlı).
describe('oracleProbe — canlıya geçiş önizlemesi', () => {
  const base: OracleLivePreview = {
    sourceId: 's1', sourceName: 'core-oracle', mode: 'shadow',
    opened24h: 37, opened7d: 1212, critical7d: 40, clusters7d: 3, openNow: 5,
    top: [{ subject: 'bsa-payments', opened: 61 }, { subject: 'ext:core-oracle/OP9', opened: 20 }],
    notifyKind: 'anomaly', channelsEnabled: 4, channelsAccepting: 2, channelNames: ['ops-mail', 'sre-slack'],
    teamMail: true, openCapPerTick: 20, generatedAt: 0,
  };
  it('gölge + alıcı var: sayılar, tür, kanallar, açık Problem notu, tavan, özneler', () => {
    const t = livePreviewText(base);
    expect(t.warn).toBe(false);
    expect(t.headline).toBe('Kayıtlı kip gölge: son 24 saatte 37, son 7 günde 1.212 Problem açıldı (40 kritik, en az 3 küme) · şu an açık 5.');
    expect(t.lines).toEqual([
      'Canlıda her yeni açılış bir bildirimdir (tür: Anomali): etkin 4 kanaldan 2 tanesi alıyor (ops-mail, sre-slack); ekip maili alıyor. Kanalın servis / ekip / öncelik süzgeçleri ayrıca uygulanır.',
      'Kipi canlıya alınca zaten açık olan 5 Problem için bildirim gönderilmez; yalnız yeni açılışlar bildirilir.',
      "Okuma başına en çok 20 açılış; fazlası tek özet Problem'de toplanır (Anomali ayarları).",
      'En çok açan özneler (7 gün): bsa-payments (61), ext:core-oracle/OP9 (20).',
    ]);
  });
  it('hiçbir kanal ve ekip maili türü almıyor → uyarı: canlıda bildirim GİTMEZ', () => {
    const t = livePreviewText({ ...base, channelsAccepting: 0, channelNames: [], teamMail: false });
    expect(t.warn).toBe(true);
    expect(t.lines[0]).toContain('Canlıda bildirim GİTMEZ');
    expect(t.lines[0]).toContain('"Anomali"');
    expect(t.lines[0]).toContain('etkin 4 kanalın hiçbiri');
  });
  it('yalnız ekip maili alıyorsa uyarı yok; adı sığmayan kanallar +N', () => {
    expect(livePreviewText({ ...base, channelsAccepting: 0, channelNames: [], teamMail: true }).warn).toBe(false);
    const many = livePreviewText({ ...base, channelsAccepting: 14, channelNames: ['a', 'b'] });
    expect(many.lines[0]).toContain('(a, b +12)');
  });
  it('kapalı kip uyarır; canlı kipte "açık Problem" notu yok; boş hafta ayrı cümle; küme yoksa anılmaz', () => {
    const off = livePreviewText({ ...base, mode: 'off' });
    expect(off.warn).toBe(true);
    expect(off.headline).toContain('Kayıtlı kip kapalı: tarama koşmuyor');
    const live = livePreviewText({ ...base, mode: 'live' });
    expect(live.headline.startsWith('Kayıtlı kip canlı:')).toBe(true);
    expect(live.lines.some(l => l.includes('Kipi canlıya alınca'))).toBe(false);
    const empty = livePreviewText({ ...base, opened24h: 0, opened7d: 0, critical7d: 0, clusters7d: 0, openNow: 0, top: [] });
    expect(empty.headline).toBe('Kayıtlı kip gölge: son 7 günde bu kaynaktan hiç Problem açılmadı (şu an açık 0).');
    expect(empty.lines.some(l => l.startsWith('En çok açan'))).toBe(false);
    expect(livePreviewText({ ...base, clusters7d: 0 }).headline).toContain('(40 kritik)');
  });
  it('sekme: istek yalnız açılınca gider (prefetch yok), kayıtlı kaynakta çizilir', () => {
    const tab = readFileSync(resolve(__dirname, 'OracleTab.tsx'), 'utf8');
    expect(tab).toContain("queryKey: ['oracle-live-preview', id], queryFn: () => api.oracleLivePreview(id), staleTime: 60_000, enabled: open");
    expect(tab).toContain('{src.id && <OracleLivePreviewLine id={src.id} />}');
  });
});

// v0.10.999 — özne kapsamı (Oracle odak "2": satır → servis oranı ve nedenleri).
describe('oracleProbe — özne kapsamı', () => {
  const base: OracleSubjectCoverage = {
    sourceId: 's1', sourceName: 'core-oracle', hours: 24,
    rowsTotal: 1100, rowsListed: 1000, rowsResolved: 703, rowsLearned: 400, rowsPod: 303,
    opsTotal: 12, opsListed: 9, opsResolved: 3,
    byReason: { no_trace_id: 120, trace_not_found: 80, multi_service: 50, dead_service: 30, unconfirmed: 12, no_operation: 5 },
    unresolved: [
      { operation: 'OP_HOST', rows: 120, withTrace: 0, status: 'unresolved', reason: 'no_trace_id', instance: 'WMOBAPPP84', host: 'WMOBAPPP84' },
      { operation: 'OP_MULTI', rows: 50, withTrace: 50, status: 'unresolved', reason: 'multi_service', service: 'bsa-a-prod', votes: '4/10' },
      { operation: 'OP_PODX', rows: 9, withTrace: 0, status: 'unresolved', reason: 'no_trace_id', instance: 'legacy-batch-7b9949bb74-l4bg5', podLike: true },
      { operation: '', rows: 5, withTrace: 0, status: 'unresolved', reason: 'no_operation', host: 'WMOBAPPP90' },
    ],
    aliveKnown: true, fnChecked: false, fnEnabled: false, rowsFunctionCode: 0, learnedEntries: 5, generatedAt: 0,
  };
  it('özet: oran, basamak kırılımı, kısmi sınıflama notu, nedenler büyükten küçüğe', () => {
    const t = coverageText(base);
    expect(t.warn).toBe(false);
    expect(t.headline).toBe('Son 24 saatte 1.000 satırın %70 kadarı bir servise bağlanıyor (703 satır: öğrenilmiş eşleme 400, pod adından 303) · 3/9 operasyon.');
    expect(t.lines).toEqual([
      'Yalnız en çok satırlı 9 operasyon sınıflandı (12 operasyon, 1.100 satırın 1.000 tanesi).',
      "Bağlanmayan 297 satır — neden: satırlarda trace kimliği yok 120 · trace Coremetry'de yok 80 · çok servisli operasyon 50 · öğrenilmiş servis canlı değil 30 · eşleme henüz onaysız 12 · operasyon kodu boş 5.",
    ]);
    expect(t.ops).toEqual([
      'OP_HOST · 120 satır · satırlarda trace kimliği yok · instance WMOBAPPP84 (pod adı değil)',
      'OP_MULTI · 50 satır · çok servisli operasyon · aday bsa-a-prod (4/10)',
      'OP_PODX · 9 satır · satırlarda trace kimliği yok · pod legacy-batch-7b9949bb74-l4bg5 (canlı bir servise çözülmedi)',
      '(boş) · 5 satır · operasyon kodu boş · host WMOBAPPP90',
    ]);
  });
  it('yarıdan azı bağlanıyorsa uyarı; canlı liste okunamadıysa alt sınır notu; satır yoksa tek cümle', () => {
    const low = coverageText({ ...base, rowsResolved: 420, aliveKnown: false });
    expect(low.warn).toBe(true);
    expect(low.headline).toContain('%42 kadarı');
    expect(low.lines.some(l => l.includes('oran alt sınırdır'))).toBe(true);
    const none = coverageText({ ...base, rowsTotal: 0, rowsListed: 0, rowsResolved: 0, unresolved: [], byReason: {} });
    expect(none).toEqual({ headline: 'Son 24 saatte bu kaynaktan satır yok.', lines: [], ops: [], warn: false });
    const all = coverageText({ ...base, rowsTotal: 1000, rowsResolved: 1000, unresolved: [], byReason: {} });
    expect(all.lines).toEqual([]);
    expect(all.headline).toContain('%100 kadarı');
  });
  // v0.10.1000 — fonksiyon kodu basamağı: kapalıyken "açılırsa ne kazandırır",
  // açıkken pay başlıkta; okuma yolu yoksa / ölçülemediyse nedeni yazar.
  it('fonksiyon kodu: kapalıyken potansiyel, açıkken başlıkta pay, yol yoksa neden', () => {
    const off = coverageText({ ...base, fnChecked: true, fnEnabled: false, fnSource: 'rollup', rowsFunctionCode: 150 });
    expect(off.headline).toBe(coverageText(base).headline);
    expect(off.lines).toContain('Fonksiyon kodu eşlemesi açılırsa 150 satır daha servise bağlanır (oran %70 → %85).');
    const zero = coverageText({ ...base, fnChecked: true, fnEnabled: false, fnSource: 'spans', rowsFunctionCode: 0 });
    expect(zero.lines.some(l => l.startsWith('Fonksiyon kodu eşlemesi açılsa da ek satır bağlanmıyor'))).toBe(true);
    const on = coverageText({ ...base, rowsResolved: 853, fnChecked: true, fnEnabled: true, fnSource: 'rollup', rowsFunctionCode: 150 });
    expect(on.headline).toBe('Son 24 saatte 1.000 satırın %85 kadarı bir servise bağlanıyor (853 satır: öğrenilmiş eşleme 400, pod adından 303, fonksiyon kodundan 150) · 3/9 operasyon.');
    expect(on.lines.some(l => l.includes('Fonksiyon kodu'))).toBe(false);
    const noPath = coverageText({ ...base, fnChecked: true, fnEnabled: true, fnSource: '', rowsFunctionCode: 0 });
    expect(noPath.headline).toBe(coverageText(base).headline);
    expect(noPath.lines).toContain('Fonksiyon kodu eşlemesi okunamıyor: geniş rollup tablosu da FUNCTION_CODE terfi kolonu da yok.');
    const failed = coverageText({ ...base, fnError: 'timeout' });
    expect(failed.lines).toContain('Fonksiyon kodu eşlemesi ölçülemedi: timeout');
    expect(coverageOpLine({ operation: 'DIGITAL_PAYMENT_EFT', rows: 300, withTrace: 0, status: 'unresolved', reason: 'no_trace_id', fnRows: 300, fnService: 'eft-svc' }))
      .toBe('DIGITAL_PAYMENT_EFT · 300 satır · satırlarda trace kimliği yok · fonksiyon kodundan eft-svc (300 satır)');
  });
  it('satır metni nedeni olmayan op\'ta da kırılmaz; sekme isteği yalnız açılınca atar', () => {
    expect(coverageOpLine({ operation: 'X', rows: 1, withTrace: 1, status: 'unresolved' })).toBe('X · 1 satır · çözülmedi');
    const tab = readFileSync(resolve(__dirname, 'OracleTab.tsx'), 'utf8');
    expect(tab).toContain("queryKey: ['oracle-subject-coverage', id], queryFn: () => api.oracleSubjectCoverage(id), staleTime: 60_000, enabled: open");
    expect(tab).toContain('{src.id && <OracleCoverageLine id={src.id} />}');
  });
});
