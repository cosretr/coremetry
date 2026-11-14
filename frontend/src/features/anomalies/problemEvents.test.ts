// v0.10.2163 (Davis tarzı tek Problem D4) — olay çizelgesi metni.
import { describe, it, expect } from 'vitest';
import type { ProblemEvent } from '@/lib/types';
import { problemEventName, problemEventRatio, problemEventRaw, problemEventLines, splitEventLines, EVENT_LINES_SHOWN } from './problemEvents';

const ev = (over: Partial<ProblemEvent>): ProblemEvent => ({
  detector: 'svc_error_rate', startedAt: 1, lastSeen: 2, observed: 0.034, baseline: 0.01, direction: 'up', live: true, ...over,
});
const clock = (ns: number) => `t${ns}`;

describe('problemEventName', () => {
  it.each([
    [{ detector: 'svc_slowdown', ruleId: 'svc-slowdown:orders-api' }, 'Yaygın yavaşlama'],
    [{ detector: 'metric_anomaly', ruleId: 'anomaly:orders-api:error_rate' }, 'Hata oranı anomalisi'],
    [{ detector: 'metric_anomaly', ruleId: 'anomaly:orders-api:p99_ms' }, 'Gecikme (p99) anomalisi'],
    [{ detector: 'metric_anomaly', ruleId: 'anomaly:orders-api:request_rate' }, 'İstek hızı anomalisi'],
    [{ detector: 'metric_anomaly', ruleId: 'anomaly:orders-api:bilinmeyen' }, 'Metrik anomalisi'],
    [{ detector: 'metric_anomaly' }, 'Metrik anomalisi'],
    [{ detector: 'anomaly_promotion', ruleId: 'anomaly-auto:1f2e' }, 'Anomali terfisi'],
    [{ detector: 'svc_error_rate' }, 'Servis hata oranı (ML)'],
    [{ detector: 'svc_breadth' }, 'Yaygın yavaşlama (ML)'],
    [{ detector: 'yeni_dedektor' }, 'yeni_dedektor'],
  ])('%j → %s', (e, want) => {
    expect(problemEventName(e)).toBe(want);
  });
});

describe('problemEventRatio', () => {
  it('gözlenen / olağan; düşüşte tersi; Türkçe ondalık', () => {
    expect(problemEventRatio({ observed: 0.034, baseline: 0.01, direction: 'up' })).toBe('3,4×');
    expect(problemEventRatio({ observed: 9000, baseline: 300, direction: 'up' })).toBe('30×');
    expect(problemEventRatio({ observed: 2, baseline: 40, direction: 'down' })).toBe('20×');
    expect(problemEventRatio({ observed: 90000, baseline: 300, direction: 'up' })).toBe('300×');
  });
  it('olağan değer yoksa / oran anlamsızsa oran YAZILMAZ', () => {
    expect(problemEventRatio({ observed: 12, baseline: 0, direction: 'up' })).toBeNull();
    expect(problemEventRatio({ observed: 0, baseline: 40, direction: 'down' })).toBeNull();
    expect(problemEventRatio({ observed: 0, baseline: 0, direction: 'up' })).toBeNull();
    expect(problemEventRatio({ observed: 0, baseline: 5, direction: 'up' })).toBeNull();
  });
});

describe('problemEventRaw', () => {
  it('ham değerler Türkçe ondalıkla; olağan yoksa söylenir; değer yoksa null', () => {
    expect(problemEventRaw({ observed: 0.034, baseline: 0.01 })).toBe('gözlenen 0,034 · olağan 0,01');
    expect(problemEventRaw({ observed: 9000, baseline: 300 })).toBe('gözlenen 9.000 · olağan 300');
    expect(problemEventRaw({ observed: 42, baseline: 0 })).toBe('gözlenen 42 · olağan değer yok');
    expect(problemEventRaw({ observed: 0, baseline: 0 })).toBeNull();
  });
});

describe('problemEventLines', () => {
  it('saat + ad + oran; sahip "(açan)", biten "(bitti)", ML olayı detay bağlantılı', () => {
    const lines = problemEventLines([
      ev({ detector: 'svc_slowdown', ruleId: 'svc-slowdown:orders-api', own: true, startedAt: 10, observed: 9000, baseline: 300 }),
      ev({ detector: 'svc_error_rate', eventId: 'ml 1', startedAt: 20 }),
      ev({ detector: 'metric_anomaly', ruleId: 'anomaly:orders-api:p99_ms', startedAt: 30, observed: 900, baseline: 0, live: false }),
    ], clock);
    expect(lines.map(l => l.text)).toEqual([
      't10 Yaygın yavaşlama (açan) 30×',
      't20 Servis hata oranı (ML) 3,4×',
      't30 Gecikme (p99) anomalisi (bitti)',
    ]);
    expect(lines.map(l => l.href)).toEqual([null, '/anomalies/ml/ml%201', null]);
    expect(lines.map(l => l.live)).toEqual([true, true, false]);
    expect(lines.map(l => l.raw)).toEqual(['gözlenen 9.000 · olağan 300', 'gözlenen 0,034 · olağan 0,01', 'gözlenen 900 · olağan değer yok']);
    expect(new Set(lines.map(l => l.key)).size).toBe(3);
  });
  it('boş / tanımsız girdi → satır yok (çizelge gizli)', () => {
    expect(problemEventLines(undefined, clock)).toEqual([]);
    expect(problemEventLines([], clock)).toEqual([]);
  });
});

describe('splitEventLines', () => {
  it(`ilk ${EVENT_LINES_SHOWN} satır + kalan sayısı`, () => {
    const lines = problemEventLines(Array.from({ length: 8 }, (_, i) => ev({ startedAt: i })), clock);
    const { head, rest } = splitEventLines(lines);
    expect(head).toHaveLength(EVENT_LINES_SHOWN);
    expect(rest).toBe(3);
    expect(splitEventLines(lines.slice(0, 2))).toEqual({ head: lines.slice(0, 2), rest: 0 });
  });
});
