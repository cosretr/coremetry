// cosreChartSpec.route.test — v0.10.1738 (CoSRE v2 rota incelemesi; operatör
// eki: VM varsa VM, yoksa route MV): rota grafiği spec'inin saf yarısı — tür,
// başlık + kaynak etiketi (VM / CH), birim, çizgiler, uç parametreleri.
import { describe, expect, it } from 'vitest';
import type { CosreRouteRED } from '@/lib/types';
import { chartBlocks } from '@/lib/chatBlocks';
import { cosreSpecToPanel } from '@/lib/chatPanelSpec';
import { routeChartItems, routeChartKind, routeChartParams, routeChartTitle, routeChartUnit, type CosreChartSpec } from './cosreChartSpec';

const FROM = 1_761_912_000_000_000_000;
const TO = FROM + 3600e9;
const thr: CosreChartSpec = { service: 'orders-api', agg: 'rate', source: 'route', route: '/v1/orders', method: 'POST', env: 'prod', src: 'VM', rangeS: 3600, fromNs: FROM, toNs: TO };
const lat: CosreChartSpec = { ...thr, agg: 'p95', src: 'CH' };
const pts = [{ time: FROM, value: 1 }, { time: FROM + 60e9, value: 2 }];
const resp: CosreRouteRED = {
  source: 'VM', stepS: 60,
  throughput: [{ name: 'toplam', points: pts }, { name: '5xx', points: pts }],
  latency: [{ name: 'p50', points: pts }, { name: 'p95', points: pts }, { name: 'boş', points: [] }],
};

describe('rota grafiği spec (iki kaynak)', () => {
  it('tür + birim agg\'den', () => {
    expect([routeChartKind(thr), routeChartUnit(thr)]).toEqual(['throughput', 'req/s']);
    expect([routeChartKind(lat), routeChartUnit(lat)]).toEqual(['latency', 'ms']);
  });
  it('başlık tür + kaynak etiketi; cevabın kaynağı spec\'i ezer, model başlığı yok sayılır', () => {
    expect(routeChartTitle(thr)).toBe('Throughput · VM');
    expect(routeChartTitle(lat)).toBe('Response time · CH');
    expect(routeChartTitle(lat, 'VM')).toBe('Response time · VM');
    expect(routeChartTitle({ ...thr, src: undefined, title: 'model başlığı' })).toBe('Throughput');
  });
  it('çizgiler: throughput toplam + 5xx (hata rolü), latency p50 + p95; boş çizgi düşer', () => {
    const t = routeChartItems(resp, thr);
    expect(t.map(i => [i.name, i.role])).toEqual([['toplam', 'data'], ['5xx', 'error']]);
    expect(t[0].series[0]).toEqual({ groupKey: ['toplam'], points: pts });
    expect(routeChartItems(resp, lat).map(i => i.name)).toEqual(['p50', 'p95']);
    expect(routeChartItems({ ...resp, source: 'CH', latency: null }, lat)).toEqual([]);
    expect(routeChartItems(undefined, thr)).toEqual([]);
  });
  it('uç parametreleri: mutlak pencere; yoksa now − rangeS; fiil/env yalnız doluysa', () => {
    expect(routeChartParams(thr, 0)).toEqual({ service: 'orders-api', route: '/v1/orders', method: 'POST', env: 'prod', from: FROM, to: TO });
    expect(routeChartParams({ service: 's', agg: 'rate', source: 'route', route: '/r', rangeS: 600 }, 1e12))
      .toEqual({ service: 's', route: '/r', from: 1e12 - 600e9, to: 1e12 });
    expect(routeChartParams({ service: 's', agg: 'rate', source: 'route', route: '/r' }, 1e13).from).toBe(1e13 - 3600e9);
  });
  it('tipli chart bloğu olarak geçer; panele (dashboard) çevrilmez', () => {
    const blocks = [{ id: 'b1', type: 'chart', seq: 1, final: true, payload: thr }];
    expect(chartBlocks(blocks as never)).toHaveLength(1);
    expect(cosreSpecToPanel(thr)).toBeNull();
  });
});
