import { describe, it, expect } from 'vitest';
import { encodeRolloutParam, decodeRolloutParam } from './rolloutRow';

// rolloutHref.test.ts — v0.10.203 çekmece URL codec'i (audit §12 Faz 4).
describe('rollout param codec', () => {
  const id = { clusterId: 'c-2db91847', namespace: 'demo', workload: 'api-gateway', revision: 'api-gateway-mv4nq', startedAt: 1788119700000 };
  it('gidiş-dönüş', () => {
    expect(decodeRolloutParam(encodeRolloutParam(id))).toEqual(id);
  });
  it('özel karakterler (~ / % boşluk) kimliği bozmaz', () => {
    const odd = { ...id, workload: 'a~b c/d%e', namespace: '' };
    expect(decodeRolloutParam(encodeRolloutParam(odd))).toEqual(odd);
  });
  it('bozuk token → null', () => {
    for (const bad of [null, '', 'a|b', 'a|b|c|d|notanumber', 'a|b|c|d|0', '||||5', 'a|b|c|d|5|f']) {
      expect(decodeRolloutParam(bad)).toBeNull();
    }
  });
});

// v0.10.984 — Rollouts v2 P2.3 (docs/rollouts/v2-audit.md §2.4, karar 14):
// 6 parçalı anahtar (rollout_events) + parça sayısıyla ayıran çözücü; 5
// parçalı eski bağlantılar aynen çözülür.
describe('rollout param codec — 6 parça (v2)', () => {
  const v2 = { clusterId: 'c-2db91847', namespace: 'demo', kind: 'StatefulSet', workload: 'pg|main', incarnationAt: 1788119640000, generation: 12 };
  it('gidiş-dönüş (ayraç içeren ad dahil)', () => {
    const enc = encodeRolloutParam(v2);
    expect(enc.split('|')).toHaveLength(6);
    expect(decodeRolloutParam(enc)).toEqual(v2);
  });
  it('v2 satırı (generation + incarnationAt + kind) 6 parçaya, v1 satırı 5 parçaya kodlanır', () => {
    const row = { clusterId: 'c', namespace: 'n', workload: 'w', revision: 'w-1', startedAt: 5, kind: 'Deployment', generation: 3, incarnationAt: 60000 };
    expect(decodeRolloutParam(encodeRolloutParam(row))).toEqual({ clusterId: 'c', namespace: 'n', kind: 'Deployment', workload: 'w', incarnationAt: 60000, generation: 3 });
    const v1row = { ...row, generation: undefined, incarnationAt: undefined };
    expect(decodeRolloutParam(encodeRolloutParam(v1row))).toEqual({ clusterId: 'c', namespace: 'n', workload: 'w', revision: 'w-1', startedAt: 5 });
  });
  it('eski 5 parçalı bağlantı hâlâ v1 kimliği', () => {
    const dec = decodeRolloutParam('c-1|pay|api|api-7f|1700000000000');
    expect(dec).toEqual({ clusterId: 'c-1', namespace: 'pay', workload: 'api', revision: 'api-7f', startedAt: 1700000000000 });
    expect(dec && 'generation' in dec).toBe(false);
  });
  it('bozuk 6 parça → null', () => {
    for (const bad of ['a|b|c|d|5|0', 'a|b|c|d|0|5', 'a||c|d|5|5', 'a|b||d|5|5', 'a|b|c||5|5', 'a|b|c|d|5.5|5', 'a|b|c|d|-5|5', 'a|b|c|d|5|1e3', 'a|b|c|d|5|5|x']) {
      expect(decodeRolloutParam(bad)).toBeNull();
    }
  });
});
