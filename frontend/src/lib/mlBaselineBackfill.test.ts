// v0.10.1815 — baseline geçmiş doldurma metinleri + yoklama kararı.
import { describe, it, expect } from 'vitest';
import { backfillEstimateText, backfillProgress, backfillSkippedLines, durationTR } from './mlBaselineBackfill';
import { backfillActive } from './queries/ml';
import type { MLBaselineBackfillView } from './types';

const plan = { days: 56, signals: ['log', 'trace'], es: 1344, vm: 0, ch: 1, intervalMs: 2000, timeoutMs: 30000, estimatedSec: 4032,
  perSignal: { log: { days: 56, es: 1344, vm: 0 }, trace: { days: 56, es: 0, vm: 0, skipped: 'VictoriaMetrics yapılandırılmamış' } }, notCovered: [] };

describe('mlBaselineBackfill', () => {
  it('tahmin cümlesi ES/VM sayısı ve süreyi söyler', () => {
    expect(backfillEstimateText(plan)).toContain('yaklaşık 1.344 ES sorgusu, 0 VM sorgusu');
    expect(backfillEstimateText(plan)).toContain('tahmini süre 1,1 sa');
    expect(backfillSkippedLines(plan)).toEqual(['Trace: VictoriaMetrics yapılandırılmamış']);
    expect(durationTR(45)).toBe('45 sn');
    expect(durationTR(720)).toBe('12 dk');
  });
  it('ilerleme: sinyal başına gün X/Y, satır, hata', () => {
    const v: MLBaselineBackfillView = {
      request: { id: 'b1', days: 3, signals: ['log'], at: 1 },
      status: { id: 'b1', days: 3, signals: ['log'], planDays: [], startedAt: 1, updatedAt: 2, running: true, complete: false, cancelled: false,
        perSignal: { log: { daysTotal: 3, daysDone: 1, rows: 1200, errors: 2, lastError: 'es 500' } } },
    };
    const p = backfillProgress(v);
    expect(p?.head).toBe('Sürüyor');
    expect(p?.lines[0]).toEqual({ signal: 'log', text: 'Log · gün 1/3 · 1.200 satır · 2 hata (son: es 500)', warn: true });
    expect(backfillActive(v)).toBe(true);
    expect(backfillActive({ ...v, status: { ...v.status!, complete: true, running: false } })).toBe(false);
    expect(backfillActive({ ...v, request: { ...v.request!, cancel: true } })).toBe(false);
    expect(backfillProgress({ request: null, status: null })).toBeNull();
    expect(backfillProgress({ request: { id: 'b2', days: 1, signals: ['log'], at: 1 }, status: v.status })?.head).toContain('Sırada');
  });
});
