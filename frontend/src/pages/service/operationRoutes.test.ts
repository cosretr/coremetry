// operationRoutes.test.ts — v0.10.1023. Operatör bildirimi: "Operation
// kısmında POST GET neden detail gözükmüyor, sonra trace'e girince çıkıyor."
// Çivilenen: çıplak fiil satırı yalnız en az bir dolu rota varken bölünür,
// artık satır işaretlenir, satır kimliği ad çakışmasında bile tekil kalır,
// Traces çipleri rota filtresini taşır, kapsanmayan / kesilmiş yanıt bölmez.
import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import type { OperationSummary } from '@/lib/types';
import { encodeFilters } from '@/lib/urlState';
import { opDisplayName } from '@/lib/opDisplayName';
import { expandBareVerbRows, opRowKey, opTraceFilters, routeRowsForBundle, usableRouteRows } from './operationRoutes';

const op = (name: string, spanCount: number, route?: string): OperationSummary => ({
  name, spanCount, errorCount: 0, errorRate: 0, avgDurationMs: 1,
  p50DurationMs: 1, p95DurationMs: 1, p99DurationMs: 1, apdex: 0,
  ...(route !== undefined ? { route } : {}),
});
// Sunucu `route: ""`'yi omitempty ile hiç göndermez — artık satır böyle gelir.
const residual = (name: string, spanCount: number) => op(name, spanCount);
const view = (rows: ReturnType<typeof expandBareVerbRows>['rows']) =>
  rows.map(r => `${opDisplayName(r.name, r.route)}|${r.spanCount}${r.splitResidual ? '|residual' : ''}`).sort();

describe('expandBareVerbRows', () => {
  const cases: {
    name: string;
    raw: OperationSummary[];
    routes: OperationSummary[] | null | undefined;
    want: string[];
    split: number;
  }[] = [
    {
      name: 'fiiller rotalarına bölünür, artık satır işaretli, fiil olmayan satır aynen',
      raw: [op('GET', 10), op('POST', 5), op('SELECT shop.orders', 3)],
      routes: [op('GET', 7, '/metrics'), residual('GET', 3), op('POST', 5, '/api/orders')],
      want: ['GET /metrics|7', 'GET|3|residual', 'POST /api/orders|5', 'SELECT shop.orders|3'],
      split: 2,
    },
    {
      name: 'fiilin yalnız boş rotası var → bölünmez',
      raw: [op('GET', 4)],
      routes: [residual('GET', 4)],
      want: ['GET|4'],
      split: 0,
    },
    {
      name: 'karışık: GET bölünür, PUT yalnız boş rota, DELETE rota satırsız',
      raw: [op('GET', 9), op('PUT', 2), op('DELETE', 1)],
      routes: [op('GET', 6, '/a'), op('GET', 3, '/b'), residual('PUT', 2)],
      want: ['DELETE|1', 'GET /a|6', 'GET /b|3', 'PUT|2'],
      split: 1,
    },
    {
      name: 'ham satırı olmayan fiilin rota satırları EKLENMEZ',
      raw: [op('GET', 3)],
      routes: [op('GET', 3, '/x'), op('PATCH', 8, '/y')],
      want: ['GET /x|3'],
      split: 1,
    },
    {
      name: 'küçük harfli ham ad sunucunun büyük harfli satırlarıyla eşleşmez',
      raw: [op('get', 3)],
      routes: [op('GET', 3, '/x')],
      want: ['get|3'],
      split: 0,
    },
    { name: 'rota satırı yok (boş dizi)', raw: [op('GET', 2)], routes: [], want: ['GET|2'], split: 0 },
    { name: 'rota yanıtı yok (yükleniyor / hata)', raw: [op('GET', 2)], routes: undefined, want: ['GET|2'], split: 0 },
    { name: 'kapsanmayan yanıt (null)', raw: [op('GET', 2)], routes: null, want: ['GET|2'], split: 0 },
  ];
  for (const c of cases) {
    it(c.name, () => {
      const got = expandBareVerbRows(c.raw, c.routes);
      expect(view(got.rows)).toEqual([...c.want].sort());
      expect(got.split).toBe(c.split);
    });
  }

  it('rota satırı yokken ham dizi AYNEN döner (kimlik — gereksiz yeniden çizim yok)', () => {
    const raw = [op('GET', 1)];
    expect(expandBareVerbRows(raw, []).rows).toBe(raw);
    expect(expandBareVerbRows(raw, undefined).rows).toBe(raw);
  });

  it('artık satır route: "" taşır (sunucu alanı göndermese de)', () => {
    const { rows } = expandBareVerbRows([op('GET', 4)], [op('GET', 3, '/a'), residual('GET', 1)]);
    const res = rows.find(r => r.splitResidual)!;
    expect(res.route).toBe('');
    expect(res.name).toBe('GET');
  });

  it('ad çakışması: gerçekten "GET /metrics" adlı span + bölünmüş GET /metrics — kimlikler tekil', () => {
    const { rows } = expandBareVerbRows(
      [op('GET', 5), op('GET /metrics', 2)],
      [op('GET', 4, '/metrics'), residual('GET', 1)],
    );
    const keys = rows.map(opRowKey);
    expect(new Set(keys).size).toBe(rows.length);
    expect(rows.map(r => opDisplayName(r.name, r.route)).filter(t => t === 'GET /metrics')).toHaveLength(2);
  });
});

describe('opRowKey', () => {
  it('ad + NUL + rota; rotasız satırda boş', () => {
    expect(opRowKey({ name: 'GET', route: '/metrics' })).toBe('GET\u0000/metrics');
    expect(opRowKey({ name: 'GET /metrics' })).toBe('GET /metrics\u0000');
    // route '' ile route yok aynı (sunucu omitempty'si) — ikisi de bölünmemiş satır.
    expect(opRowKey({ name: 'GET', route: '' })).toBe(opRowKey({ name: 'GET' }));
  });
  // v0.10.1023 inceleme R6 — artık satır (bölmeden gelen rotasız GET) ham
  // "GET" satırıyla kimlik PAYLAŞMAZ: farklı span kümeleri; ham↔bölünmüş
  // geçişinde açık detay paneli öbürüne yapışmasın. (Önceki pin tersini
  // söylüyordu: ikisi aynı anahtardı.)
  it('artık satır ham satırdan ayrı kimlik taşır', () => {
    const bare = { name: 'GET' };
    const res = { name: 'GET', route: '', splitResidual: true };
    expect(opRowKey(res)).not.toBe(opRowKey(bare));
    const { rows } = expandBareVerbRows([op('GET', 4)], [op('GET', 3, '/a'), residual('GET', 1)]);
    expect(rows.map(opRowKey)).not.toContain(opRowKey(bare));
  });
});

// İnceleme R2 — Service.tsx tablo satırlarını VE sekme rozetini yalnız
// routeRowsForBundle çıktısından kurar; sorgu verisine başka yoldan dokunmaz.
describe('Service.tsx kablolaması', () => {
  const src = readFileSync(resolve(__dirname, '../Service.tsx'), 'utf8')
    .replace(/\/\*[\s\S]*?\*\//g, '').replace(/^\s*\/\/.*$/gm, '');
  it('tek karar noktası', () => {
    expect(src).toContain('const routeRows = routeRowsForBundle(routesQ, opsWindow, svc, normalized);');
    expect(src).toContain('expandBareVerbRows(operations, routeRows)');
    expect(src).toContain(': rawOps.rows;');
    expect(src).toContain('opCount={rawOps.rows.length}');
    expect(src).toContain('setOpsWindow({ svc, from: r.from, to: r.to, env });');
    expect(src.match(/routesQ\./g) ?? []).toEqual([]);
    expect(src).not.toContain('usableRouteRows');
  });
});

describe('routeRowsForBundle (inceleme R1/R2)', () => {
  const win = { svc: 'payments-api', from: 1000, to: 2000, env: '' };
  const resp = (over: Partial<{ covered: boolean; truncated: boolean }> = {}) =>
    ({ rows: [op('GET', 1, '/a')], covered: true, ...over });
  const q = (over: Partial<{ for: typeof win; resp: ReturnType<typeof resp>; isPlaceholderData: boolean; isSuccess: boolean; noData: boolean }> = {}) => ({
    data: over.noData ? undefined : { for: over.for ?? win, resp: over.resp ?? resp() },
    isPlaceholderData: over.isPlaceholderData ?? false,
    isSuccess: over.isSuccess ?? true,
  });
  const cases: { name: string; query: ReturnType<typeof q>; opsWindow: typeof win | null; svc?: string; normalized?: boolean; want: boolean }[] = [
    { name: 'birebir eşleşme → satırlar', query: q(), opsWindow: win, want: true },
    { name: 'başka pencere (from)', query: q({ for: { ...win, from: 999 } }), opsWindow: win, want: false },
    { name: 'başka pencere (to)', query: q({ for: { ...win, to: 2001 } }), opsWindow: win, want: false },
    { name: 'başka servis (yanıt damgası)', query: q({ for: { ...win, svc: 'checkout' } }), opsWindow: win, want: false },
    { name: 'sayfa başka servise geçti, bundle henüz eski', query: q(), opsWindow: win, svc: 'checkout', want: false },
    { name: 'yer tutucu veri (keepPreviousData)', query: q({ isPlaceholderData: true }), opsWindow: win, want: false },
    // Kapalı sorgu + eski anahtarın verisi: RQ onu yer tutucu olarak verir (isSuccess de olabilir).
    { name: 'kapalı sorgu, eski veri yer tutucu', query: q({ isPlaceholderData: true, isSuccess: true, for: { ...win, to: 1500 } }), opsWindow: win, want: false },
    { name: 'kapalı sorgu, veri yok', query: q({ noData: true, isSuccess: false }), opsWindow: win, want: false },
    { name: 'yükleniyor / hata', query: q({ isSuccess: false }), opsWindow: win, want: false },
    { name: 'env seçili', query: q({ for: { ...win, env: 'prod' } }), opsWindow: { ...win, env: 'prod' }, want: false },
    { name: 'kapsanmıyor', query: q({ resp: resp({ covered: false }) }), opsWindow: win, want: false },
    { name: 'kesik', query: q({ resp: resp({ truncated: true }) }), opsWindow: win, want: false },
    { name: 'bundle penceresi yok', query: q(), opsWindow: null, want: false },
    { name: 'Normalized kip', query: q(), opsWindow: win, normalized: true, want: false },
  ];
  for (const c of cases) {
    it(c.name, () => {
      const got = routeRowsForBundle(c.query, c.opsWindow, c.svc ?? 'payments-api', c.normalized ?? false);
      if (c.want) expect(got).toBe(c.query.data!.resp.rows);
      else expect(got).toBeUndefined();
    });
  }
});

describe('usableRouteRows', () => {
  const rows = [op('GET', 1, '/a')];
  it('yalnız kapsanmış ve kesilmemiş yanıt satır verir', () => {
    expect(usableRouteRows({ rows, covered: true })).toBe(rows);
    expect(usableRouteRows({ rows, covered: false })).toBeNull();
    expect(usableRouteRows({ rows, covered: true, truncated: true })).toBeNull();
    expect(usableRouteRows(undefined)).toBeNull();
    expect(usableRouteRows(null)).toBeNull();
  });
});

describe('opTraceFilters', () => {
  it('bölünmemiş ham satır: yalnız ad (opHref\'in bugünkü çipi)', () => {
    const f = opTraceFilters({ name: 'GET' }, false);
    expect(f).toEqual([{ k: 'name', op: '=', v: ['GET'] }]);
    expect(encodeFilters(f)).toBe('[{"k":"name","op":"=","v":["GET"]}]');
  });
  it('rota satırı: ad + http.route =', () => {
    expect(opTraceFilters({ name: 'GET', route: '/api/orders/:id' }, false)).toEqual([
      { k: 'name', op: '=', v: ['GET'] },
      { k: 'http.route', op: '=', v: ['/api/orders/:id'] },
    ]);
  });
  it('artık satır: ad + http.route NOT EXISTS', () => {
    expect(opTraceFilters({ name: 'POST', route: '' }, true)).toEqual([
      { k: 'name', op: '=', v: ['POST'] },
      { k: 'http.route', op: 'NOT EXISTS', v: [] },
    ]);
  });
  it('fiil olmayan ad (rota yok, artık değil): yalnız ad', () => {
    expect(opTraceFilters({ name: 'SELECT shop.orders' }, false)).toEqual([{ k: 'name', op: '=', v: ['SELECT shop.orders'] }]);
  });
  // v0.10.1115 — Normalized kipte satır adı bir op_group ŞEKLİ; `name = <şekil>`
  // hiçbir span adına eşit değildi (boş Traces listesi). Tek çip op_group.
  it('Normalized: yalnız op_group = <şekil> (ad / rota çipi yok)', () => {
    const f = opTraceFilters({ name: 'GET /orders/:id' }, false, true);
    expect(f).toEqual([{ k: 'op_group', op: '=', v: ['GET /orders/:id'] }]);
    expect(encodeFilters(f)).toBe('[{"k":"op_group","op":"=","v":["GET /orders/:id"]}]');
    // Rota / artık bayrağı taşınsa bile şekil tek kimlik.
    expect(opTraceFilters({ name: 'GET /orders/:id', route: '/orders/:id' }, true, true))
      .toEqual([{ k: 'op_group', op: '=', v: ['GET /orders/:id'] }]);
  });
  it('Raw (normalized=false açıkça): bugünkü ad çipi', () => {
    expect(opTraceFilters({ name: 'GET /orders/8421' }, false, false)).toEqual([{ k: 'name', op: '=', v: ['GET /orders/8421'] }]);
  });
});
