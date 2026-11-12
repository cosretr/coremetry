// v0.10.2027 — /anomalies tek liste: ML grupları + eski anomaly_events aynı
// satır modelinde; iki kaynağa birlikte gruplama. Fikstür nötr.
import { describe, it, expect } from 'vitest';
import type { AnomalyEvent, MLEvent } from '@/lib/types';
import { isOpenRow, legacyEvidenceGroups, legacyHeadline, legacySignal, unifiedRows } from './unifiedRows';

const min = 60e9;
const T = 1_800_000_000e9;
const ae = (o: Partial<AnomalyEvent>): AnomalyEvent => ({
  id: 'a', kind: 'log_template_new', pattern: 'Batch job <*> failed', service: 'orders-batch',
  startedAt: T, lastSeen: T, peakRatio: 0, currentRatio: 0, currentCount: 3, sample: 'Batch job 7 failed', status: 'active', ...o,
} as AnomalyEvent);
const me = (o: Partial<MLEvent>): MLEvent => ({
  id: 'm', detector: 'error_rate', signal: 'log', stage: 'shadow', service: 'orders-batch',
  startedAt: T, lastSeen: T + 10 * min, windowSec: 300, baseline: 1, observed: 9, score: 6, direction: 'up', summary: 'orders-batch: hata logları arttı', ...o,
});

describe('unifiedRows', () => {
  it('aynı saniyede doğan iki yeni kalıp tek satır; satır sayısı cümlede', () => {
    const rows = unifiedRows([], [ae({ id: 'a1', currentCount: 3 }), ae({ id: 'a2', currentCount: 5, peakRatio: 1 })]);
    expect(rows).toHaveLength(1);
    expect(rows[0].legacy).toHaveLength(2);
    expect(rows[0].headline).toBe('orders-batch: yeni bir log kalıbı belirdi, 5 satır');
    expect(rows[0].signals).toEqual(['log']);
    expect(rows[0].href).toBe('/anomalies/a2');
  });
  it('aynı serviste ML olayı varsa eski olay onunla birleşir; cümle ML üyesinden', () => {
    const rows = unifiedRows([me({ id: 'm1' })], [ae({ id: 'a1', startedAt: T + 5 * min, lastSeen: T + 5 * min })]);
    expect(rows).toHaveLength(1);
    expect(rows[0].headline).toBe('orders-batch: hata logları arttı');
    expect(rows[0].href).toBe('/anomalies/ml/m1');
  });
  it('farklı servis ya da boşluk > teyit penceresi → ayrı satır', () => {
    expect(unifiedRows([], [ae({ id: 'a1' }), ae({ id: 'a2', service: 'payments' })])).toHaveLength(2);
    expect(unifiedRows([], [ae({ id: 'a1' }), ae({ id: 'a2', startedAt: T + 11 * min, lastSeen: T + 11 * min })])).toHaveLength(2);
  });
  it('sinyal türden: log_* → Log, trace_op* → Trace, davranış/özne → Metrik, elastic_ml → Log', () => {
    expect(legacySignal({ kind: 'log_pattern' })).toBe('log');
    expect(legacySignal({ kind: 'trace_op_latency' })).toBe('trace');
    expect(legacySignal({ kind: 'behavior_change' })).toBe('metric');
    expect(legacySignal({ kind: 'subject_red' })).toBe('metric');
    expect(legacySignal({ kind: 'elastic_ml' })).toBe('log');
  });
  it('sıra: süren önce, sonra yeni başlayan; biten ayrı', () => {
    const rows = unifiedRows([], [
      ae({ id: 'old', service: 's1', startedAt: T - 60 * min, lastSeen: T - 60 * min, status: 'cleared' }),
      ae({ id: 'a', service: 's2', startedAt: T - 30 * min, lastSeen: T - 30 * min }),
      ae({ id: 'b', service: 's3', startedAt: T, lastSeen: T }),
    ]);
    expect(rows.map(r => r.legacy[0].id)).toEqual(['b', 'a', 'old']);
    expect(rows.map(isOpenRow)).toEqual([true, true, false]);
  });
  it('legacyHeadline: kalıp dışındaki türlerde sayı eklenmez', () => {
    expect(legacyHeadline(ae({ kind: 'trace_op', pattern: 'POST /orders', currentCount: 40 }))).not.toContain('satır');
  });
});

// v0.10.2038 — operatör: aynı kalıp 100 ms arayla iki ayrı kanıt satırıydı.
describe('legacyEvidenceGroups', () => {
  it('aynı tür + kalıp tek satır; satırlar toplanır; örnek tekrar etmez, en çok 2', () => {
    const g = legacyEvidenceGroups([
      ae({ id: 'a1', currentCount: 2, sample: 'Batch job 7 failed' }),
      ae({ id: 'a2', currentCount: 1, sample: 'Batch job 7 failed' }),
      ae({ id: 'a3', currentCount: 0, sample: 'Batch job 9 failed' }),
      ae({ id: 'a4', currentCount: 1, sample: 'Batch job 11 failed' }),
    ]);
    expect(g).toHaveLength(1);
    expect(g[0]).toMatchObject({ ids: ['a1', 'a2', 'a3', 'a4'], pattern: 'Batch job <*> failed', text: '5 satır; örnek:', samples: ['Batch job 7 failed', 'Batch job 9 failed'], source: 'log kalıpları (Drain)' });
  });
  it('farklı kalıp ayrı satır; log dışı tür kendi cümlesiyle', () => {
    const g = legacyEvidenceGroups([ae({ id: 'a1' }), ae({ id: 'a2', pattern: 'Other <*>' }), ae({ id: 't', kind: 'trace_op', pattern: 'POST /orders', peakRatio: 3, sample: '' })]);
    expect(g.map(x => x.ids)).toEqual([['a1'], ['a2'], ['t']]);
    expect(g[2].pattern).toBeUndefined();
    expect(g[2].text.length).toBeGreaterThan(0);
  });
});
