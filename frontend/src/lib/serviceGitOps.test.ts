import { describe, it, expect } from 'vitest';
import { appsForWorkload, syncTone, healthTone, matchLabel, syncsSummary, syncsTotal, syncsFailed, repoShort, autoSyncLabel, argoSourceBadge, argoSourceMeta } from './serviceGitOps';
import type { ArgoServiceApp } from './types';

// v0.10.981 — servis GitOps sekmesi yardımcıları.

const app = (name: string, workloads: ArgoServiceApp['workloads'], extra: Partial<ArgoServiceApp> = {}): ArgoServiceApp =>
  ({ hubClusterId: 'hub', appNamespace: 'apps', name, match: 'name', confidence: 70, workloads, syncs24h: null, ...extra });

describe('appsForWorkload', () => {
  const w = { clusterId: 'c-a', namespace: 'pay', workload: 'checkout' };
  const apps = [
    app('pin', [w], { match: 'manual', confidence: 100 }),
    app('other-cluster', [{ ...w, clusterId: 'c-b' }]),
    app('both', [{ ...w, clusterId: 'c-b' }, w]),
  ];
  it('iş yükünün üç alanı da eşleşmeli; sunucu sırası korunur', () => {
    expect(appsForWorkload(apps, w).map(a => a.name)).toEqual(['pin', 'both']);
    expect(appsForWorkload(apps, { ...w, namespace: 'x' })).toEqual([]);
  });
});

describe('tonlar', () => {
  it('sync', () => {
    expect(syncTone('Synced')).toBe('success');
    expect(syncTone('OutOfSync')).toBe('warning');
    expect(syncTone('Unknown')).toBe('neutral');
    expect(syncTone(undefined)).toBe('neutral');
  });
  it('health', () => {
    expect(healthTone('Healthy')).toBe('success');
    expect(healthTone('Progressing')).toBe('info');
    expect(healthTone('Degraded')).toBe('danger');
    expect(healthTone('Missing')).toBe('danger');
    expect(healthTone('Suspended')).toBe('neutral');
  });
});

describe('matchLabel', () => {
  it('pin / tahmini güven', () => {
    expect(matchLabel({ match: 'manual', confidence: 100 })).toBe('pin');
    expect(matchLabel({ match: 'name', confidence: 70 })).toBe('tahmini %70');
  });
});

describe('senkron özeti', () => {
  it('okunmadı / yok / fazlar sıralı', () => {
    expect(syncsSummary(undefined)).toBe('—');
    expect(syncsSummary(null)).toBe('—');
    expect(syncsSummary({})).toBe('yok');
    expect(syncsSummary({ Failed: 1, Succeeded: 3, Custom: 2, Error: 0 })).toBe('3 başarılı · 1 başarısız · 2 Custom');
  });
  it('toplam ve başarısızlık', () => {
    expect(syncsTotal(undefined)).toBe(-1);
    expect(syncsTotal({ Succeeded: 2, Failed: 1 })).toBe(3);
    expect(syncsFailed({ Succeeded: 2 })).toBe(false);
    expect(syncsFailed({ Error: 1 })).toBe(true);
    expect(syncsFailed(undefined)).toBe(false);
  });
});

describe('repoShort / autoSyncLabel', () => {
  it('repo', () => {
    expect(repoShort('https://github.com/org/repo.git')).toBe('github.com/org/repo');
    expect(repoShort('ssh://git@dev.azure.com/org/p/_git/r')).toBe('dev.azure.com/org/p/_git/r');
    expect(repoShort('git@github.com:org/repo.git')).toBe('github.com:org/repo');
    expect(repoShort(undefined)).toBe('—');
  });
  it('autosync', () => {
    expect(autoSyncLabel(true)).toBe('açık');
    expect(autoSyncLabel(false)).toBe('kapalı');
    expect(autoSyncLabel(undefined)).toBe('—');
  });
});

// v0.10.985 — Argo bölümünün kaynak etiketleri (eşleyici tablosu | canlı Thanos).
describe('argo kaynağı', () => {
  it('rozet: eşleyici yalnız "mapper"da; diğer her durumda bugünkü Thanos rozeti', () => {
    expect(argoSourceBadge('mapper')).toBe('eşleyici · argocd_app_mapping');
    expect(argoSourceBadge('live')).toBe('Thanos · argocd_app_info');
    expect(argoSourceBadge(undefined)).toBe('Thanos · argocd_app_info');
  });
  it('meta: kaynak yoksa boş', () => {
    expect(argoSourceMeta('mapper')).toContain('eşleyici');
    expect(argoSourceMeta('live')).toBe('canlı sorgu');
    expect(argoSourceMeta(undefined)).toBe('');
    expect(argoSourceMeta('x')).toBe('');
  });
});
