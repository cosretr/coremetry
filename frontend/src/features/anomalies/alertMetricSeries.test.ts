// alertMetricSeries — v0.10.1064 (operatör, prod alarm problemi detayı:
// "grafik olmadığı için de anlamak çok zor artışları"). Grafiği olan
// problem türleri, sorulan pencere, birim, eşik etiketi, başlangıç bölgesi,
// çizim durumu — tablo testleri.
import { describe, it, expect } from 'vitest';
import type { AlertRuleSeries, Problem } from '@/lib/types';
import {
  alertMetricUnit, alertSeriesArgs, alertSeriesState, alertSeriesToSpan, alertSeriesXRange, alertThreshold,
  hasAlertMetricChart, isAlertSeriesMetric, problemRegion, windowLabel,
} from './alertMetricSeries';

const MIN = 60e9;
const HOUR = 60 * MIN;
const T0 = 1_791_015_960 * 1e9;

const base: Pick<Problem, 'ruleId' | 'metric' | 'service' | 'kind' | 'threshold'> = {
  ruleId: 'builtin-warn-http-p99-3s', metric: 'http_p99_ms', service: 'checkout-svc', threshold: 3000,
};

describe('hasAlertMetricChart — grafiği olan türler', () => {
  const cases: [string, Partial<Problem>, boolean][] = [
    ['yerleşik HTTP p99 kuralı (sürdürülen)', {}, true],
    ['yerleşik error_rate kuralı', { ruleId: 'builtin-error-rate-15pct', metric: 'error_rate' }, true],
    ['kullanıcı kuralı (onaltılık id) request_rate', { ruleId: '3f9a1c0d7b2e4a10', metric: 'request_rate' }, true],
    ['kullanıcı kuralı db_p99_ms', { ruleId: '3f9a1c0d7b2e4a10', metric: 'db_p99_ms' }, true],
    ['mq_consume_p99_ms', { ruleId: 'builtin-mq-consume-p99-2m', metric: 'mq_consume_p99_ms' }, true],
    ['http_5xx_rate', { ruleId: 'abc123', metric: 'http_5xx_rate' }, true],
    ['kind=service açık', { kind: 'service' }, true],
    ['anomali problemi (anomaly:)', { ruleId: 'anomaly:checkout-svc:p99_ms', metric: 'p99_ms' }, false],
    ['terfi anomalisi', { ruleId: 'anomaly-auto:abcd', metric: 'p99_ms' }, false],
    ['SLO yanması', { ruleId: 'slo:checkout:critical', metric: 'error_rate' }, false],
    ['runtime pod', { ruleId: 'runtime:jvm-gc:checkout-svc:pod-1', metric: 'p99_ms' }, false],
    ['self-health', { ruleId: 'self-volume-spike', metric: 'request_rate' }, false],
    ['exception fırtınası', { ruleId: 'exception-storm', metric: 'error_count' }, false],
    ['yavaş SQL', { ruleId: 'db-slow-stmt', metric: 'p99_ms' }, false],
    ['log sorgusu kuralı', { ruleId: 'abc123', metric: 'log_query' }, false],
    ['watcher kuralı', { ruleId: 'abc123', metric: 'watcher' }, false],
    ['hedefli DB ifadesi', { ruleId: 'abc123', metric: 'db_stmt_p95_ms' }, false],
    ['hedefli route', { ruleId: 'abc123', metric: 'http_route_p99_ms' }, false],
    ['Kafka istemcisi', { ruleId: 'abc123', metric: 'kafka_lag_max' }, false],
    ['db öznesi', { kind: 'db', service: 'db:oracle@core-db-01' }, false],
    // v0.10.1073 — db-health: grafik kapsamı iddia etmez (kural id ':' taşır
    // VE özne db) — iki kapı da tek başına kapatır.
    ['db-health (db öznesi)', { ruleId: 'db-health:oracle@db-host-01/crm-db', metric: 'db.error_pct', kind: 'db', service: 'db:oracle@crm-db', threshold: 5 }, false],
    ['db-health, kind boş gelse bile', { ruleId: 'db-health:oracle@db-host-01/crm-db', metric: 'db_p99_ms', threshold: 2000 }, false],
    ['servis yok', { service: '' }, false],
    ['eşik sayı değil', { threshold: Number.NaN }, false],
  ];
  for (const [name, over, want] of cases) {
    it(name, () => { expect(hasAlertMetricChart({ ...base, ...over })).toBe(want); });
  }
  it('metrik kümesi değerlendiricinin span yoluyla aynı', () => {
    for (const ok of ['p50_ms', 'avg_ms', 'error_count', 'http_4xx_rate', 'http_error_rate', 'rpc_error_rate', 'db_count', 'mq_publish_avg_ms'])
      expect(isAlertSeriesMetric(ok)).toBe(true);
    for (const bad of ['', 'p90_ms', 'bogus', 'http_route_rate', 'db_stmt_max_ms', 'kafka_producer_error_rate', 'cpu_util'])
      expect(isAlertSeriesMetric(bad)).toBe(false);
  });
});

describe('alertSeriesArgs — sorulan pencere', () => {
  const p = { ruleId: 'r1', service: 'checkout-svc', metric: 'http_p99_ms', startedAt: T0 + 17e9 };
  const cases: [string, Partial<Problem>, number, number | null][] = [
    ['açık: başlangıç − 1 sa (dakikaya aşağı), to yok', { status: 'open' }, T0 - HOUR, null],
    ['onaylanmış ama açık: canlı', { status: 'acknowledged' }, T0 - HOUR, null],
    ['kısa bitmiş: 1 sa önce, kapanış + 10 dk (yukarı)', { status: 'resolved', resolvedAt: T0 + 17e9 + 20 * MIN }, T0 - HOUR, T0 + 31 * MIN],
    ['uzun bitmiş (3 sa): süresi kadar önce', { status: 'resolved', resolvedAt: T0 + 17e9 + 3 * HOUR }, T0 - 3 * HOUR, T0 + 3 * HOUR + 11 * MIN],
    ['çok uzun bitmiş (10 sa): en çok 6 sa önce', { status: 'resolved', resolvedAt: T0 + 17e9 + 10 * HOUR }, T0 - 6 * HOUR, T0 + 10 * HOUR + 11 * MIN],
  ];
  for (const [name, over, fromNs, toNs] of cases) {
    it(name, () => {
      expect(alertSeriesArgs({ ...p, status: 'open', ...over }))
        .toEqual({ ruleId: 'r1', service: 'checkout-svc', metric: 'http_p99_ms', fromNs, toNs });
    });
  }
});

describe('birim / eşik / pencere etiketi', () => {
  it.each([
    ['http_p99_ms', 'ms'], ['avg_ms', 'ms'], ['error_rate', 'percent'], ['http_5xx_rate', 'percent'],
    ['request_rate', 'reqps'], ['error_count', 'short'], ['db_count', 'short'],
  ])('%s → %s', (metric, unit) => { expect(alertMetricUnit(metric)).toBe(unit); });

  it.each([
    [{ threshold: 3000, comparator: '>', severity: 'warning', metric: 'http_p99_ms' }, '> 3000 ms', 'var(--warn)'],
    [{ threshold: 0.01, comparator: '<', severity: 'critical', metric: 'request_rate' }, '< 0.01/s', 'var(--err)'],
    [{ threshold: 5, comparator: undefined, severity: 'warning', metric: 'error_rate' }, '> 5%', 'var(--warn)'],
    [{ threshold: 100, comparator: '>=', severity: 'info', metric: 'error_count' }, '>= 100', 'var(--warn)'],
    [{ threshold: 2.3456, comparator: '>', severity: 'warning', metric: 'avg_ms' }, '> 2.35 ms', 'var(--warn)'],
  ] as const)('%o → %s', (p, label, color) => {
    expect(alertThreshold(p as Pick<Problem, 'threshold' | 'comparator' | 'severity' | 'metric'>))
      .toEqual({ value: p.threshold, label, color });
  });

  it.each([[600, '10 dk pencere'], [3600, '1 sa pencere'], [60, '1 dk pencere'], [90, '90 sn pencere'], [0, '']])(
    '%d → %s', (sec, want) => { expect(windowLabel(sec)).toBe(want); });
});

describe('çizim', () => {
  const s: AlertRuleSeries = {
    metric: 'http_p99_ms', service: 'checkout-svc', windowSec: 600, stepSec: 300,
    from: T0 - HOUR, to: T0 + 5 * MIN,
    points: [{ t: T0 - 55 * MIN, v: 800 }, { t: T0 - 50 * MIN, v: null }, { t: T0 + 2 * MIN, v: 3900 }],
  };
  it('null nokta NaN (boşluk), sıfır değil', () => {
    expect(alertSeriesToSpan(s)[0].points.map(p => p.value)).toEqual([800, Number.NaN, 3900]);
  });
  it('x ekseni: hizalı baş → son nokta; nokta yoksa to', () => {
    expect(alertSeriesXRange(s)).toEqual({ from: (T0 - HOUR) / 1e9, to: (T0 + 2 * MIN) / 1e9 });
    expect(alertSeriesXRange({ ...s, points: [] })).toEqual({ from: (T0 - HOUR) / 1e9, to: (T0 + 5 * MIN) / 1e9 });
  });
  it('başlangıç bölgesi: açıkta grafiğin sağ ucuna, bitmişte kapanışa (endSec)', () => {
    expect(problemRegion({ startedAt: T0, status: 'open' }, (T0 + 2 * MIN) / 1e9))
      .toEqual({ fromSec: T0 / 1e9, toSec: (T0 + 2 * MIN) / 1e9, color: 'var(--err)', label: 'başladı' });
    expect(problemRegion({ startedAt: T0, resolvedAt: T0 + 20 * MIN, status: 'resolved' }, (T0 + 30 * MIN) / 1e9))
      .toEqual({ fromSec: T0 / 1e9, toSec: (T0 + 20 * MIN) / 1e9, endSec: (T0 + 20 * MIN) / 1e9, color: 'var(--err)', label: 'başladı' });
  });
  it.each([
    ['hata boşluğu kapsar', { isPending: false, isError: true, data: undefined }, 'error'],
    ['404 → gone', { isPending: false, isError: false, data: null }, 'gone'],
    ['yükleniyor', { isPending: true, isError: false, data: undefined }, 'loading'],
    ['hepsi null → empty', { isPending: false, isError: false, data: { ...s, points: [{ t: 1, v: null }] } }, 'empty'],
    ['nokta yok → empty', { isPending: false, isError: false, data: { ...s, points: [] } }, 'empty'],
    ['değer var → ready', { isPending: false, isError: false, data: s }, 'ready'],
    ['0 değer de veridir', { isPending: false, isError: false, data: { ...s, points: [{ t: 1, v: 0 }] } }, 'ready'],
  ] as const)('%s', (_n, q, want) => {
    expect(alertSeriesState(q as Parameters<typeof alertSeriesState>[0])).toBe(want);
  });
});
