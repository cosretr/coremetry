// infraRoutes.ts — v0.10.2434 (operatör onaylı, RED metrik-önce): metrik (VM)
// RED'inin rota serileri platform-tesisat rotalarını (sağlık / hazırlık
// yoklaması, Prometheus scrape'i, keepalive) VARSAYILAN olarak dışlar. Prod
// örneği: "/<APP>/actuator/prometheus" 3.4 req/s toplamın 1.7'sini taşıyordu.
// Parça listesi Go internal/infraroutes.Fragments ile BAYT BAYT eşli (Go pin
// testi TestInfraRouteFragmentsMatchFrontend). Eşleşme harf duyarsız alt-dize;
// PromQL'de `route!~".*(?:<gövde>).*"` — rota etiketi olmayan seri korunur.
import { urlSpec, useUrlParams, zParam, zOneOf } from './urlSchema';

export const INFRA_ROUTE_FRAGMENTS: readonly string[] = [
  '/health', '/healthz', '/healthcheck', '/livez', '/readyz',
  '/metrics', '/-/metrics', '/prometheus', '/actuator/prometheus',
  '/actuator/health', '/actuator/info',
  '/ping', '/heartbeat', '/keepalive',
  '/.well-known/', '/-/ready', '/-/healthy',
];

const reEscape = (s: string) => s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');

/** Harf duyarsız alternasyon gövdesi `(?i:a|b|…)` — Go infraroutes.RegexBody ile aynı. */
export function infraRouteRegexBody(): string {
  return `(?i:${INFRA_ROUTE_FRAGMENTS.map(reEscape).join('|')})`;
}

/** SAF: rota bir tesisat parçası içeriyor mu (harf duyarsız alt-dize). */
export function isInfraRoute(route: string): boolean {
  const l = route.toLowerCase();
  return INFRA_ROUTE_FRAGMENTS.some(f => l.includes(f));
}

/**
 * Rota dışlama listesi (HttpScope.routeExclude gövdeleri): yöneticinin
 * kalıpları + (gösterilmiyorsa) tesisat gövdesi.
 */
export function withInfraExclude(admin: readonly string[], showInfra: boolean): string[] {
  return showInfra ? [...admin] : [...admin, infraRouteRegexBody()];
}

const INFRA_URL_SPEC = urlSpec({ infra: zParam(zOneOf([0, 1] as const), 0) });

/** `?infra=1` → tesisat rotaları gösterilir (varsayılan gizli). */
export function useShowInfraRoutes(): [boolean, (v: boolean) => void] {
  const [{ infra }, set] = useUrlParams(INFRA_URL_SPEC);
  return [infra === 1, (v: boolean) => set({ infra: v ? 1 : 0 })];
}
