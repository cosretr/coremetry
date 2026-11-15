import { describe, it, expect } from 'vitest';
import type { ClusterSummary } from '@/lib/types';
import { etaPhrase, overviewSentence, overviewBoxes, topSeriesText } from './overviewSummary';
import { parseOpenSections, serializeOpenSections } from './openSections';

// Clusters S7 (plan §0 D2, D6): Overview düz cümlesi, dört kutu, "en yüksek
// node" altyazısı ve kapalı bölüm kalıcılığının saf çekirdeği.
const GiB = 1024 ** 3;
const ok = (days: number) => ({ status: 'ok' as const, days, points: 100, windowDays: 7 });
const sum = (o: Partial<ClusterSummary>): ClusterSummary => ({ cluster: 'c1', ...o } as ClusterSummary);

describe('Overview düz cümle (D2)', () => {
  it('bellek % + ETA + kritik alarm', () => {
    expect(overviewSentence(sum({ memUsedBytes: 72 * GiB, memCapacityBytes: 100 * GiB, memForecast: ok(14), alertsCritical: 2 })))
      .toBe('Çalışıyor. Bellek %72 dolu ve bu hızla yaklaşık 14 günde dolar; 2 kritik alarm açık.');
  });
  it('alan yoksa parça düşer; 0 kritik yazılmaz', () => {
    expect(overviewSentence(sum({ memUsedBytes: 10 * GiB, memCapacityBytes: 100 * GiB, alertsCritical: 0 })))
      .toBe('Çalışıyor. Bellek %10 dolu.');
    expect(overviewSentence(sum({ alertsCritical: 1 }))).toBe('Çalışıyor. 1 kritik alarm açık.');
    expect(overviewSentence(sum({}))).toBe('Çalışıyor.');
    expect(overviewSentence(undefined)).toBe('');
  });
  it('ETA ifadesi saat / tavan / tahmin yok', () => {
    expect(etaPhrase(ok(0.375))).toBe('yaklaşık 9 saatte dolar');
    expect(etaPhrase({ status: 'at_limit', points: 1, windowDays: 7 })).toBe('eğilim tavanda');
    expect(etaPhrase({ status: 'none', points: 1, windowDays: 7 })).toBe('');
    expect(etaPhrase(undefined)).toBe('');
  });
});

describe('Overview dört kutu (D2)', () => {
  it('CPU / Memory kapasiteyle, Pods, Alerts', () => {
    const boxes = overviewBoxes(sum({
      cpuUsedCores: 148, cpuCapacityCores: 320, nodes: 12,
      memUsedBytes: 72 * GiB, memCapacityBytes: 100 * GiB, memForecast: ok(14),
      podsRunning: 1204, podsPending: 3, podsFailed: 1, alertsCritical: 2, alertsWarning: 5,
    }), true);
    expect(boxes.map(b => b.label)).toEqual(['CPU', 'Memory', 'Pods', 'Alerts']);
    expect(boxes[0]).toMatchObject({ value: '148 / 320 çekirdek', sub: '%46 · 12 node', tone: null });
    expect(boxes[1]).toMatchObject({ value: '72.0 GiB / 100 GiB', sub: '%72 · yaklaşık 14 günde dolar', tone: 'warn' });
    expect(boxes[2]).toMatchObject({ value: '1.2K çalışıyor', sub: '3 bekliyor · 1 başarısız' });
    expect(boxes[3]).toMatchObject({ value: '2 kritik', sub: '5 uyarı', tone: 'err' });
  });
  it('kapasite yoksa yalnız kullanılan; alarm ailesi bilinmiyorsa Alerts kutusu yok', () => {
    const boxes = overviewBoxes(sum({ cpuUsedCores: 2.4, memUsedBytes: 8 * GiB }), false);
    expect(boxes.map(b => [b.label, b.value])).toEqual([['CPU', '2.4 çekirdek'], ['Memory', '8.00 GiB']]);
  });
});

describe('En yüksek node altyazısı', () => {
  const s = (name: string, last: number) => ({ name, points: [{ bucket: 1, value: 0 }, { bucket: 2, value: last }] });
  it('son değeri en yüksek seri; tek seride boş', () => {
    expect(topSeriesText([s('worker-1', 3), s('worker-7', 21.4)], v => `${v} çekirdek`)).toBe('En yüksek: worker-7 · 21.4 çekirdek');
    expect(topSeriesText([s('total', 3)], String)).toBe('');
    expect(topSeriesText(undefined, String)).toBe('');
  });
});

describe('Kapalı bölüm kalıcılığı (D6)', () => {
  const known = ['net', 'gauges'] as const;
  it('bozuk / bilinmeyen değer yok sayılır; sıra sabit', () => {
    expect([...parseOpenSections('["gauges","x","net"]', known)].sort()).toEqual(['gauges', 'net']);
    expect(parseOpenSections('{bozuk', known).size).toBe(0);
    expect(parseOpenSections(null, known).size).toBe(0);
    expect(serializeOpenSections(new Set(['gauges', 'net'] as const), known)).toBe('["net","gauges"]');
  });
});

// v0.10.2189 — İngilizce arayüz: cümle, kutular ve altyazı İngilizce.
describe('overview — EN', () => {
  it('cümle + kutular + en yüksek', () => {
    expect(overviewSentence(sum({ memUsedBytes: 72 * GiB, memCapacityBytes: 100 * GiB, memForecast: ok(14), alertsCritical: 2 }), 'en'))
      .toBe('Running. Memory 72% full and at this rate fills in about 14 days; 2 critical alerts open.');
    const boxes = overviewBoxes(sum({ cpuUsedCores: 4, cpuCapacityCores: 8, nodes: 1, podsRunning: 5, podsPending: 1, podsFailed: 0, alertsCritical: 0, alertsWarning: 1 }), true, 'en');
    expect(boxes.map(b => [b.label, b.value, b.sub])).toEqual([
      ['CPU', '4 / 8 cores', '50% · 1 node'],
      ['Pods', '5 running', '1 pending · 0 failed'],
      ['Alerts', '0 critical', '1 warning'],
    ]);
    expect(topSeriesText([{ name: 'a', points: [{ t: 0, value: 1 }] }, { name: 'b', points: [{ t: 0, value: 2 }] }] as never, String, 'en'))
      .toBe('Highest: b · 2');
  });
});
