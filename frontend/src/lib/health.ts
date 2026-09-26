// health.ts — the single mapping from a 0..100 error-rate percentage to a
// CSS-variable health token. Shared by the topology surfaces (service picker
// status dots, neighborhood node dots, edge tints) so a node, its row in the
// picker, and its incoming edges all read the same green/amber/red at a
// glance. Thresholds mirror the canvas ServiceGraph (>5% red, >1% amber) so
// the operator's eye doesn't recalibrate between the two views.
//
// Returns a `var(--…)` string — never a raw hex — so light/dark theming and
// the project "tokens only" rule both hold.
// v0.10.929 (K5) — sağlıklı dal yeşil değil, nötr --text3 (eşikler aynı);
// renk yalnız sapmada (amber/kırmızı).
export function healthToken(errorRate: number): string {
  return errorRate > 5 ? 'var(--err)' : errorRate > 1 ? 'var(--warn)' : 'var(--text3)';
}
