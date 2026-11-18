// lib/httpMetricsLinks.ts — v0.10.2346. Servis → HTTP / gRPC sekmesinin
// grafik panelleri için Metrics açılışları (lib/metricsLink). Her tanım
// panelin httpQueries ifadesinin builder karşılığı: aynı seri ailesi, aynı
// fonksiyon (rate / yüzdelik), aynı kırılım, AYNI eşleyiciler (ortam, route
// / method süzgeci, route dışlamaları, durum kümesi). serverFilters /
// clientFilters serverSel / clientSel'in yapısal ikizi; kayma testte
// yakalanır (httpMetricsLinks.test.ts: süzgeçlerden yeniden yazılan seçici
// == serverSel).
import type { MxFilter } from './metricsExplorer/model';
import { famOf, HTTP_LABELS, type HttpScope } from './httpMetrics';
import { parsePromMatchers, type MetricsLinkQuery, type MetricsLinkSpec } from './metricsLink';

const CLIENT_BASE = 'http_client_request_duration_seconds';
const CC = `${CLIENT_BASE}_count`;

/** serverSel'in service_name SONRASI eşleyicileri; ortam eşleyicisi çözülemezse null. */
export function serverFilters(s: HttpScope, extra: MxFilter[] = []): MxFilter[] | null {
  const F = famOf(s);
  const FL = F.labels;
  const env = s.envSel ? parsePromMatchers(s.envSel) : [];
  if (!env) return null;
  const out: MxFilter[] = [...env];
  if (s.routeRe !== undefined) out.push({ k: FL.route, op: '=~', v: s.routeRe });
  else if (s.route) out.push({ k: FL.route, op: '=', v: s.route });
  if (s.method) out.push({ k: FL.method, op: '=', v: s.method });
  if (s.routeExclude?.length && F.kind === 'http' && s.routeRe === undefined && !s.route) {
    for (const p of s.routeExclude) out.push({ k: FL.route, op: '!~', v: `.*(?:${p}).*` });
  }
  return [...out, ...extra];
}

/** clientSel'in service_name SONRASI eşleyicileri (yalnız ortam + ek). */
export function clientFilters(s: HttpScope, extra: MxFilter[] = []): MxFilter[] | null {
  const env = s.envSel ? parsePromMatchers(s.envSel) : [];
  return env ? [...env, ...extra] : null;
}

/**
 * Sekmenin grafik id'si (httpQueries anahtarı, `chart()`'ın ilk id'si) →
 * Metrics açılışı. `exprs` = aynı scope'un httpQueries çıktısı: builder'ın
 * ifade edemediği panel (durum SINIFI, label_replace) birebir PromQL'le açılır.
 * Ortam eşleyicisi okunamazsa hiçbir panel link almaz.
 */
export function httpPanelLinks(s: HttpScope, exprs: Record<string, string>): Record<string, MetricsLinkSpec> {
  const F = famOf(s);
  const FL = F.labels;
  const sf = serverFilters(s);
  const cf = clientFilters(s);
  if (!sf || !cf) return {};
  const SC = F.names.count;
  const base = SC.replace(/_count$/, '');
  const byMR = [FL.method, FL.route];
  const stErr = (re: string): MxFilter => ({ k: FL.status, op: '=~', v: re });
  const pq = (agg: MetricsLinkQuery['agg'], by?: string[]): MetricsLinkQuery =>
    ({ metric: base, agg, by, unit: F.unit, mtype: 'histogram', filters: sf });
  const cp95: MetricsLinkQuery = { metric: CLIENT_BASE, agg: 'p95', by: ['server_address'], unit: 's', mtype: 'histogram', filters: cf };
  const ccRate = (extra: MxFilter[] = [], by = ['server_address'], hidden = false): MetricsLinkQuery =>
    ({ metric: CC, agg: 'rate', by, filters: [...cf, ...extra], ...(hidden ? { hidden } : {}) });
  const links: Record<string, MetricsLinkSpec> = {
    rateByRoute: { queries: [{ metric: SC, agg: 'rate', by: byMR, filters: sf }] },
    statusClass: { code: exprs.statusClass ?? '' },
    p50: { queries: [pq('p50'), pq('p90'), pq('p95'), pq('p99')] },
    p95ByRoute: { queries: [pq('p95', byMR)] },
    err5ByRoute: { queries: [{ metric: SC, agg: 'rate', by: [FL.route, FL.method, FL.status], filters: [...sf, stErr(F.serverErr)] }] },
    err4ByRoute: { queries: [{ metric: SC, agg: 'rate', by: [FL.route, FL.method, FL.status], filters: [...sf, stErr(F.clientErr)] }] },
  };
  if (F.kind === 'http') {
    Object.assign(links, {
      dRateByAddr: { queries: [ccRate()] },
      dP95ByAddr: { queries: [cp95] },
      p95: { queries: [pq('p95'), cp95] },
      dErrByAddr: {
        queries: [ccRate([{ k: HTTP_LABELS.status, op: '=~', v: '5..|' }], undefined, true), ccRate([], undefined, true)],
        formula: { expr: 'A / B * 100', unit: '%' },
      },
      dErrTypes: { queries: [ccRate([{ k: 'error_type', op: '!=', v: '' }], ['server_address', 'error_type'])] },
    } satisfies Record<string, MetricsLinkSpec>);
  }
  return links;
}
