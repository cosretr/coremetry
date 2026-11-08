// v0.10.1976 — healthcheck route'ları HTTP sekmesi PromQL'inden varsayılan düşer.
import { describe, it, expect } from 'vitest';
import { serverSel } from './httpMetrics';

describe('serverSel routeExclude', () => {
  it('HTTP ailesinde, route süzgeci yokken !~ eklenir', () => {
    expect(serverSel({ app: 'a', routeExclude: ['(?i)healthcheck'] })).toBe('{service_name="a", http_route!~".*(?:(?i)healthcheck).*"}');
  });
  it('açık route seçiliyse ya da desen yoksa eklenmez', () => {
    expect(serverSel({ app: 'a', route: '/x/healthcheck', routeExclude: ['(?i)healthcheck'] })).not.toContain('!~');
    expect(serverSel({ app: 'a', routeExclude: [] })).toBe('{service_name="a"}');
  });
});
