// endpointPick.test — v0.10.1737 (operatör onayı, mockup "Endpoint seç"):
// yük okuma, sıra, biçim, iki düğmenin gönderim bağlamı; v1 dokunulmaz.
import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import type { CosreEndpointPick, EndpointRow } from '@/lib/types';
import { parseDataPick } from '@/lib/cosre/protocol';
import {
  endpointInspectText, fmtErrPct, fmtP95, fmtRps, isBadErr, isBadP95, moreLabel, pickItemsFromRows, pickOf,
  showEndpointPick, sortPickItems, withEndpointInspect, withEndpointSummarize,
} from './endpointPick';

const PICK: CosreEndpointPick = {
  kind: 'endpoint', service: 'orders-api', env: 'prod', rangeS: 3600, total: 14,
  items: [{ route: 'POST /v1/orders', rps: 42.1, p95Ms: 1900, errPct: 4.8 }, { route: '/v1/orders', rps: 31.7, p95Ms: 480, errPct: 0 }],
};

describe('parseDataPick / pickOf', () => {
  it('geçerli yük okunur, son parça kazanır', () => {
    const parts = [
      { type: 'data-scope', data: {} },
      { type: 'data-pick', data: { ...PICK, total: 3 } },
      { type: 'data-pick', data: PICK },
    ];
    expect(pickOf(parts)).toEqual(PICK);
  });
  it.each([
    [{ ...PICK, kind: 'team' }],
    [{ ...PICK, service: '' }],
    [{ ...PICK, rangeS: 0 }],
    [{ ...PICK, total: '14' }],
    [{ ...PICK, items: [{ route: '/a', rps: 'x', p95Ms: 1, errPct: 0 }] }],
    [{ ...PICK, items: [{ route: '', rps: 1, p95Ms: 1, errPct: 0 }] }],
  ])('biçimsiz yük null: %j', d => {
    expect(parseDataPick({ type: 'data-pick', data: d })).toBeNull();
  });
  it('kart yalnız ≥2 endpoint ve satırla', () => {
    expect(showEndpointPick(PICK)).toBe(true);
    expect(showEndpointPick({ ...PICK, total: 1 })).toBe(false);
    expect(showEndpointPick({ ...PICK, items: [] })).toBe(false);
    expect(showEndpointPick(null)).toBe(false);
  });
});

describe('sıra + biçim', () => {
  it('hata % azalan, p95 azalan, route artan (sunucuyla aynı)', () => {
    const got = sortPickItems([
      { route: '/b', rps: 1, p95Ms: 100, errPct: 0 },
      { route: '/a', rps: 1, p95Ms: 100, errPct: 0 },
      { route: '/slow', rps: 1, p95Ms: 900, errPct: 0 },
      { route: '/err', rps: 1, p95Ms: 10, errPct: 2 },
    ]).map(i => i.route);
    expect(got).toEqual(['/err', '/slow', '/a', '/b']);
  });
  it('arama satırları: req/s = çağrı / pencere, fiil öne, trafiksiz düşer', () => {
    const rows = [
      { service: 'orders-api', path: '/v1/carts', method: 'GET', calls: 7200, errors: 0, errorRate: 0, avgMs: 1, p99Ms: 1, p95Ms: 40 },
      { service: 'orders-api', path: '/idle', calls: 0, errors: 0, errorRate: 0, avgMs: 0, p99Ms: 0 },
    ] as EndpointRow[];
    expect(pickItemsFromRows(rows, 3600)).toEqual([{ route: 'GET /v1/carts', rps: 2, p95Ms: 40, errPct: 0 }]);
    expect(pickItemsFromRows(null, 3600)).toEqual([]);
  });
  it('Türkçe ondalık + birim', () => {
    expect(fmtRps(42.14)).toBe('42,1');
    expect(fmtRps(0.04)).toBe('<0,1');
    expect(fmtP95(210)).toBe('210 ms');
    expect(fmtP95(1900)).toBe('1,9 sn');
    expect(fmtErrPct(4.8)).toBe('%4,8');
    expect(fmtErrPct(0)).toBe('%0,0');
  });
  it('yalnız sapan değer renklenir', () => {
    expect([isBadErr(4.8), isBadErr(0.3), isBadP95(1900), isBadP95(480)]).toEqual([true, false, true, false]);
  });
  it('+N daha etiketi (tavanlı "+N+")', () => {
    expect(moreLabel(PICK)).toBe('+12 endpoint daha · yazarak ara');
    expect(moreLabel({ ...PICK, capped: true })).toBe('+12+ endpoint daha · yazarak ara');
    expect(moreLabel({ ...PICK, total: 2 })).toBe('');
  });
});

describe('gönderim', () => {
  it('İncele: metin servis + route, bağlam servis + env + pencere + kart atlanır (base korunur)', () => {
    expect(endpointInspectText(PICK, ' /v1/orders/{id} ')).toBe('orders-api /v1/orders/{id}');
    expect(withEndpointInspect({ conversation: 'c1', service: 'other', rangeS: 900 }, PICK))
      .toEqual({ conversation: 'c1', service: 'orders-api', env: 'prod', rangeS: 3600, skipEndpointPick: true });
    expect(withEndpointInspect(undefined, { ...PICK, env: undefined })).toEqual({ service: 'orders-api', rangeS: 3600, skipEndpointPick: true });
  });
  it('Hepsini özetle: aynı bağlam + skipEndpointPick', () => {
    expect(withEndpointSummarize({ conversation: 'c1' })).toEqual({ conversation: 'c1', skipEndpointPick: true });
    expect(withEndpointSummarize(undefined)).toEqual({ skipEndpointPick: true });
  });
});

describe('v1 dokunulmaz', () => {
  it('CopilotChat (v1) endpoint kartını / data-pick yükünü bilmez', () => {
    const v1 = readFileSync(resolve(__dirname, '..', '..', 'CopilotChat.tsx'), 'utf8');
    expect(v1).not.toMatch(/EndpointPick|data-pick|skipEndpointPick/);
  });
});
