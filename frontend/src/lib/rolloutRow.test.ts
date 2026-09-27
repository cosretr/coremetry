// rolloutRow.test.ts — v0.10.201 sözleşmesi (rolloutRow.ts başlığı).
import { describe, it, expect } from 'vitest';
import { rolloutKey, upsertRollouts, statusTone, rolloutDurationSec, shortRevision, imageDiff, rolloutTracesFilters, rolloutEvidenceHref, decodeRolloutParam, rolloutPlaceLabel } from './rolloutRow';
import type { WorkloadRollout } from './types';

const base: WorkloadRollout = {
  clusterId: 'c-1', namespace: 'pay', workload: 'api', kind: 'Deployment', revision: 'api-7fb7dffckb',
  startedAt: 1_700_000_000_000, status: 'in_progress', prevRevision: 'api-6c9d', image: 'reg/api', imageTag: 'release.2', prevImage: 'reg/api', prevImageTag: 'release.1',
  firstSpanAt: 0, trafficConfirmedAt: 0, ksmStartedAt: 0, podsReadyAt: 0, ksmNotReadySince: 0, completedAt: 0,
  detectedBy: 'spans', spanCount: 10, note: '', updatedAt: 1_700_000_100_000,
};

describe('rolloutRow', () => {
  it('kimlik beş parçadan; startedAt farkı ayrı satır', () => {
    expect(rolloutKey(base)).toBe('c-1|pay|api|api-7fb7dffckb|1700000000000');
    expect(rolloutKey({ ...base, startedAt: 1 })).not.toBe(rolloutKey(base));
  });
  it('upsert: yalnız daha yeni updatedAt ezer; yeni kimlik eklenir; eski sıra korunur', () => {
    const older = { ...base, status: 'completed', updatedAt: base.updatedAt - 1 };
    const newer = { ...base, status: 'completed', updatedAt: base.updatedAt + 1 };
    const other = { ...base, revision: 'api-zzzz', updatedAt: 5 };
    let rows = upsertRollouts([base], [older]);
    expect(rows[0].status).toBe('in_progress');
    rows = upsertRollouts(rows, [newer, other]);
    expect(rows[0].status).toBe('completed');
    expect(rows).toHaveLength(2);
    expect(rows[1].revision).toBe('api-zzzz');
    expect(upsertRollouts(rows, [])).toBe(rows);
  });
  it('durum tonu + süre + kısa revizyon + imaj diff', () => {
    expect(statusTone('completed')).toBe('success');
    expect(statusTone('rolled_back')).toBe('danger');
    expect(statusTone('stalled')).toBe('warning');
    expect(statusTone('x')).toBe('neutral');
    expect(rolloutDurationSec(base, base.startedAt + 90_000)).toBe(90);
    expect(rolloutDurationSec({ ...base, completedAt: base.startedAt + 30_000 }, base.startedAt + 90_000)).toBe(30);
    expect(shortRevision('api-7fb7dffckb', 'api')).toBe('7fb7dffckb');
    expect(shortRevision('7fb7dffckb', 'api')).toBe('7fb7dffckb');
    expect(imageDiff(base)).toBe('release.1 → release.2');
    expect(imageDiff({ imageTag: 'r1', prevImageTag: 'r1' })).toBe('r1');
    expect(imageDiff({ imageTag: '', prevImageTag: '' })).toBe('—');
  });
});

// v0.10.211 — STS/DS revizyonu imaj tag'idir: replicaset filtresi yerine
// workload + imaj tag'i. Deployment eski sözleşmede kalır.
describe('rolloutTracesFilters', () => {
  it('Deployment → replicaset + namespace', () => {
    const f = rolloutTracesFilters({ kind: 'Deployment', namespace: 'demo', workload: 'api', revision: 'api-7fb7dffckb' });
    expect(f).toEqual([
      { k: 'resource.k8s.replicaset.name', op: '=', v: ['api-7fb7dffckb'] },
      { k: 'resource.k8s.namespace.name', op: '=', v: ['demo'] },
    ]);
  });
  it('StatefulSet/DaemonSet → workload + imaj tag + namespace', () => {
    const f = rolloutTracesFilters({ kind: 'StatefulSet', namespace: 'db', workload: 'pg', revision: 'release.20260807.1' });
    expect(f[0]).toEqual({ k: 'resource.k8s.statefulset.name', op: '=', v: ['pg'] });
    expect(f[1]).toEqual({ k: 'resource.container.image.tag', op: '=', v: ['release.20260807.1'] });
    const d = rolloutTracesFilters({ kind: 'DaemonSet', namespace: 'sys', workload: 'agent', revision: 't2' });
    expect(d[0].k).toBe('resource.k8s.daemonset.name');
  });
  it("bilinmeyen/boş tür Deployment gibi davranır (eski satırlar)", () => {
    expect(rolloutTracesFilters({ kind: '', namespace: 'n', workload: 'w', revision: 'w-abc' })[0].k).toBe('resource.k8s.replicaset.name');
  });
  // İnceleme (v0.10.984): v2 STS/DS revizyonu controller revizyonu — tag imageTag'den.
  const v2 = { generation: 4, incarnationAt: 1_700_000_000_000 };
  it('v2 StatefulSet/DaemonSet → imaj tag imageTag\'den, revizyondan değil', () => {
    const f = rolloutTracesFilters({ kind: 'StatefulSet', namespace: 'db', workload: 'web', revision: 'web-7d9f8c6b5', imageTag: '1.4.2', ...v2 });
    expect(f).toEqual([
      { k: 'resource.k8s.statefulset.name', op: '=', v: ['web'] },
      { k: 'resource.container.image.tag', op: '=', v: ['1.4.2'] },
      { k: 'resource.k8s.namespace.name', op: '=', v: ['db'] },
    ]);
    const d = rolloutTracesFilters({ kind: 'DaemonSet', namespace: 'sys', workload: 'agent', revision: '5c7f9d8b4', imageTag: 't3', ...v2 });
    expect(d[1]).toEqual({ k: 'resource.container.image.tag', op: '=', v: ['t3'] });
    expect(JSON.stringify(d)).not.toContain('5c7f9d8b4');
  });
  it('v2 STS tag yoksa → yalnız iş yükü + namespace (boş tag süzgeci yok)', () => {
    const f = rolloutTracesFilters({ kind: 'StatefulSet', namespace: 'db', workload: 'web', revision: 'web-7d9f8c6b5', imageTag: '', ...v2 });
    expect(f).toEqual([
      { k: 'resource.k8s.statefulset.name', op: '=', v: ['web'] },
      { k: 'resource.k8s.namespace.name', op: '=', v: ['db'] },
    ]);
  });
  it('revizyonu boş Deployment → deployment adı + namespace (replicaset.name=\'\' değil)', () => {
    expect(rolloutTracesFilters({ kind: 'Deployment', namespace: 'pay', workload: 'api', revision: '', ...v2 })).toEqual([
      { k: 'resource.k8s.deployment.name', op: '=', v: ['api'] },
      { k: 'resource.k8s.namespace.name', op: '=', v: ['pay'] },
    ]);
  });
  it('v2 Deployment revizyonu RS adı → v1 gibi replicaset', () => {
    expect(rolloutTracesFilters({ kind: 'Deployment', namespace: 'pay', workload: 'api', revision: 'api-8a', imageTag: '2.0', ...v2 })[0])
      .toEqual({ k: 'resource.k8s.replicaset.name', op: '=', v: ['api-8a'] });
  });
});

// v0.10.234 — Operator-reported: çekmecede "hangi sürümden hangisine" görünmüyordu,
// "devralındı" olayın türünü söylemiyordu. Değişiklik türü imaj kimliğinden.
import { rolloutChangeKind, changeKindLabel, statusTitle, imageRef } from './rolloutRow';
describe('rolloutChangeKind', () => {
  const base = { prevRevision: 'w-aaa', image: 'reg/app', imageTag: '2.0', prevImage: 'reg/app', prevImageTag: '1.0' };
  it('imaj tag değişti → deployment', () => {
    expect(rolloutChangeKind(base)).toBe('deployment');
    expect(changeKindLabel('deployment')).toBe('Deployment');
  });
  it('imaj repo değişti, tag aynı → deployment', () => {
    expect(rolloutChangeKind({ ...base, prevImage: 'reg/other', imageTag: '1.0' })).toBe('deployment');
  });
  it('imaj aynı, revizyon farklı → config rollout', () => {
    expect(rolloutChangeKind({ ...base, imageTag: '1.0' })).toBe('config');
    expect(changeKindLabel('config')).toBe('Rollout (config)');
  });
  it('önceki revizyon yok → ilk gözlem (imaj olsa da)', () => {
    expect(rolloutChangeKind({ ...base, prevRevision: '' })).toBe('initial');
  });
  it('imaj bilgisi bir tarafta yok → bilinmiyor', () => {
    expect(rolloutChangeKind({ ...base, prevImage: '', prevImageTag: '' })).toBe('unknown');
    expect(rolloutChangeKind({ ...base, image: '', imageTag: '', prevImage: '', prevImageTag: '' })).toBe('unknown');
  });
  it('durum ipuçları her durumda dolu, bilinmeyen boş', () => {
    for (const st of ['in_progress', 'completed', 'rolled_back', 'superseded', 'stalled']) expect(statusTitle(st)).not.toBe('');
    expect(statusTitle('weird')).toBe('');
  });
  it('imageRef', () => {
    expect(imageRef('reg/app', '1.0')).toBe('reg/app:1.0');
    expect(imageRef('reg/app', '')).toBe('reg/app');
    expect(imageRef('', '')).toBe('—');
  });
});

// v0.10.984 — Rollouts v2 P2.3: v2 satırı kimliği 6 parça, tür change_type'tan,
// durum ipucu KSM anlamıyla; kanıt v2 anahtarı taşırsa 6 parçalı bağlantı.
import { isV2Rollout, replicaSummary, changeKindTone } from './rolloutRow';
describe('v2 satırı (v0.10.984)', () => {
  const v2row: WorkloadRollout = { ...base, revision: '', detectedBy: 'ksm', generation: 7, incarnationAt: 1_699_999_940_000, changeType: 'rollback',
    specReplicas: 3, updatedReplicas: 2, availableReplicas: 1 };
  it('kimlik 6 parça; v1 satırı eski 5 parça', () => {
    expect(isV2Rollout(v2row)).toBe(true);
    expect(isV2Rollout(base)).toBe(false);
    expect(rolloutKey(v2row)).toBe('c-1|pay|Deployment|api|1699999940000|7');
    expect(rolloutKey(base)).toBe('c-1|pay|api|api-7fb7dffckb|1700000000000');
    // aynı iş yükünün iki nesli ayrı satır; upsert v2 kimliğiyle çalışır
    const next = { ...v2row, generation: 8, updatedAt: 5 };
    expect(upsertRollouts([v2row], [next])).toHaveLength(2);
  });
  it("tür change_type'tan (imaj kıyası yapılmaz)", () => {
    expect(rolloutChangeKind(v2row)).toBe('rollback');
    expect(changeKindLabel('rollback')).toBe('Rollback');
    expect(changeKindTone('rollback')).toBe('warning');
    expect(rolloutChangeKind({ ...v2row, changeType: 'rollout' })).toBe('deployment');
    expect(rolloutChangeKind({ ...v2row, changeType: 'config' })).toBe('config');
    expect(rolloutChangeKind({ ...v2row, changeType: 'initial' })).toBe('initial');
  });
  it('durum ipucu v2 anlamıyla; replika özeti yalnız v2', () => {
    for (const st of ['in_progress', 'completed', 'rolled_back', 'superseded', 'stalled']) {
      expect(statusTitle(st, true)).not.toBe('');
      expect(statusTitle(st, true)).not.toBe(statusTitle(st));
    }
    expect(replicaSummary(v2row)).toBe('2/1/3');
    expect(replicaSummary(base)).toBeNull();
  });
  it('kanıt v2 anahtarı taşırsa 6 parçalı bağlantı (ns → ms)', () => {
    const href = rolloutEvidenceHref({ clusterId: 'c-1', namespace: 'pay', workload: 'api', revision: '', startedAtNs: 1, workloadKind: 'Deployment', generation: 7, incarnationAtNs: 1699999940000_000000 });
    expect(decodeRolloutParam(href.slice('/rollouts?rollout='.length))).toEqual({ clusterId: 'c-1', namespace: 'pay', kind: 'Deployment', workload: 'api', incarnationAt: 1699999940000, generation: 7 });
  });
});

describe('rolloutEvidenceHref (v0.10.243)', () => {
  it('ns → ms sınırı: çekmece parametresi ms, decode ile geri döner', () => {
    const href = rolloutEvidenceHref({ clusterId: 'c-1', namespace: 'pay', workload: 'api', revision: 'api-7fb7dffckb', startedAtNs: 1700000000123456789 });
    expect(href.startsWith('/rollouts?rollout=')).toBe(true);
    const dec = decodeRolloutParam(href.slice('/rollouts?rollout='.length));
    expect(dec).toEqual({ clusterId: 'c-1', namespace: 'pay', workload: 'api', revision: 'api-7fb7dffckb', startedAt: 1700000000123 });
  });
});

// v0.10.338 — Operator-reported: çekmecede cluster yoktu (aynı workload iki
// cluster'da). Yer satırı ad çözülünce adı, çözülmeyince ID'yi taşır; asla boş.
describe('rolloutPlaceLabel (v0.10.338)', () => {
  it('küme adı · namespace · tür', () => {
    expect(rolloutPlaceLabel(base, 'ocp-prod-a')).toBe('ocp-prod-a · pay · Deployment');
  });
  it('ad çözülemezse cluster ID; tür yoksa Deployment; namespace yoksa ?', () => {
    expect(rolloutPlaceLabel(base)).toBe('c-1 · pay · Deployment');
    expect(rolloutPlaceLabel({ clusterId: '', namespace: '', kind: '' })).toBe('? · ? · Deployment');
    expect(rolloutPlaceLabel({ clusterId: 'c-2', namespace: 'x', kind: 'StatefulSet' }, '  ')).toBe('c-2 · x · StatefulSet');
  });
});

