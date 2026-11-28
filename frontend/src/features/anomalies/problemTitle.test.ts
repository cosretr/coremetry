// problemTitle / problemSummary — v0.10.2487 (entity tutarlılığı dilim 3):
// liste satırı ve detay AYNI girdiden AYNI cümleyi üretir; kural kimliği
// cümleye girmez.
import { describe, it, expect } from 'vitest';
import type { ProblemStandard } from '@/lib/types';
import { problemTitle, problemSummary } from './problemTitle';

const STD: ProblemStandard = {
  family: 'error', kind: 'svc_slowdown', subject: 'orders',
  headline: 'orders: yanıt süresi normalde 120 ms, şimdi 900 ms',
  impact: { primary: { text: 'x', value: 0.9, normal: 0.12 }, known: true },
  since: { startedAt: 0, durationSec: 600, ongoing: true },
  evidence: { sentence: '10 dakikadır eşiğin üstünde.', source: 'vm', sampled: false },
  confidence: { level: 'high', gates: [] },
  nextStep: { text: 'Yavaş operasyonlara bak' },
};

describe('problemTitle', () => {
  it('standart varsa: özne + gövde, tek ayırıcı; liste ve detay girdisi aynı cümle', () => {
    const list = problemTitle({ std: STD, ruleName: 'svc-slowdown', service: 'orders', description: 'başka cümle' }, 'tr');
    const detail = problemTitle({ std: STD, ruleName: 'svc-slowdown', ruleId: 'svc-slowdown:orders', metric: 'p99_ms', value: 900, threshold: 500, service: 'orders' }, 'tr');
    expect(list).toEqual(detail);
    expect(list).toEqual({ subject: 'orders', rest: 'yanıt süresi normalde 120 ms, şimdi 900 ms', text: 'orders · yanıt süresi normalde 120 ms, şimdi 900 ms', orig: false });
  });

  it('standart yoksa: ölçü biliniyorsa ölçü cümlesi, kural kimliği yok', () => {
    const r = problemTitle({ ruleName: 'High latency', ruleId: 'builtin:p99', metric: 'p99_ms', value: 812, threshold: 500, service: 'svc-a' }, 'tr');
    expect(r.subject).toBe('');
    expect(r.text).toContain('High latency');
    expect(r.text).not.toContain('builtin:');
  });

  it('standart ve ölçü yoksa: olayın düz cümlesi (yaşam döngüsü eki kırpılır)', () => {
    expect(problemTitle({ ruleName: 'r', description: 'Kuyruk birikti · auto-resolved: sakin' }, 'tr').text).toBe('Kuyruk birikti');
  });
});

describe('problemSummary', () => {
  it('standardın kanıt cümlesi; standart yoksa null', () => {
    expect(problemSummary({ std: STD }, 'tr')).toEqual({ text: '10 dakikadır eşiğin üstünde.', orig: false });
    expect(problemSummary({ ruleName: 'x' }, 'tr')).toBeNull();
  });
});
