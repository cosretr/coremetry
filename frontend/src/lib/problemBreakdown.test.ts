// v0.10.2388 — "Hata nerede" bölümünün SAF görünüm işlevleri. Sözleşme: bölüm
// yalnız servis özneli hata oranı Problem'inde istenir; sayılar dile göre
// yazılır ve küçük ama sıfır olmayan değer sıfır diye okunmaz; deploy gerçeği
// satırın taşıdığı alandan gelir, sürüm sayısı üretilmez. Saat sabit (nowMs).
import { describe, it, expect } from 'vitest';
import {
  BREAKDOWN_CLOSED_HORIZON_MS, breakdownCapable, breakdownDeployFact, breakdownWindowMinutes,
  fmtBreakdownCount, fmtBreakdownRate, fmtDependencyValue, fmtPerMin, hasBreakdownBlocks,
} from './problemBreakdown';

const NOW = Date.UTC(2026, 10, 18, 21, 15, 40);
const base = { service: 'orders', metric: 'error_rate', kind: 'service', status: 'open' };

describe('breakdownCapable', () => {
  it.each([
    ['açık hata oranı Problem\'i', base, true],
    ['kind boş = servis', { ...base, kind: undefined }, true],
    ['gecikme Problem\'i', { ...base, metric: 'p99_ms' }, false],
    ['veritabanı öznesi', { ...base, kind: 'db' }, false],
    ['dış kaynak öznesi', { ...base, kind: 'external' }, false],
    ['servis yok', { ...base, service: '' }, false],
    ['bir saat önce kapanmış', { ...base, status: 'resolved', resolvedAt: (NOW - 3600e3) * 1e6 }, true],
    ['ufkun tam sınırında', { ...base, status: 'resolved', resolvedAt: (NOW - BREAKDOWN_CLOSED_HORIZON_MS) * 1e6 }, true],
    ['8 gün önce kapanmış', { ...base, status: 'resolved', resolvedAt: (NOW - 8 * 86400e3) * 1e6 }, false],
    ['kapanmış ama kapanış anı yok', { ...base, status: 'resolved' }, true],
  ])('%s', (_name, p, want) => {
    expect(breakdownCapable(p, NOW)).toBe(want);
  });
});

describe('hasBreakdownBlocks', () => {
  it('konu dışı ya da boş yanıt: blok yok', () => {
    expect(hasBreakdownBlocks(undefined)).toBe(false);
    expect(hasBreakdownBlocks({ problemId: 'p', applicable: false })).toBe(false);
    expect(hasBreakdownBlocks({ problemId: 'p', applicable: true })).toBe(false);
  });
  it('tek blok yeter; okunamayan blok da bloktur', () => {
    expect(hasBreakdownBlocks({ problemId: 'p', applicable: true, exceptions: { rows: [{ type: 'X', count: 1 }] } })).toBe(true);
    expect(hasBreakdownBlocks({ problemId: 'p', applicable: true, dependencies: { failed: true, rows: [] } })).toBe(true);
  });
});

describe('breakdownWindowMinutes', () => {
  it('ns farkı → dakika; pencere yoksa 0', () => {
    expect(breakdownWindowMinutes({ windowFrom: 1e18, windowTo: 1e18 + 12 * 60e9 })).toBe(12);
    expect(breakdownWindowMinutes({})).toBe(0);
    expect(breakdownWindowMinutes({ windowFrom: 5, windowTo: 5 })).toBe(0);
  });
});

describe('sayı biçimleri', () => {
  it('oran: Türkçe ondalık virgül ve önde %, İngilizce nokta ve sonda %', () => {
    expect(fmtBreakdownRate(0.182, 'tr')).toBe('%18');
    expect(fmtBreakdownRate(0.031, 'tr')).toBe('%3,1');
    expect(fmtBreakdownRate(0.003, 'tr')).toBe('%0,3');
    expect(fmtBreakdownRate(0.031, 'en')).toBe('3.1%');
    expect(fmtBreakdownRate(0, 'tr')).toBe('%0');
    expect(fmtBreakdownRate(undefined, 'tr')).toBe('—');
    expect(fmtBreakdownRate(Number.NaN, 'en')).toBe('—');
  });
  it('dakikada: küçük ama sıfır olmayan değer sıfır okunmaz', () => {
    expect(fmtPerMin(0, 'tr')).toBe('0');
    expect(fmtPerMin(0.02, 'tr')).toBe('<0,1');
    expect(fmtPerMin(0.02, 'en')).toBe('<0.1');
    expect(fmtPerMin(2.24, 'tr')).toBe('2,2');
    expect(fmtPerMin(2.24, 'en')).toBe('2.2');
    expect(fmtPerMin(9.96, 'tr')).toBe('10');
    expect(fmtPerMin(36.4, 'tr')).toBe('36');
    expect(fmtPerMin(2890, 'tr')).toBe('2.890');
    expect(fmtPerMin(2890, 'en')).toBe('2,890');
    expect(fmtPerMin(-1, 'tr')).toBe('—');
    expect(fmtPerMin(undefined, 'tr')).toBe('—');
  });
  it('kayıt sayısı', () => {
    expect(fmtBreakdownCount(11260, 'tr')).toBe('11.260');
    expect(fmtBreakdownCount(11260, 'en')).toBe('11,260');
    expect(fmtBreakdownCount(undefined, 'tr')).toBe('—');
  });
  it('bağımlılık değeri: hata oranı yüzde, gecikme ms / sn', () => {
    expect(fmtDependencyValue({ measure: 'error_rate' }, 0.31, 'tr')).toBe('%31');
    expect(fmtDependencyValue({ measure: 'error_rate' }, 0.002, 'tr')).toBe('%0,2');
    expect(fmtDependencyValue({ measure: 'latency' }, 40, 'tr')).toBe('40 ms');
    expect(fmtDependencyValue({ measure: 'latency' }, 1250, 'tr')).toBe('1,3 sn');
    expect(fmtDependencyValue({ measure: 'latency' }, 1250, 'en')).toBe('1.3 s');
    expect(fmtDependencyValue({ measure: 'latency' }, 12400, 'tr')).toBe('12 sn');
    expect(fmtDependencyValue({ measure: 'latency' }, Number.POSITIVE_INFINITY, 'tr')).toBe('—');
  });
});

describe('breakdownDeployFact', () => {
  it('satırın taşıdığı deploy → sürüm + dakika (en az 1)', () => {
    expect(breakdownDeployFact({ recentDeploy: { version: 'v2.14.1', timeUnixNs: 1, ageSeconds: 372 } })).toEqual({ version: 'v2.14.1', minutes: 6 });
    expect(breakdownDeployFact({ recentDeploy: { version: 'v2.14.1', timeUnixNs: 1, ageSeconds: 10 } })).toEqual({ version: 'v2.14.1', minutes: 1 });
  });
  it('deploy yok / sürüm boş / yaş bozuk → null', () => {
    expect(breakdownDeployFact({})).toBeNull();
    expect(breakdownDeployFact({ recentDeploy: { version: '', timeUnixNs: 1, ageSeconds: 60 } })).toBeNull();
    expect(breakdownDeployFact({ recentDeploy: { version: 'v1', timeUnixNs: 1, ageSeconds: -5 } })).toBeNull();
  });
});
