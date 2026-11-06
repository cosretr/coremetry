// MLTraceHeatmap — v0.10.1870: ısı haritası isteğinin süzgeci ve sütun
// sayısı. Süzgeç giriş span'leri + yalnız http.route değeri (rpc etiketi
// sunucuda zaten boş gelir); sütun dakikada bir, 30–120 arası.
import { describe, it, expect } from 'vitest';
import { mlHeatmapBuckets, mlHeatmapFilters } from './MLTraceHeatmap';

const base = { view: 'heatmap', service: 'orders-api', fromNs: 0, toNs: 0, onsetNs: 0, endNs: 0 };

describe('mlHeatmapFilters', () => {
  it('servis + giriş span türü + endpoint', () => {
    expect(mlHeatmapFilters({ ...base, route: '/orders/{id}' })).toEqual([
      { k: 'service.name', op: '=', v: ['orders-api'] },
      { k: 'kind', op: 'IN', v: ['server', 'consumer'] },
      { k: 'http.route', op: '=', v: ['/orders/{id}'] },
    ]);
  });
  it('endpoint yoksa servisin tüm giriş istekleri', () => {
    expect(mlHeatmapFilters(base).map(f => f.k)).toEqual(['service.name', 'kind']);
  });
});

describe('mlHeatmapBuckets', () => {
  it.each([[10, 30], [90, 90], [360, 120]])('%s dk → %s sütun', (min, want) => {
    expect(mlHeatmapBuckets(0, min * 60e9)).toBe(want);
  });
});
