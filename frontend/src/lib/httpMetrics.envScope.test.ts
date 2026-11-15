import { describe, it, expect } from 'vitest';
import golden from './httpMetrics.envScope.golden.json';
import {
  serverSel, clientSel, windowQueries, httpQueries, listQueries, endpointListScope, endpointHttpQueries,
  totalRequestsQuery, statusCodeRateQuery, type HttpScope, type EndpointIdentity,
} from './httpMetrics';
import { overviewWindowItems } from '@/pages/service/overviewWindowKpi';
import { httpEnvScopeNote, httpEnvScopeReady, httpEnvScopeView } from './httpEnvScopeNote';
import type { HttpEnvScope } from './types';

// v0.10.1377 — metrik kipi env/cluster süzgeci (adlar SENTETİK).
// SÖZLEŞME: (1) envSel yoksa HER üretici bugünküyle bayt bayt aynı;
// (2) envSel varsa seçicide service_name'den HEMEN sonra girer — başka hiçbir
// şey değişmez; (3) Go'nun küme kıyası sorgularıyla aynı yer: altın dosya
// (TestHTTPEnvSelectorMatchesFrontend_v0101376 Go tarafı) bu tarafta da tutar.

const ENV = 'deployment_environment_name="prod-eu-a"';
const ENV2 = 'deployment_environment_name=~"dr-eu-a|prod-eu-a"';

describe('altın dosya paritesi (Go ↔ TS)', () => {
  for (const c of golden.cases) {
    it(c.name, () => {
      const sc: HttpScope = { app: c.app, route: c.route, method: c.method || undefined, envSel: c.envSel || undefined };
      expect(serverSel(sc)).toBe(c.serverSel);
      expect(windowQueries(sc, c.windowSec, c.totalBy).total).toBe(c.total);
      const q = windowQueries(sc, c.windowSec, c.quantileBy);
      expect(q.p50).toBe(c.p50);
      expect(q.p95).toBe(c.p95);
    });
  }
});

describe('envSel yok → bayt bayt eski sorgular', () => {
  it('seçiciler ve tekil sorgular (literal pin)', () => {
    expect(serverSel({ app: 'orders-api-prod', route: '/a', method: 'GET' }))
      .toBe('{service_name="orders-api-prod", http_route="/a", http_request_method="GET"}');
    expect(clientSel({ app: 'orders-api-prod' }, 'error_type!=""')).toBe('{service_name="orders-api-prod", error_type!=""}');
    expect(totalRequestsQuery('orders-api-prod', 3600))
      .toBe('sum(increase(http_server_request_duration_seconds_count{service_name="orders-api-prod"}[3600s]))');
    expect(statusCodeRateQuery('orders-api-prod', 3600))
      .toBe('sum by (http_response_status_code) (rate(http_server_request_duration_seconds_count{service_name="orders-api-prod"}[60s]))');
  });
  it('boş envSel = envSel yok (her üretici)', () => {
    const W = 3600;
    expect(httpQueries({ app: 's', envSel: '' }, W)).toEqual(httpQueries({ app: 's' }, W));
    expect(listQueries(endpointListScope('s', 'ord', ''), W, 50, true)).toEqual(listQueries(endpointListScope('s', 'ord'), W, 50, true));
    expect(endpointListScope('s')).toEqual({ app: 's' });
    expect(endpointListScope('s', 'ord', undefined)).not.toHaveProperty('envSel');
    expect(overviewWindowItems('s', W, 60, '')).toEqual(overviewWindowItems('s', W, 60));
    expect(totalRequestsQuery('s', W, '')).toBe(totalRequestsQuery('s', W));
    expect(statusCodeRateQuery('s', W, undefined)).toBe(statusCodeRateQuery('s', W));
    const ep: EndpointIdentity = { service: 's', route: '/a', method: 'GET' };
    expect(endpointHttpQueries({ ...ep, envSel: '' }, W, { latQ: 'p95', compare: true }))
      .toEqual(endpointHttpQueries(ep, W, { latQ: 'p95', compare: true }));
    for (const v of Object.values(httpQueries({ app: 's' }, W))) expect(v).not.toContain('deployment_environment');
  });
});

describe('envSel var → yalnız service_name\'den sonra eklenir', () => {
  // Tek değişiklik: her `service_name="<app>"` → `service_name="<app>", <envSel>`.
  const inj = (s: string, app: string, sel: string) => s.split(`service_name="${app}"`).join(`service_name="${app}", ${sel}`);
  const W = 10800;
  for (const sel of [ENV, ENV2]) {
    it(`httpQueries (inbound + downstream) · ${sel}`, () => {
      const base = httpQueries({ app: 'orders-api-prod', route: '/shop/orders/{id}', method: 'GET' }, W);
      const got = httpQueries({ app: 'orders-api-prod', route: '/shop/orders/{id}', method: 'GET', envSel: sel }, W);
      for (const id of Object.keys(base)) expect(got[id]).toBe(inj(base[id], 'orders-api-prod', sel));
      expect(got.dRps).toContain(`{service_name="orders-api-prod", ${sel}}`); // clientSel
    });
    it(`windowQueries + listQueries + endpointHttpQueries · ${sel}`, () => {
      const wq = windowQueries({ app: 'a', envSel: sel }, W, 'x', 60);
      const wb = windowQueries({ app: 'a' }, W, 'x', 60);
      for (const k of Object.keys(wb) as (keyof typeof wb)[]) expect(wq[k]).toBe(inj(wb[k], 'a', sel));
      const lq = listQueries(endpointListScope('a', 'ord', sel), W, 50, true);
      listQueries(endpointListScope('a', 'ord'), W, 50, true).forEach((q, i) => expect(lq[i].expr).toBe(inj(q.expr, 'a', sel)));
      for (const ep of [{ service: 'a', route: '/u/:id', sig: true }, { service: 'a', route: '' }, { service: 'a', route: '/b', method: 'POST' }] as EndpointIdentity[]) {
        const e = endpointHttpQueries({ ...ep, envSel: sel }, W, { latQ: 'p50', compare: true });
        endpointHttpQueries(ep, W, { latQ: 'p50', compare: true }).forEach((q, i) => expect(e[i].expr).toBe(inj(q.expr, 'a', sel)));
      }
    });
    it(`Overview pencere karoları + toplam + durum kodu · ${sel}`, () => {
      const ov = overviewWindowItems('a', W, 3600, sel);
      overviewWindowItems('a', W, 3600).forEach((q, i) => expect(ov[i].expr).toBe(inj(q.expr, 'a', sel)));
      expect(totalRequestsQuery('a', W, sel)).toBe(inj(totalRequestsQuery('a', W), 'a', sel));
      expect(statusCodeRateQuery('a', W, sel)).toBe(inj(statusCodeRateQuery('a', W), 'a', sel));
    });
  }
  it('sıra: service_name, ortam, route(regex), method, ek', () => {
    expect(serverSel({ app: 'a', envSel: ENV, routeRe: '/u/[^/]+', method: 'GET' }, 'le="0.5"'))
      .toBe(`{service_name="a", ${ENV}, http_route=~"/u/[^/]+", http_request_method="GET", le="0.5"}`);
  });
});

const applied: HttpEnvScope = {
  label: 'deployment_environment_name', source: 'default', applied: true,
  matcher: ENV2,
  terms: [{ role: 'env', want: 'eu-a', label: 'deployment_environment_name', values: ['dr-eu-a', 'prod-eu-a'], tier: 'prefix' }],
};

describe('httpEnvScopeView — durum', () => {
  it('terim yok / kapalı → off (sorgular beklemez)', () => {
    expect(httpEnvScopeView('', ' ', {})).toEqual({ state: 'off' });
    expect(httpEnvScopeView('eu-a', '', { data: applied }, false)).toEqual({ state: 'off' });
    expect(httpEnvScopeReady(httpEnvScopeView('', '', {}))).toBe(true);
  });
  it('çözüm bekleniyor → pending (sorgular BEKLER)', () => {
    const v = httpEnvScopeView('eu-a', '', {});
    expect(v.state).toBe('pending');
    expect(httpEnvScopeReady(v)).toBe(false);
  });
  it('uygulandı → envSel = matcher', () => {
    const v = httpEnvScopeView('eu-a', '', { data: applied });
    expect(v).toMatchObject({ state: 'applied', envSel: ENV2 });
    expect(httpEnvScopeReady(v)).toBe(true);
  });
  it('çözülmedi / başarısız / HTTP hatası → envSel YOK (süzgeçsiz)', () => {
    const un = httpEnvScopeView('us-x', '', { data: { ...applied, applied: false, matcher: undefined, reason: 'r' } });
    expect(un.state).toBe('unresolved');
    expect(un.envSel).toBeUndefined();
    const f = httpEnvScopeView('eu-a', '', { data: { label: '', source: '', terms: null, applied: false, failed: true, reason: 'VM etiket sorgusu başarısız: timeout' } });
    expect(f).toMatchObject({ state: 'error', reason: 'VM etiket sorgusu başarısız: timeout' });
    expect(f.envSel).toBeUndefined();
    expect(httpEnvScopeView('eu-a', '', { error: new Error('HTTP 400: yalnız VictoriaMetrics kaynağında (metricsrc=vm)') }))
      .toEqual({ state: 'error', reason: 'HTTP 400: yalnız VictoriaMetrics kaynağında (metricsrc=vm)' });
  });
});

describe('httpEnvScopeNote — bant metni', () => {
  it('kapalı → bant yok; bekliyor → nötr', () => {
    expect(httpEnvScopeNote({ state: 'off' })).toBeNull();
    expect(httpEnvScopeNote({ state: 'pending' })).toMatchObject({ tone: 'neutral', text: 'Ortam süzgeci çözülüyor…' });
  });
  it('uygulandı: etiket=değerler + terim başına kademe', () => {
    const n = httpEnvScopeNote(httpEnvScopeView('eu-a', '', { data: applied }))!;
    expect(n.tone).toBe('info');
    expect(n.text).toBe('Ortam süzgeci metrikte uygulandı: deployment_environment_name=dr-eu-a|prod-eu-a (env eu-a → dr-eu-a | prod-eu-a, önek eşleşmesi)');
    expect(n.title).toContain(`PromQL: ${ENV2}`);
  });
  it('uygulandı: env + cluster aynı etikette → kesişim; birebir değer tekrarlanmaz', () => {
    const sc: HttpEnvScope = {
      ...applied, matcher: ENV,
      terms: [applied.terms![0], { role: 'cluster', want: 'prod-eu-a', label: 'deployment_environment_name', values: ['prod-eu-a'], tier: 'exact' }],
    };
    expect(httpEnvScopeNote(httpEnvScopeView('eu-a', 'prod-eu-a', { data: sc }))!.text)
      .toBe('Ortam süzgeci metrikte uygulandı: deployment_environment_name=prod-eu-a (env eu-a → dr-eu-a | prod-eu-a, önek eşleşmesi; cluster prod-eu-a, birebir)');
  });
  it('v0.10.2211: cluster varlığı → span ortamı (cl-a → prod-cl-a)', () => {
    const sc: HttpEnvScope = {
      label: 'deployment_environment_name', source: 'default', applied: true, matcher: 'deployment_environment_name="prod-cl-a"',
      terms: [{ role: 'cluster', want: 'cl-a', label: 'deployment_environment_name', values: ['prod-cl-a'], tier: 'entity' }],
    };
    expect(httpEnvScopeNote(httpEnvScopeView('', 'cl-a', { data: sc }))!.text)
      .toBe('Ortam süzgeci metrikte uygulandı: deployment_environment_name=prod-cl-a (cluster cl-a → prod-cl-a, cluster varlığının span ortamları)');
  });
  it('uygulanamadı: ilk çözülmeyen terimi adlandırır, TÜM ortamlar uyarısı', () => {
    const sc: HttpEnvScope = {
      label: 'deployment_environment_name', source: 'default', applied: false,
      reason: 'metrikte bu ortam yok (deployment_environment_name değerleri arasında "us-x" eşleşmedi)',
      terms: [{ role: 'env', want: 'us-x', label: 'deployment_environment_name', tier: 'none' }],
    };
    const n = httpEnvScopeNote(httpEnvScopeView('us-x', '', { data: sc }))!;
    expect(n.tone).toBe('warning');
    expect(n.text).toBe('Ortam süzgeci uygulanamadı (metrikte bu ortam yok: deployment_environment_name değerleri arasında "us-x" eşleşmedi) — seriler TÜM ortamları kapsıyor');
    const empty = { ...sc, reason: 'env ve cluster aynı etikette kesişmiyor', terms: [applied.terms![0], { role: 'cluster' as const, want: 'eu-b', label: 'deployment_environment_name', values: ['eu-b'], tier: 'exact' as const }] };
    expect(httpEnvScopeNote(httpEnvScopeView('eu-a', 'eu-b', { data: empty }))!.text)
      .toBe('Ortam süzgeci uygulanamadı (env ve cluster aynı etikette kesişmiyor) — seriler TÜM ortamları kapsıyor');
  });
  it('çözülemedi: sebep + süzgeç uygulanmadı', () => {
    const n = httpEnvScopeNote({ state: 'error', reason: 'VM etiket sorgusu başarısız: timeout' })!;
    expect(n).toMatchObject({ tone: 'warning', text: 'Ortam süzgeci çözülemedi (VM etiket sorgusu başarısız: timeout) — süzgeç uygulanmadı, seriler TÜM ortamları kapsıyor' });
  });
});
