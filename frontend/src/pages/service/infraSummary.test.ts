import { describe, it, expect } from 'vitest';
import type { ClusterPodRow } from '@/lib/types';
import { summarizeInfraClusters } from './infraClusters';
import { infraSentence, infraAttention, infraCounts, usagePhrase, type InfraSummaryInput } from './infraSummary';

// v0.10.2144 (servis sekmeleri S5) — Infra üst cümlesi + "Dikkat isteyenler".
const pod = (cluster: string, name: string, over: Partial<ClusterPodRow> = {}): ClusterPodRow => ({
  cluster, pod: name, namespace: 'ns-a', cpuCores: 0.1, memBytes: 1e8, phase: 'Running', restarts: 0, ...over,
} as ClusterPodRow);
function input(pods: ClusterPodRow[], unreachable: string[] = [], entityOnly: { pod: string; cluster: string }[] = []): InfraSummaryInput {
  const order = [...new Set(pods.map(p => p.cluster))];
  return { clusters: summarizeInfraClusters(pods, order), pods, unreachable, entityOnly };
}

describe('infraSentence', () => {
  it.each<[string, InfraSummaryInput, string]>([
    ['pod yok: cümle yok', input([]), ''],
    ['tek cluster, hepsi çalışıyor', input([pod('c1', 'a'), pod('c1', 'b')]), "2 pod, 1 cluster'da çalışıyor. Durum normal."],
    ['çok cluster, hepsi normal', input([pod('c1', 'a'), pod('c2', 'b'), pod('c3', 'c')]), "3 pod, 3 cluster'da çalışıyor. Hepsi normal."],
    ['yanıt vermeyen cluster', input([pod('c1', 'a'), pod('c2', 'b')], ['c9']),
      "2 pod, 2 cluster'da çalışıyor. 2 normal, 1 cluster'dan durum alınamıyor."],
    ['çalışmayan pod', input([pod('c1', 'a'), pod('c2', 'b', { phase: 'Pending' })]),
      "2 pod, 2 cluster'da çalışıyor. 1 normal, 1 cluster'da çalışmayan pod var."],
    ['kube-state yok: normal DENMEZ', input([pod('c1', 'a', { phase: undefined })]),
      "1 pod, 1 cluster'da çalışıyor. 1 cluster'ın pod durumu bilinmiyor."],
    ['hepsi birden', input([pod('c1', 'a'), pod('c2', 'b', { phase: 'CrashLoopBackOff' }), pod('c3', 'c', { phase: undefined })], ['c8', 'c9']),
      "3 pod, 3 cluster'da çalışıyor. 1 normal, 1 cluster'da çalışmayan pod var, 1 cluster'ın pod durumu bilinmiyor, 2 cluster'dan durum alınamıyor."],
  ])('%s', (_n, inp, want) => expect(infraSentence(inp)).toBe(want));

  it('sayımlar', () => {
    expect(infraCounts(input([pod('c1', 'a'), pod('c2', 'b', { phase: 'Failed' })], ['c9'])))
      .toEqual({ pods: 2, clusters: 2, normal: 1, failing: 1, unknown: 0, unreachable: 1 });
  });
});

describe('infraAttention', () => {
  it('sapma yoksa boş (kutu çizilmez)', () => {
    expect(infraAttention(input([pod('c1', 'a'), pod('c1', 'done', { phase: 'Succeeded' })]))).toEqual([]);
  });
  it('yalnız sapanlar, önem sırasıyla; pod en ağır tek nedeniyle', () => {
    const items = infraAttention(input([
      pod('c1', 'ok'),
      pod('c1', 'r2', { restarts: 2 }),
      pod('c1', 'r9', { restarts: 9 }),
      pod('c2', 'oom', { lastTermReason: 'OOMKilled', restarts: 4 }),
      pod('c2', 'pend', { phase: 'Pending', restarts: 7, lastTermReason: 'OOMKilled' }),
    ], ['c9'], [{ pod: 'ghost', cluster: 'c7' }]));
    // 2026-11-21 (operatör): yanıt vermeyen cluster (üst cümle + tablo satırı
    // zaten söylüyor) ve Thanos'ta görünmeyen pod Dikkat'e girmez.
    expect(items.map(i => `${i.kind}:${i.subject}`)).toEqual([
      'notRunning:pend', 'oom:oom', 'restarts:r9', 'restarts:r2',
    ]);
    expect(items[0]).toMatchObject({ tone: 'err', cluster: 'c2', text: 'çalışmıyor (Pending)' });
    expect(items[2]).toMatchObject({ tone: 'warn', text: '9 kez yeniden başladı' });
  });
  it('yalnız yanıt vermeyen cluster / Thanos dışı pod varken kutu yok', () => {
    expect(infraAttention(input([pod('c1', 'ok')], ['c9'], [{ pod: 'ghost', cluster: 'c7' }]))).toEqual([]);
  });
  it('restart sayısı bilinmiyorsa sapma uydurulmaz', () => {
    expect(infraAttention(input([pod('c1', 'a', { restartsUnknown: true, restarts: 5 })]))).toEqual([]);
  });
});

describe('usagePhrase', () => {
  it.each<[number | null, string, string | undefined]>([
    [null, '3.2 (limit bilinmiyor)', undefined],
    [41, "3.2 (limitin %41'i)", undefined],
    [75, "3.2 (limitin %75'i)", 'warn'],
    [92, "3.2 (limitin %92'i)", 'err'],
  ])('%s', (pct, text, tone) => expect(usagePhrase('3.2', pct)).toEqual({ text, tone }));
});
