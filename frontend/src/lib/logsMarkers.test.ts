// logsMarkers.test.ts — v0.10.1895 (Log Search Faz 4 dilim 4.1): histogram
// işaretleri. Üç tür ayrı simge + metin; pencere dışı / işaret olmayan tür
// düşer; tık penceresi mevcut pencereye kırpılır.
import { describe, it, expect } from 'vitest';
import { logsMarkerKind, logsMarkers, logsMarkerRegions, logsMarkerZoom, logsMarkersSummary, LOGS_MARKER_META } from './logsMarkers';
import type { AnnotationItem } from './types';

const S = 1e9;
const item = (ts: number, kind: AnnotationItem['kind'], title = '', targetId?: string): AnnotationItem => ({ ts: ts * S, kind, title, targetId });

describe('logsMarkerKind', () => {
  it.each([
    ['deploy', 'rollout'], ['rollout', 'rollout'], ['anomaly', 'anomaly'], ['alert_fired', 'problem'],
    ['alert_resolved', null], ['event', null], ['bogus', null],
  ])('%s → %s', (k, want) => expect(logsMarkerKind(k)).toBe(want));
});

describe('logsMarkers', () => {
  it('pencere içi, sıralı, simge metinden ayıklanır', () => {
    const got = logsMarkers([
      item(300, 'alert_fired', '🔥 P2 · orders-api error rate', 'p1'),
      item(100, 'rollout', 'rollout ↻ 3 pod · v1.4.2'),
      item(200, 'anomaly', '◈ log_spike: payment timeout', 'a1'),
      item(50, 'deploy', 'deploy v1'),       // pencere dışı
      item(250, 'alert_resolved', 'çözüldü'), // işaret değil
    ], 100 * S, 400 * S);
    expect(got.map(m => m.kind)).toEqual(['rollout', 'anomaly', 'problem']);
    expect(got[1].text).toBe('log_spike: payment timeout');
    expect(got[2].text).toBe('P2 · orders-api error rate');
  });
  it('aynı an + aynı hedef deploy/rollout tek işaret; tavan en yenileri tutar', () => {
    expect(logsMarkers([item(10, 'deploy', 'a'), item(10, 'rollout', 'b')], 0, 100 * S)).toHaveLength(1);
    const many = Array.from({ length: 10 }, (_, i) => item(i + 1, 'anomaly', `x${i}`, `a${i}`));
    const got = logsMarkers(many, 0, 100 * S, 3);
    expect(got.map(m => m.text)).toEqual(['x7', 'x8', 'x9']);
  });
  it('boş / bozuk pencere → boş', () => {
    expect(logsMarkers(null, 0, 10)).toEqual([]);
    expect(logsMarkers([item(1, 'anomaly')], 10, 10)).toEqual([]);
  });
});

describe('logsMarkerRegions', () => {
  it('her tür kendi simgesi + metni + rengiyle; genişlik bir kova', () => {
    const ms = logsMarkers([item(100, 'rollout'), item(200, 'anomaly', '', 'a'), item(300, 'alert_fired', '', 'p')], 0, 1000 * S);
    const rg = logsMarkerRegions(ms, 30);
    expect(rg.map(r => r.label)).toEqual(['↻ Rollout', '◈ Anomali', '⚠ Problem']);
    expect(new Set(rg.map(r => r.color)).size).toBe(3);
    expect(rg[0]).toMatchObject({ fromSec: 100, toSec: 130, id: ms[0].key });
    expect(LOGS_MARKER_META.problem.glyph).not.toBe(LOGS_MARKER_META.anomaly.glyph);
  });
});

describe('logsMarkerZoom', () => {
  it('geniş pencerede ±15 dk', () => {
    expect(logsMarkerZoom(10_000 * S, 0, 86_400 * S)).toEqual([10_000 - 900, 10_000 + 900]);
  });
  it('dar pencerede 1/8, en az 1 dk; mevcut pencereye kırpılır', () => {
    // 15 dk pencere → pay 112.5 sn
    expect(logsMarkerZoom(450 * S, 0, 900 * S)).toEqual([450 - 112.5, 450 + 112.5]);
    expect(logsMarkerZoom(10 * S, 0, 900 * S)).toEqual([0, 10 + 112.5]);
    expect(logsMarkerZoom(100 * S, 0, 200 * S)).toEqual([40, 160]);
  });
});

describe('logsMarkersSummary', () => {
  it('düz Türkçe sayım', () => {
    const ms = logsMarkers([item(1, 'rollout'), item(2, 'rollout', 'x', 'r2'), item(3, 'alert_fired', '', 'p')], 0, 10 * S);
    expect(logsMarkersSummary(ms)).toBe('2 rollout · 1 Problem');
  });
});
