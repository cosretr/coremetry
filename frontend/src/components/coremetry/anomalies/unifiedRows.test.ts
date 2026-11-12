// v0.10.2027 — /anomalies tek liste: ML grupları + eski anomaly_events aynı
// satır modelinde. Fikstür nötr.
import { describe, it, expect } from 'vitest';
import type { AnomalyEvent, MLEvent, UnifiedAnomalyRow } from '@/lib/types';
import { legacyEvidenceGroups, legacyHeadline, legacySignal, listHeadline, rowHeadline, rowHref } from './unifiedRows';

const T = 1_800_000_000e9;
const ae = (o: Partial<AnomalyEvent>): AnomalyEvent => ({
  id: 'a', kind: 'log_template_new', pattern: 'Batch job <*> failed', service: 'orders-batch',
  startedAt: T, lastSeen: T, peakRatio: 0, currentRatio: 0, currentCount: 3, sample: 'Batch job 7 failed', status: 'active', ...o,
} as AnomalyEvent);
const me = (o: Partial<MLEvent>): MLEvent => ({
  id: 'm', detector: 'error_rate', signal: 'log', stage: 'shadow', service: 'orders-batch',
  startedAt: T, lastSeen: T + 600e9, windowSec: 300, baseline: 1, observed: 9, score: 6, direction: 'up', summary: 'orders-batch: hata logları arttı', ...o,
});

// v0.10.2051 — birleştirme (gruplama, durum, sıra) sunucuda; aynı durumlar
// internal/ml/unified_test.go'da. Burada satırın cümlesi ve bağlantısı:
// lider ML üyesiyse sunucu özeti, değilse eski olayın anlatımı.
const row = (o: Partial<UnifiedAnomalyRow>): UnifiedAnomalyRow => ({
  key: 'k', service: 'orders-batch', start: T, end: T, signals: ['log'], status: 'watching', mlCount: 0, legacyCount: 0, ...o,
});

describe('satır cümlesi + bağlantı', () => {
  it('eski olay lideri: yeni kalıpta satır sayısı cümlede', () => {
    const r = row({ legacyLead: ae({ id: 'a2', currentCount: 5, peakRatio: 1 }), legacyCount: 2 });
    expect(rowHeadline(r)).toBe('orders-batch: yeni bir log kalıbı belirdi, 5 satır');
    expect(rowHref(r)).toBe('/anomalies/a2');
    expect(listHeadline(r)).toBe('Yeni bir log kalıbı belirdi, 5 satır');
  });
  it('ML lideri: cümle sunucu özetinden, bağlantı ML detayına', () => {
    const r = row({ mlLead: me({ id: 'm1' }), mlCount: 1, legacyLead: ae({ id: 'a1' }), legacyCount: 1 });
    expect(rowHeadline(r)).toBe('orders-batch: hata logları arttı');
    expect(rowHref(r)).toBe('/anomalies/ml/m1');
  });
  it('sinyal türden: log_* → Log, trace_op* → Trace, davranış/özne → Metrik, elastic_ml → Log', () => {
    expect(legacySignal({ kind: 'log_pattern' })).toBe('log');
    expect(legacySignal({ kind: 'trace_op_latency' })).toBe('trace');
    expect(legacySignal({ kind: 'behavior_change' })).toBe('metric');
    expect(legacySignal({ kind: 'subject_red' })).toBe('metric');
    expect(legacySignal({ kind: 'elastic_ml' })).toBe('log');
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
