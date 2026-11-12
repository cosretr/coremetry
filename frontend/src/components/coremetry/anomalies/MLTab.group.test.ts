// MLTab — v0.10.2013 (anomali tekilleştirme D2): "Grup" sütunu. Yalnız yeni
// alan; tek üyeli satır "—", çok üyeli satır kaç dedektörün birlikte
// tetiklendiğini söyler, adlar ipucunda.
import { describe, it, expect } from 'vitest';
import type { MLEvent } from '@/lib/types';
import { mlGroupLabel, tickLabel, ML_DETECTOR_TR, detectorGroup, thanosTickText } from './MLTab';
import { signalKindOf } from '@/lib/signalKind';

// v0.10.2018 — T-err: yeni trace dedektörü adı, türü ve tik özeti grubu.
describe('trace_error_count', () => {
  it('Türkçe ad, trace türü, trace_errors tik grubu', () => {
    expect(ML_DETECTOR_TR.trace_error_count).toBe('Operasyon hata sayısı');
    expect(signalKindOf('trace_error_count')).toBe('trace');
    expect(detectorGroup('trace_error_count')).toBe('trace_errors');
  });
});

const base: MLEvent = {
  id: 'e1', detector: 'op_latency', signal: 'trace', stage: 'shadow', service: 'checkout',
  startedAt: 0, lastSeen: 0, windowSec: 300, baseline: 1, observed: 2, score: 5, direction: 'up',
};

describe('mlGroupLabel', () => {
  it('grupsuz / tek üyeli satır boş', () => {
    expect(mlGroupLabel(base).text).toBe('');
    expect(mlGroupLabel({ ...base, groupId: 'g', groupSize: 1, groupDetectors: ['op_latency'] }).text).toBe('');
  });
  it('çok dedektör: sayı + Türkçe adlar ipucunda', () => {
    const g = mlGroupLabel({ ...base, groupId: 'g', groupSize: 3, groupDetectors: ['dependency_latency', 'op_latency', 'trace_change_point'] });
    expect(g.text).toBe('3 dedektör birlikte');
    expect(g.title).toContain('Bağımlılık gecikmesi');
    expect(g.title).toContain('Kırılma (trace)');
  });
  it('v0.10.2015 D3 — gölge karar: açardı + öncelik; açmazdı yalnız ipucunda', () => {
    const g = { ...base, groupId: 'g', groupSize: 2, groupDetectors: ['op_latency', 'dependency_latency'] };
    const open = mlGroupLabel({ ...g, groupShadow: { group: 'g', wouldOpen: true, rule: 'anomaly-auto:group:g', priority: 'P2', reason: 'açardı: 2 dedektör', detectors: [], at: 1 } });
    expect(open.text).toBe('2 dedektör birlikte · açardı (P2)');
    expect(open.title).toContain('Problem açılmadı');
    const no = mlGroupLabel({ ...g, groupShadow: { group: 'g', wouldOpen: false, rule: 'r', reason: 'açmazdı: takvim dilimi tanımlı değil', detectors: [], at: 1 } });
    expect(no.text).toBe('2 dedektör birlikte');
    expect(no.title).toContain('takvim');
  });
  it('tik satırı gölge sayacını söyler', () => {
    const t = { at: 100, durationMs: 1000, events: 3, backoff: [], shadowGroups: 4, shadowWouldOpen: 1 };
    expect(tickLabel(t, 100)).toContain('gölge terfi: 4 grup, 1 Problem açardı (açılmadı)');
  });
  it('aynı dedektörün birden çok olayı', () => {
    expect(mlGroupLabel({ ...base, groupId: 'g', groupSize: 2, groupDetectors: ['op_latency'] }).text).toBe('2 olay birlikte');
  });
});

// v0.10.2019 — tikin Thanos ölçümü ayrı sayaçla görünür.
describe('thanosTickText', () => {
  it('sorgu, süre, hata, tavan, eşlenemeyen, yanıtsız cluster', () => {
    const s = thanosTickText({ queries: 14, ms: 5300, maxMs: 2100, errors: 1, budgetCut: 2, unmatched: 40, failed: ['prod-west'] });
    expect(s).toBe('Thanos 14 sorgu · 5.3 sn (en yavaş 2.1 sn) · 1 hata/kısmi · 2 ifade tavan dışı · 40 pod servise eşlenemedi · yanıtsız: prod-west');
    expect(tickLabel({ at: 1, durationMs: 1, events: 0, backoff: [], thanos: { queries: 3, ms: 100, maxMs: 50, errors: 0 } }, 1)).toContain('Thanos 3 sorgu');
  });
});
