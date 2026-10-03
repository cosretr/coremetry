// detailSummary.test.ts — v0.10.1032.
//
// Operatör: "Anlaşılır olsun. Çok detay verince daha anlaşılır olmuyor —
// alert ve anomaliler de." Alarm kuralı ve anomali detayının ilk satırı tek
// Türkçe cümle + "ne zaman" satırı. Bu tablo her türü, her eksik-alan
// düşüşünü ve yukarı/aşağı yönü çiviler; hiçbir dalda "NaN", "0 katına",
// "undefined" ya da boş parantez basılmaz.
import { describe, it, expect } from 'vitest';
import type { AnomalyEvent, BehaviorChangeDetails, Problem } from '@/lib/types';
import {
  alertProblemSummary, anomalyRecurrence, anomalySummary, anomalyWhenLine, behaviorMetricLabel, detailWhenLine, fmtRatio,
  genericAnomalySentence, priorDeployText, problemWhenLine, promotedRecurrence, recurrenceClause, recurrenceTitle,
} from './detailSummary';

type Ev = Pick<AnomalyEvent, 'kind' | 'service' | 'pattern' | 'peakRatio'>;
const ev = (over: Partial<Ev> = {}): Ev => ({ kind: 'log_pattern', service: 'checkout', pattern: 'ORA-00060', peakRatio: 4.2, ...over });
const bd = (over: Partial<BehaviorChangeDetails> = {}): BehaviorChangeDetails => ({
  metric: 'p99_ms', signal: 'regime', direction: 'up', ratio: 2.4, z: 6.1, baseline: 120, current: 290,
  unit: 'ms', hourOfWeek: 33, dwell: 4, onsetNs: 1, ...over,
});

// Bozuk / eski satır: yön alanı boş gelebilir (tip birliğinin dışında).
const NO_DIR = '' as string as BehaviorChangeDetails['direction'];

const NEVER = /NaN|undefined|null|Infinity|\(\s*\)|(?<![\d.])0(\.0+)? katına| {2}/;

describe('anomalySummary — türe göre tek cümle', () => {
  it.each<[string, Ev, BehaviorChangeDetails | null, string]>([
    // ── davranış değişimi (kanıt ayrıştırıldı) ──
    ['davranış, yukarı', ev({ kind: 'behavior_change' }), bd(),
      'checkout servisinde p99 gecikme normalin 2.4 katına çıktı (120ms → 290ms).'],
    ['davranış, aşağı (oran < 1)', ev({ kind: 'behavior_change' }),
      bd({ metric: 'request_rate', direction: 'down', ratio: 0.35, baseline: 40, current: 14, unit: '/s' }),
      'checkout servisinde istek hızı normalin 0.35 katına düştü (40/s → 14/s).'],
    ['davranış, hata oranı, büyük oran tam sayı', ev({ kind: 'behavior_change' }),
      bd({ metric: 'error_rate', ratio: 12.6, baseline: 0.5, current: 6.31, unit: '%' }),
      'checkout servisinde hata oranı normalin 13 katına çıktı (0.5% → 6.31%).'],
    ['davranış, dış kaynak metriği', ev({ kind: 'behavior_change' }),
      bd({ metric: 'ext:oracle_sessions', ratio: 3, baseline: 10, current: 30, unit: '' }),
      'checkout servisinde oracle_sessions (dış kaynak) normalin 3 katına çıktı (10 → 30).'],
    ['davranış, oran yok → oransız', ev({ kind: 'behavior_change' }), bd({ ratio: Number.NaN }),
      'checkout servisinde p99 gecikme normalin üstüne çıktı (120ms → 290ms).'],
    ['davranış, oran yönle çelişiyor → oransız', ev({ kind: 'behavior_change' }), bd({ direction: 'down', ratio: 2 }),
      'checkout servisinde p99 gecikme normalin altına düştü (120ms → 290ms).'],
    ['davranış, değerler yok → parantez yok', ev({ kind: 'behavior_change' }),
      bd({ baseline: Number.NaN, current: Number.NaN }),
      'checkout servisinde p99 gecikme normalin 2.4 katına çıktı.'],
    ['davranış, yön yok → orandan', ev({ kind: 'behavior_change' }),
      bd({ direction: NO_DIR, ratio: 0.5, baseline: 10, current: 5, unit: 'ms' }),
      'checkout servisinde p99 gecikme normalin 0.5 katına düştü (10ms → 5ms).'],
    ['davranış, yön ve oran yok → genel', ev({ kind: 'behavior_change' }),
      bd({ direction: NO_DIR, ratio: 0 }),
      'checkout servisinde olağan dışı bir değişim saptandı.'],
    ['davranış, yuvarlanınca 0 kalan oran → oransız', ev({ kind: 'behavior_change' }),
      bd({ direction: 'down', ratio: 0.001, baseline: 900, current: 1, unit: '/s' }),
      'checkout servisinde p99 gecikme normalin altına düştü (900/s → 1/s).'],
    ['davranış, metrik boş → genel', ev({ kind: 'behavior_change' }), bd({ metric: '' }),
      'checkout servisinde olağan dışı bir değişim saptandı.'],
    ['davranış, kanıt ayrıştırılamadı → genel', ev({ kind: 'behavior_change' }), null,
      'checkout servisinde olağan dışı bir değişim saptandı.'],
    // ── trace ──
    ['trace_op', ev({ kind: 'trace_op', pattern: 'POST /pay', peakRatio: 4.24 }), null,
      'checkout servisinde POST /pay için hata normalin 4.2 katına çıktı.'],
    ['trace_op_latency', ev({ kind: 'trace_op_latency', pattern: 'GET /x', peakRatio: 3 }), null,
      'checkout servisinde GET /x için gecikme normalin 3 katına çıktı.'],
    ['trace, operasyon yok', ev({ kind: 'trace_op', pattern: '', peakRatio: 2.5 }), null,
      'checkout servisinde hata normalin 2.5 katına çıktı.'],
    ['trace, tepe 0 → genel', ev({ kind: 'trace_op', peakRatio: 0 }), null,
      'checkout servisinde olağan dışı bir değişim saptandı.'],
    ['trace, tepe NaN → genel', ev({ kind: 'trace_op_latency', peakRatio: Number.NaN }), null,
      'checkout servisinde olağan dışı bir değişim saptandı.'],
    ['trace, tepe < 1 ("katına çıktı" yalan olur) → genel', ev({ kind: 'trace_op', peakRatio: 0.8 }), null,
      'checkout servisinde olağan dışı bir değişim saptandı.'],
    // ── log ──
    ['log_pattern', ev({ kind: 'log_pattern', peakRatio: 4.2 }), null,
      'checkout loglarında bu desen normalin 4.2 katına çıktı.'],
    ['elastic_ml', ev({ kind: 'elastic_ml', peakRatio: 17.4 }), null,
      'checkout loglarında bu desen normalin 17 katına çıktı.'],
    ['log, tepe yok → genel', ev({ kind: 'log_pattern', peakRatio: 0 }), null,
      'checkout servisinde olağan dışı bir değişim saptandı.'],
    ['log_template_new (oranı yok, olmamalı)', ev({ kind: 'log_template_new', peakRatio: 0 }), null,
      'checkout loglarında daha önce görülmemiş bir satır biçimi çıktı.'],
    ['log_template_new, oran olsa da oransız', ev({ kind: 'log_template_new', peakRatio: 5 }), null,
      'checkout loglarında daha önce görülmemiş bir satır biçimi çıktı.'],
    // ── servis yok ──
    ['servissiz trace', ev({ kind: 'trace_op', service: '', pattern: 'POST /pay' }), null,
      'Bir serviste POST /pay için hata normalin 4.2 katına çıktı.'],
    ['servissiz log', ev({ kind: 'log_pattern', service: '' }), null,
      'Loglarda bu desen normalin 4.2 katına çıktı.'],
    ['servissiz genel', ev({ kind: 'trace_op', service: '', peakRatio: 0 }), null,
      'Bir serviste olağan dışı bir değişim saptandı.'],
  ])('%s', (_n, e, d, want) => {
    const got = anomalySummary(e, d);
    expect(got).toBe(want);
    expect(got).not.toMatch(NEVER);
  });

  it('bilinmeyen tür → genel cümle', () => {
    const e = { ...ev(), kind: 'mystery' } as unknown as Ev;
    expect(anomalySummary(e, null)).toBe(genericAnomalySentence('checkout'));
  });
});

describe('yardımcılar', () => {
  it.each([[0.35, '0.35'], [0.5, '0.5'], [0.05, '0.05'], [0.001, '0'], [1, '1'], [3, '3'], [4.24, '4.2'], [9.96, '10'], [10, '10'], [12.6, '13']])(
    'fmtRatio(%s) = %s', (r, want) => { expect(fmtRatio(r)).toBe(want); });
  it.each([['error_rate', 'hata oranı'], ['p99_ms', 'p99 gecikme'], ['request_rate', 'istek hızı'],
    ['ext:redis_mem', 'redis_mem (dış kaynak)'], ['cpu_custom', 'cpu_custom']])(
    'behaviorMetricLabel(%s) = %s', (m, want) => { expect(behaviorMetricLabel(m)).toBe(want); });
});

describe('alertProblemSummary — "<kural>: <metrik> değeri <değer>, eşik <eşik>."', () => {
  type P = Pick<Problem, 'ruleName' | 'metric' | 'value' | 'threshold' | 'service'>;
  const p = (over: Partial<P> = {}): P => ({ ruleName: 'High error rate', metric: 'error_rate', value: 12.4, threshold: 5, service: 'checkout', ...over });
  it.each<[string, P, string]>([
    ['tam', p(), 'High error rate — checkout: error_rate değeri 12.40, eşik 5.00.'],
    ['servis yok', p({ service: '' }), 'High error rate: error_rate değeri 12.40, eşik 5.00.'],
    ['db öznesi okunur etiketle', p({ service: 'db:oracle@core-scan.prod' }),
      'High error rate — oracle · core-scan.prod: error_rate değeri 12.40, eşik 5.00.'],
    ['eşik yok', p({ threshold: Number.NaN }), 'High error rate — checkout: error_rate değeri 12.40.'],
    ['değer yok', p({ value: Number.NaN }), 'High error rate — checkout: error_rate eşiği 5.00 aşıldı.'],
    ['ikisi de yok', p({ value: Number.NaN, threshold: Number.NaN }), 'High error rate — checkout: alarm tetiklendi.'],
    ['metrik yok', p({ metric: '' }), 'High error rate — checkout: alarm tetiklendi.'],
    ['kural adı yok → metrik', p({ ruleName: '' }), 'error_rate — checkout: error_rate değeri 12.40, eşik 5.00.'],
    ['kural ve metrik yok', p({ ruleName: ' ', metric: '' }), 'Alarm — checkout: alarm tetiklendi.'],
    ['sıfır değer geçerli bir sayıdır', p({ value: 0, threshold: 1 }), 'High error rate — checkout: error_rate değeri 0.00, eşik 1.00.'],
  ])('%s', (_n, prob, want) => {
    const got = alertProblemSummary(prob);
    expect(got).toBe(want);
    expect(got).not.toMatch(/NaN|undefined|null|—\.|\(\s*\)/);
  });

  // v0.10.1032 (inceleme) — anomaly: önekli kuralda ikinci sayı olağan / medyan
  // değer, eşik DEĞİL; "eşik" kelimesi hiçbir dalda geçmez.
  it.each<[string, P & { ruleId: string }, string]>([
    ['anomali kuralı, tam', { ...p({ ruleName: 'Error rate anomaly' }), ruleId: 'anomaly:error_rate' },
      'Error rate anomaly — checkout: error_rate değeri 12.40, olağan değer 5.00.'],
    ['anomali kuralı, olağan değer yok', { ...p({ threshold: Number.NaN }), ruleId: 'anomaly:p99' },
      'High error rate — checkout: error_rate değeri 12.40.'],
    ['anomali kuralı, değer yok', { ...p({ value: Number.NaN }), ruleId: 'anomaly:checkout:error_rate' },
      'High error rate — checkout: error_rate olağan dışı (olağan değer 5.00).'],
    ['anomali kuralı, ikisi de yok', { ...p({ value: Number.NaN, threshold: Number.NaN }), ruleId: 'anomaly:x' },
      'High error rate — checkout: error_rate olağan dışı.'],
    ['eşik kuralı aynen', { ...p(), ruleId: 'builtin:error_rate' },
      'High error rate — checkout: error_rate değeri 12.40, eşik 5.00.'],
    // v0.10.1055 — terfi Problem'i ANOMALY rozeti alır ama threshold'u gerçek
    // kapı (MinPeakRatio): "eşik" kalır; küme de (üye alt sınırı).
    ['terfi Problem\'i: eşik gerçek kapı', { ...p({ ruleName: 'Anomaly · nightly-job', metric: 'anomaly_ratio', value: 5, threshold: 3 }), ruleId: 'anomaly-auto:0123456789abcdef' },
      'Anomaly · nightly-job — checkout: anomaly_ratio değeri 5.00, eşik 3.00.'],
    ['küme Problem\'i: eşik üye alt sınırı', { ...p({ ruleName: 'Anomaly cluster · checkout', metric: 'cluster', value: 4, threshold: 2 }), ruleId: 'anomaly-cluster:checkout' },
      'Anomaly cluster · checkout — checkout: cluster değeri 4.00, eşik 2.00.'],
  ])('%s', (_n, prob, want) => {
    const got = alertProblemSummary(prob);
    expect(got).toBe(want);
    if (prob.ruleId.startsWith('anomaly:')) expect(got).not.toContain('eşik');
  });

  // v0.10.1083 — operatör: "Oracle hataları da problemse hâlâ düşmüyor". Dış
  // seride (`anomaly:ext:`) threshold = max(medyan, taban 5): medyan 0'lı
  // Oracle satırı "olağan değer 5.00" diyerek yalan söylerdi → "eşik".
  // ext-down / ext-cap ("anomaly:ext-…") zaten anomaly: ailesi; ext: serisi değil.
  it('Oracle hata serisi (medyan 0, taban eşik 5) → "eşik", "olağan değer" değil', () => {
    const got = alertProblemSummary({
      ruleName: 'Anomaly · error_count (external)', metric: 'ext:error_count', value: 12, threshold: 5,
      service: 'ext:oracle-errlog/OP_PAY/ORA-00060',
      ruleId: 'anomaly:ext:oracle-errlog/OP_PAY/ORA-00060:ext:error_count',
    });
    expect(got).toContain('ext:error_count değeri 12.00, eşik 5.00.');
    expect(got).not.toContain('olağan değer');
  });
});

describe('detailWhenLine — "<başlangıç> başladı · <süre> · sürüyor / <bitiş> bitti"', () => {
  const fmt = (ns: number) => `T${ns / 1e9}`;
  const clock = (ns: number) => `C${ns / 1e9}`;
  // Aynı gün: 2026-10-02 06:00 UTC ve 08:30 UTC; ertesi gün: +1 gün.
  const D0 = Date.UTC(2026, 9, 2, 6, 0) * 1e6;
  const D0b = Date.UTC(2026, 9, 2, 8, 30) * 1e6;
  const D1 = Date.UTC(2026, 9, 3, 6, 0) * 1e6;
  it.each<[string, { startedAt: number; durationNs: number; ongoing: boolean; endedAt?: number }, string]>([
    ['sürüyor', { startedAt: 100e9, durationNs: 12 * 60e9, ongoing: true }, 'T100 başladı · 12m · sürüyor'],
    ['bitti, bitiş bilinmiyor', { startedAt: 100e9, durationNs: 2.5 * 3600e9, ongoing: false }, 'T100 başladı · 2.5h · bitti'],
    ['süre 0 → basılmaz', { startedAt: 100e9, durationNs: 0, ongoing: true }, 'T100 başladı · sürüyor'],
    ['süre NaN → basılmaz', { startedAt: 100e9, durationNs: Number.NaN, ongoing: false }, 'T100 başladı · bitti'],
    ['başlangıç yok → yalnız durum', { startedAt: 0, durationNs: 60e9, ongoing: true }, 'sürüyor'],
    ['bitti, aynı gün → yalnız saat', { startedAt: D0, durationNs: D0b - D0, ongoing: false, endedAt: D0b },
      `T${D0 / 1e9} başladı · 2.5h · C${D0b / 1e9} bitti`],
    ['bitti, başka gün → tam tarih', { startedAt: D0, durationNs: D1 - D0, ongoing: false, endedAt: D1 },
      `T${D0 / 1e9} başladı · 24.0h · T${D1 / 1e9} bitti`],
    ['sürüyorsa bitiş yok sayılır', { startedAt: D0, durationNs: 60e9, ongoing: true, endedAt: D0b },
      `T${D0 / 1e9} başladı · 60s · sürüyor`],
    ['başlangıçtan önce biten (bozuk) → yalnız "bitti"', { startedAt: D0b, durationNs: 0, ongoing: false, endedAt: D0 },
      `T${D0b / 1e9} başladı · bitti`],
  ])('%s', (_n, w, want) => {
    expect(detailWhenLine(w, fmt, clock)).toBe(want);
  });
  it('varsayılan biçimciler gerçek tarih ve saat basar (tsMinute)', () => {
    const s = detailWhenLine({ startedAt: 1_700_000_000e9, durationNs: 60e9, ongoing: true });
    expect(s).toMatch(/^\d{2}\.\d{2}\.\d{4} \d{2}:\d{2} başladı · 60s · sürüyor$/);
    const t = detailWhenLine({ startedAt: D0, durationNs: D0b - D0, ongoing: false, endedAt: D0b });
    expect(t).toMatch(/^\d{2}\.\d{2}\.\d{4} \d{2}:\d{2} başladı · 2\.5h · \d{2}:\d{2} bitti$/);
  });
});

// v0.10.1049 — yinelenen anomali. Operatör: "Yinelenen anomali ayrımı: her
// gece tekrar eden bir anomali artık her seferinde 'yeni' görünüyor ve önceki
// deploy'a bağlanıyor." Detay sayfasının "ne zaman" satırına TEK ek; sayaç
// ≤ 1 / yoksa satır detailWhenLine'ınkiyle bayt bayt aynı.
describe('anomalyWhenLine — yinelenen olayda tek ek', () => {
  const fmt = (ns: number) => `T${ns / 1e9}`;
  const clock = (ns: number) => `C${ns / 1e9}`;
  type W = Parameters<typeof anomalyWhenLine>[0];
  const base: W = { startedAt: 100e9, lastSeen: 100e9 + 12 * 60e9, status: 'active' };
  // v0.10.1032'deki satır-içi kurulumun birebir kopyası (sayfa eskiden bunu yazıyordu).
  const before = (e: W) => detailWhenLine({
    startedAt: e.startedAt, durationNs: Math.max(0, e.lastSeen - e.startedAt), ongoing: e.status === 'active',
    endedAt: e.status === 'active' ? undefined : e.lastSeen,
  }, fmt, clock);

  it.each<[string, W]>([
    ['sayaç yok (eski satır / kolon yok)', base],
    ['sayaç 1', { ...base, episodeCount: 1, firstStartedAt: 100e9 }],
    ['sayaç 0', { ...base, episodeCount: 0 }],
    ['sayaç NaN', { ...base, episodeCount: Number.NaN }],
    ['bitti, sayaç 1', { ...base, status: 'cleared', episodeCount: 1 }],
  ])('%s → bugünkü metin, bayt bayt', (_n, e) => {
    const got = anomalyWhenLine(e, fmt, clock);
    expect(got).toBe(before(e));
    expect(got).not.toContain('yinelenen');
  });

  it('sayaç 3, ilk görülme biliniyor → "yinelenen · bu 3. kez · ilk kez <tarih>"', () => {
    expect(anomalyWhenLine({ ...base, episodeCount: 3, firstStartedAt: 40e9 }, fmt, clock))
      .toBe('T100 başladı · 12m · sürüyor · yinelenen · bu 3. kez · ilk kez T40');
  });
  it('sayaç 2, bitmiş olay → ek bitişten sonra', () => {
    expect(anomalyWhenLine({ ...base, status: 'cleared', episodeCount: 2, firstStartedAt: 40e9 }, fmt, clock))
      .toBe('T100 başladı · 12m · C820 bitti · yinelenen · bu 2. kez · ilk kez T40');
  });
  it('ilk görülme bilinmiyor (0) → tarih parçası düşer', () => {
    expect(anomalyWhenLine({ ...base, episodeCount: 2, firstStartedAt: 0 }, fmt, clock))
      .toBe('T100 başladı · 12m · sürüyor · yinelenen · bu 2. kez');
  });
  it('hiçbir dalda NaN / undefined basılmaz', () => {
    for (const e of [{ ...base, episodeCount: 5, firstStartedAt: Number.NaN }, { ...base, episodeCount: 2.7 }]) {
      expect(anomalyWhenLine(e, fmt, clock)).not.toMatch(NEVER);
    }
  });
});

describe('recurrence — satır işaretinin ipucu', () => {
  const fmt = (ns: number) => `T${ns / 1e9}`;
  it('yinelenmemiş → null / boş', () => {
    expect(anomalyRecurrence({})).toBeNull();
    expect(anomalyRecurrence({ episodeCount: 1, firstStartedAt: 5e9 })).toBeNull();
    expect(recurrenceClause(null)).toBe('');
    expect(recurrenceTitle(null)).toBe('');
  });
  it('sayı + ilk tarih + sayacın sınırı', () => {
    const r = anomalyRecurrence({ episodeCount: 4, firstStartedAt: 7e9 });
    expect(r).toEqual({ count: 4, firstNs: 7e9 });
    expect(recurrenceTitle(r, fmt)).toBe(
      'Yinelenen anomali: bu 4. kez, ilk kez T7. Sayaç kaydın ömrüyle sınırlı (son tetiklenmeden 30 gün sonra kayıt düşer).');
  });
});

// v0.10.1054 — anomaliden terfi eden Problem. Operatör: "Anomaliden terfi eden
// problem de 'yinelenen' kuralına uysun; bugün deploy'a hâlâ eski kurala göre
// bağlanıyor." Alarm detayının "ne zaman" satırı terfi Problem'i yineleniyorsa
// anomaliyle AYNI tek eki alır; diğer her Problem'de (alan gelse bile) metin
// sayfanın eski satır-içi kurulumuyla BAYT BAYT aynı.
describe('problemWhenLine — terfi Problem\'inde tek ek, diğerlerinde bayt bayt aynı', () => {
  const fmt = (ns: number) => `T${ns / 1e9}`;
  const clock = (ns: number) => `C${ns / 1e9}`;
  type P = Parameters<typeof problemWhenLine>[0];
  const PROMOTED = 'anomaly-auto:0123456789abcdef';
  const base: P = { ruleId: 'builtin:error_rate', startedAt: 100e9, status: 'open' };
  const end = 100e9 + 12 * 60e9;
  // ProblemDetail.tsx'in v0.10.1032'den beri satır-içi yazdığı kurulumun BİREBİR kopyası.
  const before = (p: P, endNs: number) => detailWhenLine({
    startedAt: p.startedAt, durationNs: endNs - p.startedAt,
    ongoing: p.status !== 'resolved', endedAt: p.resolvedAt,
  }, fmt, clock);

  it.each<[string, P, number]>([
    ['alarm kuralı', base, end],
    ['alarm kuralı, alan gelse bile (savunma)', { ...base, episodeCount: 3, firstStartedAt: 40e9 }, end],
    ['metrik dedektörü (anomaly:)', { ...base, ruleId: 'anomaly:checkout:p99_ms', episodeCount: 3, firstStartedAt: 40e9 }, end],
    ['küme (anomaly-cluster:)', { ...base, ruleId: 'anomaly-cluster:checkout', episodeCount: 3 }, end],
    ['exception', { ...base, ruleId: 'exception:shared-dependency' }, end],
    ['çözülmüş alarm', { ...base, status: 'resolved', resolvedAt: 100e9 + 90 * 60e9 }, 100e9 + 90 * 60e9],
    ['terfi, sayaç yok', { ...base, ruleId: PROMOTED }, end],
    ['terfi, sayaç 1', { ...base, ruleId: PROMOTED, episodeCount: 1, firstStartedAt: 100e9 }, end],
    ['ruleId boş', { ...base, ruleId: '' }, end],
  ])('%s → bugünkü metin, bayt bayt', (_n, p, endNs) => {
    const got = problemWhenLine(p, endNs, fmt, clock);
    expect(got).toBe(before(p, endNs));
    expect(got).not.toContain('yinelenen');
  });

  it('terfi, sayaç 3 → "yinelenen · bu 3. kez · ilk kez <tarih>" (anomali satırıyla aynı ek)', () => {
    expect(problemWhenLine({ ...base, ruleId: PROMOTED, episodeCount: 3, firstStartedAt: 40e9 }, end, fmt, clock))
      .toBe('T100 başladı · 12m · sürüyor · yinelenen · bu 3. kez · ilk kez T40');
  });
  it('terfi, çözülmüş, sayaç 2 → ek bitişten sonra; ilk görülme bilinmiyorsa tarih düşer', () => {
    expect(problemWhenLine({ ...base, ruleId: PROMOTED, status: 'resolved', resolvedAt: end, episodeCount: 2 }, end, fmt, clock))
      .toBe('T100 başladı · 12m · C820 bitti · yinelenen · bu 2. kez');
  });
  it('promotedRecurrence yalnız terfi önekinde', () => {
    expect(promotedRecurrence({ ruleId: PROMOTED, episodeCount: 4, firstStartedAt: 7e9 })).toEqual({ count: 4, firstNs: 7e9 });
    expect(promotedRecurrence({ ruleId: 'builtin:x', episodeCount: 4, firstStartedAt: 7e9 })).toBeNull();
    expect(promotedRecurrence({ ruleId: 'anomaly:x', episodeCount: 4 })).toBeNull();
    expect(promotedRecurrence({ ruleId: PROMOTED, episodeCount: 1 })).toBeNull();
    expect(promotedRecurrence({ ruleId: PROMOTED })).toBeNull();
  });
  it('hiçbir dalda NaN / undefined basılmaz', () => {
    for (const p of [{ ...base, ruleId: PROMOTED, episodeCount: 5, firstStartedAt: Number.NaN }, { ...base, ruleId: PROMOTED, episodeCount: 2.7 }]) {
      expect(problemWhenLine(p, end, fmt, clock)).not.toMatch(NEVER);
    }
  });
});

// v0.10.1054 — "hiçbir şey kaybolmaz". İnceleme senaryosu: gece işi 7 gece,
// deploy, 8. bölüm; kural deploy'u "olası neden" saymadı (recentDeploy yok) →
// deploy NÖTR priorDeploy'da: ipucuna ve "ne zaman" ekine renksiz girer. Sayaç
// ≤ 1 / priorDeploy yok → metin bugünküyle bayt bayt aynı.
describe('priorDeploy — bastırılan deploy nötr metinde kalır', () => {
  const fmt = (ns: number) => `T${ns / 1e9}`;
  const clock = (ns: number) => `C${ns / 1e9}`;
  const PROMOTED = 'anomaly-auto:0123456789abcdef';
  const prior = { version: 'v2.0.0', timeUnixNs: 90e9, ageSeconds: 600 };
  const end = 100e9 + 12 * 60e9;

  it('priorDeployText: sürüm + dakika (en az 1); bozuk girdi boş', () => {
    expect(priorDeployText(prior)).toBe('deploy v2.0.0 10 dk önce');
    expect(priorDeployText({ ...prior, ageSeconds: 20 })).toBe('deploy v2.0.0 1 dk önce');
    expect(priorDeployText(undefined)).toBe('');
    expect(priorDeployText({ ...prior, version: ' ' })).toBe('');
    expect(priorDeployText({ ...prior, ageSeconds: Number.NaN })).toBe('');
  });
  it('(b) alarm detayı: "… · yinelenen · bu 8. kez · ilk kez <tarih> · deploy v2.0.0 10 dk önce (öncesinde de görülüyordu)"', () => {
    expect(problemWhenLine({ ruleId: PROMOTED, startedAt: 100e9, status: 'open', episodeCount: 8, firstStartedAt: 40e9, priorDeploy: prior }, end, fmt, clock))
      .toBe('T100 başladı · 12m · sürüyor · yinelenen · bu 8. kez · ilk kez T40 · deploy v2.0.0 10 dk önce (öncesinde de görülüyordu)');
  });
  it('(b) anomali detayı: aynı ek', () => {
    expect(anomalyWhenLine({ startedAt: 100e9, lastSeen: end, status: 'active', episodeCount: 8, firstStartedAt: 40e9, priorDeploy: prior }, fmt, clock))
      .toBe('T100 başladı · 12m · sürüyor · yinelenen · bu 8. kez · ilk kez T40 · deploy v2.0.0 10 dk önce (öncesinde de görülüyordu)');
  });
  it('(b) satır ipucu: yeni satırda "deploy … — öncesinde de görülüyordu"', () => {
    expect(recurrenceTitle({ count: 8, firstNs: 40e9 }, fmt, prior)).toBe(
      'Yinelenen anomali: bu 8. kez, ilk kez T40. Sayaç kaydın ömrüyle sınırlı (son tetiklenmeden 30 gün sonra kayıt düşer).\n' +
      'deploy v2.0.0 10 dk önce — öncesinde de görülüyordu');
  });
  it('priorDeploy yok / sayaç ≤ 1 / terfi değil → bugünkü metin bayt bayt', () => {
    const r = { count: 3, firstNs: 40e9 };
    expect(recurrenceClause(r, fmt, undefined)).toBe(recurrenceClause(r, fmt));
    expect(recurrenceTitle(r, fmt, null)).toBe(recurrenceTitle(r, fmt));
    expect(recurrenceClause(null, fmt, prior)).toBe('');
    expect(recurrenceTitle(null, fmt, prior)).toBe('');
    const before = detailWhenLine({ startedAt: 100e9, durationNs: 12 * 60e9, ongoing: true }, fmt, clock);
    expect(problemWhenLine({ ruleId: PROMOTED, startedAt: 100e9, status: 'open', episodeCount: 1, priorDeploy: prior }, end, fmt, clock)).toBe(before);
    expect(problemWhenLine({ ruleId: 'builtin:error_rate', startedAt: 100e9, status: 'open', episodeCount: 8, priorDeploy: prior }, end, fmt, clock)).toBe(before);
    expect(anomalyWhenLine({ startedAt: 100e9, lastSeen: end, status: 'active', episodeCount: 1, priorDeploy: prior }, fmt, clock)).toBe(before);
  });
});
