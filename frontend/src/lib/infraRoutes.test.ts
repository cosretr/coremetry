import { describe, it, expect } from 'vitest';
import { infraRouteRegexBody, isInfraRoute, withInfraExclude } from './infraRoutes';
import { serverSel, infraHiddenCountQuery, infraHiddenCount, endpointListScope } from './httpMetrics';
import { SPANMETRICS_FAMILY } from './redFamily';

// v0.10.2434 (operatör onaylı) — tesisat rotaları metrik RED'inden varsayılan dışlanır.
describe('infraRoutes', () => {
  it('harf duyarsız alt-dize; önekli yollar eşleşir, iş rotaları eşleşmez', () => {
    expect(isInfraRoute('/APP/actuator/prometheus')).toBe(true);
    expect(isInfraRoute('/shop/Actuator/Health')).toBe(true);
    expect(isInfraRoute('/api/v1/orders/{id}')).toBe(false);
    const re = new RegExp(`^.*(?:${infraRouteRegexBody().replace('(?i:', '(?:')}).*$`, 'i');
    expect(re.test('/APP/actuator/prometheus')).toBe(true);
    expect(re.test('/Xwell-known/x')).toBe(false); // nokta kaçışlı
    expect(re.test('')).toBe(false); // rota etiketi yok → korunur
  });
  it('varsayılan dışlama serverSel\'e !~ olarak girer; gösterilirken yok', () => {
    const ex = withInfraExclude([], false);
    const sel = serverSel({ app: 'orders-api', routeExclude: ex });
    expect(sel).toContain('http_route!~".*(?:(?i:/health|/healthz|');
    expect(serverSel({ app: 'orders-api', routeExclude: withInfraExclude([], true) })).not.toContain('!~');
    // Yönetici kalıpları korunur.
    expect(withInfraExclude(['/internal'], false)).toEqual(['/internal', infraRouteRegexBody()]);
    // Liste: arama yokken dışlar.
    expect(endpointListScope('orders-api', '', undefined, undefined, ex).routeExclude).toEqual(ex);
  });
  it('gizlenen sayı sorgusu: tesisat rotalarını sayar; spanmetrics giriş eşleyicisi', () => {
    const q = infraHiddenCountQuery('mainframe-adapter', 3600, infraRouteRegexBody(), undefined, SPANMETRICS_FAMILY);
    expect(q).toMatch(/^count\(sum by \(http_route\) \(increase\(traces_span_metrics_duration_seconds_count\{/);
    expect(q).toContain('span_kind=~"SPAN_KIND_SERVER|SPAN_KIND_CONSUMER"');
    expect(q).toContain('http_route=~".*(?:(?i:/health|');
    expect(infraHiddenCount([])).toBe(0);
    expect(infraHiddenCount(undefined)).toBeNull();
  });
});
