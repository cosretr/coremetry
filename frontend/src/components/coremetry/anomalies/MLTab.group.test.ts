// MLTab — v0.10.2013 (anomali tekilleştirme D2): "Grup" sütunu. Yalnız yeni
// alan; tek üyeli satır "—", çok üyeli satır kaç dedektörün birlikte
// tetiklendiğini söyler, adlar ipucunda.
import { describe, it, expect } from 'vitest';
import type { MLEvent } from '@/lib/types';
import { mlGroupLabel } from './MLTab';

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
  it('aynı dedektörün birden çok olayı', () => {
    expect(mlGroupLabel({ ...base, groupId: 'g', groupSize: 2, groupDetectors: ['op_latency'] }).text).toBe('2 olay birlikte');
  });
});
