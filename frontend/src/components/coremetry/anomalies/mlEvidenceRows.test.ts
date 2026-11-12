// v0.10.2044 — operatör: kanıt başlık cümlesini tekrar ediyordu; aynı
// dedektörün iki olayı "2 sinyal" sayılıyordu. Panel tepesi: Messaging lag
// paneli (en kötü partition = dedektörün max'ı). Fikstür nötr.
import { describe, it, expect } from 'vitest';
import type { MLEvent } from '@/lib/types';
import { evidenceRows, evidenceSub } from './mlEvidenceRows';
import { peakInRange } from './mlSourcePanel';

const T = Date.UTC(2026, 10, 12, 16, 23) * 1e6;
const ev = (o: Partial<MLEvent>): MLEvent => ({
  id: 'e', detector: 'log_volume', signal: 'log', stage: 'shadow', service: 'orders-api', startedAt: T, lastSeen: T + 300e9,
  windowSec: 300, baseline: 40, observed: 880, score: 9, direction: 'up', summary: 'orders-api: log hacmi dakikada ~880 satıra çıktı (normalde ~40)', ...o,
});

describe('evidenceRows', () => {
  it('aynı dedektörün olayları tek satır; başlıkla aynı cümle yerine Ne kadar', () => {
    const ms = [ev({ id: 'a', score: 9 }), ev({ id: 'b', score: 4, startedAt: T + 57 * 60e9, summary: 'orders-api: log hacmi dakikada ~300 satıra çıktı (normalde ~40)' })];
    const rows = evidenceRows(ms, 'Log hacmi dakikada ~880 satıra çıktı (normalde ~40)', 'Son 5 dakikada 4.400 satır.', 'orders-api');
    expect(rows).toHaveLength(1);
    expect(rows[0].text).toBe(`Son 5 dakikada 4.400 satır. (+1 olay daha: ${new Date((T + 57 * 60e9) / 1e6).getHours().toString().padStart(2, '0')}:20)`);
    expect(evidenceSub(ms)).toBeUndefined();
  });
  it('farklı tür → sinyal türü sayısı; aynı tür farklı dedektör → dedektör sayısı', () => {
    expect(evidenceSub([ev({}), ev({ id: 't', detector: 'op_latency', signal: 'trace' })])).toBe('2 sinyal türü aynı olayda');
    expect(evidenceSub([ev({}), ev({ id: 'r', detector: 'error_rate' })])).toBe('2 dedektör aynı olayda');
  });
  it('başlıktan farklı cümle aynen (öneksiz)', () => {
    const rows = evidenceRows([ev({ id: 't', detector: 'op_latency', signal: 'trace', summary: 'orders-api: POST /orders yavaşladı' })], 'başka cümle', 'x', 'orders-api');
    expect(rows[0].text).toBe('POST /orders yavaşladı');
  });
});

describe('peakInRange', () => {
  const s = (vals: [number, number][], k = 'c1') => ({ groupKey: [k], points: vals.map(([time, value]) => ({ time, value })) });
  it('her an serilerin en büyüğü, aralıktaki en büyük; düşüşte en küçük; aralık dışı sayılmaz', () => {
    const series = [s([[1, 100], [2, 2800], [3, 50], [9, 9999]]), s([[1, 300], [2, 10]], 'c2')];
    expect(peakInRange(series, 1, 3)).toBe(2800);
    expect(peakInRange(series, 1, 3, 'down')).toBe(50);
    expect(peakInRange(series, 4, 5)).toBeNull();
  });
});
