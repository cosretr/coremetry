// serviceRolloutLabel.test.ts — v0.10.984 sözleşmesi (serviceRolloutLabel.ts başlığı).
import { describe, it, expect } from 'vitest';
import { rolloutMarkerLabel, rolloutMarkerDescription, rolloutHeadline, rolloutCounts, rolloutExplainVersion, isKsmRollout, rolloutRowKey } from './serviceRolloutLabel';
import type { Rollout } from './types';

const churn: Rollout = { timeUnixNs: 1, kind: 'deploy', podsAdded: 3, podsRemoved: 3, activePods: 3, versionBefore: '1.0', versionAfter: '1.1' };
const ksm: Rollout = {
  timeUnixNs: 2, kind: 'deploy', podsAdded: 2, podsRemoved: 0, activePods: 1, versionBefore: '1.1', versionAfter: '1.2',
  source: 'ksm', workloadKind: 'StatefulSet', workload: 'pg', namespace: 'db', status: 'in_progress', specReplicas: 3, updatedReplicas: 2,
};

describe('serviceRolloutLabel', () => {
  it('pod-churn satırı bugünkü metinler (v1 değişmez)', () => {
    expect(isKsmRollout(churn)).toBe(false);
    expect(rolloutMarkerLabel(churn)).toBe('↻ 3p');
    expect(rolloutMarkerDescription(churn)).toBe('rollout · 3 pods replaced (+3) · 1.0→1.1');
    expect(rolloutHeadline(churn)).toBe('↻ 3 pods replaced');
    expect(rolloutHeadline({ ...churn, podsRemoved: 1 })).toBe('↻ 1 pod replaced');
    expect(rolloutCounts(churn)).toEqual({ text: '+3/−3', title: '3 new · 3 retired · 3 now active' });
    expect(rolloutExplainVersion({ ...churn, versionAfter: '' })).toBe('rollout (3 pods)');
    expect(rolloutExplainVersion({ ...churn, versionAfter: '', kind: 'restart' })).toBe('restart (3 pods)');
  });
  it('KSM satırı: iş yükü + durum + replika; "0 pods replaced" basılmaz', () => {
    expect(isKsmRollout(ksm)).toBe(true);
    expect(rolloutMarkerLabel(ksm)).toBe('↻ pg');
    expect(rolloutMarkerDescription(ksm)).toBe('rollout · StatefulSet pg · sürüyor · 2/3 replika (KSM) · 1.1→1.2');
    expect(rolloutHeadline(ksm)).toBe('↻ StatefulSet pg · sürüyor');
    expect(rolloutCounts(ksm).text).toBe('2/3');
    expect(rolloutCounts(ksm).title).toContain('1 hazır');
    for (const s of [rolloutMarkerDescription(ksm), rolloutHeadline(ksm), rolloutCounts(ksm).text]) expect(s).not.toMatch(/replaced|−0/);
    expect(rolloutExplainVersion({ ...ksm, versionAfter: '' })).toBe('rollout (StatefulSet pg)');
    expect(rolloutHeadline({ ...ksm, workloadKind: undefined, workload: undefined })).toBe('↻ Deployment ? · sürüyor');
  });
  it('satır anahtarı: aynı KSM scrape zamanlı iki iş yükü ayrışır; pod-churn zamanla kalır (İnceleme)', () => {
    expect(rolloutRowKey(churn)).toBe('1');
    const a = { ...ksm, cluster: 'prod', workloadKind: 'Deployment', workload: 'api' };
    const b = { ...a, workload: 'worker' };
    expect(rolloutRowKey(a)).not.toBe(rolloutRowKey(b));
    expect(rolloutRowKey({ ...a, cluster: 'dr' })).not.toBe(rolloutRowKey(a));
    expect(rolloutRowKey(a)).toBe('2|prod|db|Deployment|api');
  });
});
