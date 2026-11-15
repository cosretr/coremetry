import { describe, it, expect } from 'vitest';
import type { ClusterPodRow } from '@/lib/types';
import {
  podStatusWord, podRestartText, podProblemRank, workloadService, workloadMemText, workloadCpuText,
  namespaceBoxes, namespaceSentence, workloadStatusWord,
} from './nsDetailModel';

// Clusters S9 (plan §9.3, onaylı Namespace mockup'ı): namespace detayının saf
// çekirdeği — durum sözcüğü, restart nedeni, sorun sırası, iş yükü servisi,
// dört kutu ve düz cümle. Alan yoksa parça düşer.
const GiB = 1024 ** 3;
const pod = (o: Partial<ClusterPodRow>): ClusterPodRow => ({ cluster: 'c', namespace: 'orders', pod: 'p', cpuCores: 0.1, memBytes: GiB, phase: 'Running', restarts: 0, ...o } as ClusterPodRow);

describe('pod durum sözcüğü', () => {
  it('bekleme sebebi fazı ezer; sözlük dışı sebep "Bekliyor · <sebep>"', () => {
    expect(podStatusWord(pod({}), 'CrashLoopBackOff')).toEqual({ word: 'Sürekli çöküyor', tone: 'err' });
    expect(podStatusWord(pod({ phase: 'Pending' }), 'ImagePullBackOff').word).toBe('Bekliyor · imaj çekilemedi');
    expect(podStatusWord(pod({ phase: 'Pending' }), 'Garip').word).toBe('Bekliyor · Garip');
    expect(podStatusWord(pod({}))).toEqual({ word: 'Çalışıyor', tone: null });
    expect(podStatusWord(pod({ phase: 'Pending' })).tone).toBe('warn');
    expect(podStatusWord(pod({ phase: undefined })).word).toBe('');
    // S13 — zamanlanamıyor (probe).
    expect(podStatusWord(pod({ phase: 'Pending', unschedulable: true }))).toEqual({ word: 'Bekliyor · yer yok', tone: 'warn' });
  });
  it('restart hücresi nedenle; bilinmiyorsa boş', () => {
    expect(podRestartText(pod({ restarts: 7, lastTermReason: 'OOMKilled' }))).toBe('7 · bellek bitti');
    expect(podRestartText(pod({ restarts: 0, lastTermReason: 'OOMKilled' }))).toBe('0');
    expect(podRestartText(pod({ restartsUnknown: true }))).toBe('');
  });
  it('sorun sırası: çöküş > bekliyor > restart / bellek ≥ %90 > normal', () => {
    expect(podProblemRank(pod({}), 'CrashLoopBackOff')).toBe(3);
    expect(podProblemRank(pod({ phase: 'Pending' }))).toBe(2);
    expect(podProblemRank(pod({ memPct: 95 }))).toBe(1);
    expect(podProblemRank(pod({}))).toBe(0);
  });
});

describe('iş yükü hücreleri', () => {
  it('servis = pod çoğunluğu; eşleşme yoksa boş', () => {
    const m = new Map([['a', pod({ pod: 'a', service: 'order-api' })], ['b', pod({ pod: 'b', service: 'order-api' })], ['c', pod({ pod: 'c', service: 'x' })]]);
    expect(workloadService({ podNames: ['a', 'b', 'c'] }, m)).toBe('order-api');
    expect(workloadService({ podNames: ['zzz'] }, m)).toBe('');
  });
  it('kullanılan / istek, kullanılan / limit · %', () => {
    expect(workloadCpuText(4.2, 9.5)).toBe('4.2 / 9.5 çekirdek');
    expect(workloadCpuText(4.2, 0)).toBe('4.2 çekirdek');
    expect(workloadMemText(31 * GiB, 38 * GiB)).toEqual({ text: '31.0 / 38.0 GiB · %82', pct: expect.closeTo(81.6, 1) });
    expect(workloadMemText(GiB, 0)).toEqual({ text: '1.00 GiB', pct: null });
    expect(workloadStatusWord('Progressing')).toEqual({ word: 'Eksik replika', tone: 'warn' });
  });
});

describe('dört kutu ve düz cümle', () => {
  const pods = [pod({ pod: 'a' }), pod({ pod: 'b', phase: 'Pending' }), pod({ pod: 'c', memPct: 95 })];
  const summary = { cpuRequestCores: 16, cpuLimitCores: 32, memRequestBytes: 48 * GiB, memLimitBytes: 64 * GiB, restarts24h: 7, waiting: { b: 'ImagePullBackOff' } };
  it('kutular', () => {
    const boxes = namespaceBoxes(pods, summary, { cpu: 9.4, mem: 52 * GiB }, false);
    expect(boxes.map(b => [b.label, b.value, b.sub])).toEqual([
      ['Pods', '2 / 3 çalışıyor', '1 bekliyor'],
      ['CPU', '9.40 çekirdek', 'istek 16.00 · limit 32.00'],
      ['Memory', '52.0 GiB', 'istek 48.0 GiB (üstünde) · limit 64.0 GiB'],
      ['Yeniden başlama (24 sa)', '7', ''],
    ]);
    expect(boxes[2].tone).toBe('warn');
  });
  it('kesik envanterde "en az"; özet yoksa kutu düşer', () => {
    expect(namespaceBoxes(pods, undefined, {}, true).map(b => b.value)).toEqual(['en az 2 / 3 çalışıyor']);
  });
  it('cümle', () => {
    expect(namespaceSentence(6, pods, summary, false))
      .toBe("6 iş yükü, 2/3 pod çalışıyor; 1 pod bellek limitinin %90'ında; 1 pod bekliyor; son 24 sa 7 yeniden başlama.");
    expect(namespaceSentence(0, [], undefined, false)).toBe('');
  });
});

// v0.10.2189 — İngilizce arayüz: durum sözcükleri, kutular ve cümle İngilizce.
describe('namespace detayı — EN', () => {
  it('durum sözcükleri', () => {
    expect(podStatusWord(pod({ phase: 'Pending', unschedulable: true }), undefined, 'en').word).toBe('Pending · unschedulable');
    expect(podStatusWord(pod({}), 'CrashLoopBackOff', 'en').word).toBe('Crash looping');
    expect(podStatusWord(pod({}), undefined, 'en').word).toBe('Running');
    expect(workloadStatusWord('Progressing', 'en').word).toBe('Missing replicas');
    expect(podRestartText(pod({ restarts: 7, lastTermReason: 'OOMKilled' }), 'en')).toBe('7 · out of memory');
    expect(workloadCpuText(4.2, 9.5, 'en')).toBe('4.2 / 9.5 cores');
  });
  it('kutular + cümle', () => {
    const pods = [pod({ pod: 'a' }), pod({ pod: 'b', phase: 'Pending' }), pod({ pod: 'c', memPct: 95 })];
    const summary = { cpuRequestCores: 16, memRequestBytes: 48 * GiB, restarts24h: 7, waiting: { b: 'ImagePullBackOff' } };
    expect(namespaceBoxes(pods, summary, { cpu: 9.4, mem: 52 * GiB }, true, 'en').map(b => [b.label, b.value, b.sub])).toEqual([
      ['Pods', 'at least 2 / 3 running', '1 pending'],
      ['CPU', '9.40 cores', 'request 16.00'],
      ['Memory', '52.0 GiB', 'request 48.0 GiB (exceeded)'],
      ['Restarts (24h)', '7', ''],
    ]);
    expect(namespaceSentence(1, pods, summary, false, 'en'))
      .toBe('1 workload, 2/3 pods running; 1 pod at 90% of its memory limit; 1 pod waiting; 7 restarts in the last 24h.');
  });
});
